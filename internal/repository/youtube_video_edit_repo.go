package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	// the SPA can re-route the operator to the same Dark Editor URL
	// on every click. See YouTubeVideoEditRepository.FindOrCreateEditableSession
	// for the race-safe sequence.
	FindOrCreateEditableSession(ctx context.Context, workspaceID int64, platformAccountID int64, youtubeVideoID string, sessionIDHint string, projectIDHint string) (*models.YouTubeVideoEdit, error)
	// SaveDraft (P2 — Dark Editor auto-save) atomically writes the
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
	SaveDraft(ctx context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, draftUpdatedAt time.Time) error
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
	edit := &models.YouTubeVideoEdit{}
	var err error
	if inFlightTimeout <= 0 {
		// Strict branch: only editing/failed are claimable.
		err = r.db.QueryRowContext(ctx,
			`UPDATE youtube_video_edits
			 SET status = 'publishing', updated_at = NOW(),
			     desired_privacy = $2, publish_at = $3
			 WHERE id = $1 AND status IN ('editing','failed')
			 RETURNING `+youtubeVideoEditSelectColumns,
			id, desiredPrivacy, publishAt,
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
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
			 RETURNING `+youtubeVideoEditSelectColumns,
			id, desiredPrivacy, publishAt, int(inFlightTimeout.Seconds()),
		).Scan(
			&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
			&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
			&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
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
		 RETURNING `+youtubeVideoEditSelectColumns,
		sessionID, thumbnailMediaID,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
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

// MarkPublishedWithActualPrivacy (P0#7 actual_privacy read-back) is
// the atomic CAS that stamps the YouTube-side projection in the same
// SQL statement as the status='published' flip, so a concurrent
// dashboard reader cannot observe a 'published' row with stale NULL
// actual_privacy / pending youtube_sync_status.
//
// Why CAS, not a follow-up Update:
//   The orchestrator's previous final-step pattern (`edit.Status =
//   "published"; Update(...)`) had a subtle race: a concurrent GET on
//   the same velox_project_id could observe Status='published' but
//   ActualPrivacy=NULL (the row had not yet been written with the new
//   projection). Operators then saw "Pubblicato" badges with no
//   actual privacy colour — confusing + missed drift. The CAS below
//   guarantees the four columns (status, actual_privacy,
//   youtube_sync_status, updated_at) flip together in the same SQL,
//   so a reader either sees the row pre-CAS or post-CAS, never in
//   between.
//
// CAS predicate: status='publishing' (only). MarkPublishing was the
// gate prior to this — a row in 'editing'/'failed' must never reach
// this branch because PublishThumbnail has not successfully run yet,
// and a row in 'published' (e.g. on a replay) was caught at the
// orchestrator's idempotency guard.
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows — the
// orchestrator maps to 409 (CAS-loss). This is distinct from a real
// Postgres error so the handlers can branch on errors.Is.
func (r *YouTubeVideoEditRepository) MarkPublishedWithActualPrivacy(
	ctx context.Context,
	id string,
	actualPrivacy string,
	syncStatus string,
) (*models.YouTubeVideoEdit, error) {
	// syncStatus is constrained at the DB layer (CHECK constraint,
	// migration 072). We do NOT re-validate here: the application
	// layer is the single source of truth for the four lifecycle
	// values; overlaying an allow-list at this layer would just
	// drift the truth away from the DB constraint.
	//
	// actualPrivacy uses NULLIF($2, '') so the orchestrator's
	// pending fallback (empty string when videos.list read-back
	// errored) collapses to a true SQL NULL on the column. This
	// preserves the schema invariant the verdict pivots on:
	//   NULL  = no read-back yet (transient state)
	//   ''    = read-back completed AND YouTube reports empty
	//            privacy (a different failure mode — emitted only
	//            if YouTube returns a malformed status payload).
	// Both keep the DTO's `omitempty` behaviour intact.
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`UPDATE youtube_video_edits
		 SET status = 'published', updated_at = NOW(),
		     last_error = '',
		     actual_privacy = NULLIF($2, ''), youtube_sync_status = $3
		 WHERE id = $1 AND status = 'publishing'
		 RETURNING `+youtubeVideoEditSelectColumns,
		id, actualPrivacy, syncStatus,
	).Scan(
		&edit.ID, &edit.WorkspaceID, &edit.PlatformAccountID, &edit.YouTubeVideoID,
		&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
		&edit.DesiredPrivacy, &edit.PublishAt, &edit.Status, &edit.LastError,
		&edit.ActualPrivacy, &edit.YouTubeSyncStatus,
		&edit.CreatedAt, &edit.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("youtube video edit MarkPublishedWithActualPrivacy: %w", err)
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
			&edit.VeloxProjectID, &edit.SourceThumbnailURL, &edit.ThumbnailMediaID,
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

// FindOrCreateEditableSession atomically returns the open (non-terminal)
// editor session for the (workspaceID, platformAccountID, youtubeVideoID)
// triple, or creates a fresh one if no such row exists.
//
// Three-step race-safe sequence:
//   1. SELECT — lookup an existing open session for the triple.
//      If a row in ('editing','failed','publishing') state is found,
//      return it WITH its existing (id, velox_project_id) untouched so
//      the SPA reuses the same Dark Editor URL across clicks.
//   2. INSERT — if no row exists, mint a fresh (session_id, velox_project_id)
//      from the hint args (or auto-generate UUIDs when the hints are
//      empty) and pin status='editing'. Single-row INSERT; the partial
//      UNIQUE INDEX `uniq_youtube_video_edits_open_session` (migration
//      071) protects against two concurrent inserts for the same triple.
//   3. ON 23505 CONFLICT — if step 2 loses the race, the winning row was
//      inserted by a peer goroutine between our SELECT and INSERT.
//      Re-SELECT the triple; the partial UNIQUE INDEX guarantees
//      exactly one row is visible; return it. The rare case where the
//      re-SELECT returns (nil, nil) — meaning the row flipped to a
//      terminal state in the tiny window between INSERT-fail and
//      SELECT — surfaces the original 23505 sentinel so the operator
//      can re-trigger the click.
//
// Args:
//   - sessionIDHint: optional pre-generated UUID for the new row's ID.
//     Empty → repo auto-generates (`uuid.NewString`). The handler always
//     supplies a hint to keep id/velox_project_id generated in the same
//     logging boundary as the request, but the repo tolerates empty.
//   - projectIDHint: optional pre-generated `ve_<uuid>` for the new row's
//     Velox project id. Empty → repo auto-generates.
//
// Edge cases:
//   - workspaceID <= 0 / platformAccountID <= 0 / empty videoID → error
//     (no SQL executed). The handler-level CreateEditorSession already
//     runs the same gate; this is defence-in-depth.
//   - found pointer's ID/velox_project_id are returned as-is. The hint
//     args are silently discarded on the SELECT path — they only take
//     effect on the INSERT path.
func (r *YouTubeVideoEditRepository) FindOrCreateEditableSession(
	ctx context.Context,
	workspaceID int64,
	platformAccountID int64,
	youtubeVideoID string,
	sessionIDHint string,
	projectIDHint string,
) (*models.YouTubeVideoEdit, error) {
	if workspaceID <= 0 || platformAccountID <= 0 || youtubeVideoID == "" {
		return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession: invalid triple (workspaceID=%d platformAccountID=%d youtubeVideoID=%q)", workspaceID, platformAccountID, youtubeVideoID)
	}

	// Step 1 — SELECT fast path: an editor session in
	// ('editing','failed','publishing') state already exists for this
	// triple. Return it so the SPA keeps the same velox_project_id.
	existing, err := r.findOpenEditableSessionByTriple(ctx, workspaceID, platformAccountID, youtubeVideoID)
	if err != nil {
		return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession lookup: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Step 2 — INSERT: mint a fresh (session_id, velox_project_id)
	// from the hint args or auto-generate. The partial UNIQUE INDEX
	// (migration 071) protects this INSERT from racing siblings.
	now := time.Now().UTC()
	sessionID := sessionIDHint
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	projectID := projectIDHint
	if projectID == "" {
		projectID = "ve_" + uuid.NewString()
	}
	newRow := &models.YouTubeVideoEdit{
		ID:               sessionID,
		WorkspaceID:      workspaceID,
		PlatformAccountID: platformAccountID,
		YouTubeVideoID:    youtubeVideoID,
		VeloxProjectID:    projectID,
		DesiredPrivacy:    "public",
		Status:            "editing",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.Create(ctx, newRow); err == nil {
		return newRow, nil
	} else if !isPQUniqueViolation(err) {
		return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession insert: %w", err)
	} else {
		// Step 3 — RACE-LOSER: 23505 means a concurrent goroutine won
		// the partial-UNIQUE contest between step 1's SELECT and our
		// step 2 INSERT. Re-SELECT to pick up the winner's row.
		winner, err2 := r.findOpenEditableSessionByTriple(ctx, workspaceID, platformAccountID, youtubeVideoID)
		if err2 != nil {
			return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession re-lookup: %w", err2)
		}
		if winner == nil {
			// Defence-in-depth: the partial UNIQUE guarantees at
			// least one open row exists for this triple the moment
			// 23505 fires. If the row flipped through 'published'
			// before our re-SELECT, surface the original error so
			// the caller can re-trigger and let the new session be
			// minted (partial UNIQUE allows re-creating after
			// 'published').
			return nil, fmt.Errorf("youtube video edit FindOrCreateEditableSession: 23505 fired but re-SELECT found no open row (original=%v)", err)
		}
		return winner, nil
	}
}

// findOpenEditableSessionByTriple is the inner SELECT used by both
// the fast path and the race-loser re-lookup of
// FindOrCreateEditableSession. Returns (nil, nil) when no open row
// exists yet for the triple — the contract every caller branches on.
//
// Index hint: the partial UNIQUE INDEX `uniq_youtube_video_edits_open_session`
// (migration 071) covers exactly the predicate below, so this
// query is always an index-only scan regardless of the workspace's
// total row count.
func (r *YouTubeVideoEditRepository) findOpenEditableSessionByTriple(
	ctx context.Context,
	workspaceID int64,
	platformAccountID int64,
	youtubeVideoID string,
) (*models.YouTubeVideoEdit, error) {
	edit := &models.YouTubeVideoEdit{}
	err := r.db.QueryRowContext(ctx,
		`SELECT `+youtubeVideoEditSelectColumns+`
		 FROM youtube_video_edits
		 WHERE workspace_id = $1
		   AND platform_account_id = $2
		   AND youtube_video_id = $3
		   AND status IN ('editing', 'failed', 'publishing')
		 LIMIT 1`,
		workspaceID, platformAccountID, youtubeVideoID,
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
		return nil, fmt.Errorf("findOpenEditableSessionByTriple: %w", err)
	}
	return edit, nil
}

// isPQUniqueViolation reports whether err is a Postgres SQLSTATE 23505
// unique_violation raised by lib/pq. Centralised so the
// FindOrCreateEditableSession race-handler keeps a single source of
// truth for the violation check (and so other repository methods can
// borrow the helper when they grow the same pattern).
func isPQUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
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

// SaveDraft (P2 — Dark Editor auto-save) atomically writes the operator's
// mid-edit form values to youtube_video_edits.draft_* AND stamps
// dirty_flag=false AND draft_updated_at=$11 in a single SQL statement.
//
// CAS predicate: id=$1 AND status IN ('editing','failed'). The publish
// orchestrator owns the row during the 'publishing' window — racing a
// draft save against a publish would let an operator's keystrokes
// silently overwrite the privacy/title the orchestrator just pushed
// to YouTube. Once a row lands in 'published' state, the partial
// UNIQUE INDEX `uniq_youtube_video_edits_open_session` (migration
// 071) keeps 'published' rows invisible to the predicate; a re-edit
// click after a successful publish will mint a FRESH row through
// FindOrCreateEditableSession (CAS predicate matches open rows only).
//
// Returns ErrYouTubeVideoEditNotFound (wrapped) on 0-rows match — the
// handler maps to 409 (CAS-loss). A real *sql.DB error propagates
// wrapped. Idempotency: same payload, same final UPSERT, no timestamp
// drift per call (draftUpdatedAt is supplied by the handler so the
// response echo and the row stamp agree to the microsecond).
//
// draft_translations encoding (P2 architecture verdict Option A):
//   - non-nil map → json.Marshal then bind as the JSONB column;
//   - nil map → SQL NULL (lighter row than '{}', and the SPA can
//     distinguish "draft cleared translations" via the typed echo).
// We do NOT run YouTubePublishOptions.Validate() here — strict bounds
// live at the publish endpoint. Drafts by definition can be
// incomplete (mid-typing) or temporarily out-of-spec (the operator
// is shortening an over-long title).
func (r *YouTubeVideoEditRepository) SaveDraft(
	ctx context.Context,
	id string,
	title string,
	description string,
	tags []string,
	defaultLanguage string,
	defaultAudioLanguage string,
	translations map[string]models.YouTubeTranslation,
	desiredPrivacy string,
	draftUpdatedAt time.Time,
) error {
	// Nil → SQL NULL. Empty map → {} (so the SPA can distinguish
	// "operator just opened the editor" / "operator actively cleared").
	var translationsJSON interface{}
	if translations != nil {
		b, err := json.Marshal(translations)
		if err != nil {
			return fmt.Errorf("youtube video edit SaveDraft marshal translations: %w", err)
		}
		translationsJSON = b
	}
	// Nil tags → NULL (lighter than '{}}::text[]'). Empty
	// []string{} → '{}'::text[] so the SPA can distinguish
	// "all-tags-removed" from "no draft yet".
	var tagsVal interface{}
	if tags != nil {
		tagsVal = pq.Array(tags)
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_video_edits
		 SET draft_title = $2,
		     draft_description = $3,
		     draft_tags = $4,
		     draft_default_language = $5,
		     draft_default_audio_language = $6,
		     draft_translations = $7,
		     draft_desired_privacy = $8,
		     draft_updated_at = $9,
		     dirty_flag = FALSE,
		     updated_at = NOW()
		 WHERE id = $1
		   AND status IN ('editing','failed')`,
		id,
		nullableDraftString(title),
		nullableDraftString(description),
		tagsVal,
		nullableDraftString(defaultLanguage),
		nullableDraftString(defaultAudioLanguage),
		translationsJSON,
		nullableDraftString(desiredPrivacy),
		draftUpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("youtube video edit SaveDraft: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("youtube video edit SaveDraft rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrYouTubeVideoEditNotFound, id)
	}
	return nil
}

// nullableDraftString binds a string column from an optional form
// value WITHOUT coercing empty-string to NULL. The P2 architect
// verdict explicitly preserves two distinct states the operator can
// produce:
//
//   - NULL    = "no draft written yet" (column never touched)
//   - ""      = "operator actively cleared this field" (column was
//               written with an empty value, distinct from no-row)
//
// Coercing empty -> nil here would collapse both into the same SQL
// NULL, removing the operator's "I cleared the title" intent from
// the read-back. We bind "" as "" so the JSON DTO renders
// `draft_title: ""` (the SPA's "Bozza salvata" indicator shows the
// field was cleared) vs `draft_title` omitted when the column is
// genuinely NULL.
//
// The function is a thin type-coercion helper (string -> any); the
// name is preserved for grep-tracking against the architect verdict.
func nullableDraftString(s string) interface{} {
	return s
}
