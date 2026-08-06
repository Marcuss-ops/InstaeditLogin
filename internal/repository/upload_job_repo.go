package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ErrUploadJobNotFoundForPost is the sentinel returned when a post
// has no linked upload_jobs row (post.ingest path never wrote one).
// Returned by FindByPostID only when postID is positive but no row
// matches — distinct from a real *sql.DB error so callers can
// branch on errors.Is.
var ErrUploadJobNotFoundForPost = errors.New("upload job not found for post")

// UploadJobRepository handles persistence for upload_jobs — the background
// queue that downloads videos from Google Drive and publishes them.
type UploadJobRepository struct {
	db *sql.DB
}

// NewUploadJobRepository creates a new UploadJobRepository.
func NewUploadJobRepository(db *sql.DB) *UploadJobRepository {
	return &UploadJobRepository{db: db}
}

// UploadJobListFilter narrows the rows returned by ListByUser and
// ListByAccount. Zero-value fields are interpreted as "no filter"; the
// handler applies only the predicates it has non-zero values for.
type UploadJobListFilter struct {
	AccountID        *int64                  // restrict to jobs whose targets @> jsonb_build_array(AccountID)
	Status           *models.UploadJobStatus // restrict to one of the enum values
	From             *time.Time              // publish_at >= From (nil = no lower bound)
	To               *time.Time              // publish_at <= To   (nil = no upper bound)
	AfterPublishAt   *time.Time              // keyset cursor; nil timestamp means the NULL publish_at tail
	AfterID          int64
	AfterPublishNull bool
	Limit            int // hard cap; 0 = default 200
}

const uploadJobListDefaultLimit = 200

// UploadJobPendingCount is the per-account rollup returned by
// PendingCountsByAccount.
type UploadJobPendingCount struct {
	AccountID     int64
	Count         int
	NextPublishAt *time.Time
}

// ExternalDeliveryLinker is the narrow persistence contract
// LinkToExternalDelivery forwards through. The real impl is
// *ExternalDeliveryRepository (LinkUploadJob); defined inline so
// upload_job_repo doesn't import the other repo + so tests can
// inject fakes without dragging the full repo surface.
//
// Forwarder-only — does NOT validate the supplied status against
// the external_deliveries row. The FSM (internal/worker/ingest_fsm.go)
// owns CAN-transitionTo enforcement; this helper owns the SQL
// FK-stamp path ONLY. A caller that wants "stamp only if external
// delivery is in <expected> state" wraps this in
//
//	if d, _ := linker.GetByID(ctx, externalDeliveryID); d.Status != expectedStatus {
//	    return ErrStatusMismatch
//	}
//	return r.LinkToExternalDelivery(ctx, linker, ...)
//
// at the worker level. See internal/worker/ingest_fsm.go::Transition
// for the canonical pattern.
type ExternalDeliveryLinker interface {
	LinkUploadJob(ctx context.Context, deliveryID string, uploadJobID int64) error
}

// LinkToExternalDelivery is the upload-job-side bridge: forwards
// to ExternalDeliveryLinker.LinkUploadJob, which stamps the
// upload_job_id FK on the external_deliveries row (single-source-
// of-truth direction per migration 055). Called once per delivery
// from the worker right after upload_job Create() succeeds.
//
// Parameters:
//
//	uploadJobID       — the new upload_job row id (int64, must be > 0)
//	externalDeliveryID — the sdel_01J... id from external_deliveries
//
// NOTE: an earlier draft of this method included a third `status` parameter
// (the expected external_delivery status at link-time). The code-reviewer
// flagged it as BLOCKING because the helper did NOT validate or use the value
// — keeping a dead parameter misleads callers. The status check is the FSM's
// job (internal/worker/ingest_fsm.go::Transition, called IMMEDIATELY before this
// helper at the worker boundary). Canonical worker pattern:
//
//		if !fsm.Transition(ctx, deliveryID, currentStatus, models.ExternalDeliveryStatusArtifactVerified, ...) {
//			return // operator runbook
//		}
//		r.LinkToExternalDelivery(ctx, linker, uploadJobID, externalDeliveryID)
//	  - empty externalDeliveryID
//	  - uploadJobID <= 0
//
// Single-shot (NOT idempotent): LinkUploadJob filters on
// `upload_job_id IS NULL` so a second call with the SAME uploadJobID
// returns 0 rows → ErrExternalDeliveryNotFound (forwarder wraps into
// `forward to external_delivery_repo.LinkUploadJob`). A re-call with
// a DIFFERENT uploadJobID surfaces the same error — symmetric fail-
// loud contract catches accidental FK-swap bugs in worker retry
// paths. Operator recovery for "link didn't land because the row was
// already linked": DELETE the orphan upload_job (triggers ON DELETE
// SET NULL on the FK column), then the next LinkUploadJob succeeds.
// See external_delivery_repo.LinkUploadJob doc for the full recovery
// path.
func (r *UploadJobRepository) LinkToExternalDelivery(
	ctx context.Context,
	linker ExternalDeliveryLinker,
	uploadJobID int64,
	externalDeliveryID string,
) error {
	if linker == nil {
		return errors.New("upload job LinkToExternalDelivery: nil linker (wire external_delivery_repo at bootstrap)")
	}
	if uploadJobID <= 0 {
		return fmt.Errorf("upload job LinkToExternalDelivery: uploadJobID must be positive (got %d)", uploadJobID)
	}
	if externalDeliveryID == "" {
		return errors.New("upload job LinkToExternalDelivery: empty externalDeliveryID")
	}
	if err := linker.LinkUploadJob(ctx, externalDeliveryID, uploadJobID); err != nil {
		return fmt.Errorf("upload job LinkToExternalDelivery: forward to external_delivery_repo.LinkUploadJob: %w", err)
	}
	return nil
}

// Create inserts a new upload job and returns the generated id plus timestamps.
// P1#4 — ingest_after + publish_at replace the old scheduled_at column
// (migration 049c). ingest_after is server-side DEFAULT NOW() so a
// fresh Insert without an explicit value lands at NOW() (the user's
// published window does not block ingest from ClaimBatch's
// perspective). publish_at is nullable so callers that want
// immediate publish (single-file imports, the historical default)
// pass nil. folder_id continues to be nullable (migration 038).
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// createUploadJob performs the INSERT of a new upload_jobs row against any
// queryer (*sql.DB or *sql.Tx). It is the shared implementation used by
// UploadJobRepository.Create and by ExternalDeliveryRepository's atomic
// create-and-link transaction so the SQL stays single-source-of-truth.
func createUploadJob(ctx context.Context, q queryer, job *models.UploadJob) error {
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
	// default_privacy_level verbatim. Metadata is the original publication
	// envelope and is retained for the post/publish workers.
	// also writes placeholder "" (DEFAULT '' from migration 053). order
	// matches the column list verbatim — column-list-vs-bind-list is a manual
	// invariant here, like every other INSERT in this repo.
	return q.QueryRowContext(ctx,
		`INSERT INTO upload_jobs
			(user_id, workspace_id, source_type, source_id, drive_account_id, folder_id,
			 title, caption, metadata, targets, status, ingest_after, publish_at, batch_id, default_privacy_level)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING id, created_at, updated_at`,
		job.UserID,
		job.WorkspaceID,
		string(job.SourceType),
		job.SourceID,
		job.DriveAccountID,
		folderID,
		job.Title,
		job.Caption,
		job.Metadata,
		targetsJSON,
		string(job.Status),
		job.IngestAfter,
		publishAt,
		batchID,
		job.DefaultPrivacyLevel,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)
}

func (r *UploadJobRepository) Create(job *models.UploadJob) error {
	return createUploadJob(context.Background(), r.db, job)
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
		        default_privacy_level, metadata
		 FROM upload_jobs
		 WHERE id = $1`,
		id,
	)
	return scanUploadJob(row)
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
