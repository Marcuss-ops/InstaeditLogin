package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// listYouTubeEditorSessionEntry is the per-row JSON shape returned
// by GET /api/v1/youtube/editor-sessions. The shape is intentionally
// a SUBSET of models.YouTubeVideoEdit: last_error (an internal
// diagnostic) is omitted, created_at/updated_at/source_thumbnail_url
// are omitted (the SPA renders them via follow-up single-row reads
// when an operator clicks a row), and the editor_url is reconstructed
// server-side from velox_project_id so the SPA does not have to
// bundle the editor base URL into its bundle. workspace_id /
// platform_account_id are also omitted because they are implied by
// the ?workspace_id query and a multi-row response in which every
// row shares the same filter would just add bytes without semantics.
type listYouTubeEditorSessionEntry struct {
	ID                string     `json:"id"`
	YouTubeVideoID    string     `json:"youtube_video_id"`
	VeloxProjectID    string     `json:"velox_project_id"`
	EditorURL         string     `json:"editor_url"`
	Status            string     `json:"status"`
	ThumbnailMediaID  *string    `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy    string     `json:"desired_privacy"`
	PublishAt         *time.Time `json:"publish_at,omitempty"`
	ActualPrivacy     *string    `json:"actual_privacy,omitempty"`
	YouTubeSyncStatus *string    `json:"youtube_sync_status,omitempty"`
}

// listYouTubeEditorSessionsResponse is the envelope. `sessions: []`
// is returned (not 404) when no rows match the filter — the SPA's
// dashboard renders an "empty state" banner rather than treating
// "nothing to do" as an error.
type listYouTubeEditorSessionsResponse struct {
	Sessions []listYouTubeEditorSessionEntry `json:"sessions"`
}

// handleListYouTubeEditorSessions is the HTTP entry point for
// GET /api/v1/youtube/editor-sessions. It is the read-side companion
// to the POST/PATCH/POST-publish cycle and powers the SPA's
// dashboard "code da modificare" widget.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when ?workspace_id is missing/invalid, when ?limit is out
//     of range, or when a ?status value is off the allow-list.
//   - 404 when the workspace is unknown OR the caller does not have
//     access. Both branches return the SAME 404 + message so a
//     cross-tenant probe cannot distinguish "no such workspace" from
//     "workspace exists but not yours" (defence-in-depth on top of
//     the SQL `WHERE workspace_id = $1` guard).
//   - 200 + {"sessions": [...]} when the filter resolves cleanly.
//     Empty result is 200 + {"sessions": []}, NOT 404.
//
// Concurrency:
//
//	The repository method is a read-only SELECT; no row-level locks.
//	Two concurrent dashboard refreshes returning different "snapshots"
//	are expected (updated_at moves forward under writes), so the
//	the SPA should re-fetch on interval rather than rely on snapshot
//	equality.
func (r *Router) handleListYouTubeEditorSessions(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	q := req.URL.Query()
	workspaceIDRaw := strings.TrimSpace(q.Get("workspace_id"))
	workspaceID, err := parsePositiveQueryInt(workspaceIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required and must be a positive integer")
		return
	}

	// Workspace ownership check (handler-only gate; the repository
	// SQL also filters on workspace_id so a hostile caller cannot
	// return rows from a foreign workspace even if they bypass this).
	workspace, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	filter := repository.YouTubeEditorSessionListFilter{
		WorkspaceID: workspaceID,
		// Keep terminal rows visible when explicitly requested so the
		// dashboard can observe editing -> publishing -> published.
		IncludeTerminal: strings.EqualFold(strings.TrimSpace(q.Get("include_terminal")), "true"),
	}

	if accountIDRaw := strings.TrimSpace(q.Get("account_id")); accountIDRaw != "" {
		accountID, err := parsePositiveQueryInt(accountIDRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "account_id must be a positive integer")
			return
		}
		fid := accountID
		filter.AccountID = &fid
	}

	if statusRaw := strings.TrimSpace(q.Get("status")); statusRaw != "" {
		// Comma-separated multi-status support (?status=editing,failed)
		// lets the SPA wire a "filter by state" multi-select without a
		// second request. Whitespace is tolerated around commas to make
		// hand-typed URLs ergonomic.
		rawStatuses := strings.Split(statusRaw, ",")
		statuses := make([]string, 0, len(rawStatuses))
		for _, s := range rawStatuses {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "" {
				continue
			}
			statuses = append(statuses, s)
		}
		if len(statuses) == 0 {
			writeError(w, http.StatusBadRequest, "status query parameter contained no valid values")
			return
		}
		filter.Statuses = statuses
	}

	if limitRaw := strings.TrimSpace(q.Get("limit")); limitRaw != "" {
		limit, err := parsePositiveQueryInt(limitRaw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		// parsePositiveQueryInt returns int64 (consistent with the
		// Go stdlib strconv.ParseInt). The repository's filter struct
		// keeps Limit as int because the upper bound is
		// YouTubeEditorSessionListMaxLimit (500) — safely within
		// int32. Cast is a no-op at runtime, but the explicit
		// conversion makes the boundary obvious to future readers.
		filter.Limit = int(limit)
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	rows, err := r.youtubeVideoEditStore.ListByWorkspace(req.Context(), filter)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditListLimitInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if errors.Is(err, repository.ErrYouTubeVideoEditListStatusInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "list editor sessions: "+err.Error())
		return
	}

	entries := make([]listYouTubeEditorSessionEntry, 0, len(rows))
	for _, edit := range rows {
		entries = append(entries, listYouTubeEditorSessionEntry{
			ID:                edit.ID,
			YouTubeVideoID:    edit.YouTubeVideoID,
			VeloxProjectID:    edit.VeloxProjectID,
			EditorURL:         r.editorURLForProject(edit.VeloxProjectID),
			Status:            edit.Status,
			ThumbnailMediaID:  edit.ThumbnailMediaID,
			DesiredPrivacy:    edit.DesiredPrivacy,
			PublishAt:         edit.PublishAt,
			ActualPrivacy:     edit.ActualPrivacy,
			YouTubeSyncStatus: edit.YouTubeSyncStatus,
		})
	}
	writeJSON(w, http.StatusOK, listYouTubeEditorSessionsResponse{Sessions: entries})
}

// parsePositiveQueryInt parses an HTTP query-string int64 and rejects
// zero/negative/non-numeric values. Centralised here so the dashboard
// list handler's workspace_id / account_id / limit parsing keeps the
// same shape (the inline equivalent is 8 lines repeated 3x); future
// GET endpoints reading a positive int from the query can reuse it.
//
// Returns (0, nil) on empty input — callers must decide whether
// empty is an error (workspace_id) or "use default" (limit, account_id).
func parsePositiveQueryInt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("empty value")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if n <= 0 {
		return 0, fmt.Errorf("value %d is not positive", n)
	}
	return n, nil
}
