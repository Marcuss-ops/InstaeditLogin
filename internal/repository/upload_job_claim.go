package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ClaimBatchForPublish is the publish-pool counterpart to
// ClaimBatch: claims rows whose status = 'ingest_completed' (the
// ingest pool has streamed them to S3 and stamped asset_id) AND
// whose publish_at cursor is now-or-past. P1#4 — the publish pool
// no longer races the user-supplied schedule; rows that have not
// reached their publish_at sit at-rest in 'ingest_completed'
// indefinitely (no lease held during the wait).
//
// Selection:
//
//	status = 'ingest_completed'              (P1#4 rename; was 'ready_to_publish')
//	publish_at IS NULL OR publish_at <= NOW() (P1#4 — the time gate)
//	next_attempt_at <= NOW() (or NULL)
//	no active lease
//
// CTE + UPDATE-FROM + RETURNING shape mirrors ClaimBatch. Same row-
// state transition (leased + lease_owner + heartbeat + attempt_count
// += 1). The workerID prefix should be 'upload-<host>-<pid>' so the
// ingest pool's leases are visibly disjoint.
//
// Note that the attempt budget is SHARED across ingest + publish:
// each phase increments attempt_count on claim, so 4 ingest fails + 4
// publish fails still exhaust max_attempts (default 8). Operators
// observing 'attempts exhausted' on a publish-pool failure should
// investigate the ingest pool separately — the budget shape is
// intentionally flat for now to keep the state machine simple.
func (r *UploadJobRepository) ClaimBatchForPublish(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error) {
	if workerID == "" {
		return nil, fmt.Errorf("upload job ClaimBatchForPublish: empty workerID")
	}
	if limit <= 0 {
		return nil, nil
	}
	if lease <= 0 {
		return nil, fmt.Errorf("upload job ClaimBatchForPublish: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)

	rows, err := r.db.QueryContext(ctx,
		SQLClaimBatchForPublish,
		limit, workerID, leaseUntil,
	)
	if err != nil {
		return nil, fmt.Errorf("upload job ClaimBatchForPublish: %w", err)
	}
	defer rows.Close()

	var out []*models.UploadJob
	for rows.Next() {
		job, scanErr := scanUploadJobRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("upload job ClaimBatchForPublish scan: %w", scanErr)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("upload job ClaimBatchForPublish rows: %w", err)
	}
	return out, nil
}

// ClaimBatch atomically claims up to `limit` upload_jobs for the calling
// worker, transitioning them from ('pending' | 'retry_wait') to
// 'leased' and stamping the lease columns. Replaces the legacy
// single-row ClaimNext with a CTE + FOR UPDATE SKIP LOCKED so multiple
// worker replicas can drain the queue concurrently without
// double-claiming rows. The CTE form (SELECT-FOR-UPDATE-SKIP-LOCKED +
// UPDATE-FROM-CTE) is the documented Postgres queue-table pattern: the
// lock acquired in the CTE propagates into the UPDATE-FROM so the
// same tx commits without re-locking races.
//
// P1#4 — the SELECT adds a time gate on ingest_after so the ingest
// pool skips rows whose ingest_after is in the future (operators
// can stage "ingest starting at T+0" schedules without blocking on
// the row's existence). The publish_at cursor lives on the row too
// but is NOT gated here — that gate is publish-pool's job
// (ClaimBatchForPublish).
//
// Per-row state transition:
//
//	pending | retry_wait
//	          ↓
//	leased, lease_owner = workerID, lease_expires_at = NOW()+lease,
//	heartbeat_at = NOW(), attempt_count += 1,
//	started_at = COALESCE(started_at, NOW())   -- preserve across retries
//
// Returns 0+ claimed jobs; an empty slice is the normal "queue empty
// or every row leased by a peer" case (the worker treats this as
// "sleep until next tick"). SQLSTATE / driver errors wrap and bubble
// to the caller unchanged.
//
// Concurrency: safe for N worker replicas against a single upload_jobs
// table. The partial index idx_upload_jobs_claim (priority ASC,
// created_at ASC WHERE status IN ('pending','retry_wait')) keeps the
// candidate scan index-only.
func (r *UploadJobRepository) ClaimBatch(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error) {
	if workerID == "" {
		return nil, fmt.Errorf("upload job ClaimBatch: empty workerID")
	}
	if limit <= 0 {
		return nil, nil
	}
	if lease <= 0 {
		return nil, fmt.Errorf("upload job ClaimBatch: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)

	rows, err := r.db.QueryContext(ctx, SQLClaimBatch, limit, workerID, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("upload job ClaimBatch: %w", err)
	}
	defer rows.Close()

	var out []*models.UploadJob
	for rows.Next() {
		job, scanErr := scanUploadJobRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("upload job ClaimBatch scan: %w", scanErr)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("upload job ClaimBatch rows: %w", err)
	}
	return out, nil
}

// Heartbeat extends the lease on a claim the worker still owns. The
// worker calls this on every in-flight job every `leaseTTL / 3` while
// it's processing the row, so a slow upload (e.g. a 16 MB chunk PUT
// to the YouTube resumable endpoint over a slow uplink) doesn't lose
// the lease to the reaper.
//
// CAS: the row must still be owned by workerID + still in
// status='leased'. Either condition failing (peer claim, reaper
// release, peer Mark*) returns ErrUploadJobLeaseLost; the worker
// should drop the in-flight work and let ClaimBatch re-queue if any
// retries are left.
func (r *UploadJobRepository) Heartbeat(ctx context.Context, jobID int64, workerID string, lease time.Duration) error {
	if workerID == "" {
		return fmt.Errorf("upload job Heartbeat: empty workerID")
	}
	if lease <= 0 {
		return fmt.Errorf("upload job Heartbeat: non-positive lease (%s)", lease)
	}
	leaseUntil := time.Now().Add(lease)
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET lease_expires_at = $1,
             heartbeat_at     = NOW()
         WHERE id = $2
           AND lease_owner   = $3
           AND status        = 'leased'`,
		leaseUntil, jobID, workerID,
	)
	if err != nil {
		return fmt.Errorf("upload job Heartbeat: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("upload job Heartbeat rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, jobID, workerID)
	}
	return nil
}

// ReclaimExpiredLeases is the recoverer: scans for leased rows whose
// lease_expires_at is in the past AND whose heartbeat is more than 5
// minutes stale (a grace window so a heartbeat goroutine that just
// hasn't fired yet doesn't lose its row) and returns them to
// status='pending' with the lease columns cleared. A subsequent
// ClaimBatch picks them back up.
//
// Capped at `maxRows` per call so a backlog of crashed workers can't
// tie up the DB; the upload worker calls this on its own ticker
// (~ leaseTTL cadence) until the backlog drains. Returns the number
// of rows reclaimed; a non-zero count in a production report =
// "workers are dying mid-claim"; pair with app-level worker-crash
// alerts.
func (r *UploadJobRepository) ReclaimExpiredLeases(ctx context.Context, maxRows int) (int64, error) {
	if maxRows <= 0 {
		maxRows = 100
	}
	res, err := r.db.ExecContext(ctx,
		SQLReclaimExpiredLeases,
		maxRows,
	)
	if err != nil {
		return 0, fmt.Errorf("upload job ReclaimExpiredLeases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("upload job ReclaimExpiredLeases rows affected: %w", err)
	}
	return n, nil
}
