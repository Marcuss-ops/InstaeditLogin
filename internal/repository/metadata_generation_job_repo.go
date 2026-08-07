package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// MetadataGenerationJobRepository persists metadata_generation_jobs.
// Method style mirrors OutboxRepository: no context.Context in the
// repository (the worker wraps in its own ctx-aware loop), sentinel
// errors for not-found / empty-queue, (nil, nil) for queriers.
type MetadataGenerationJobRepository struct {
	db *sql.DB
}

// NewMetadataGenerationJobRepository creates a new repository.
func NewMetadataGenerationJobRepository(db *sql.DB) *MetadataGenerationJobRepository {
	return &MetadataGenerationJobRepository{db: db}
}

// ErrMetadataGenAlreadyClaimed is returned by ClaimNext when the queue
// is empty (no pending row available to claim). The worker treats this
// as "nothing to do right now" and sleeps until the next tick.
var ErrMetadataGenAlreadyClaimed = errors.New("metadata_generation: no pending row available to claim")

// ErrMetadataGenGone is returned by Mark* / RenewLease when the row no
// longer exists (deleted, or a peer already finished it).
var ErrMetadataGenGone = errors.New("metadata_generation: row not found")

// Create inserts a new job in status='queued'. Returns the assigned id.
// The caller must set WorkspaceID, VeloxProjectID, and Prompt.
func (r *MetadataGenerationJobRepository) Create(job *models.MetadataGenerationJob) error {
	job.Status = models.MetadataGenJobQueued
	job.AttemptCount = 0
	job.MaxAttempts = 3
	err := r.db.QueryRow(
		`INSERT INTO metadata_generation_jobs (workspace_id, velox_project_id, prompt, max_attempts)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		job.WorkspaceID, job.VeloxProjectID, job.Prompt, job.MaxAttempts,
	).Scan(&job.ID, &job.CreatedAt)
	if err != nil {
		return fmt.Errorf("metadata_generation Insert: %w", err)
	}
	return nil
}

// FindByID returns a job by primary key. Returns (nil, nil) when not
// found (caller checks for nil vs error).
func (r *MetadataGenerationJobRepository) FindByID(id int64) (*models.MetadataGenerationJob, error) {
	row := r.db.QueryRow(
		`SELECT id, workspace_id, velox_project_id, prompt, status, result, error_message,
		        attempt_count, max_attempts, next_attempt_at, locked_by, locked_at,
		        created_at, updated_at, completed_at
		 FROM metadata_generation_jobs WHERE id = $1`, id)
	job, err := scanMetadataGenJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("metadata_generation FindByID: %w", err)
	}
	return job, nil
}

// ClaimNext atomically claims one pending job using SKIP LOCKED.
// Returns ErrMetadataGenAlreadyClaimed when the queue is empty.
func (r *MetadataGenerationJobRepository) ClaimNext(leaseID string, leaseTTL time.Duration) (*models.MetadataGenerationJob, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("metadata_generation ClaimNext begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	job, err := r.claimNextInTx(tx, leaseID, leaseTTL)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("metadata_generation ClaimNext commit: %w", err)
	}
	return job, nil
}

func (r *MetadataGenerationJobRepository) claimNextInTx(tx *sql.Tx, leaseID string, leaseTTL time.Duration) (*models.MetadataGenerationJob, error) {
	row := tx.QueryRow(
		`SELECT id, workspace_id, velox_project_id, prompt, status, result, error_message,
		        attempt_count, max_attempts, next_attempt_at, locked_by, locked_at,
		        created_at, updated_at, completed_at
		 FROM metadata_generation_jobs
		 WHERE status = 'queued'
		   AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		 ORDER BY id ASC
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED`)
	job, err := scanMetadataGenJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMetadataGenAlreadyClaimed
		}
		return nil, fmt.Errorf("metadata_generation ClaimNext select: %w", err)
	}

	now := time.Now().UTC()
	_, err = tx.Exec(
		`UPDATE metadata_generation_jobs
		 SET status = 'processing', locked_by = $1, locked_at = $2, updated_at = $2
		 WHERE id = $3 AND status = 'queued'`,
		leaseID, now, job.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("metadata_generation ClaimNext update: %w", err)
	}
	job.Status = models.MetadataGenJobProcessing
	job.LockedBy = leaseID
	job.LockedAt = &now
	return job, nil
}

// RenewLease extends the lock on a processing row. Returns
// ErrMetadataGenGone when the row is no longer found.
func (r *MetadataGenerationJobRepository) RenewLease(id int64, leaseID string, leaseTTL time.Duration) error {
	res, err := r.db.Exec(
		`UPDATE metadata_generation_jobs
		 SET locked_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND locked_by = $2`,
		id, leaseID)
	if err != nil {
		return fmt.Errorf("metadata_generation RenewLease: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMetadataGenGone
	}
	return nil
}

// MarkCompleted transitions a processing row to completed with the
// generated result JSON. Returns ErrMetadataGenGone when the row is
// not found (peer already moved on).
func (r *MetadataGenerationJobRepository) MarkCompleted(id int64, leaseID string, result []byte) error {
	now := time.Now().UTC()
	res, err := r.db.Exec(
		`UPDATE metadata_generation_jobs
		 SET status = 'completed', result = $1::jsonb, completed_at = $2,
		     locked_by = '', locked_at = NULL, updated_at = $2
		 WHERE id = $3 AND locked_by = $4`,
		string(result), now, id, leaseID)
	if err != nil {
		return fmt.Errorf("metadata_generation MarkCompleted: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMetadataGenGone
	}
	return nil
}

// MarkFailed transitions a processing row back to queued (with backoff)
// or to failed terminal when max_attempts reached. backoff is a
// decorrelated-jittered duration; when nil, next_attempt_at = NOW().
// Returns ErrMetadataGenGone when the row is not found.
func (r *MetadataGenerationJobRepository) MarkFailed(id int64, leaseID string, lastError string, backoff *time.Duration) error {
	// Read + update in ONE transaction so the attempt_count read is
	// not racy against a peer (e.g. a lease expiring between the
	// SELECT and the UPDATE). The lease guard is WHERE locked_by =
	// $2 — only the owning worker may transition its row.
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("metadata_generation MarkFailed begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	row := tx.QueryRow(
		`SELECT attempt_count, max_attempts FROM metadata_generation_jobs
		 WHERE id = $1 AND locked_by = $2 FOR UPDATE`,
		id, leaseID)
	var attemptCount, maxAttempts int
	if err := row.Scan(&attemptCount, &maxAttempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMetadataGenGone
		}
		return fmt.Errorf("metadata_generation MarkFailed select: %w", err)
	}

	attemptCount++
	now := time.Now().UTC()
	if attemptCount >= maxAttempts {
		// Terminal failed — no more retries.
		if _, err := tx.Exec(
			`UPDATE metadata_generation_jobs
			 SET status = 'failed', error_message = $1, attempt_count = $2,
			     locked_by = '', locked_at = NULL, completed_at = NOW(), updated_at = NOW()
			 WHERE id = $3 AND locked_by = $4`,
			lastError, attemptCount, id, leaseID); err != nil {
			return fmt.Errorf("metadata_generation MarkFailed terminal: %w", err)
		}
	} else {
		// Transient — requeue with backoff.
		var next time.Time
		if backoff != nil {
			next = now.Add(*backoff)
		} else {
			next = now
		}
		if _, err := tx.Exec(
			`UPDATE metadata_generation_jobs
			 SET status = 'queued', error_message = $1, attempt_count = $2,
			     next_attempt_at = $3, locked_by = '', locked_at = NULL, updated_at = NOW()
			 WHERE id = $4 AND locked_by = $5`,
			lastError, attemptCount, next, id, leaseID); err != nil {
			return fmt.Errorf("metadata_generation MarkFailed requeue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("metadata_generation MarkFailed commit: %w", err)
	}
	return nil
}

// ReclaimExpired resets processing rows whose lease expired back to
// queued (with next_attempt_at = NOW()) so they are re-claimable.
func (r *MetadataGenerationJobRepository) ReclaimExpired(leaseTTL time.Duration) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE metadata_generation_jobs
		 SET status = 'queued', locked_by = '', locked_at = NULL,
		     next_attempt_at = NOW(), updated_at = NOW()
		 WHERE status = 'processing'
		   AND locked_at < NOW() - $1::interval`,
		fmt.Sprintf("%f seconds", leaseTTL.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("metadata_generation ReclaimExpired: %w", err)
	}
	return res.RowsAffected()
}

// scanMetadataGenJob scans a single row from the columns select list.
//
// TIMESTAMPTZ columns are scanned into sql.NullTime (NOT NullString +
// manual RFC3339 parsing): Postgres drivers format timestamptz with a
// space separator ("2026-08-07 16:50:22.123+00"), which time.Parse with
// RFC3339Nano would reject — silently yielding a zero time with a
// non-nil pointer. A zero locked_at would make ReclaimExpired reclaim
// an actively-processing row immediately (multi-replica hazard).
func scanMetadataGenJob(scanner interface {
	Scan(dest ...interface{}) error
}) (*models.MetadataGenerationJob, error) {
	job := &models.MetadataGenerationJob{}
	var result sql.NullString
	var nextAttempt, lockedAt, completedAt sql.NullTime
	err := scanner.Scan(
		&job.ID, &job.WorkspaceID, &job.VeloxProjectID, &job.Prompt,
		&job.Status, &result, &job.ErrorMessage,
		&job.AttemptCount, &job.MaxAttempts, &nextAttempt,
		&job.LockedBy, &lockedAt,
		&job.CreatedAt, &job.UpdatedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}
	if result.Valid {
		job.Result = json.RawMessage(result.String)
	}
	if nextAttempt.Valid {
		job.NextAttemptAt = &nextAttempt.Time
	}
	if lockedAt.Valid {
		job.LockedAt = &lockedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = &completedAt.Time
	}
	return job, nil
}
