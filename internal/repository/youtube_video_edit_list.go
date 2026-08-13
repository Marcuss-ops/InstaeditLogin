package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeVideoEditNonTerminalStatuses is the canonical "still editable"
// status set used by GET /api/v1/youtube/editor-sessions when the
// caller does not supply an explicit ?status filter. Excludes
// 'published' (terminal success — thumbnail already shipped) and
// any future 'canceled' state. 'failed' is included because the
// "Publish" button on the dashboard surfaces it for retry — a row
// in 'failed' is non-terminal from the operator's POV.
var YouTubeVideoEditNonTerminalStatuses = []string{"editing", "failed", "publishing"}

// YouTubeEditorSessionListDefaultLimit / MaxLimit bound the dashboard
// list endpoint. 100 keeps a single workspace page under the SPA
// render budget; 500 is the hard cap so a misbehaving client cannot
// ask for the entire table at once.
const (
	YouTubeEditorSessionListDefaultLimit = 100
	YouTubeEditorSessionListMaxLimit     = 500
)

// ErrYouTubeVideoEditListLimitInvalid is the typed sentinel returned
// by the handler when ?limit is out of [1, MaxLimit]. Handlers map to
// HTTP 400 via errors.Is.
var ErrYouTubeVideoEditListLimitInvalid = errors.New("youtube video edit list: limit out of range")

// ErrYouTubeVideoEditListStatusInvalid is the typed sentinel returned
// when the caller supplies a ?status value the repository does not
// recognise (anything not in {'editing','failed','publishing','published'}).
// Off-list values could be a sign of a misconfigured caller probing
// internal states, so we fail closed rather than silently coerce.
var ErrYouTubeVideoEditListStatusInvalid = errors.New("youtube video edit list: invalid status value")

// YouTubeEditorSessionListFilter is the slice the GET handler hands to
// the repository's ListByWorkspace method. Optional fields are nil/0;
// the repository applies the default non-terminal status set when
// Statuses is empty.
//
// Limit=0 falls back to YouTubeEditorSessionListDefaultLimit at the
// repository layer; this keeps "limit" semantically correct (a count
// of rows to return) at every call site without callers having to
// negotiate the default value themselves.
type YouTubeEditorSessionListFilter struct {
	WorkspaceID     int64
	AccountID       *int64   // nil = no per-account filter
	Statuses        []string // empty = non-terminal default
	IncludeTerminal bool     // when true, Statuses may include 'published' (future-proof)
	Limit           int      // 0 = default
}

// YouTubeEditorSessionListStatusAllowList enumerates every status the
// repository will accept as a query input. Adding a new status here
// is the single place that decides "this value is request-friendly";
// any pre-existing row in a status NOT listed here will still be
// queryable only via an explicit admin path (out of scope for now).
var YouTubeEditorSessionListStatusAllowList = map[string]struct{}{
	"editing":    {},
	"failed":     {},
	"publishing": {},
	"published":  {}, // accepted but only via IncludeTerminal (operator opt-in)
}

// ListByWorkspace returns the editor sessions visible to the
// dashboard "code da modificare" widget for the filter's workspace.
//
// SQL notes:
//
//   - The `($2::bigint IS NULL OR platform_account_id = $2)` predicate
//     lets a single SQL statement serve both "all accounts" and
//     "specific account" without dynamic string concatenation. The
//     planner keeps a single plan across both cases; the IS NULL
//     short-circuits when AccountID is nil.
//   - `status = ANY($3)` uses lib/pq's pq.Array marshalling so the
//     caller-supplied slice becomes a PostgreSQL ARRAY(TEXT) bind.
//     ANY with an empty array matches no rows (NOT matches all —
//     exactly what we want when the handler accidentally hands us an
//     empty Statuses slice and didn't let us hydrate the default).
//   - `ORDER BY updated_at DESC LIMIT $4` bounds the response size
//     regardless of the workspace's row count. The `idx_youtube_video_edits_workspace`
//   - `idx_youtube_video_edits_status` indexes (migration 065)
//     make this query a planner-friendly index range scan.
//
// Validation:
//   - filter.WorkspaceID <= 0 → 0-rows (the WHERE clause never
//     matches); we return early with (nil, nil) so a misconfigured
//     caller doesn't trigger a Postgres-side error.
//   - filter.Statuses: each entry is validated against
//     YouTubeEditorSessionListStatusAllowList. 'published' is only
//     accepted when filter.IncludeTerminal is true (the GET handler
//     does not set it for now — future "include published" toggle).
//   - filter.Limit: clamped to [1, MaxLimit]; out-of-range values
//     return ErrYouTubeVideoEditListLimitInvalid.
//   - filter.Limit == 0 → YouTubeEditorSessionListDefaultLimit.
func (r *YouTubeVideoEditRepository) ListByWorkspace(ctx context.Context, filter YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
	if filter.WorkspaceID <= 0 {
		return nil, nil
	}
	// Limit resolution.
	limit := filter.Limit
	switch {
	case limit == 0:
		limit = YouTubeEditorSessionListDefaultLimit
	case limit < 1 || limit > YouTubeEditorSessionListMaxLimit:
		return nil, fmt.Errorf("%w: limit=%d (max=%d)", ErrYouTubeVideoEditListLimitInvalid, limit, YouTubeEditorSessionListMaxLimit)
	}
	// Status resolution + validation.
	statuses := filter.Statuses
	if len(statuses) == 0 {
		statuses = YouTubeVideoEditNonTerminalStatuses
		if filter.IncludeTerminal {
			// The dashboard uses IncludeTerminal for the post-publish
			// confirmation card. Keep the default editable statuses and
			// append published rows so a completed session does not
			// disappear immediately after the publish response.
			statuses = append(append([]string(nil), statuses...), "published")
		}
	}
	for _, s := range statuses {
		if _, ok := YouTubeEditorSessionListStatusAllowList[s]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrYouTubeVideoEditListStatusInvalid, s)
		}
		if s == "published" && !filter.IncludeTerminal {
			return nil, fmt.Errorf("%w: %q (requires IncludeTerminal=true)", ErrYouTubeVideoEditListStatusInvalid, s)
		}
	}

	var accountID interface{}
	if filter.AccountID != nil {
		accountID = *filter.AccountID
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+youtubeVideoEditSelectColumns+`
		 FROM youtube_video_edits
		 WHERE workspace_id = $1
		   AND ($2::bigint IS NULL OR platform_account_id = $2)
		   AND status = ANY($3)
		 ORDER BY updated_at DESC
		 LIMIT $4`,
		filter.WorkspaceID, accountID, pq.Array(statuses), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("youtube video edit ListByWorkspace query: %w", err)
	}
	defer rows.Close()
	out := make([]*models.YouTubeVideoEdit, 0, limit)
	for rows.Next() {
		edit := &models.YouTubeVideoEdit{}
		if err := rows.Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.CategoryID, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
			&edit.CreatedAt, &edit.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("youtube video edit ListByWorkspace scan: %w", err)
		}
		out = append(out, edit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube video edit ListByWorkspace rows: %w", err)
	}
	return out, nil
}

// ListByWorkspaceAccountIDs returns every youtube_video_edits row
// in the given workspace whose platform_account_id is in the
// supplied slice. Backs GET /api/v1/groups/{group_id}/youtube/videos's
// "project existing editor sessions onto YouTube's fresh listing"
// join: one SQL query feeds every channel in the group, avoiding the
// N+1 round-trip cost of calling ListByWorkspace per account.
//
// SQL notes:
//   - workspace_id predicate is the cross-tenant guard (the
//     handler ALSO verifies the caller owns the workspace, but
//     defence-in-depth on top of the SQL filter).
//   - `platform_account_id = ANY($2)` uses lib/pq's pq.Array to
//     bind the slice as a PostgreSQL ARRAY(BIGINT). Index
//     `idx_youtube_video_edits_account` keeps this query to an
//     index range scan when accountIDs is small (the common case
//     for a group with <20 channels).
//   - NO Limit / NO Statuses filter — we want every row that has
//     been touched for the channels (the SPA needs sessions in
//     'published' state too so the card can show a "re-edit" CTA
//     after a publish completed). The handler caps the WS row
//     count indirectly via the per-channel account count.
//
// Order: ORDER BY updated_at DESC so the "most-recently-touched"
// card surfaces first when the SPA applies a default sort.
//
// Empty inputs collapse to (nil, nil) — a misconfigured caller
// never triggers a Postgres-side error.
func (r *YouTubeVideoEditRepository) ListByWorkspaceAccountIDs(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
	if workspaceID <= 0 {
		return nil, nil
	}
	if len(accountIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+youtubeVideoEditSelectColumns+`
		 FROM youtube_video_edits
		 WHERE workspace_id = $1
		   AND platform_account_id = ANY($2)
		 ORDER BY updated_at DESC`,
		workspaceID, pq.Array(accountIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("youtube video edit ListByWorkspaceAccountIDs query: %w", err)
	}
	defer rows.Close()
	out := make([]*models.YouTubeVideoEdit, 0, len(accountIDs))
	for rows.Next() {
		edit := &models.YouTubeVideoEdit{}
		if err := rows.Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.CategoryID, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
			&edit.CreatedAt, &edit.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("youtube video edit ListByWorkspaceAccountIDs scan: %w", err)
		}
		out = append(out, edit)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube video edit ListByWorkspaceAccountIDs rows: %w", err)
	}
	return out, nil
}
