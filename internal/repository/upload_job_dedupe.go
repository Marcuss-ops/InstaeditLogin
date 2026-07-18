package repository

import (
	"database/sql"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const duplicateUploadJobMessage = "duplicate source suppressed before upload"

// CreateIfSourceAbsent inserts job only when the same source has not already
// been queued, processed, or completed for the same user, Drive account, and
// target set. Failed rows are intentionally ignored so operators can retry a
// source after fixing a transient error.
//
// The advisory transaction lock serializes concurrent schedulers for the same
// source key. This keeps the check-and-insert atomic without spreading
// duplicate detection across commands, handlers, and workers.
func (r *UploadJobRepository) CreateIfSourceAbsent(job *models.UploadJob) (bool, error) {
	job.Targets = canonicalUploadTargets(job.Targets)
	targetsJSON, err := job.TargetsJSON()
	if err != nil {
		return false, fmt.Errorf("failed to marshal upload job targets: %w", err)
	}

	var scheduledAt sql.NullTime
	if job.ScheduledAt != nil {
		scheduledAt = sql.NullTime{Time: *job.ScheduledAt, Valid: true}
	}
	var folderID sql.NullString
	if job.FolderID != nil {
		folderID = sql.NullString{String: *job.FolderID, Valid: true}
	}

	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin upload job dedupe tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	lockKey := uploadJobSourceLockKey(job, targetsJSON)
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`, lockKey); err != nil {
		return false, fmt.Errorf("failed to lock upload job source: %w", err)
	}

	var exists bool
	if err := tx.QueryRow(
		`SELECT EXISTS (
			SELECT 1
			FROM upload_jobs
			WHERE user_id = $1
			  AND source_type = $2
			  AND source_id = $3
			  AND drive_account_id IS NOT DISTINCT FROM $4
			  AND targets = $5::jsonb
			  AND status <> 'failed'
		)`,
		job.UserID,
		string(job.SourceType),
		job.SourceID,
		job.DriveAccountID,
		targetsJSON,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check upload job source: %w", err)
	}

	if exists {
		if err := tx.Commit(); err != nil {
			return false, fmt.Errorf("failed to commit upload job dedupe tx: %w", err)
		}
		return false, nil
	}

	if err := tx.QueryRow(
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
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return false, fmt.Errorf("failed to create upload job: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit upload job creation: %w", err)
	}
	return true, nil
}

// SuppressPendingDuplicates marks redundant pending rows as failed while
// preserving the best existing row for each source key. Completed rows win,
// followed by processing rows and then the oldest pending row.
func (r *UploadJobRepository) SuppressPendingDuplicates(userID int64) (int64, error) {
	res, err := r.db.Exec(
		`WITH ranked AS (
			SELECT
				id,
				ROW_NUMBER() OVER (
					PARTITION BY user_id, source_type, source_id, COALESCE(drive_account_id, 0), targets
					ORDER BY
						CASE status
							WHEN 'completed' THEN 0
							WHEN 'processing' THEN 1
							WHEN 'pending' THEN 2
							ELSE 3
						END,
						id ASC
				) AS duplicate_rank
			FROM upload_jobs
			WHERE user_id = $1
			  AND status <> 'failed'
		), duplicates AS (
			SELECT id
			FROM ranked
			WHERE duplicate_rank > 1
		)
		UPDATE upload_jobs AS job
		SET status = 'failed',
			error_message = $2,
			updated_at = NOW()
		FROM duplicates
		WHERE job.id = duplicates.id
		  AND job.status = 'pending'`,
		userID,
		duplicateUploadJobMessage,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to suppress duplicate upload jobs: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read duplicate upload job count: %w", err)
	}
	return count, nil
}

func canonicalUploadTargets(targets []int64) []int64 {
	if len(targets) == 0 {
		return []int64{}
	}

	canonical := append([]int64(nil), targets...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i] < canonical[j] })

	write := 1
	for read := 1; read < len(canonical); read++ {
		if canonical[read] == canonical[write-1] {
			continue
		}
		canonical[write] = canonical[read]
		write++
	}
	return canonical[:write]
}

func uploadJobSourceLockKey(job *models.UploadJob, targetsJSON []byte) string {
	var driveAccountID int64
	if job.DriveAccountID != nil {
		driveAccountID = *job.DriveAccountID
	}
	return fmt.Sprintf("%d|%s|%s|%d|%s", job.UserID, job.SourceType, job.SourceID, driveAccountID, targetsJSON)
}
