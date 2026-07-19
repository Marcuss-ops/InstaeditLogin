package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
// P1#4 — ingest_after + publish_at replace the old scheduled_at column
// (migration 049c). ingest_after is server-side DEFAULT NOW() so a
// fresh Insert without an explicit value lands at NOW() (the user's
// published window does not block ingest from ClaimBatch's
// perspective). publish_at is nullable so callers that want
// immediate publish (single-file imports, the historical default)
// pass nil. folder_id continues to be nullable (migration 038).
func (r *UploadJobRepository) Create(job *models.UploadJob) error {
	targetsJSON, err := job.TargetsJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal upload job targets: %w", err)
	}

	var publishAt sql.NullTime
	if job.PublishAt != nil {
		publishAt = sql.NullTime{Time: *job.PublishAt, Valid: true}
	}
	var folderID sql.NullString
	if job.FolderID != nil {
		folderID = sql.NullString{String: *job.FolderID, Valid: true}
	}
	// P1#7 — batch_id optional FK to import_batches. NULL for
	// single-file imports + the synchronous v1 Drive folder endpoint;
	// non-NULL when the async folder crawler stamped the row.
	// Encode to a string explicitly so lib/pq emits the UUID form
	// (Pg parameter type) without relying on the uuid.UUID
	// driver.Valuer path.
	var batchID interface{}
	if job.BatchID != nil {
		batchID = job.BatchID.String()
	}

	// P1 (migration 053) — INSERT now writes the inherited batch
	// default_privacy_level verbatim. Bound as $14 so caller-passed nil/empty
	// also writes placeholder "" (DEFAULT '' from migration 053). order
	// matches the column list verbatim — column-list-vs-bind-list is a manual
	// invariant here, like every other INSERT in this repo.
	return r.db.QueryRow(
		`INSERT INTO upload_jobs
			(user_id, workspace_id, source_type, source_id, drive_account_id, folder_id,
			 title, caption, targets, status, ingest_after, publish_at, batch_id, default_privacy_level)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
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
		job.IngestAfter,
		publishAt,
		batchID,
		job.DefaultPrivacyLevel,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)
}

// FindByID returns the upload job with the given id, or (nil, nil) if not found.
func (r *UploadJobRepository) FindByID(id int64) (*models.UploadJob, error) {
	// P1 (migration 053) — every SELECT projection against upload_jobs now
	// includes default_privacy_level. Column-list-vs-Scan-list is a manual
	// invariant; lookups and inserts both include this column in the same
	// position (last, before the column-set the worker touches most).
	row := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, ingest_after, publish_at, created_at, updated_at,
		        attempt_count, max_attempts, next_attempt_at, lease_owner, lease_expires_at, heartbeat_at,
		        progress_bytes, total_bytes, error_code, priority, started_at, completed_at,
		        youtube_session_uri, youtube_session_offset, youtube_session_expires_at, youtube_chunk_size, youtube_last_chunk_at,
		        default_privacy_level
		 FROM upload_jobs
		 WHERE id = $1`,
		id,
	)
	return scanUploadJob(row)
}

// ClaimBatchForPublish is the publish-pool counterpart to
// ClaimBatch: claims rows whose status = 'ingest_completed' (the
// ingest pool has streamed them to S3 and stamped asset_id) AND
// whose publish_at cursor is now-or-past. P1#4 — the publish pool
// no longer races the user-supplied schedule; rows that have not
// reached their publish_at sit at-rest in 'ingest_completed'
// indefinitely (no lease held during the wait).
//
// Selection:
//   status = 'ingest_completed'              (P1#4 rename; was 'ready_to_publish')
//   publish_at IS NULL OR publish_at <= NOW() (P1#4 — the time gate)
//   next_attempt_at <= NOW() (or NULL)
//   no active lease
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
		`WITH candidates AS (
            SELECT id
            FROM upload_jobs
            WHERE status = 'ingest_completed'
              AND (publish_at IS NULL OR publish_at <= NOW())
              AND COALESCE(next_attempt_at, NOW()) <= NOW()
              AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
            ORDER BY priority ASC, created_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE upload_jobs j
        SET status           = 'leased',
            lease_owner      = $2,
            lease_expires_at = $3,
            heartbeat_at     = NOW(),
            attempt_count    = attempt_count + 1,
            started_at       = COALESCE(started_at, NOW()),
            updated_at       = NOW()
        FROM candidates
        WHERE j.id = candidates.id
        RETURNING j.*`,
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
//   pending | retry_wait
//             ↓
//   leased, lease_owner = workerID, lease_expires_at = NOW()+lease,
//   heartbeat_at = NOW(), attempt_count += 1,
//   started_at = COALESCE(started_at, NOW())   -- preserve across retries
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

	rows, err := r.db.QueryContext(ctx,
		`WITH candidates AS (
            SELECT id
            FROM upload_jobs
            WHERE status IN ('pending', 'retry_wait')
              AND COALESCE(next_attempt_at, NOW()) <= NOW()
              AND (ingest_after IS NULL OR ingest_after <= NOW())
              AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
            ORDER BY priority ASC, created_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE upload_jobs j
        SET status           = 'leased',
            lease_owner      = $2,
            lease_expires_at = $3,
            heartbeat_at     = NOW(),
            attempt_count    = attempt_count + 1,
            started_at       = COALESCE(started_at, NOW()),
            updated_at       = NOW()
        FROM candidates
        WHERE j.id = candidates.id
        RETURNING j.*`,
		limit, workerID, leaseUntil,
	)
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

// MarkDeadLetter transitions the row to terminal failure (retry budget
// exhausted). status = 'dead_letter', error_code + error_message stamped,
// lease cleared, completed_at = NOW(). The worker calls this when
// attempt_count >= max_attempts — the row is out of retry budget and
// surfaces in the operator-triage dashboard.
//
// Same CAS protection as MarkCompleted / MarkFailed: a late delivery
// from a worker whose lease expired cannot overwrite a peer's
// terminal write.
func (r *UploadJobRepository) MarkDeadLetter(ctx context.Context, id int64, workerID, errorCode, errMessage string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET status           = 'dead_letter',
             error_message    = $2,
             error_code       = $3,
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
		return fmt.Errorf("failed to mark upload job dead_letter: %w", err)
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
		`WITH expired AS (
            SELECT id
            FROM upload_jobs
            WHERE status          = 'leased'
              AND lease_expires_at < NOW()
              AND heartbeat_at    IS NOT NULL
              AND heartbeat_at    < NOW() - INTERVAL '5 minutes'
            ORDER BY lease_expires_at ASC
            FOR UPDATE SKIP LOCKED
            LIMIT $1
        )
        UPDATE upload_jobs j
        SET status                    = 'pending',
            lease_owner               = NULL,
            lease_expires_at          = NULL,
            heartbeat_at              = NULL,
            error_code                = COALESCE(error_code, 'lease_expired'),
            youtube_session_uri       = NULL,
            youtube_session_offset    = NULL,
            youtube_session_expires_at = NULL,
            youtube_chunk_size        = NULL,
            youtube_last_chunk_at     = NULL,
            updated_at                = NOW()
        FROM expired
        WHERE j.id = expired.id`,
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

// UploadJobListFilter narrows the rows returned by ListByUser and
// ListByAccount. Zero-value fields are interpreted as "no filter"; the
// handler applies only the predicates it has non-zero values for. This
// keeps the SQL simple (one statement, NULL-or-equal predicates) and
// lets the caller opt into any combination of filters without us
// having to maintain N specialised query methods.
type UploadJobListFilter struct {
	AccountID *int64                  // restrict to jobs whose targets @> jsonb_build_array(AccountID)
	Status    *models.UploadJobStatus // restrict to one of the 4 enum values
	From      *time.Time              // scheduled_at >= From (nil = no lower bound)
	To        *time.Time              // scheduled_at <= To   (nil = no upper bound)
	Limit     int                     // hard cap; 0 = default 200
}

// ErrUploadJobNotFound is the typed sentinel Reschedule/Cancel return
// to differentiate "job id doesn't exist" from "job id exists but
// already moved past pending (worker claimed / completed / failed)".
// The handler maps both to 404 — leaking the distinction would let a
// caller probe whether an id has been processed yet.
var ErrUploadJobNotFound = errors.New("upload job not found or no longer pending")

// ErrUploadJobLeaseLost is the typed sentinel returned by
// Heartbeat / MarkCompleted / MarkFailed / MarkRetry / MarkDeadLetter
// when the row is no longer owned by the calling worker. Causes:
//   - lease_expires_at elapsed and a peer's ReclaimExpiredLeases flipped
//     the row back to 'pending' (worker host crashed mid-upload).
//   - A peer ClaimBatch re-leased the row after our lease expired.
//   - An operator deleted the row.
//   - Our Mark* fired AFTER another worker's Mark* already won the
//     CAS (lease_owner string no longer matches ours).
//
// The worker treats ErrUploadJobLeaseLost as "drop the in-flight work;
// the row is already in someone else's hands; don't double-publish or
// overwrite a peer's state". Same shape as outbox_repo.go's
// ErrOutboxGone / ErrOutboxRace for the dispatcher.
var ErrUploadJobLeaseLost = errors.New("upload job: lease lost (row claimed by peer or recovered by reaper)")

const uploadJobListDefaultLimit = 200

// ListByUser returns upload_jobs scoped to userID, optionally narrowed
// by filter. Ordered by scheduled_at ASC NULLS LAST so the calendar's
// "earliest first" presentation is one round trip. The Limit defaults
// to 200 when zero; callers that need ALL rows should page (call with
// LIMIT + To set to the last seen scheduled_at).
//
// Security: user_id is the FIRST predicate so a stolen job id from a
// different tenant cannot be enumerated — the index btree on
// (user_id, created_at DESC) makes that part O(log n).
//
// Performance: GIN index on `targets` (migration 040) makes the
// per-account filter O(matching-rows) via the jsonb_ops
// containment opclass. Combined with the user_id btree via BitmapAnd,
// the planner sticks to indexes for any reasonable scale.
func (r *UploadJobRepository) ListByUser(userID int64, filter UploadJobListFilter) ([]models.UploadJob, error) {
	if filter.Limit <= 0 {
		filter.Limit = uploadJobListDefaultLimit
	}

	var (
		accountID sql.NullInt64
		status    sql.NullString
		timeFrom  sql.NullTime
		timeTo    sql.NullTime
	)
	if filter.AccountID != nil {
		accountID = sql.NullInt64{Int64: *filter.AccountID, Valid: true}
	}
	if filter.Status != nil {
		status = sql.NullString{String: string(*filter.Status), Valid: true}
	}
	if filter.From != nil {
		timeFrom = sql.NullTime{Time: *filter.From, Valid: true}
	}
	if filter.To != nil {
		timeTo = sql.NullTime{Time: *filter.To, Valid: true}
	}

	// P1 (migration 053) — appended default_privacy_level to the projection so
	// ListByUser returns it for the dashboard's "what privacy will this row
	// publish at" preview column (future taglio).
	rows, err := r.db.Query(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, ingest_after, publish_at, created_at, updated_at,
		        attempt_count, max_attempts, next_attempt_at, lease_owner, lease_expires_at, heartbeat_at,
		        progress_bytes, total_bytes, error_code, priority, started_at, completed_at,
		        youtube_session_uri, youtube_session_offset, youtube_session_expires_at, youtube_chunk_size, youtube_last_chunk_at,
		        default_privacy_level
		 FROM upload_jobs
		 WHERE user_id = $1
		   AND ($2::bigint              IS NULL OR targets @> jsonb_build_array($2::bigint))
		   AND ($3::upload_job_status   IS NULL OR status = $3::upload_job_status)
		   AND ($4::timestamptz         IS NULL OR publish_at >= $4)
		   AND ($5::timestamptz         IS NULL OR publish_at <= $5)
		 ORDER BY publish_at ASC NULLS LAST, id ASC
		 LIMIT $6`,
		userID, accountID, status, timeFrom, timeTo, filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list upload_jobs by user: %w", err)
	}
	defer rows.Close()

	var out []models.UploadJob
	for rows.Next() {
		job, scanErr := scanUploadJobRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan upload job: %w", scanErr)
		}
		out = append(out, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate upload jobs: %w", err)
	}
	return out, nil
}

// Reschedule atomically updates scheduled_at for a pending upload_job.
// Security: scoped to BOTH id AND user_id — a stolen job id from
// another tenant returns ErrUploadJobNotFound (the handler maps it to
// 404 the same as a non-existent id; no information leak).
//
// Idempotency / state machine: only status='pending' rows can be
// rescheduled. Once the worker has claimed the row (status='processing')
// or it's terminal (completed/failed), the UPDATE matches zero rows and
// we return ErrUploadJobNotFound. This is the desired UX: dragging a
// chip that the worker has already picked up should surface a clean
// error, not silently mutate a row that's mid-publish.
//
// newScheduledAt must be in the future. The handler enforces this with
// 400; the repository itself is permissive (operator scripts may want
// to backdate for testing) — defence-in-depth without an opinionated
// invariant.
func (r *UploadJobRepository) Reschedule(jobID, userID int64, newPublishAt time.Time) (models.UploadJob, error) {
	res, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET publish_at = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'pending'`,
		jobID, userID, newPublishAt,
	)
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to reschedule upload job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		// Either id is wrong, id belongs to another tenant, OR the
		// row already left 'pending' (worker claimed / ingested / publish_failed).
		// All three return the same sentinel — no information leak.
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	job, err := r.FindByID(jobID)
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to re-read after reschedule: %w", err)
	}
	if job == nil || job.UserID != userID {
		// Defensive: the row was deleted between UPDATE and re-read.
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	return *job, nil
}

// UploadJobPendingCount is the per-account rollup returned by
// PendingCountsByAccount — the single-query aggregate that backs the
// dashboard widget's per-account "Programmati" badge. It's exposed
// at the repository level so the dashboard handler can stream an
// exact count + earliest-scheduled row for every target the user
// has, in one SELECT — no client-side bucketing and no limit cap
// hiding uploads past the 200-row budget that drives the per-account
// list endpoint.
type UploadJobPendingCount struct {
	AccountID     int64
	Count         int
	NextPublishAt *time.Time
}

// PendingCountsByAccount returns the GROUP BY per target for every
// pending upload owned by userID. Uses jsonb_array_elements_text to
// unnest the JSONB `targets` column into bigints at the SQL layer
// (cheaper than fetching rows + bucketing in Go). The query hits:
//   - the GIN index on targets (migration 040) for the LATERAL unnesting
//   - the (user_id, status) btree for the WHERE clause
//
// so it's an index range scan + a small hash aggregate. Order is
// stable on account_id ASC so the SPA can rely on row order for
// optimistic renders.
func (r *UploadJobRepository) PendingCountsByAccount(userID int64) ([]UploadJobPendingCount, error) {
	rows, err := r.db.Query(
		`SELECT
			e.elem::bigint        AS account_id,
			COUNT(*)              AS pending_count,
			MIN(u.publish_at)     AS next_publish_at
		 FROM upload_jobs u
		 CROSS JOIN LATERAL jsonb_array_elements_text(u.targets) AS e(elem)
		 WHERE u.user_id    = $1
		   AND u.status     = 'pending'
		   AND u.publish_at IS NOT NULL
		 GROUP BY e.elem::bigint
		 ORDER BY account_id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate upload_jobs by target: %w", err)
	}
	defer rows.Close()

	var out []UploadJobPendingCount
	for rows.Next() {
		var c UploadJobPendingCount
		var nextAt sql.NullTime
		if err := rows.Scan(&c.AccountID, &c.Count, &nextAt); err != nil {
			return nil, fmt.Errorf("failed to scan pending-count row: %w", err)
		}
		if nextAt.Valid {
			t := nextAt.Time
			c.NextPublishAt = &t
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate pending counts: %w", err)
	}
	return out, nil
}

// PendingDistinctCount returns the user's total number of pending
// upload_jobs as DISTINCT rows (not per-target expansions). The
// dashboard's "Pending uploads" stat reads from this — SUM over the
// PendingCountsByAccount result would over-count one upload that
// targets multiple accounts (e.g. drive_batch on FB+IG). Hits the
// (user_id, status) btree so the planner does an index-only count,
// no row fetch needed.
func (r *UploadJobRepository) PendingDistinctCount(userID int64) (int64, error) {
	var n int64
	err := r.db.QueryRow(
		`SELECT COUNT(*)
		 FROM upload_jobs
		 WHERE user_id = $1 AND status = 'pending'`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count distinct pending uploads: %w", err)
	}
	return n, nil
}

// Cancel atomically deletes a pending upload_job. Same authz + state-
// machine contract as Reschedule: scoped to (id, user_id, status=pending)
// so a stolen id, or one that's already been claimed/processed/finished,
// returns ErrUploadJobNotFound without leaking the distinction.
//
// Concurrent-claim safety: the publish worker uses
// `SELECT ... FOR UPDATE SKIP LOCKED` on pending rows; this DELETE
// holds an implicit row lock from the WHERE predicate. Whichever tx
// lands first wins; the other sees zero rows affected and surfaces an
// error to the user. The post-Cancel row is gone, so the worker's
// next claim skips it cleanly.
func (r *UploadJobRepository) Cancel(jobID, userID int64) error {
	res, err := r.db.Exec(
		`DELETE FROM upload_jobs
		 WHERE id = $1
		   AND user_id = $2
		   AND status = 'pending'`,
		jobID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to cancel upload job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return ErrUploadJobNotFound
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
			COUNT(*) FILTER (WHERE status = 'pending')         AS pending_count,
			COUNT(*) FILTER (WHERE status = 'retry_wait')      AS retry_wait_count,
			COUNT(*) FILTER (WHERE status = 'leased')          AS leased_count,
			COUNT(*) FILTER (WHERE status = 'processing')      AS processing_count,
			COUNT(*) FILTER (WHERE status = 'ingest_completed') AS ready_to_publish_count,
			COUNT(*) FILTER (WHERE status = 'publish_completed') AS completed_count,
			COUNT(*) FILTER (WHERE status = 'failed')          AS failed_count,
			COUNT(*) FILTER (WHERE status = 'dead_letter')     AS dead_letter_count,
			COUNT(*) FILTER (WHERE status = 'cancelled')       AS cancelled_count,
			MIN(publish_at) AS first_publish_at,
			MAX(publish_at) AS last_publish_at
		 FROM upload_jobs
		 WHERE folder_id = $1
		   AND user_id    = $2`,
		folderID, userID,
	)
	var summary models.BatchStatusSummary
	var firstAt, lastAt sql.NullTime
	if err := row.Scan(
		&summary.PendingCount,
		&summary.RetryWaitCount,
		&summary.LeasedCount,
		&summary.ProcessingCount,
		&summary.ReadyToPublishCount,
		&summary.CompletedCount,
		&summary.FailedCount,
		&summary.DeadLetterCount,
		&summary.CancelledCount,
		&firstAt,
		&lastAt,
	); err != nil {
		return summary, fmt.Errorf("failed to aggregate upload_jobs by folder: %w", err)
	}
	// keep aligned with UploadJobStatus enum — a future new status
	// (e.g. 'cancelled' has been added in migration 045) must add a
	// COUNT FILTER clause above AND a term in this sum, otherwise it
	// silently drops off the dashboard.
	summary.TotalCount = summary.PendingCount + summary.RetryWaitCount + summary.LeasedCount +
		summary.ProcessingCount + summary.ReadyToPublishCount + summary.CompletedCount +
		summary.FailedCount + summary.DeadLetterCount + summary.CancelledCount
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

// scanUploadJobRows is the *sql.Rows equivalent of scanUploadJob —
// the column list is identical but we iterate in the caller. Splitting
// them keeps the row-vs-rows contract distinct at the type level so
// adding a column doesn't have to thread through two function bodies.
// scanUploadJobRows is the *sql.Rows equivalent of scanUploadJob —
// the column list is identical but we iterate in the caller. Splitting
// them keeps the row-vs-rows contract distinct at the type level so
// adding a column doesn't have to thread through two function bodies.
//
// The 29 column list must stay in lockstep with the SELECT projections
// in FindByID / ListByUser / ClaimBatch; a mismatch causes sql.Scan to
// return an error on the first row. Columns past index 16 are the P1
// worker-pool additions (migration 046) and use sql.Null* wrappers
// because they're nullable on disk.
// SaveYouTubeSession (P1#5) persists the resumable-upload state
// after every successful chunk PUT so a worker restart can pick up
// from youtube_session_offset instead of restreaming the entire
// file from byte 0. CAS-guarded against lease_owner so a peer
// that stole the lease (reaper release + re-claim) cannot overwrite
// our writes. Caller (the worker's processPublishJob) computes the
// offset from the Content-Range header on the successful PUT
// response.
//
// expiresAt is set to NOW() + YOUTUBE_SESSION_DEFAULT_TTL_HOURS
// (default 168 = 7 days, matching YouTube's documented default).
// The worker checks NOW() >= expiresAt before trusting the URI for
// a ResumeSession probe; if past, the URI is treated as dead and the
// worker re-initiates via the existing POST path.
func (r *UploadJobRepository) SaveYouTubeSession(ctx context.Context, id int64, workerID, sessionURI string, offset, chunkSize int64, expiresAt time.Time) error {
	if workerID == "" {
		return fmt.Errorf("upload job SaveYouTubeSession: empty workerID")
	}
	if sessionURI == "" {
		return fmt.Errorf("upload job SaveYouTubeSession: empty sessionURI")
	}
	if chunkSize <= 0 {
		return fmt.Errorf("upload job SaveYouTubeSession: non-positive chunkSize (%d)", chunkSize)
	}
	if offset < 0 {
		return fmt.Errorf("upload job SaveYouTubeSession: negative offset (%d)", offset)
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET youtube_session_uri       = $2,
             youtube_session_offset    = $3,
             youtube_session_expires_at = $4,
             youtube_chunk_size        = $5,
             youtube_last_chunk_at     = NOW(),
             progress_bytes            = $3,
             updated_at                = NOW()
         WHERE id = $1
           AND lease_owner            = $6
           AND status                 = 'leased'`,
		id, sessionURI, offset, expiresAt, chunkSize, workerID,
	)
	if err != nil {
		return fmt.Errorf("upload job SaveYouTubeSession: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("upload job SaveYouTubeSession rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}

// ClearYouTubeSession (P1#5) removes any persisted resumable-upload
// state from the row. Used by processPublishJob on three paths:
//   - MarkCompleted: terminal-success row is no longer in flight;
//     the URI is dead to anyone with read access, so nulling the
//     5 columns keeps the row tidy.
//   - ResumeSession probe returned 404 (session expired): the URI
//     is confirmed dead but the row WILL re-initiate immediately,
//     so clearing is the right call.
//   - On MarkDeadLetter we KEEP the session fields (operator triage
//     needs the URI + offset to resume by hand) — caller invokes
//     SaveYouTubeSession on retry rather than Clear here.
//
// Same CAS protection as the other Mark* helpers.
func (r *UploadJobRepository) ClearYouTubeSession(ctx context.Context, id int64, workerID string) error {
	if workerID == "" {
		return fmt.Errorf("upload job ClearYouTubeSession: empty workerID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE upload_jobs
         SET youtube_session_uri       = NULL,
             youtube_session_offset    = NULL,
             youtube_session_expires_at = NULL,
             youtube_chunk_size        = NULL,
             youtube_last_chunk_at     = NULL,
             updated_at                = NOW()
         WHERE id = $1
           AND lease_owner            = $2
           AND status                 = 'leased'`,
		id, workerID,
	)
	if err != nil {
		return fmt.Errorf("upload job ClearYouTubeSession: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("upload job ClearYouTubeSession rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d workerID=%s", ErrUploadJobLeaseLost, id, workerID)
	}
	return nil
}

func scanUploadJobRows(rows *sql.Rows) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var publishAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString
	var nextAttemptAt, leaseExpiresAt, heartbeatAt, startedAt, completedAt sql.NullTime
	var leaseOwner sql.NullString
	var totalBytes sql.NullInt64
	var errorCode sql.NullString
	// P1#5 — YouTube resumable session columns (migration 048). All
	// nullable: NULL means "no session yet" or "session was cleared".
	var youtubeSessionURI sql.NullString
	var youtubeSessionOffset, youtubeChunkSize sql.NullInt64
	var youtubeSessionExpiresAt, youtubeLastChunkAt sql.NullTime

	err := rows.Scan(
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
		&job.IngestAfter,
		&publishAt,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextAttemptAt,
		&leaseOwner,
		&leaseExpiresAt,
		&heartbeatAt,
		&job.ProgressBytes,
		&totalBytes,
		&errorCode,
		&job.Priority,
		&startedAt,
		&completedAt,
		&youtubeSessionURI,
		&youtubeSessionOffset,
		&youtubeSessionExpiresAt,
		&youtubeChunkSize,
		&youtubeLastChunkAt,
		&job.DefaultPrivacyLevel,
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
	if publishAt.Valid {
		t := publishAt.Time
		job.PublishAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if nextAttemptAt.Valid {
		t := nextAttemptAt.Time
		job.NextAttemptAt = &t
	}
	if leaseOwner.Valid {
		v := leaseOwner.String
		job.LeaseOwner = &v
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		job.LeaseExpiresAt = &t
	}
	if heartbeatAt.Valid {
		t := heartbeatAt.Time
		job.HeartbeatAt = &t
	}
	if totalBytes.Valid {
		v := totalBytes.Int64
		job.TotalBytes = &v
	}
	if errorCode.Valid {
		v := errorCode.String
		job.ErrorCode = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		job.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		job.CompletedAt = &t
	}
	// P1#5 — YouTube session field assignments. youtube_session_uri
	// is the only credential-adjacent field; it carries the
	// `json:"-"` tag on the model so it never leaves the backend
	// via any API response. Workers MUST redact it on log lines
	// (see internal/worker/redactYouTubeSessionURI).
	if youtubeSessionURI.Valid {
		v := youtubeSessionURI.String
		job.YouTubeSessionURI = &v
	}
	if youtubeSessionOffset.Valid {
		v := youtubeSessionOffset.Int64
		job.YouTubeSessionOffset = &v
	}
	if youtubeSessionExpiresAt.Valid {
		t := youtubeSessionExpiresAt.Time
		job.YouTubeSessionExpiresAt = &t
	}
	if youtubeChunkSize.Valid {
		v := youtubeChunkSize.Int64
		job.YouTubeChunkSize = &v
	}
	if youtubeLastChunkAt.Valid {
		t := youtubeLastChunkAt.Time
		job.YouTubeLastChunkAt = &t
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}

func scanUploadJob(row *sql.Row) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var publishAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString
	var nextAttemptAt, leaseExpiresAt, heartbeatAt, startedAt, completedAt sql.NullTime
	var leaseOwner sql.NullString
	var totalBytes sql.NullInt64
	var errorCode sql.NullString
	// P1#5 — YouTube resumable session columns (migration 048). All
	// nullable: NULL means "no session yet" or "session was cleared".
	var youtubeSessionURI sql.NullString
	var youtubeSessionOffset, youtubeChunkSize sql.NullInt64
	var youtubeSessionExpiresAt, youtubeLastChunkAt sql.NullTime

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
		&job.IngestAfter,
		&publishAt,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.AttemptCount,
		&job.MaxAttempts,
		&nextAttemptAt,
		&leaseOwner,
		&leaseExpiresAt,
		&heartbeatAt,
		&job.ProgressBytes,
		&totalBytes,
		&errorCode,
		&job.Priority,
		&startedAt,
		&completedAt,
		&youtubeSessionURI,
		&youtubeSessionOffset,
		&youtubeSessionExpiresAt,
		&youtubeChunkSize,
		&youtubeLastChunkAt,
		&job.DefaultPrivacyLevel,
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
	if publishAt.Valid {
		t := publishAt.Time
		job.PublishAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if nextAttemptAt.Valid {
		t := nextAttemptAt.Time
		job.NextAttemptAt = &t
	}
	if leaseOwner.Valid {
		v := leaseOwner.String
		job.LeaseOwner = &v
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		job.LeaseExpiresAt = &t
	}
	if heartbeatAt.Valid {
		t := heartbeatAt.Time
		job.HeartbeatAt = &t
	}
	if totalBytes.Valid {
		v := totalBytes.Int64
		job.TotalBytes = &v
	}
	if errorCode.Valid {
		v := errorCode.String
		job.ErrorCode = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		job.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		job.CompletedAt = &t
	}
	// P1#5 — YouTube session field assignments. youtube_session_uri
	// is the only credential-adjacent field; it carries the
	// `json:"-"` tag on the model so it never leaves the backend
	// via any API response. Workers MUST redact it on log lines
	// (see internal/worker/redactYouTubeSessionURI).
	if youtubeSessionURI.Valid {
		v := youtubeSessionURI.String
		job.YouTubeSessionURI = &v
	}
	if youtubeSessionOffset.Valid {
		v := youtubeSessionOffset.Int64
		job.YouTubeSessionOffset = &v
	}
	if youtubeSessionExpiresAt.Valid {
		t := youtubeSessionExpiresAt.Time
		job.YouTubeSessionExpiresAt = &t
	}
	if youtubeChunkSize.Valid {
		v := youtubeChunkSize.Int64
		job.YouTubeChunkSize = &v
	}
	if youtubeLastChunkAt.Valid {
		t := youtubeLastChunkAt.Time
		job.YouTubeLastChunkAt = &t
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}
