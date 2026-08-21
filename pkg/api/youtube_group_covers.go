package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// groupCoverEntry is the per-row JSON shape returned by
// GET /api/v1/groups/{group_id}/covers. It is the covers-hub
// projection of models.GroupCover: the cover project identity +
// status + preview pointers, joined with the InstaEditor session
// handle (velox_project_id + editor_url) and the YouTube video the
// cover was made for.
//
// The response intentionally carries both preview pointers:
//   - preview_media_id — thumbnail_projects.preview_media_id, the
//     rendered cover preview (media_assets UUID) when the cover has
//     been exported at least once; the SPA resolves the signed URL
//     via GET /api/v1/media/{id} (existing media library contract);
//   - source_thumbnail_url — the original YouTube video thumbnail,
//     a stable fallback for covers that were never rendered yet.
//
// EditorURL is reconstructed server-side from velox_project_id (same
// contract as the editor-sessions list) so the SPA never bundles the
// editor base URL.
type groupCoverEntry struct {
	ProjectID          string  `json:"project_id"`
	WorkspaceID        int64   `json:"workspace_id"`
	SessionID          string  `json:"session_id"`
	VeloxProjectID     string  `json:"velox_project_id"`
	EditorURL          string  `json:"editor_url"`
	Name               string  `json:"name"`
	ProjectStatus      string  `json:"project_status"`
	EditStatus         string  `json:"edit_status"`
	LifecycleStatus    string  `json:"lifecycle_status"`
	PreviewMediaID     *string `json:"preview_media_id,omitempty"`
	ThumbnailMediaID   *string `json:"thumbnail_media_id,omitempty"`
	SourceThumbnailURL string  `json:"source_thumbnail_url,omitempty"`
	YouTubeVideoID     string  `json:"youtube_video_id"`
	PlatformAccountID  int64   `json:"platform_account_id"`
	ChannelName        string  `json:"channel_name,omitempty"`
	Language           string  `json:"language,omitempty"`
	DraftTitle         *string `json:"draft_title,omitempty"`
	DraftDescription   *string `json:"draft_description,omitempty"`
	// CategoryID mirrors youtube_video_edits.category_id (the YouTube
	// video category stamped at session creation) so the covers hub
	// card can show it alongside the video manager.
	CategoryID string `json:"category_id,omitempty"`
	// PrivacyStatus is the live YouTube visibility of the underlying
	// video: actual_privacy when the publish orchestrator read it
	// back, desired_privacy otherwise (same resolution as the editor
	// session detail DTO).
	PrivacyStatus  string    `json:"privacy_status"`
	ProjectVersion int64     `json:"project_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// groupCoversResponse is the envelope. `covers: []` is returned (NOT
// 404) when the group has no covers yet — the SPA renders an
// empty-state banner rather than treating "nothing here" as an error.
type groupCoversResponse struct {
	Covers []groupCoverEntry `json:"covers"`
}

// privacyStatusForCover resolves the cover's privacy_status the same
// way the editor session detail DTO does: the live read-back
// (actual_privacy) wins when the publish orchestrator stamped it,
// otherwise the operator's intended desired_privacy.
func privacyStatusForCover(c *models.GroupCover) string {
	if c == nil {
		return ""
	}
	if c.ActualPrivacy != nil && strings.TrimSpace(*c.ActualPrivacy) != "" {
		return *c.ActualPrivacy
	}
	return c.DesiredPrivacy
}

func lifecycleStatusForCover(c *models.GroupCover) string {
	if c == nil || c.EditStatus == "failed" {
		return "error"
	}
	if c.EditStatus == "published" {
		return "published"
	}
	if c.ThumbnailMediaID != nil && strings.TrimSpace(*c.ThumbnailMediaID) != "" {
		return "applied"
	}
	if c.ProjectStatus == models.ThumbnailProjectStatusReady {
		return "ready"
	}
	return "draft"
}

// handleListGroupCovers is the HTTP entry point for
// GET /api/v1/groups/{group_id}/covers — the covers-hub projection of
// the Copertine page.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when {group_id} is not a positive integer.
//   - 404 when the group is unknown OR the caller does not own its
//     workspace. Both branches return the SAME 404 + message so a
//     cross-tenant probe cannot distinguish "no such group" from
//     "group exists but not yours".
//   - 501 when groups are not configured on this server.
//   - 200 + {"covers": [...]} in every other case. Archived covers
//     are included (status='archived' projects stay in the list so
//     the hub shows the full history); soft-deleted projects are
//     excluded.
//
// Data source: one repository query (ListCoversByGroupAccounts)
// joins thumbnail_projects → velox_project_bridges →
// youtube_video_edits for the group's accounts, so the response is a
// single SQL round-trip with no per-cover session lookups.
func (r *Router) handleListGroupCovers(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	groupIDRaw := strings.TrimSpace(chi.URLParam(req, "group_id"))
	groupID, err := parsePositiveQueryInt(groupIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "group_id path parameter must be a positive integer")
		return
	}

	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace store not configured")
		return
	}

	// Group lookup + workspace ownership, collapsed 404 (mirrors the
	// group-videos resolver phase 1 so a foreign-tenant probe cannot
	// distinguish "no group" from "not yours").
	group, err := r.groupStore.FindByID(groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find group: "+err.Error())
		return
	}
	if group == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	workspace, err := r.workspaceStore.FindByID(group.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}

	accountIDs, err := r.groupStore.ListAccountsInGroup(groupID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list accounts in group: "+err.Error())
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "editor session store not configured")
		return
	}

	covers, err := r.youtubeVideoEditStore.ListCoversByGroupAccounts(req.Context(), group.WorkspaceID, accountIDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list group covers: "+err.Error())
		return
	}

	// Resolve channel display name + language per account in ONE pass
	// (same pattern as resolveGroupYouTubeAccounts phase 4 — an
	// account that fails to resolve just loses its display metadata,
	// it never fails the response).
	accountMeta := make(map[int64]*models.PlatformAccount, len(accountIDs))
	if r.userRepo != nil {
		for _, aid := range accountIDs {
			acc, accErr := r.userRepo.FindPlatformAccountByID(aid)
			if accErr != nil || acc == nil {
				continue
			}
			accountMeta[aid] = acc
		}
	}

	entries := make([]groupCoverEntry, 0, len(covers))
	for _, c := range covers {
		entry := groupCoverEntry{
			ProjectID:          c.ProjectID,
			WorkspaceID:        c.WorkspaceID,
			SessionID:          c.SessionID,
			VeloxProjectID:     c.VeloxProjectID,
			EditorURL:          r.editorURLForProject(c.VeloxProjectID),
			Name:               c.ProjectName,
			ProjectStatus:      string(c.ProjectStatus),
			EditStatus:         c.EditStatus,
			LifecycleStatus:    lifecycleStatusForCover(c),
			PreviewMediaID:     c.PreviewMediaID,
			ThumbnailMediaID:   c.ThumbnailMediaID,
			SourceThumbnailURL: c.SourceThumbnailURL,
			YouTubeVideoID:     c.YouTubeVideoID,
			PlatformAccountID:  c.PlatformAccountID,
			DraftTitle:         c.DraftTitle,
			DraftDescription:   c.DraftDescription,
			CategoryID:         c.CategoryID,
			PrivacyStatus:      privacyStatusForCover(c),
			ProjectVersion:     c.ProjectVersion,
			CreatedAt:          c.ProjectCreatedAt,
			UpdatedAt:          c.ProjectUpdatedAt,
		}
		if acc, ok := accountMeta[c.PlatformAccountID]; ok {
			entry.ChannelName = strings.TrimSpace(acc.Username)
			if entry.ChannelName == "" {
				entry.ChannelName = acc.PlatformUserID
			}
			entry.Language = accountLanguage(acc)
		}
		entries = append(entries, entry)
	}
	writeJSON(w, http.StatusOK, groupCoversResponse{Covers: entries})
}
