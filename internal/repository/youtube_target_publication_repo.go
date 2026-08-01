package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
