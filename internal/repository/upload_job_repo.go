package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UploadJobRepository handles persistence for upload_jobs — the background
// queue that downloads videos from Google Drive and publishes them.
type UploadJobRepository struct {
	db *sql.DB
}

// NewUploadJobRepository creates a new UploadJobRepository.
func NewUploadJobRepository(db *sql.DB) *UploadJobRepository {
	return &UploadJobRepository{db: db}
}

// Create inserts a new upload job and returns the generated id plus timestamps.
// scheduled_at and folder_id are written as sql.Null* so the nullable
// migration-037 + migration-038 columns accept both NULL (legacy
// single-file imports without a folder scope) and a populated value.
// Note that Today we use folder_id only on batch-driven drive imports;
// single-file async imports leave it NULL and are excluded from per-folder
// status queries (the partial index covers only non-NULL rows).
func (r *UploadJobRepository) Create(job *models.UploadJob) error {
	targetsJSON, err := job.TargetsJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal upload job targets: %w", err)
	}

	var scheduledAt sql.NullTime
	if job.ScheduledAt != nil {
		scheduledAt = sql.NullTime{Time: *job.ScheduledAt, Valid: true}
	}
	var folderID sql.NullString
	if job.FolderID != nil {
		folderID = sql.NullString{String: *job.FolderID, Valid: true}
	}

	return r.db.QueryRow(
		`INSERT INTO upload_jobs
			(user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption, targets, status, scheduled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		job.UserID,
		job.WorkspaceID,
		string(job.SourceType),
		job.SourceID,
		job.DriveAccountID,
		folderID,
		job.Title,
		job.Caption,
		targetsJSON,
		string(job.Status),
		scheduledAt,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)
}

// FindByID returns the upload job with the given id, or (nil, nil) if not found.
func (r *UploadJobRepository) FindByID(id int64) (*models.UploadJob, error) {
	row := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, scheduled_at, created_at, updated_at
		 FROM upload_jobs
		 WHERE id = $1`,
		id,
	)
	return scanUploadJob(row)
}

// ClaimNext atomically claims the oldest pending upload job, transitioning
// its status to 'processing'. Returns (nil, nil) when no pending job exists.
func (r *UploadJobRepository) ClaimNext() (*models.UploadJob, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin upload job claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRow(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, scheduled_at, created_at, updated_at
		 FROM upload_jobs
		 WHERE status = 'pending'
		 ORDER BY created_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
	)
	job, err := scanUploadJob(row)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}

	_, err = tx.Exec(
		`UPDATE upload_jobs SET status = 'processing', updated_at = NOW() WHERE id = $1`,
		job.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to mark upload job as processing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit upload job claim: %w", err)
	}

	job.Status = models.UploadJobStatusProcessing
	return job, nil
}

// MarkCompleted updates the job to completed and stores the resulting post/asset IDs.
func (r *UploadJobRepository) MarkCompleted(id int64, postID int64, assetID string) error {
	_, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET status = 'completed', post_id = $2, asset_id = $3, error_message = NULL, updated_at = NOW()
		 WHERE id = $1`,
		id, postID, assetID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job completed: %w", err)
	}
	return nil
}

// MarkFailed updates the job to failed with an error message.
func (r *UploadJobRepository) MarkFailed(id int64, errMessage string) error {
	_, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET status = 'failed', error_message = $2, updated_at = NOW()
		 WHERE id = $1`,
		id, errMessage,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job failed: %w", err)
	}
	return nil
}

// AggregateByFolder returns a per-folder rollup of upload_jobs scoped to
// a single user. Authz: matches BOTH folder_id AND user_id so a folder id
// from another tenant cannot leak counts into the dashboard. Uses a
// single indexed FILTER aggregation (Postgres-specific) instead of
// N separate COUNT queries — one round-trip, one row returned.
//
// If no row matches, the returned BatchStatusSummary has all-zero counts
// and nil timestamps. The handler turns that into a 200 + zero
// dashboard rather than a 404, so an immediate-after-import poll does
// not jump into a not-found UI state.
func (r *UploadJobRepository) AggregateByFolder(folderID string, userID int64) (models.BatchStatusSummary, error) {
	row := r.db.QueryRow(
		`SELECT
			COUNT(*) FILTER (WHERE status = 'pending')    AS pending_count,
			COUNT(*) FILTER (WHERE status = 'processing') AS processing_count,
			COUNT(*) FILTER (WHERE status = 'completed')  AS completed_count,
			COUNT(*) FILTER (WHERE status = 'failed')     AS failed_count,
			MIN(scheduled_at) AS first_publish_at,
			MAX(scheduled_at) AS last_publish_at
		 FROM upload_jobs
		 WHERE folder_id = $1
		   AND user_id    = $2`,
		folderID, userID,
	)
	var summary models.BatchStatusSummary
	var firstAt, lastAt sql.NullTime
	if err := row.Scan(
		&summary.PendingCount,
		&summary.ProcessingCount,
		&summary.CompletedCount,
		&summary.FailedCount,
		&firstAt,
		&lastAt,
	); err != nil {
		return summary, fmt.Errorf("failed to aggregate upload_jobs by folder: %w", err)
	}
	// keep aligned with UploadJobStatus enum — a future new status
	// (e.g. 'cancelled') must add a COUNT FILTER clause above AND a
	// term in this sum, otherwise it silently drops off the dashboard.
	summary.TotalCount = summary.PendingCount + summary.ProcessingCount + summary.CompletedCount + summary.FailedCount
	if firstAt.Valid {
		t := firstAt.Time
		summary.FirstPublishAt = &t
	}
	if lastAt.Valid {
		t := lastAt.Time
		summary.LastPublishAt = &t
	}
	return summary, nil
}

func scanUploadJob(row *sql.Row) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var scheduledAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString

	err := row.Scan(
		&job.ID,
		&job.UserID,
		&job.WorkspaceID,
		&rawSource,
		&job.SourceID,
		&driveAccountID,
		&folderID,
		&job.Title,
		&job.Caption,
		&targetsJSON,
		&rawStatus,
		&errorMessage,
		&job.PostID,
		&assetID,
		&scheduledAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan upload job: %w", err)
	}

	job.SourceType = models.UploadJobSource(rawSource)
	job.Status = models.UploadJobStatus(rawStatus)
	if driveAccountID.Valid {
		v := driveAccountID.Int64
		job.DriveAccountID = &v
	}
	if folderID.Valid {
		v := folderID.String
		job.FolderID = &v
	}
	if scheduledAt.Valid {
		t := scheduledAt.Time
		job.ScheduledAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}
