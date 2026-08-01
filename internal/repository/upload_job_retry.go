package repository

import (
	"context"
	"fmt"
	"time"
)

// MarkCompleted transitions the row to the terminal success state.
// P1#4 — renamed from 'completed' to 'publish_completed' so the
// upload_job lifecycle halves map 1:1 to the user's mental model:
// ingest_completed = ingest done, publish_completed = ingest AND
// publish done (terminal).
//
// Stamps post_id + asset_id (legacy), clears the lease, sets
// completed_at. The CAS against lease_owner guards against
// the late-delivery race: a worker whose lease expired (or was
// stolen by the reaper) cannot overwrite a peer's terminal write.
// On CAS loss, returns ErrUploadJobLeaseLost.
func (r *UploadJobRepository) MarkCompleted(ctx context.Context, id int64, workerID string, postID int64, assetID string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET status           = 'publish_completed',
             post_id          = $2,
             asset_id         = $3,
             error_message    = NULL,
             error_code       = NULL,
             lease_owner      = NULL,
             lease_expires_at = NULL,
             completed_at     = NOW(),
             updated_at       = NOW()
         WHERE id = $1
           AND lease_owner   = $4
           AND status        = 'leased'`,
		id, postID, assetID, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job completed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}

// MarkFailed is the worker-classified terminal fail: status = 'failed',
// error_code + error_message stamped, lease cleared, completed_at = NOW().
// Reserved for transient-but-classified-as-fatal failures (e.g. a 4xx
// from the provider that the worker has determined is non-retryable).
// For pure transient failures use MarkRetry; for "retry budget
// exhausted" use MarkDeadLetter.
//
// Note: the dashboard's 'failed' count includes BOTH MarkFailed +
// MarkDeadLetter rows so the operator sees the union of terminal-fail
// jobs in one badge.
func (r *UploadJobRepository) MarkFailed(ctx context.Context, id int64, workerID, errorCode, errMessage string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET status           = 'failed',
             error_message    = $2,
             error_code       = NULLIF($3, ''),
             lease_owner      = NULL,
             lease_expires_at = NULL,
             completed_at     = NOW(),
             updated_at       = NOW()
         WHERE id = $1
           AND lease_owner   = $4
           AND status        = 'leased'`,
		id, errMessage, errorCode, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}

// MarkRetry transitions the row to retry_wait: clears the lease,
// stamps the error taxonomy + schedules next_attempt_at = NOW() +
// caller's backoff (caller-computed so the worker enforces
// exponential + jitter consistently). ClaimBatch will not re-pick
// the row until next_attempt_at <= NOW(). The worker is responsible
// for the retry-vs-dead-letter branch (compare attempt_count vs
// max_attempts before deciding).
func (r *UploadJobRepository) MarkRetry(ctx context.Context, id int64, workerID, errorCode, errMessage string, nextAttemptAt time.Time) error {
	var errorCodeArg interface{}
	if errorCode != "" {
		errorCodeArg = errorCode
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET status           = 'retry_wait',
             error_message    = $2,
             error_code       = $3,
             lease_owner      = NULL,
             lease_expires_at = NULL,
             next_attempt_at  = $4,
             updated_at       = NOW()
         WHERE id = $1
           AND lease_owner   = $5
           AND status        = 'leased'`,
		id, errMessage, errorCodeArg, nextAttemptAt, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job retry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}

// MarkIngested transitions the row from 'leased' (claimed by the
// ingest pool) to 'ingest_completed' (publish pool eligible), stamps
// the asset_id + total_bytes + progress_bytes, and clears the lease
// columns. Called by the ingest pool AFTER mediaStore.MarkReady has
// streamed the bytes to S3.
//
// P1#4 rename: 'ready_to_publish' → 'ingest_completed'. The row now
// sits at-rest in 'ingest_completed', waiting for its publish_at
// cursor to elapse. When (publish_at <= NOW()) ClaimBatchForPublish
// picks it up and transitions to 'leased'.
//
// CAS against lease_owner guards the late-delivery race: a worker
// whose lease expired cannot overwrite a peer's terminal write.
// On CAS loss, returns ErrUploadJobLeaseLost.
//
// total_bytes is also written to progress_bytes so the dashboard's
// resumable-upload progress reads 100% the instant the ingest
// completes; future code (P1#5 resumable YouTube) will overwrite
// progress_bytes with the streaming-uploader's byte counter.
func (r *UploadJobRepository) MarkIngested(ctx context.Context, id int64, workerID, assetID string, totalBytes int64) error {
	if workerID == "" {
		return fmt.Errorf("upload job MarkIngested: empty workerID")
	}
	if assetID == "" {
		return fmt.Errorf("upload job MarkIngested: empty assetID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET status           = 'ingest_completed',
             asset_id         = $2,
             total_bytes      = $3,
             progress_bytes   = $3,
             lease_owner      = NULL,
             lease_expires_at = NULL,
             heartbeat_at     = NULL,
             updated_at       = NOW()
         WHERE id = $1
           AND lease_owner   = $4
           AND status        = 'leased'`,
		id, assetID, totalBytes, workerID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job ingested: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}
