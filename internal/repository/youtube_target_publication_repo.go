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

// ErrYouTubeTargetPublicationNotFound is the sentinel returned when an
// Update / Mark* call hits 0 rows. Callers use errors.Is to detect the
// case and either re-claim or 404 the API call.
var ErrYouTubeTargetPublicationNotFound = errors.New("youtube target publication not found")

// ErrYouTubeUploadedEmptyVideoID (Blocco #1 followup — Finding #3
// split-tx drift fix) is the typed-sentinel returned by
// MarkYouTubeUploadedAtomic when videoID == "". The pre-flight
// guard rejects empty ids BEFORE issuing any UPDATE so the row
// stays in the pre-call state (no `attempt_count++`, no status
// flip, no `youtube_video_id` stamp). Wrapped alongside the
// publish id so the worker log surfaces BOTH the failure mode
// and which row the guard fired on. Use errors.Is to branch
// from caller code (vs. parsing error strings).
var ErrYouTubeUploadedEmptyVideoID = errors.New("youtube target publication MarkYouTubeUploadedAtomic: empty videoID (rejected pre-flight; row not mutated)")

// YouTubeTargetPublicationStore is the persistence contract other packages
// (workers, API handlers) depend on. Mirrors the conventions of
// youtube_video_edit_repo.go::YouTubeVideoEditStore — declared next to
// the concrete implementation so callers can either bind the interface
// in tests or wire the concrete *Repository at bootstrap.
type YouTubeTargetPublicationStore interface {
	Create(ctx context.Context, pub *models.YouTubeTargetPublication) error
	FindByID(ctx context.Context, id int64) (*models.YouTubeTargetPublication, error)
	FindByPostTargetID(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error)
	FindByYouTubeVideoID(ctx context.Context, videoID string) (*models.YouTubeTargetPublication, error)
	ListByUploadJobID(ctx context.Context, uploadJobID int64) ([]*models.YouTubeTargetPublication, error)
	Update(ctx context.Context, pub *models.YouTubeTargetPublication) error
	MarkYouTubeUploaded(ctx context.Context, id int64, videoID string) error
	MarkThumbnailReady(ctx context.Context, id int64, mediaID string) error
	IncrementAttempt(ctx context.Context, id int64, lastError string) error
	// MarkYouTubeUploadedAtomic (Blocco #1 followup — Finding #3
	// split-tx drift fix) combines IncrementAttempt-by-1 + the
	// MarkYouTubeUploaded flip + youtube_uploaded_at + youtube_video_id
	// stamp into ONE Postgres UPDATE statement. Row-level UPDATE in
	// Postgres is ACID-atomic, so the chunked-PUT-success and the
	// attempt-counter increment cannot desync from one another —
	// either both fields commit, or neither does. The pre-flight
	// guard rejects videoID="" upfront so the method does NOT start
	// a tx / issue an UPDATE for an empty video id (a worker bug
	// upstream would otherwise leave the row in the
	// `youtube_uploading` state with attempt_count bumped, mirroring
	// the pre-fix failure mode). Returns ErrYouTubeTargetPublicationNotFound
	// when 0 rows match the id — the upload_worker surfaces this as
	// a parent-job retry signal (same shape as MarkYouTubeUploaded).
	MarkYouTubeUploadedAtomic(ctx context.Context, id int64, videoID string) error
	// ClearYouTubeUpload (Blocco #1 followup — Finding #4
	// orphan-video recovery) nullifies the Phase-1 youtube_video_id
	// stamp + resets status to 'upload_session_initiated' and
	// attempt_count to 0 in ONE Postgres UPDATE. Called by
	// PublishWorker.publishTarget when videos.update reports a 404
	// on the Phase-1 stamped video_id (YouTube Studio deletion,
	// moderator takedown, etc.) so the next tick does NOT re-take
	// the bypass branch with a dead video_id. The fresh
	// publisher.Publish call that follows the ClearYouTubeUpload
	// upload-progresses through upload_worker.uploadVideoAsPrivateForTarget
	// which stamps a NEW video_id via MarkYouTubeUploadedAtomic.
	// Returns ErrYouTubeTargetPublicationNotFound on 0 rows.
	ClearYouTubeUpload(ctx context.Context, id int64) error
	MarkPublished(ctx context.Context, id int64) error
	// MarkYouTubeProcessed (migration 067, Blocco #3 P0) flips
	// youtube_processing_status to 'processed' AND stamps
	// youtube_processed_at = NOW() in one atomic statement. Called
	// by the reconcile worker / YouTube webhook when
	// processingStatus='processed' is observed on the live API.
	MarkYouTubeProcessed(ctx context.Context, id int64) error
	// ListPendingEditorSessionTargets (Blocco #4 P0) returns YT pub
	// rows in 'processed' state that haven't yet been linked to an
	// editor session (editor_session_id IS NULL). Bounded by `limit`
	// so a tick that finds thousands of backlog rows doesn't tie up
	// the DB; the reconciler schedules a follow-up tick to drain.
	// Ordered by id ASC so older rows (longer wait) get prioritised
	// over newer ones — preserves FIFO for cross-replica fairness
	// when multiple reconciler replicas agree on a backlog order.
	ListPendingEditorSessionTargets(ctx context.Context, limit int) ([]*models.YouTubeTargetPublication, error)
	// MarkEditorSessionCreated (Blocco #4 P0) is the atomic CAS-link
	// from a created youtube_video_edits session to a YT pub row.
	// The predicate `editor_session_id IS NULL` guarantees that two
	// reconcilers racing the same YT pub row can't both stamp the
	// session (the SECOND one's UPDATE matches 0 rows). The
	// migration-068 UNIQUE constraint on editor_session_id adds
	// defence-in-depth at the DB layer.
	MarkEditorSessionCreated(ctx context.Context, id int64, editorSessionID, veloxProjectID string) error
}

// YouTubeTargetPublicationRepository is the concrete *sql.DB-backed
// implementation of YouTubeTargetPublicationStore.
type YouTubeTargetPublicationRepository struct {
	db *sql.DB
}

// NewYouTubeTargetPublicationRepository returns a Repository bound to db.
// Callers typically wire this exactly once at bootstrap.
func NewYouTubeTargetPublicationRepository(db *sql.DB) *YouTubeTargetPublicationRepository {
	return &YouTubeTargetPublicationRepository{db: db}
}

// ytTargetPubsSelectColumns lists every youtube_target_publications column
// in a fixed order that the row-scanner below mirrors. Column-list-vs-
// Scan-list is a manual invariant — keep these two lists in sync when
// adding new columns.
//
// Blocco #3 P0 (migration 067) — YouTubeUploadedAt +
// YouTubeProcessedAt slots in AFTER the processing-status string
// (sisters of the two status enums they timestamp).
const ytTargetPubsSelectColumns = `
	id, upload_job_id, post_target_id, platform_account_id,
	youtube_video_id, youtube_upload_status, youtube_processing_status,
	youtube_uploaded_at, youtube_processed_at,
	editor_session_id, velox_project_id, thumbnail_media_id, thumbnail_status,
	desired_privacy, publish_at, published_at, last_error, attempt_count,
	created_at, updated_at`

// ytPubsRowScanner matches both *sql.Row and *sql.Rows via their shared
// Scan signature, so we can reuse the same column-list scan for one-off
// QueryRowContext calls AND for QueryContext iteration loops.
// Prefixed ytPubs (youtube_target_publications) to avoid collision with
// apikey_repo's package-level `rowScanner` (same signature, different table).
type ytPubsRowScanner interface {
	Scan(dest ...any) error
}

// scanYouTubeTargetPublication reads one ytTargetPubsSelectColumns-shaped
// row into pub. Mirrors youtube_video_edit_repo's scan style (separate
// NullString/NullTime locals → populate pointer fields on success).
//
// Blocco #3 P0 (migration 067) — YouTubeUploadedAt + YouTubeProcessedAt
// scan as sql.NullTime so pre-067 rows (NULL on both new columns) and
// post-067 rows (NOW()-stamped by the Mark* helpers) both round-trip
// cleanly.
func scanYouTubeTargetPublication(s ytPubsRowScanner, pub *models.YouTubeTargetPublication) error {
	var (
		youtubeVideoID          sql.NullString
		youtubeProcessingStatus sql.NullString
		youtubeUploadedAt       sql.NullTime
		youtubeProcessedAt      sql.NullTime
		editorSessionID         sql.NullString
		veloxProjectID          sql.NullString
		thumbnailMediaID        sql.NullString
		thumbnailStatus         sql.NullString
		publishAt               sql.NullTime
		publishedAt             sql.NullTime
	)
	if err := s.Scan(
		&pub.ID, &pub.UploadJobID, &pub.PostTargetID, &pub.PlatformAccountID,
		&youtubeVideoID, &pub.YouTubeUploadStatus, &youtubeProcessingStatus,
		&youtubeUploadedAt, &youtubeProcessedAt,
		&editorSessionID, &veloxProjectID, &thumbnailMediaID, &thumbnailStatus,
		&pub.DesiredPrivacy, &publishAt, &publishedAt, &pub.LastError, &pub.AttemptCount,
		&pub.CreatedAt, &pub.UpdatedAt,
	); err != nil {
		return err
	}
	pub.YouTubeVideoID = ytPubsNullStringPtr(youtubeVideoID)
	pub.YouTubeProcessingStatus = ytPubsNullStringPtr(youtubeProcessingStatus)
	pub.YouTubeUploadedAt = ytPubsNullTimePtr(youtubeUploadedAt)
	pub.YouTubeProcessedAt = ytPubsNullTimePtr(youtubeProcessedAt)
	pub.EditorSessionID = ytPubsNullStringPtr(editorSessionID)
	pub.VeloxProjectID = ytPubsNullStringPtr(veloxProjectID)
	pub.ThumbnailMediaID = ytPubsNullStringPtr(thumbnailMediaID)
	pub.ThumbnailStatus = ytPubsNullStringPtr(thumbnailStatus)
	pub.PublishAt = ytPubsNullTimePtr(publishAt)
	pub.PublishedAt = ytPubsNullTimePtr(publishedAt)
	return nil
}

// ytPubsNullStringPtr / ytPubsNullTimePtr / ytPubsNullableString /
// ytPubsNullableTime are prefixed to avoid collision with
// apikey_repo (rowScanner) and capabilities_repo (nullableString which
// has a different signature — takes string, returns any).
func ytPubsNullStringPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

func ytPubsNullTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

func ytPubsNullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func ytPubsNullableTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// Create inserts a new youtube_target_publications row. Server-side fields
// (id, created_at, updated_at) are returned via the INSERT...RETURNING
// clause so the caller's struct is fully populated after the call.
//
// Returns a UNIQUE-violation error (lib/pq wraps as *pq.Error.Code 23505)
// when post_target_id already has a publication row — the caller's job
// to fall through to FindByPostTargetID and UPDATE the existing row
// instead.
//
// Blocco #3 P0 (migration 067) — youtube_uploaded_at and
// youtube_processed_at are inserted as NULL by default; the Mark*
// helpers transition them to NOW() at the moment of status change.
// Treating them as DB-default-NULL keeps Create's signature narrow and
// prevents any caller from stamping a fake "uploaded_at" without going
// through MarkYouTubeUploaded's atomic state-machine check.
func (r *YouTubeTargetPublicationRepository) Create(ctx context.Context, pub *models.YouTubeTargetPublication) error {
	return r.db.QueryRowContext(ctx,
		`INSERT INTO youtube_target_publications
			(upload_job_id, post_target_id, platform_account_id,
			 youtube_video_id, youtube_upload_status, youtube_processing_status,
			 editor_session_id, velox_project_id, thumbnail_media_id, thumbnail_status,
			 desired_privacy, publish_at, last_error, attempt_count)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 RETURNING id, created_at, updated_at`,
		pub.UploadJobID, pub.PostTargetID, pub.PlatformAccountID,
		ytPubsNullableString(pub.YouTubeVideoID), pub.YouTubeUploadStatus, ytPubsNullableString(pub.YouTubeProcessingStatus),
		ytPubsNullableString(pub.EditorSessionID), ytPubsNullableString(pub.VeloxProjectID),
		ytPubsNullableString(pub.ThumbnailMediaID), ytPubsNullableString(pub.ThumbnailStatus),
		pub.DesiredPrivacy, ytPubsNullableTime(pub.PublishAt), pub.LastError, pub.AttemptCount,
	).Scan(&pub.ID, &pub.CreatedAt, &pub.UpdatedAt)
}

// FindByID returns the row with the given id, or (nil, nil) when no row
// matches.
func (r *YouTubeTargetPublicationRepository) FindByID(ctx context.Context, id int64) (*models.YouTubeTargetPublication, error) {
	pub := &models.YouTubeTargetPublication{}
	if err := scanYouTubeTargetPublication(r.db.QueryRowContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE id = $1`, id), pub); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("youtube target publication FindByID: %w", err)
	}
	return pub, nil
}

// FindByPostTargetID returns the publication row attached to the given
// post_target id, or (nil, nil) when no row matches. Fast O(1) lookup
// (UNIQUE constraint index).
func (r *YouTubeTargetPublicationRepository) FindByPostTargetID(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error) {
	pub := &models.YouTubeTargetPublication{}
	if err := scanYouTubeTargetPublication(r.db.QueryRowContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE post_target_id = $1`, postTargetID), pub); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("youtube target publication FindByPostTargetID: %w", err)
	}
	return pub, nil
}

// FindByYouTubeVideoID returns the publication row tied to a YouTube
// video id (the partial index idx_youtube_target_pubs_video_id makes this
// O(1)). Or (nil, nil) on miss.
func (r *YouTubeTargetPublicationRepository) FindByYouTubeVideoID(ctx context.Context, videoID string) (*models.YouTubeTargetPublication, error) {
	pub := &models.YouTubeTargetPublication{}
	if err := scanYouTubeTargetPublication(r.db.QueryRowContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE youtube_video_id = $1`, videoID), pub); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("youtube target publication FindByYouTubeVideoID: %w", err)
	}
	return pub, nil
}

// ListByPostTargetIDs (Blocco Carosello content-pipeline endpoint) returns
// every youtube_target_publications row whose post_target_id appears
// in the supplied slice. (nil, nil) when postTargetIDs is empty or
// no rows match. One round-trip using WHERE post_target_id =
// ANY($1::bigint[]) so the response scales with the post's target
// fan-out (typically 1..30 rows) rather than the full table. The
// post_target_id = ANY predicate hits the UNIQUE index introduced by
// migration 066's UNIQUE(post_target_id) constraint, so the planner
// uses an index-only scan.
func (r *YouTubeTargetPublicationRepository) ListByPostTargetIDs(ctx context.Context, postTargetIDs []int64) ([]*models.YouTubeTargetPublication, error) {
	if len(postTargetIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE post_target_id = ANY($1::bigint[])
		 ORDER BY id ASC`, pq.Array(postTargetIDs))
	if err != nil {
		return nil, fmt.Errorf("youtube target publication ListByPostTargetIDs: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("youtube target publication ListByPostTargetIDs scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube target publication ListByPostTargetIDs rows: %w", err)
	}
	return out, nil
}

// ListByUploadJobID returns all publication rows tied to an upload job,
// ordered by id ASC. The pipeline view endpoint reads this and joins onto
// drive/storage accounts; empty result is a valid "no fan-out yet" state.
func (r *YouTubeTargetPublicationRepository) ListByUploadJobID(ctx context.Context, uploadJobID int64) ([]*models.YouTubeTargetPublication, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE upload_job_id = $1
		 ORDER BY id ASC`, uploadJobID)
	if err != nil {
		return nil, fmt.Errorf("youtube target publication ListByUploadJobID: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("youtube target publication ListByUploadJobID scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube target publication ListByUploadJobID rows: %w", err)
	}
	return out, nil
}

// Update persists arbitrary lifecycle changes to a publication row. Use
// the narrow Mark* methods for the common transitions; Update is the
// catch-all for callers driving the row through states the schema doesn't
// know about (e.g. external reconciliation setting both thumbnail_status
// and youtube_upload_status in one pass).
//
// Returns ErrYouTubeTargetPublicationNotFound (wrapped) when 0 rows
// match — distinct from a real *sql.DB error so callers can branch.
func (r *YouTubeTargetPublicationRepository) Update(ctx context.Context, pub *models.YouTubeTargetPublication) error {
	// Blocco #3 P0 — Update persists caller-supplied timestamp values
	// (e.g. operator backfill via a one-off script) verbatim. The
	// recommended transition path is the Mark* helpers (which stamp
	// NOW() atomically); Update is the catch-all for callers driving
	// the row through states the schema doesn't know about. We do
	// NOT touch youtube_uploaded_at/youtube_processed_at from Update's
	// SQL — those columns are exclusively Mark* managed to keep the
	// "timestamp implies status transition" invariant observable.
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET upload_job_id = $2, post_target_id = $3, platform_account_id = $4,
		     youtube_video_id = $5, youtube_upload_status = $6, youtube_processing_status = $7,
		     editor_session_id = $8, velox_project_id = $9, thumbnail_media_id = $10,
		     thumbnail_status = $11, desired_privacy = $12, publish_at = $13, published_at = $14,
		     last_error = $15, attempt_count = $16, updated_at = NOW()
		 WHERE id = $1`,
		pub.ID, pub.UploadJobID, pub.PostTargetID, pub.PlatformAccountID,
		ytPubsNullableString(pub.YouTubeVideoID), pub.YouTubeUploadStatus, ytPubsNullableString(pub.YouTubeProcessingStatus),
		ytPubsNullableString(pub.EditorSessionID), ytPubsNullableString(pub.VeloxProjectID),
		ytPubsNullableString(pub.ThumbnailMediaID), ytPubsNullableString(pub.ThumbnailStatus),
		pub.DesiredPrivacy, ytPubsNullableTime(pub.PublishAt), ytPubsNullableTime(pub.PublishedAt),
		pub.LastError, pub.AttemptCount,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication Update: %w", err)
	}
	return r.checkRowsAffected(res, pub.ID)
}

// MarkYouTubeUploaded stamps the YouTube video_id returned by the
// resumable upload API and transitions youtube_upload_status to
// 'youtube_uploaded'. Idempotent: a second call overwrites the
// video_id with whatever YouTube echoed (defensive against retries that
// picked a different URI on resume).
//
// Blocco #3 P0 (migration 067) — also stamps youtube_uploaded_at = NOW()
// atomically with the status transition. The composite write means
// any DB-shape check that asserts "youtube_upload_status='youtube_uploaded'
// ⇒ youtube_uploaded_at IS NOT NULL" holds without needing a trigger.
// The timestamp is NOT refreshed on a re-Mark (idempotency deliberately
// keeps the FIRST transition timestamp so the operator-triage
// dashboard's "time-to-upload" SLA accurate).
func (r *YouTubeTargetPublicationRepository) MarkYouTubeUploaded(ctx context.Context, id int64, videoID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_video_id = $2,
		     youtube_upload_status = 'youtube_uploaded',
		     youtube_uploaded_at = COALESCE(youtube_uploaded_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		id, videoID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkYouTubeUploaded: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkThumbnailReady is the per-target equivalent of the global thumbnail
// ready event: stamps the media_assets.id the thumbnail worker resolved
// to AND transitions thumbnail_status to 'thumbnail_ready' atomically.
// Composite 1-row transition so the unified pipeline view never renders
// a half-state.
func (r *YouTubeTargetPublicationRepository) MarkThumbnailReady(ctx context.Context, id int64, mediaID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET thumbnail_media_id = $2, thumbnail_status = 'thumbnail_ready', updated_at = NOW()
		 WHERE id = $1`,
		id, mediaID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkThumbnailReady: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// IncrementAttempt bumps attempt_count and stamps last_error in one
// statement. The worker's Claim+increment pattern stays the right shape
// — pair IncrementAttempt with whichever transition the worker is
// retrying (e.g. MarkThumbnailReady once Velox responds 200).
//
// Blocco #1 followup — Finding #3 split-tx drift fix: IncrementAttempt
// is now the **FAILURE-PATH ONLY** bump. The SUCCESS path uses
// MarkYouTubeUploadedAtomic (which folds the attempt++ with the
// status flip + video_id stamp into one atomic UPDATE). Do NOT
// replace IncrementAttempt with MarkYouTubeUploadedAtomic on
// failure paths — IncrementAttempt stamps `last_error = "upload
// failed: ..."` for the operator-triage dashboard's retry-observability
// view; a status flip + video_id stamp on a failed-but-not-uploaded
// row would silently corrupt the unified-pipeline view. Call sites:
//   - FAILURE path (UploadVideoAsPrivate returns err): IncrementAttempt(...)
//   - SUCCESS path (UploadVideoAsPrivate returns non-empty videoID): MarkYouTubeUploadedAtomic(...)
func (r *YouTubeTargetPublicationRepository) IncrementAttempt(ctx context.Context, id int64, lastError string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET attempt_count = attempt_count + 1, last_error = $2, updated_at = NOW()
		 WHERE id = $1`,
		id, lastError,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication IncrementAttempt: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkYouTubeUploadedAtomic (Blocco #1 followup — Finding #3) is the
// success-path atomic transition: it folds IncrementAttempt-by-1
// + the status flip to 'youtube_uploaded' + youtube_video_id stamp
// + the youtube_uploaded_at timestamp (only on first transition, via
// COALESCE so re-runs preserve the FIRST transition timestamp) +
// updated_at touch into one UPDATE. Postgres row-level UPDATE is
// ACID-atomic so the attempt counter and the terminal-state stamp
// cannot desync if the worker crashes mid-call (the pre-fix split
// form — separate IncrementAttempt (failure) + MarkYouTubeUploaded
// (success) on different code paths — could leave a row with
// `attempt_count++ + status='youtube_uploading'` on a partial
// commit, making the next claim's idempotent-skip false and producing
// an orphan videos.insert). Upfront guards: videoID == "" is
// rejected with a typed error, no DB call. Returns
// ErrYouTubeTargetPublicationNotFound on 0 rows (same shape as
// MarkYouTubeUploaded).
func (r *YouTubeTargetPublicationRepository) MarkYouTubeUploadedAtomic(ctx context.Context, id int64, videoID string) error {
	// Pre-flight guard (Finding #3). Empty videoID MUST be rejected
	// BEFORE issuing any UPDATE — otherwise the row would be
	// left in `status='youtube_uploading'` with attempt_count++, the
	// exact pre-fix failure mode. The typed sentinel wraps the id
	// so the worker log can attribute the rejection precisely.
	if videoID == "" {
		return fmt.Errorf("%w: pub=%d", ErrYouTubeUploadedEmptyVideoID, id)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET attempt_count = attempt_count + 1,
		     youtube_video_id = $2,
		     youtube_upload_status = 'youtube_uploaded',
		     youtube_uploaded_at = COALESCE(youtube_uploaded_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		id, videoID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkYouTubeUploadedAtomic: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// ClearYouTubeUpload (Blocco #1 followup — Finding #4 orphan-video
// recovery) nullifies the Phase-1 youtube_video_id stamp and resets
// youtube_upload_status / attempt_count to their pre-Publish
// initial values in ONE Postgres UPDATE so a future publish-worker
// tick that picks up the same post_target_id does NOT re-take the
// bypass branch with a dead video_id. Called by
// PublishWorker.publishTarget when videos.update returns a 404 on
// the Phase-1 stamped video_id (user deleted the orphan via YouTube
// Studio, moderator takedown, etc.). The next tick sees
// youtube_upload_status='upload_session_initiated' + NULL
// youtube_video_id and falls through to publisher.Publish +
// upload_worker.uploadVideoAsPrivateForTarget, which freshly uploads
// and stamps a new video_id via MarkYouTubeUploadedAtomic.
//
// Single-statement UPDATE = ACID-atomic at the row level (same
// isolation guarantee as MarkYouTubeUploadedAtomic). Returns
// ErrYouTubeTargetPublicationNotFound on 0 rows (matches the
// MarkYouTubeUploaded / MarkYouTubeUploadedAtomic shape so
// callers can use a single typed-sentinel branch).
func (r *YouTubeTargetPublicationRepository) ClearYouTubeUpload(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_video_id = NULL,
		     youtube_upload_status = 'upload_session_initiated',
		     attempt_count = 0,
		     updated_at = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication ClearYouTubeUpload: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkPublished stamps published_at and touches updated_at. youtube_upload_status
// is intentionally NOT bumped here — 'youtube_uploaded' continues to
// describe YouTube-side state; the publish phase's terminal moment lives
// on published_at alone. If the worker wants to surface a column for
// "publish phase done", the future migration can add an enum value.
func (r *YouTubeTargetPublicationRepository) MarkPublished(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET published_at = NOW(), updated_at = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkPublished: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// ListPendingEditorSessionTargets (Blocco #4 P0) returns YT pub rows
// where youtube_processing_status='processed' AND editor_session_id IS
// NULL, ordered by id ASC, bounded by limit. Used by
// youtube_processing_reconciler. (nil, nil) when no rows match —
// distinct from a real *sql.DB error so callers can branch.
func (r *YouTubeTargetPublicationRepository) ListPendingEditorSessionTargets(ctx context.Context, limit int) ([]*models.YouTubeTargetPublication, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+ytTargetPubsSelectColumns+`
		 FROM youtube_target_publications
		 WHERE youtube_processing_status = 'processed'
		   AND editor_session_id IS NULL
		 ORDER BY id ASC
		 LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("youtube target publication ListPendingEditorSessionTargets: %w", err)
	}
	defer rows.Close()

	var out []*models.YouTubeTargetPublication
	for rows.Next() {
		pub := &models.YouTubeTargetPublication{}
		if err := scanYouTubeTargetPublication(rows, pub); err != nil {
			return nil, fmt.Errorf("youtube target publication ListPendingEditorSessionTargets scan: %w", err)
		}
		out = append(out, pub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("youtube target publication ListPendingEditorSessionTargets rows: %w", err)
	}
	return out, nil
}

// MarkEditorSessionCreated (Blocco #4 P0) is the atomic CAS-link from
// a created youtube_video_edits session back to the YT pub row.
// Predicate `editor_session_id IS NULL` (combined with the
// ytTargetPubsSelectColumns row state read-out from the caller) means
// the FIRST CAS wins; a second reconciler that races the same YT pub
// row matches 0 rows and surfaces ErrYouTubeTargetPublicationNotFound
// — clean branch for the caller (skip + log, no partial write).
//
// Migration 068's UNIQUE constraint on editor_session_id is the
// defence-in-depth layer: if a future refactor accidentally drops the
// predicate, the UNIQUE index still prevents a duplicate stamp at the
// cost of an INSERT failure. The composite 1-row UPDATE keeps the
// reconciler's success path to a single SQL roundtrip.
func (r *YouTubeTargetPublicationRepository) MarkEditorSessionCreated(ctx context.Context, id int64, editorSessionID, veloxProjectID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET editor_session_id = $2,
		     velox_project_id = $3,
		     updated_at = NOW()
		 WHERE id = $1
		   AND editor_session_id IS NULL`,
		id, editorSessionID, veloxProjectID,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkEditorSessionCreated: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

// MarkYouTubeProcessed (Blocco #3 P0, migration 067) flips
// youtube_processing_status to 'processed' AND stamps
// youtube_processed_at = NOW() in one atomic statement. Called by:
//   - the YouTube webhook callback when processingStatus='processed'
//     arrives via the topics/push notification channel;
//   - the future reconcile worker poll (YouTube videos.list every
//     60s for in-progress 'processing' rows).
//
// COALESCE on youtube_processed_at keeps idempotency: a second
// reconcile poll that finds the row already 'processed' won't
// re-stamp the timestamp (preserves "time-to-processed" SLA
// accuracy across retries).
//
// Returns ErrYouTubeTargetPublicationNotFound when 0 rows match —
// distinct from a real DB error so callers can branch on
// errors.Is(..., ErrYouTubeTargetPublicationNotFound).
func (r *YouTubeTargetPublicationRepository) MarkYouTubeProcessed(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE youtube_target_publications
		 SET youtube_processing_status = 'processed',
		     youtube_processed_at = COALESCE(youtube_processed_at, NOW()),
		     updated_at = NOW()
		 WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("youtube target publication MarkYouTubeProcessed: %w", err)
	}
	return r.checkRowsAffected(res, id)
}

func (r *YouTubeTargetPublicationRepository) checkRowsAffected(res sql.Result, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("youtube target publication rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrYouTubeTargetPublicationNotFound, id)
	}
	return nil
}
