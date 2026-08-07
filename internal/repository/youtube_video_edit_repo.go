package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeVideoEditStore is the persistence contract for
// youtube_video_edits. It is declared in pkg/api and implemented by
// YouTubeVideoEditRepository so the API layer can depend on the
// interface while production wiring passes the concrete repository.
//
// The concrete type is intentionally placed in internal/repository
// (not pkg/api) to keep the API package free of *sql.DB details.
//
// ListByWorkspaceAccountIDs (P0 group videos) returns every editor
// session in the given workspace whose platform_account_id is in the
// supplied slice. Used by GET /api/v1/groups/{group_id}/youtube/videos
// to project existing per-video editor sessions onto YouTube's fresh
// video listing for all channels in the group in a single SQL query
// (avoids the N+1 round-trip of calling ListByWorkspace per account).
// The workspace_id predicate is the cross-tenant guard — the handler
// validates caller ownership of the workspace but defence-in-depth.
type YouTubeVideoEditStore interface {
	Create(ctx context.Context, edit *models.YouTubeVideoEdit) error
	FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error)
	FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	Update(ctx context.Context, edit *models.YouTubeVideoEdit) error
	// MarkPublishing (Blocco #5 P0 #2) atomically transitions status →
	// 'publishing' WITH desired_privacy + publish_at stamped in the
	// same statement. CAS predicate (extended form):
	//   status IN ('editing','failed')                  -- primary path
	//   OR (status='publishing' AND updated_at <
	//       NOW() - make_interval(secs => inFlightTimeout))  -- orphan recovery
	// The two-branch SQL is selected in Go (E1) based on
	// inFlightTimeout > 0 to avoid the timeout=0 degenerate case where
	// make_interval(secs => 0) would match ALL 'publishing' rows.
	// Returns (nil, ErrYouTubeVideoEditNotFound) on 0-rows match — the
	// handler maps to 409 (CAS-loss).
	MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error)
	// AttachThumbnail (Blocco #5 P0 #4) atomically links a verified
	// media asset (thumbnail) to an editor session. Single UPDATE
	// statement with CAS predicate `status IN ('editing','failed')` so
	// concurrent publish requests can't race the link (a row already
	// in 'publishing' or 'published' state is rejected with
	// ErrYouTubeVideoEditNotFound on 0-rows match — handler maps to
	// 409). The thumbnail_media_id is the only column updated;
	// thumbnail_status is left untouched (still owned by the broader
	// pipeline state machine). Returns the updated row or
	// ErrYouTubeVideoEditNotFound when 0 rows match.
	AttachThumbnail(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error)
	// ListByWorkspace returns the editor sessions visible to the
	// dashboard "code da modificare" widget for a single workspace.
	// Filter semantics:
	//   - WorkspaceID: required, single-tenant scoping (the SQL
	//     `WHERE workspace_id = $1` is the FIRST guard against
	//     cross-tenant leakage; the handler also verifies caller
	//     ownership of the workspace but defence-in-depth).
	//   - AccountID: optional per-platform_account narrowing so the
	//     "filter by channel" dropdown doesn't fetch every row in
	//     the workspace.
	//   - Statuses: empty slice → YouTubeVideoEditNonTerminalStatuses
	//     (editing/failed/publishing — "published" excluded). A
	//     caller-supplied status set is validated against
	//     YouTubeEditorSessionListStatusAllowList inside the method.
	//   - IncludeTerminal: opt-in flag for future callers that need
	//     rows in 'published' state too (the handler in this commit
	//     defaults to false). When true, the allow-list accepts
	//     'published' as well.
	//   - Limit: 0 → YouTubeEditorSessionListDefaultLimit (100);
	//     values outside [1, MaxLimit] are rejected with
	//     ErrYouTubeVideoEditListLimitInvalid.
	//
	// Order: ORDER BY updated_at DESC so the dashboard sees
	// "recently touched" rows first — matches the "what should I edit
	// next" intent. Bounded by LIMIT so a workspace with 10k rows
	// cannot blow up the render budget.
	//
	// Empty result is a valid (rows=0, err=nil) return — the handler
	// maps to 200 + {"sessions": []} (NOT 404).
	ListByWorkspace(ctx context.Context, filter YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error)
	// ListByWorkspaceAccountIDs (P0 group videos endpoint) — see
	// YouTubeVideoEditRepository.ListByWorkspaceAccountIDs for full
	// contract. Differences vs ListByWorkspace: NO status filter
	// (we want every row that's been touched for the channel,
	// including 'published' for the "re-edit" CTA), NO limit (the
	// channel fan-out caps the WS count upstream so per-WS row
	// volume is bounded by the chain cardinality). Caller-side
	// join happens against the (account_id, video_id) tuple.
	ListByWorkspaceAccountIDs(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error)
	// FindOrCreateEditableSession (P0#3 click-idempotency) — returns
	// the open (non-terminal) editor session for the given
	// (workspace, account, video) triple, or inserts a fresh one
	// under the partial UNIQUE INDEX `uniq_youtube_video_edits_open_session`
	// (migration 071). Same YouTube video clicked twice from the
	// dashboard card grid converges on a single velox_project_id so
	// the SPA can re-route the operator to the same InstaEditor URL
	// on every click. See YouTubeVideoEditRepository.FindOrCreateEditableSession
	// for the race-safe sequence.
	FindOrCreateEditableSession(ctx context.Context, workspaceID int64, platformAccountID int64, youtubeVideoID string, sessionIDHint string, projectIDHint string) (*models.YouTubeVideoEdit, error)
	// SaveDraft (P2 — InstaEditor auto-save) atomically writes the
	// operator's mid-edit form values to youtube_video_edits.draft_*
	// AND stamps dirty_flag=false AND draft_updated_at=NOW() in a
	// single SQL statement. CAS predicate: status IN
	// ('editing','failed'). The publish orchestrator owns the row
	// during 'publishing' — racing a draft save against a publish
	// would let an operator's keystrokes silently overwrite the
	// privacy/title the orchestrator just pushed to YouTube. Once
	// the row lands in 'published' state, the partial UNIQUE INDEX
	// `uniq_youtube_video_edits_open_session` (migration 071) keeps
	// 'published' rows invisible to the predicate — a re-edit click
	// will mint a FRESH row (FindOrCreateEditableSession re-issues).
	// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows match —
	// handler maps to HTTP 409 (CAS-loss). A real *sql.DB error
	// propagates wrapped.
	SaveDraft(ctx context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, publishAt *time.Time, draftUpdatedAt time.Time) error
	// MarkPublishedWithActualPrivacy (P0#7 actual_privacy read-back)
	// atomically transitions a row from 'publishing' → 'published'
	// AND stamps actual_privacy + youtube_sync_status in the same
	// SQL statement. CAS predicate: status='publishing' ONLY (a row
	// in 'editing'/'failed' must NEVER reach this branch — the
	// PublishThumbnail YouTube API call has not yet succeeded).
	//
	// syncStatus is the lifecycle marker the SPA uses to colour the
	// privacy badge; valid values: 'pending' / 'confirmed' /
	// 'drift' / 'failed' (CHECK constraint, migration 072).
	//
	// Returns ErrYouTubeVideoEditNotFound on 0-rows match —
	// handler maps to 409 (CAS-loss). A real *sql.DB error
	// propagates wrapped.
	MarkPublishedWithActualPrivacy(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error)
}

// YouTubeVideoEditRepository handles CRUD for youtube_video_edits.
type YouTubeVideoEditRepository struct {
	db *sql.DB
}

// NewYouTubeVideoEditRepository creates a new YouTubeVideoEditRepository.
func NewYouTubeVideoEditRepository(db *sql.DB) *YouTubeVideoEditRepository {
	return &YouTubeVideoEditRepository{db: db}
}

// Create inserts a new YouTube video edit session. It is the caller's
// responsibility to generate the id and velox_project_id.
func (r *YouTubeVideoEditRepository) Create(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO youtube_video_edits
			(id, workspace_id, platform_account_id, youtube_video_id, velox_project_id,
			 source_thumbnail_url, thumbnail_media_id, desired_privacy, publish_at,
			 status, last_error, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		edit.ID, edit.WorkspaceID, edit.PlatformAccountID, edit.YouTubeVideoID, edit.VeloxProjectID,
		edit.SourceThumbnailURL, edit.ThumbnailMediaID, edit.DesiredPrivacy, edit.PublishAt,
		edit.Status, edit.LastError, edit.CreatedAt, edit.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create youtube video edit: %w", err)
	}
	return nil
}

// FindByID returns the edit session with the given id, or (nil, nil)
// when no row matches.
func (r *YouTubeVideoEditRepository) FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`SELECT `+youtubeVideoEditSelectColumns+`
		 FROM youtube_video_edits
		 WHERE id = $1`,
		id,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
		&edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find youtube video edit: %w", err)
	}
	return edit, nil
}

// FindByVeloxProjectID returns the edit session for the given velox
// project id, or (nil, nil) when no row matches.
func (r *YouTubeVideoEditRepository) FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	if err := r.db.QueryRowContext(ctx,
		`SELECT `+youtubeVideoEditSelectColumns+`
		 FROM youtube_video_edits
		 WHERE velox_project_id = $1`,
		projectID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID, &edit.VeloxProjectID,
		&edit.SourceThumbnailURL, &edit.ThumbnailMediaID, &edit.DesiredPrivacy, &edit.PublishAt,
		&edit.Status, &edit.LastError, &edit.ActualPrivacy, &edit.YouTubeSyncStatus,
		&edit.CreatedAt, &edit.UpdatedAt,
	); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to find youtube video edit by project: %w", err)
	}
	return edit, nil
}

func (r *YouTubeVideoEditRepository) Update(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE youtube_video_edits
		 SET workspace_id = $2,
		     platform_account_id = $3,
		     youtube_video_id = $4,
		     velox_project_id = $5,
		     source_thumbnail_url = $6,
		     thumbnail_media_id = $7,
		     desired_privacy = $8,
		     publish_at = $9,
		     status = $10,
		     last_error = $11,
		     updated_at = $12
		 WHERE id = $1`,
		edit.ID, edit.WorkspaceID, edit.PlatformAccountID, edit.YouTubeVideoID, edit.VeloxProjectID,
		edit.SourceThumbnailURL, edit.ThumbnailMediaID, edit.DesiredPrivacy, edit.PublishAt,
		edit.Status, edit.LastError, edit.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to update youtube video edit: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, edit.ID)
	}
	return nil
}

// youtubeVideoEditSelectColumns is the canonical column list shared
// across all read methods on this repository. Centralising the list
// prevents the column-projection from drifting between FindByID,
// FindByVeloxProjectID and ListByWorkspace — a future column added
// to youtube_video_edits (e.g. a 'failure_reason' diagnostic) is
// then automatically returned by every read without per-call edits.
//
// actual_privacy + youtube_sync_status are the YouTube-side
// projections (migration 072). Every read method returns them so the
// publish-by-project GET, the dashboard list, and the groups videos
// endpoint all surface them without per-call SQL edits.
const youtubeVideoEditSelectColumns = `id, workspace_id, platform_account_id, youtube_video_id,
	        velox_project_id, source_thumbnail_url, thumbnail_media_id,
	        desired_privacy, publish_at, status, last_error,
	        actual_privacy, youtube_sync_status,
	        created_at, updated_at`
