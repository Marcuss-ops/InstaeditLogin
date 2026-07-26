package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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

// YouTubeVideoEditStore is the persistence contract for
// youtube_video_edits. It is declared in pkg/api and implemented by
// YouTubeVideoEditRepository so the API layer can depend on the
// interface while production wiring passes the concrete repository.
//
// The concrete type is intentionally placed in internal/repository
// (not pkg/api) to keep the API package free of *sql.DB details.
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
		`SELECT id, workspace_id, platform_account_id, youtube_video_id, velox_project_id,
		        source_thumbnail_url, thumbnail_media_id, desired_privacy, publish_at,
		        status, last_error, created_at, updated_at
		 FROM youtube_video_edits
		 WHERE id = $1`,
		id,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID, &edit.VeloxProjectID,
		&edit.SourceThumbnailURL, &edit.ThumbnailMediaID, &edit.DesiredPrivacy, &edit.PublishAt,
		&edit.Status, &edit.LastError, &edit.CreatedAt, &edit.UpdatedAt,
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
		`SELECT id, workspace_id, platform_account_id, youtube_video_id, velox_project_id,
		        source_thumbnail_url, thumbnail_media_id, desired_privacy, publish_at,
		        status, last_error, created_at, updated_at
		 FROM youtube_video_edits
		 WHERE velox_project_id = $1`,
		projectID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID, &edit.VeloxProjectID,
		&edit.SourceThumbnailURL, &edit.ThumbnailMediaID, &edit.DesiredPrivacy, &edit.PublishAt,
		&edit.Status, &edit.LastError, &edit.CreatedAt, &edit.UpdatedAt,
	); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to find youtube video edit by project: %w", err)
	}
	return edit, nil
}

// Update persists lifecycle changes to an existing edit session.
// MarkPublishing (Blocco #5 P0 #2) is the atomic CAS claim for the
// thumbnail-publish transition. The handler's previous read-then-update
// pattern (`edit.Status = "publishing"; Update(ctx, edit)`) had a TOCTOU
// race: two concurrent publish requests could both pass the row-state
// read and fire two PublishThumbnail calls (a real production bug —
// video would be thumbnail-published twice).
//
// CAS structure:
//   STRICT BRANCH (inFlightTimeout <= 0):
//     UPDATE ... SET status, updated_at, desired_privacy, publish_at
//      WHERE id=$1 AND status IN ('editing','failed')
//      RETURNING ...
//   EXTENDED BRANCH (inFlightTimeout > 0):
//     UPDATE ... SET ...  WHERE id=$1 AND (
//        status IN ('editing','failed')
//        OR (status='publishing' AND updated_at < NOW() - make_interval(secs => $4))
//     ) RETURNING ...
//
// The extended branch is the orphan-recovery path: a previous publish
// was claimed but the worker died mid-call (status stuck at
// 'publishing' with no published_at stamp). The handler's in-flight
// read at the top of the function would have already rejected a
// recent (within timeout) sticky 'publishing' row with 409; this
// branch picks up only rows older than the timeout.
//
// include-desired_privacy-and-publish_at-in-CAS so the publish call sees
// the values that were atomically stamped (no chance of a concurrent
// reader flipping the resolved privacy between the MarkPublishing
// return and the PublishThumbnail call). Avoiding the
// read-then-modify-write reverts every race that the original
// handler was subject to (privacy/publish_at swap while the row was
// being updated).
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) when 0 rows match —
// distinct from a real *sql.DB error so the handler can branch on
// errors.Is(..., ErrYouTubeVideoEditNotFound) to map to HTTP 409.
func (r *YouTubeVideoEditRepository) MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error) {
	selectColumns := `id, workspace_id, platform_account_id, youtube_video_id,
	                   velox_project_id, source_thumbnail_url, thumbnail_media_id,
	                   desired_privacy, publish_at, status, last_error,
	                   created_at, updated_at`
	edit := &models.YouTubeVideoEdit{}
	var err error
	if inFlightTimeout <= 0 {
		// Strict branch: only editing/failed are claimable.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1 AND status IN ('editing','failed')
			 RETURNING `+selectColumns,
			id, desiredPrivacy, publishAt,
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.CreatedAt, &edit.UpdatedAt,
		)
	} else {
		// Extended branch: also re-claim a stale 'publishing' row.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1
			   AND (
			       status IN ('editing','failed')
			       OR (
			            status = 'publishing'
			            AND updated_at < NOW() - make_interval(secs => $4)
			        )
			   )
			 RETURNING `+selectColumns,
			id, desiredPrivacy, publishAt, int(inFlightTimeout.Seconds()),
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.CreatedAt, &edit.UpdatedAt,
		)
	}
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit MarkPublishing: %w", err)
	}
	return edit, nil
}

// AttachThumbnail atomically links a verified media asset (the
// rendered thumbnail) to an editor session. Single UPDATE statement
// with CAS predicate `status IN ('editing','failed')` so concurrent
// publishes cannot race the link (a session already in 'publishing' or
// 'published' state will not match — handler maps 0-rows to 409).
// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows match.
func (r *YouTubeVideoEditRepository) AttachThumbnail(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`UPDATE youtube_video_edits
		 SET thumbnail_media_id = $2, updated_at = NOW()
		 WHERE id = $1 AND status IN ('editing','failed')
		 RETURNING id, workspace_id, platform_account_id, youtube_video_id,
		           velox_project_id, source_thumbnail_url, thumbnail_media_id,
		           desired_privacy, publish_at, status, last_error,
		           created_at, updated_at`,
		sessionID, thumbnailMediaID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit AttachThumbnail: %w", err)
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
const youtubeVideoEditSelectColumns = `id, workspace_id, platform_account_id, youtube_video_id,
	        velox_project_id, source_thumbnail_url, thumbnail_media_id,
	        desired_privacy, publish_at, status, last_error,
	        created_at, updated_at`

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
//     + `idx_youtube_video_edits_status` indexes (migration 065)
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
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
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
