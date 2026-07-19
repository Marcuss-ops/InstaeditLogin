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

// ImportBatchRepository handles persistence for the import_batches
// header table (P1#7). The producer handler (POST
// /api/v1/media/import/drive/folder) inserts one row IMMEDIATELY and
// returns {batch_id, status: "queued"} to the caller; the consumer
// (internal/worker/drive_batch_crawler.go) claims the row, paginates
// Drive, and creates one upload_job per file with the batch_id FK.
// upload_jobs.batch_id joins cleanly for the dashboard's "by-batch"
// aggregation query.
//
// Idempotency: ClaimNextBatch uses CTE + FOR UPDATE SKIP LOCKED so
// multiple crawler replicas can drain import_batches concurrently
// without double-claiming rows. The lease columns are
// (lease_owner, lease_expires_at, heartbeat_at) so a crashed
// crawler can be recovered by the reaper (ReclaimExpiredBatches) on
// the next tick.
type ImportBatchRepository struct {
	db *sql.DB
}

// NewImportBatchRepository creates a new ImportBatchRepository.
func NewImportBatchRepository(db *sql.DB) *ImportBatchRepository {
	return &ImportBatchRepository{db: db}
}

// Create inserts a fresh header row and returns the generated UUID +
// timestamps. The handler calls this IMMEDIATELY on the producer-side
// path so the caller can poll GET /api/v1/media/import/drive/batch/{id}
// within the same HTTP roundtrip that issued the creation.
//
// The DB generates the UUID via gen_random_uuid() default + the
// three timestamp columns via DEFAULT NOW(); we RETURNING them so
// the response can echo them without a second SELECT.
func (r *ImportBatchRepository) Create(batch *models.ImportBatch) error {
	var (
		sourceDrive    sql.NullInt64
		targetGroup    sql.NullString
		scheduleReason sql.NullString
		warningsJSON   sql.NullString
		errMessage     sql.NullString
	)
	if batch.SourceDriveAccountID != nil {
		sourceDrive = sql.NullInt64{Int64: *batch.SourceDriveAccountID, Valid: true}
	}
	if batch.TargetGroupName != nil {
		targetGroup = sql.NullString{String: *batch.TargetGroupName, Valid: true}
	}
	if batch.ScheduleClampReason != nil {
		scheduleReason = sql.NullString{String: *batch.ScheduleClampReason, Valid: true}
	}
	if len(batch.Warnings) > 0 {
		rawJSON, marshalErr := json.Marshal(batch.Warnings)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal import batch warnings: %w", marshalErr)
		}
		warningsJSON = sql.NullString{String: string(rawJSON), Valid: true}
	}
	if batch.ErrorMessage != nil {
		errMessage = sql.NullString{String: *batch.ErrorMessage, Valid: true}
	}
	err := r.db.QueryRow(
		`INSERT INTO import_batches
			(user_id, workspace_id, source_provider, source_drive_account, source_folder_id,
			 target_account_ids, target_group_name,
			 publish_schedule_start_at, publish_schedule_min_gap_seconds, publish_schedule_max_gap_seconds,
			 status, schedule_clamped, schedule_clamp_reason, warnings, error_message,
			 cursor_page_token, cursor_indexed_count, created_count, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5,
		         $6, $7,
		         $8, $9, $10,
		         $11, $12, $13, $14, $15,
		         NULL, 0, 0, NOW(), NOW())
		 RETURNING id, created_at, updated_at`,
		batch.UserID,
		batch.WorkspaceID,
		batch.SourceProvider,
		sourceDrive,
		batch.SourceFolderID,
		pq.Array(batch.TargetAccountIDs),
		targetGroup,
		batch.PublishScheduleStartAt,
		batch.PublishScheduleMinGap,
		batch.PublishScheduleMaxGap,
		string(batch.Status),
		batch.ScheduleClamped,
		scheduleReason,
		warningsJSON,
		errMessage,
	).Scan(&batch.ID, &batch.CreatedAt, &batch.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create import batch: %w", err)
	}
	return nil
}

// FindByID returns the header row for id, or (nil, nil) if not found.
// Used by the poll endpoint (GET /api/v1/media/import/drive/batch/{id})
// and the crawler's recovery path.
func (r *ImportBatchRepository) FindByID(id uuid.UUID) (*models.ImportBatch, error) {
	row := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, source_provider, source_drive_account, source_folder_id,
		        target_account_ids, target_group_name,
		        publish_schedule_start_at, publish_schedule_min_gap_seconds, publish_schedule_max_gap_seconds,
		        status, cursor_page_token, cursor_indexed_count,
		        schedule_clamped, schedule_clamp_reason, warnings, error_message,
		        created_count, created_at, updated_at, completed_at
		 FROM import_batches
		 WHERE id = $1`,
		id,
	)
	return scanImportBatch(row)
}

// ClaimNextBatch atomically claims ONE queued batch for the calling
// crawler worker. Same CTE + FOR UPDATE SKIP LOCKED pattern as
// upload_jobs.ClaimBatch but with a single-row contract: a crawler
// owns ONE batch at a time; the per-page Drive pagination is the
// long-running work (multi-minute for huge folders) and parallel
// batch processing would let a single crawler starve. The lease is
// separate from upload_job leases — lease_owner string carries the
// "crawl-<host>-<pid>" prefix so the audit log never confuses.
//
// Returns (nil, nil) when no queued batch is claimable (queue empty
// or every available row leased by a peer); the worker treats this
// as "sleep until next tick". SQL errors wrap with their original
// message; the worker surfaces them in logs.
func (r *ImportBatchRepository) ClaimNextBatch(ctx context.Context, workerID string, lease time.Duration) (*models.ImportBatch, error) {
	if workerID == "" {
		return nil, fmt.Errorf("import batch ClaimNextBatch: empty workerID")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("import batch ClaimNextBatch: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)
	row := r.db.QueryRowContext(ctx,
		`WITH candidates AS (
            SELECT id
            FROM import_batches
            WHERE status = 'queued'
              AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
            ORDER BY created_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT 1
        )
        UPDATE import_batches b
        SET status           = 'processing',
            lease_owner      = $1,
            lease_expires_at = $2,
            heartbeat_at     = NOW(),
            updated_at       = NOW()
        FROM candidates
        WHERE b.id = candidates.id
        RETURNING b.id, b.user_id, b.workspace_id, b.source_provider, b.source_drive_account, b.source_folder_id,
                  b.target_account_ids, b.target_group_name,
                  b.publish_schedule_start_at, b.publish_schedule_min_gap_seconds, b.publish_schedule_max_gap_seconds,
                  b.status, b.cursor_page_token, b.cursor_indexed_count,
                  b.schedule_clamped, b.schedule_clamp_reason, b.warnings, b.error_message,
                  b.created_count, b.created_at, b.updated_at, b.completed_at`,
		workerID, leaseUntil,
	)
	return scanImportBatch(row)
}

// UpdateCursor checkpoints the Drive page_token AFTER every page
// successfully produces upload_jobs, so a crawler crash mid-batch
// resumes from the LAST produced page instead of restreaming from
// page 1 (which would double-create upload_jobs for that range —
// the dedupe check would catch duplicates, but the bookkeeping is
// cleaner if we never double-write). Also bumps heartbeat_at so the
// batch doesn't fall to ReclaimExpiredBatches.
//
// cursorIndexedCount is the running total of upload_jobs created
// for this batch across all pages — the dashboard's "by-batch"
// query reads it directly.
func (r *ImportBatchRepository) UpdateCursor(ctx context.Context, id uuid.UUID, workerID, pageToken string, cursorIndexedCount int) error {
	if workerID == "" {
		return fmt.Errorf("import batch UpdateCursor: empty workerID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE import_batches
         SET cursor_page_token     = NULLIF($2, ''),
             cursor_indexed_count  = $3,
             heartbeat_at          = NOW(),
             lease_expires_at      = NOW() + INTERVAL '5 minutes',
             updated_at            = NOW()
         WHERE id          = $1
           AND lease_owner = $4
           AND status      = 'processing'`,
		id, pageToken, cursorIndexedCount, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to update import batch cursor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s workerID=%s", ErrImportBatchLeaseLost, id, workerID)
	}
	return nil
}

// IncrementCreatedCount adds delta to created_count without losing
// the lease. Called once per upload_job.Create inside the per-page
// loop so the dashboard's "by-batch" widget can stream a live
// progress number without polling upload_jobs by JOIN.
func (r *ImportBatchRepository) IncrementCreatedCount(ctx context.Context, id uuid.UUID, workerID string, delta int) error {
	if workerID == "" {
		return fmt.Errorf("import batch IncrementCreatedCount: empty workerID")
	}
	if delta <= 0 {
		return nil
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE import_batches
         SET created_count   = created_count + $2,
             heartbeat_at    = NOW(),
             lease_expires_at = NOW() + INTERVAL '5 minutes',
             updated_at      = NOW()
         WHERE id          = $1
           AND lease_owner = $3
           AND status      = 'processing'`,
		id, delta, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to increment import batch created_count: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s workerID=%s", ErrImportBatchLeaseLost, id, workerID)
	}
	return nil
}

// Heartbeat extends the lease on a batch the crawler still owns.
// Same CAS contract as UpdateCursor / IncrementCreatedCount.
func (r *ImportBatchRepository) Heartbeat(ctx context.Context, id uuid.UUID, workerID string, lease time.Duration) error {
	if workerID == "" {
		return fmt.Errorf("import batch Heartbeat: empty workerID")
	}
	if lease <= 0 {
		return fmt.Errorf("import batch Heartbeat: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)
	res, err := r.db.ExecContext(ctx,
		`UPDATE import_batches
         SET lease_expires_at = $1,
             heartbeat_at     = NOW()
         WHERE id          = $2
           AND lease_owner = $3
           AND status      = 'processing'`,
		leaseUntil, id, workerID,
	)
	if err != nil {
		return fmt.Errorf("import batch Heartbeat: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("import batch Heartbeat rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s workerID=%s", ErrImportBatchLeaseLost, id, workerID)
	}
	return nil
}

// MarkCompleted transitions the row to terminal success: status =
// 'completed', cursor_page_token = NULL (crawl finished), completed_at
// = NOW(), lease columns cleared. P1#7 only writes terminal success
// AFTER every page has been processed AND every upload_job is in
// the database — partial progress is checkpointed via UpdateCursor.
func (r *ImportBatchRepository) MarkCompleted(ctx context.Context, id uuid.UUID, workerID string) error {
	if workerID == "" {
		return fmt.Errorf("import batch MarkCompleted: empty workerID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE import_batches
         SET status           = 'completed',
             cursor_page_token = NULL,
             lease_owner      = NULL,
             lease_expires_at = NULL,
             heartbeat_at     = NULL,
             completed_at     = NOW(),
             updated_at       = NOW()
         WHERE id          = $1
           AND lease_owner = $2
           AND status      = 'processing'`,
		id, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark import batch completed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s workerID=%s", ErrImportBatchLeaseLost, id, workerID)
	}
	return nil
}

// MarkFailed transitions the row to terminal failure with error
// message stamped; the row is recoverable via operator retry (NOT
// dead_letter) because a single batch is a low-cost retry target.
// CAS contract mirrors MarkCompleted.
func (r *ImportBatchRepository) MarkFailed(ctx context.Context, id uuid.UUID, workerID, errorMessage string) error {
	if workerID == "" {
		return fmt.Errorf("import batch MarkFailed: empty workerID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE import_batches
         SET status           = 'failed',
             error_message    = $2,
             lease_owner      = NULL,
             lease_expires_at = NULL,
             heartbeat_at     = NULL,
             completed_at     = NOW(),
             updated_at       = NOW()
         WHERE id          = $1
           AND lease_owner = $3
           AND status      = 'processing'`,
		id, errorMessage, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark import batch failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s workerID=%s", ErrImportBatchLeaseLost, id, workerID)
	}
	return nil
}

// ReclaimExpiredBatches is the recoverer for crashed crawlers: scans
// for processing rows whose lease_expires_at is in the past AND whose
// heartbeat is more than 2 minutes stale (a grace window so a
// heartbeat goroutine that just hasn't fired yet doesn't lose its
// row) and returns them to status='queued' with the lease columns
// cleared. cursor_page_token is preserved, so a re-claimed row
// resumes mid-folder, not page 1.
//
// Capped at `maxRows` per call so a backlog of crashed workers
// can't tie up the DB. Returns the count reclaimed; a non-zero
// count in a production report = "crawlers are dying mid-batch";
// pair with app-level crawler-crash alerts.
func (r *ImportBatchRepository) ReclaimExpiredBatches(ctx context.Context, maxRows int) (int64, error) {
	if maxRows <= 0 {
		maxRows = 50
	}
	res, err := r.db.ExecContext(ctx,
		`WITH expired AS (
            SELECT id
            FROM import_batches
            WHERE status          = 'processing'
              AND lease_expires_at < NOW()
              AND heartbeat_at    IS NOT NULL
              AND heartbeat_at    < NOW() - INTERVAL '2 minutes'
            ORDER BY lease_expires_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE import_batches b
        SET status           = 'queued',
            lease_owner      = NULL,
            lease_expires_at = NULL,
            heartbeat_at     = NULL,
            updated_at       = NOW()
        FROM expired
        WHERE b.id = expired.id`,
		maxRows,
	)
	if err != nil {
		return 0, fmt.Errorf("import batch ReclaimExpiredBatches: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("import batch ReclaimExpiredBatches rows affected: %w", err)
	}
	return n, nil
}

// ErrImportBatchLeaseLost is the typed sentinel returned by
// UpdateCursor / IncrementCreatedCount / Heartbeat / MarkCompleted
// / MarkFailed when the row is no longer owned by the calling
// worker. Same shape as repository.ErrUploadJobLeaseLost for the
// upload_worker; the crawler treats it as "drop the in-flight
// work; the row is already in someone else's hands".
var ErrImportBatchLeaseLost = errors.New("import batch: lease lost (row claimed by peer or recovered by reaper)")

// scanImportBatch is the shared scan helper for *sql.Row + *sql.Rows.
// Both ClaimNextBatch and FindByID parse the column list identically;
// keeping them in lockstep avoids project-side column drift.
func scanImportBatch(row interface {
	Scan(dest ...interface{}) error
}) (*models.ImportBatch, error) {
	var (
		batch              models.ImportBatch
		rawStatus          string
		rawSourceDrive     sql.NullInt64
		rawTargetGroup     sql.NullString
		rawCursorPageToken sql.NullString
		rawScheduleReason  sql.NullString
		rawWarnings        sql.NullString
		rawErrorMessage    sql.NullString
		rawCompletedAt     sql.NullTime
		rawTargetIDs       []int64
	)
	err := row.Scan(
		&batch.ID,
		&batch.UserID,
		&batch.WorkspaceID,
		&batch.SourceProvider,
		&rawSourceDrive,
		&batch.SourceFolderID,
		&rawTargetIDs,
		&rawTargetGroup,
		&batch.PublishScheduleStartAt,
		&batch.PublishScheduleMinGap,
		&batch.PublishScheduleMaxGap,
		&rawStatus,
		&rawCursorPageToken,
		&batch.CursorIndexedCount,
		&batch.ScheduleClamped,
		&rawScheduleReason,
		&rawWarnings,
		&rawErrorMessage,
		&batch.CreatedCount,
		&batch.CreatedAt,
		&batch.UpdatedAt,
		&rawCompletedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan import batch: %w", err)
	}
	batch.Status = models.ImportBatchStatus(rawStatus)
	if rawSourceDrive.Valid {
		v := rawSourceDrive.Int64
		batch.SourceDriveAccountID = &v
	}
	if rawTargetGroup.Valid {
		v := rawTargetGroup.String
		batch.TargetGroupName = &v
	}
	if rawCursorPageToken.Valid {
		v := rawCursorPageToken.String
		batch.CursorPageToken = &v
	}
	if rawScheduleReason.Valid {
		v := rawScheduleReason.String
		batch.ScheduleClampReason = &v
	}
	if rawErrorMessage.Valid {
		v := rawErrorMessage.String
		batch.ErrorMessage = &v
	}
	if rawCompletedAt.Valid {
		t := rawCompletedAt.Time
		batch.CompletedAt = &t
	}
	if len(rawTargetIDs) > 0 {
		batch.TargetAccountIDs = rawTargetIDs
	}
	if rawWarnings.Valid && rawWarnings.String != "" {
		var warnings []string
		if jsonErr := json.Unmarshal([]byte(rawWarnings.String), &warnings); jsonErr != nil {
			return nil, fmt.Errorf("failed to unmarshal warnings: %w", jsonErr)
		}
		batch.Warnings = warnings
	}
	return &batch, nil
}
