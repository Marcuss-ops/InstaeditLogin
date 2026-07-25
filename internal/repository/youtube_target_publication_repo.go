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
	MarkPublished(ctx context.Context, id int64) error
	// MarkYouTubeProcessed (migration 067, Blocco #3 P0) flips
	// youtube_processing_status to 'processed' AND stamps
	// youtube_processed_at = NOW() in one atomic statement. Called
	// by the reconcile worker / YouTube webhook when
	// processingStatus='processed' is observed on the live API.
	MarkYouTubeProcessed(ctx context.Context, id int64) error
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
