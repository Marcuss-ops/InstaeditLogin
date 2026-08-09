package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ErrExternalDeliveryNotLinked is the typed sentinel callers (the
// worker via errors.Is dispatch) match against when no
// external_delivery row is linked to the upload_job yet.
var ErrExternalDeliveryNotLinked = errors.New("external delivery not linked to upload job")

// ErrExternalDeliveryAlreadyClaimed is returned when a worker tries to
// atomically create an upload_job for a delivery row that another worker
// already claimed (status is no longer claimable or upload_job_id is set).
var ErrExternalDeliveryAlreadyClaimed = errors.New("external delivery already claimed")

// ErrExternalDeliveryNoExpectedTriple is the typed sentinel
// when the external_delivery row exists but (size, sha) fields
// are empty/zero.
var ErrExternalDeliveryNoExpectedTriple = errors.New("external delivery has no expected triple")

// GetExpectedTripleByUploadJobID returns (expected_size_bytes,
// expected_sha256_hex) for the external_delivery row linked to
// uploadJobID. Sentinel dispatch is via errors.Is.
func (r *ExternalDeliveryRepository) GetExpectedTripleByUploadJobID(ctx context.Context, uploadJobID int64) (int64, string, error) {
	if uploadJobID <= 0 {
		return 0, "", fmt.Errorf("external delivery GetExpectedTripleByUploadJobID: non-positive uploadJobID %d", uploadJobID)
	}
	var size sql.NullInt64
	var sha sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT expected_size_bytes, expected_sha256
		 FROM external_deliveries
		 WHERE upload_job_id = $1`,
		uploadJobID,
	).Scan(&size, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrExternalDeliveryNotLinked
	}
	if err != nil {
		return 0, "", fmt.Errorf("external delivery GetExpectedTripleByUploadJobID scan: %w", err)
	}
	if !size.Valid || size.Int64 <= 0 || !sha.Valid || sha.String == "" {
		return 0, "", ErrExternalDeliveryNoExpectedTriple
	}
	return size.Int64, sha.String, nil
}

// CreateUploadJobAndLink creates an upload_jobs row and atomically claims the
// external_deliveries row for it in a single transaction. The claim UPDATE
// accepts both fresh ('accepted') and due retry ('retry_wait') deliveries,
// while requiring upload_job_id IS NULL, so only one worker can win the race.
// If the claim fails (0 rows affected) the transaction is rolled back and
// ErrExternalDeliveryAlreadyClaimed is returned, leaving the delivery row
// untouched for the winner. On success the delivery row is left with status
// 'downloading' and upload_job_id set, and the new upload job ID is returned.
func (r *ExternalDeliveryRepository) CreateUploadJobAndLink(ctx context.Context, job *models.UploadJob, deliveryID, workerID string) (int64, error) {
	if deliveryID == "" {
		return 0, errors.New("external delivery CreateUploadJobAndLink: empty deliveryID")
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("external delivery CreateUploadJobAndLink begin tx: %w", err)
	}
	defer tx.Rollback()

	if err := createUploadJob(ctx, tx, job); err != nil {
		return 0, fmt.Errorf("external delivery CreateUploadJobAndLink: create upload_job: %w", err)
	}
	jobID := job.ID

	res, err := tx.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status         = 'downloading',
		     upload_job_id  = $2,
		     updated_at     = NOW(),
		     lease_expires_at = NULL,
		     leased_by_worker_id = NULL
		 WHERE id           = $1
		   AND status       IN ('accepted', 'retry_wait')
		   AND upload_job_id IS NULL
		   AND (leased_by_worker_id = $3 OR leased_by_worker_id IS NULL)`,
		deliveryID, jobID, workerID,
	)
	if err != nil {
		return 0, fmt.Errorf("external delivery CreateUploadJobAndLink: claim delivery: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("external delivery CreateUploadJobAndLink: rows affected: %w", err)
	}
	if n == 0 {
		return 0, ErrExternalDeliveryAlreadyClaimed
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("external delivery CreateUploadJobAndLink commit: %w", err)
	}
	return jobID, nil
}

// ClaimDelivery atomically claims the next eligible external_delivery row for
// the calling worker. Eligible rows are in status 'accepted' or 'retry_wait'
// whose lease has expired (or never existed) and whose next_attempt_at window
// has opened (or is unset). The selected row is locked with FOR UPDATE
// SKIP LOCKED, then its attempt_count is incremented, lease_expires_at is set
// to NOW() + lease, leased_by_worker_id is stamped, and next_attempt_at is
// cleared. The status is left unchanged so CreateUploadJobAndLink can perform
// the final transition to 'downloading' while still verifying the lease.
// Returns ErrExternalDeliveryNotFound when no eligible row exists.
func (r *ExternalDeliveryRepository) ClaimDelivery(ctx context.Context, workerID string, lease time.Duration, maxAttempts int) (*models.ExternalDelivery, error) {
	if workerID == "" {
		return nil, errors.New("external delivery ClaimDelivery: empty workerID")
	}
	if lease <= 0 {
		return nil, fmt.Errorf("external delivery ClaimDelivery: non-positive lease %s", lease)
	}
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	row, err := scanExternalDeliveryByRow(r.db.QueryRowContext(ctx,
		`WITH candidate AS (
		    SELECT id
		      FROM external_deliveries
		     WHERE status IN ('accepted', 'retry_wait')
		       AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		       AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		     ORDER BY created_at ASC
		     LIMIT 1
		     FOR UPDATE SKIP LOCKED
		)
		UPDATE external_deliveries ed
		   SET lease_expires_at   = NOW() + ($2 * interval '1 second'),
		       leased_by_worker_id = $1,
		       attempt_count      = attempt_count + 1,
		       next_attempt_at    = NULL,
		       max_attempts       = GREATEST(max_attempts, $3),
		       updated_at         = NOW()
		  FROM candidate c
		 WHERE ed.id = c.id
		RETURNING ed.id, ed.source_system, ed.external_delivery_id, ed.idempotency_key, ed.external_destination_id,
		          ed.source_artifact_id, ed.expected_sha256, ed.expected_size_bytes, ed.expected_mime_type,
		          ed.download_url, ed.metadata, ed.publish_at, ed.callback_url,
		          ed.status, ed.request_sha256,
		          ed.upload_job_id, ed.post_id,
		          ed.platform_media_id, ed.platform_url,
		          ed.last_error_code, ed.last_error_message,
		          ed.created_at, ed.updated_at, ed.completed_at,
		          ed.attempt_count, ed.max_attempts,
		          ed.lease_expires_at, ed.next_attempt_at, ed.leased_by_worker_id`,
		workerID, lease.Seconds(), maxAttempts,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExternalDeliveryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("external delivery ClaimDelivery: %w", err)
	}
	return row, nil
}

// LinkUploadJob is the bridge to upload_job_repo: stamps the
// upload_job_id FK on the delivery row AFTER the worker has
// successfully created the upload_job (status transitions to
// 'artifact_verified' → 'ingest_completed' / 'queued'). Called once
// per delivery; NOT idempotent — the WHERE clause filters to rows
// whose upload_job_id IS NULL, so a second call with the SAME id
// returns 0 rows affected → ErrExternalDeliveryNotFound, and a
// second call with a DIFFERENT id surfaces the same error so a
// silently-overwritten FK (the prior COALESCE semantics) becomes
// operator-runbook-detectable. Callers that legitimately need to
// re-link (e.g. operator DELETE'd the orphan upload_job) recover
// via the ON DELETE SET NULL cascade; the next LinkUploadJob call
// then sees upload_job_id IS NULL again and succeeds.
//
// Returns ErrExternalDeliveryNotFound wrapped when zero rows match
// (missing row OR row already linked). The error shape is kept
// identical for both cases so callers (wrappers in
// upload_job_repo.go::LinkToExternalDelivery) treat them
// uniformly; debug-by-message-context remains available via the
// wrapped %w + id suffix.
//
// Note: the upload_job_id column has ON DELETE SET NULL (migration
// 055). If the caller subsequently deletes the upload_job, the
// delivery's upload_job_id becomes NULL; the dashboard's "by-delivery"
// query handles NULL upload_job_id via the NOT NULL partial index
// (excludes NULL rows), so a deleted upload_job doesn't pollute the
// join output.
func (r *ExternalDeliveryRepository) LinkUploadJob(ctx context.Context, deliveryID string, uploadJobID int64) error {
	if deliveryID == "" {
		return errors.New("external delivery LinkUploadJob: empty deliveryID")
	}
	if uploadJobID <= 0 {
		return errors.New("external delivery LinkUploadJob: uploadJobID must be positive")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET upload_job_id = $2,
		     updated_at     = NOW()
		 WHERE id = $1
		   AND upload_job_id IS NULL`,
		deliveryID, uploadJobID,
	)
	if err != nil {
		return fmt.Errorf("external delivery LinkUploadJob: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery LinkUploadJob rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, deliveryID)
	}
	return nil
}
