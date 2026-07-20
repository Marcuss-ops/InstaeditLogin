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

// DeliverySessionRepository is the DAO for the delivery_sessions
// table introduced in migration 057. The destination workers
// (Google Drive today; future S3/MinIO/Velox ack) own their state
// machine through this repo. Methods are CAS-guarded against a
// version token (parallel to upload_jobs lease_owner CAS but
// cleaner for state owned by a delivery worker that has no
// upload_job lease to thread).
//
// Concurrency contract:
//
//   - FindByIdempotencyKey is the dispatch hot path (called on
//     every Deliver). MUST be cheap; UNIQUE(deliverable_type,
//     idempotency_key) index makes it O(1).
//   - Create uses ON CONFLICT DO NOTHING semantics on
//     (deliverable_type, idempotency_key) so a re-Deliver call
//     for an already-tracked idempotency_key returns (nil, nil)
//     instead of stubbing against the UNIQUE constraint. The
//     caller follows up with FindByIdempotencyKey for the canonical
//     row.
//   - UpdateProgress + MarkCompleted + MarkFailed all bump version
//     atomically; CAS loss surfaces as ErrDeliverySessionVersionMismatch
//     so the caller can retry-with-exponential-backoff (a
//     concurrent worker racing on the same row is a known-outcome).
type DeliverySessionRepository struct {
	db *sql.DB
}

// NewDeliverySessionRepository constructs the repo.
func NewDeliverySessionRepository(db *sql.DB) *DeliverySessionRepository {
	return &DeliverySessionRepository{db: db}
}

// ErrDeliverySessionNotFound is the typed sentinel FindByIdempotencyKey
// returns when no row matches (deliverable_type, idempotency_key).
// Distinguishes "first-time delivery" from "row existed but was
// lost / archived" so the destination can decide between (a) POST
// + INSERT and (b) app-property lookup + INSERT on a hard wipe.
var ErrDeliverySessionNotFound = errors.New("delivery session not found")

// ErrDeliverySessionVersionMismatch is the typed sentinel the
// version-CAS UPDATE returns when the row was modified by a peer
// between FindByIdempotencyKey and Update. The worker treats it
// as transient + retries (re-Find + re-Update); surfacing the
// sentinel prevents silently overwriting a peer's progress.
var ErrDeliverySessionVersionMismatch = errors.New("delivery session version CAS mismatch")

// Create inserts a new row. ON CONFLICT DO NOTHING on
// (deliverable_type, idempotency_key) — a duplicate idempotency_key
// (re-deliver of the same target) returns (nil, nil) so the caller
// can follow up with FindByIdempotencyKey for the canonical row.
//
// Parameters:
//
//	ds — must have non-zero DeliverableType, IdempotencyKey, TotalBytes,
//	     ChunkSize, MIMEType. ID + timestamps are populated by the
//	     RETURNING clause.
func (r *DeliverySessionRepository) Create(ctx context.Context, ds *models.DeliverySession) error {
	if ds == nil {
		return errors.New("delivery session Create: nil session")
	}
	if ds.DeliverableType == "" {
		return errors.New("delivery session Create: empty deliverable_type")
	}
	if ds.IdempotencyKey == "" {
		return errors.New("delivery session Create: empty idempotency_key")
	}
	if ds.TotalBytes <= 0 {
		return fmt.Errorf("delivery session Create: non-positive total_bytes (%d)", ds.TotalBytes)
	}
	if ds.ChunkSize <= 0 {
		return fmt.Errorf("delivery session Create: non-positive chunk_size (%d)", ds.ChunkSize)
	}
	if ds.MIMEType == "" {
		return errors.New("delivery session Create: empty mime_type")
	}

	appPropsJSON, err := json.Marshal(ds.AppProperties)
	if err != nil {
		return fmt.Errorf("delivery session Create: marshal app_properties: %w", err)
	}

	var leaseExpiresAt, expiresAt sql.NullTime
	if ds.LeaseExpiresAt != nil {
		leaseExpiresAt = sql.NullTime{Time: *ds.LeaseExpiresAt, Valid: true}
	}
	if ds.ExpiresAt != nil {
		expiresAt = sql.NullTime{Time: *ds.ExpiresAt, Valid: true}
	}

	var folderID, filename, sessionURIEnc, workerID, errorMsg, errorCode sql.NullString
	if ds.FolderID != "" {
		folderID = sql.NullString{String: ds.FolderID, Valid: true}
	}
	if ds.Filename != "" {
		filename = sql.NullString{String: ds.Filename, Valid: true}
	}
	if ds.SessionURIEncrypted != "" {
		sessionURIEnc = sql.NullString{String: ds.SessionURIEncrypted, Valid: true}
	}
	if ds.WorkerID != "" {
		workerID = sql.NullString{String: ds.WorkerID, Valid: true}
	}
	if ds.ErrorMessage != "" {
		errorMsg = sql.NullString{String: ds.ErrorMessage, Valid: true}
	}
	if ds.ErrorCode != "" {
		errorCode = sql.NullString{String: ds.ErrorCode, Valid: true}
	}

	err = r.db.QueryRowContext(ctx,
		`INSERT INTO delivery_sessions
            (deliverable_type, idempotency_key, state,
             session_uri_encrypted, uploaded_bytes, total_bytes, chunk_size,
             mime_type, folder_id, filename, app_properties,
             worker_id, lease_expires_at, expires_at,
             error_message, error_code, attempt_count, version)
         VALUES ($1, $2, $3,
             $4, $5, $6, $7,
             $8, $9, $10, $11,
             $12, $13, $14,
             $15, $16, $17, 1)
         ON CONFLICT (deliverable_type, idempotency_key) DO NOTHING
         RETURNING id, created_at, updated_at, version`,
		ds.DeliverableType,
		ds.IdempotencyKey,
		string(ds.State),
		sessionURIEnc,
		ds.UploadedBytes,
		ds.TotalBytes,
		ds.ChunkSize,
		ds.MIMEType,
		folderID,
		filename,
		appPropsJSON,
		workerID,
		leaseExpiresAt,
		expiresAt,
		errorMsg,
		errorCode,
		ds.AttemptCount,
	).Scan(&ds.ID, &ds.CreatedAt, &ds.UpdatedAt, &ds.Version)
	if errors.Is(err, sql.ErrNoRows) {
		// ON CONFLICT DO NOTHING — a duplicate idempotency_key
		// (already-tracked). Caller follows up with FindByIdempotencyKey.
		return nil
	}
	if err != nil {
		return fmt.Errorf("delivery session Create: %w", err)
	}
	return nil
}

// FindByIdempotencyKey returns the row with matching
// (deliverable_type, idempotency_key), or (nil, ErrDeliverySessionNotFound).
// The dispatch hot path — called on every Deliver().
func (r *DeliverySessionRepository) FindByIdempotencyKey(ctx context.Context, deliverableType, idempotencyKey string) (*models.DeliverySession, error) {
	if deliverableType == "" {
		return nil, errors.New("delivery session FindByIdempotencyKey: empty deliverable_type")
	}
	if idempotencyKey == "" {
		return nil, errors.New("delivery session FindByIdempotencyKey: empty idempotency_key")
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT id, deliverable_type, idempotency_key, state,
		        session_uri_encrypted, uploaded_bytes, total_bytes, chunk_size,
		        mime_type, folder_id, filename, app_properties,
		        remote_file_id, remote_url,
		        worker_id, lease_expires_at, expires_at,
		        error_message, error_code, attempt_count, version,
		        created_at, updated_at
		   FROM delivery_sessions
		  WHERE deliverable_type = $1 AND idempotency_key = $2`,
		deliverableType, idempotencyKey,
	)
	ds, err := scanDeliverySession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeliverySessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("delivery session FindByIdempotencyKey: %w", err)
	}
	return ds, nil
}

// UpdateProgress persists a chunk-loop progress claim:
// state=uploading, session_uri_encrypted=<ciphertext>, uploaded_bytes=<offset>.
// Bumps version atomically (CAS).
//
// expectedVersion MUST equal the version the worker observed in
// FindByIdempotencyKey immediately before this call. A mismatch
// (concurrent writer beat us) returns ErrDeliverySessionVersionMismatch
// and the caller should re-Find + re-Update.
func (r *DeliverySessionRepository) UpdateProgress(
	ctx context.Context,
	id int64,
	expectedVersion int,
	sessionURIEncrypted string,
	uploadedBytes int64,
	workerID string,
) error {
	if id <= 0 {
		return errors.New("delivery session UpdateProgress: non-positive id")
	}
	if sessionURIEncrypted == "" {
		return errors.New("delivery session UpdateProgress: empty sessionURIEncrypted")
	}
	if uploadedBytes < 0 {
		return fmt.Errorf("delivery session UpdateProgress: negative uploadedBytes (%d)", uploadedBytes)
	}
	if workerID == "" {
		return errors.New("delivery session UpdateProgress: empty workerID")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE delivery_sessions
            SET session_uri_encrypted = $2,
                uploaded_bytes         = $3,
                state                  = 'uploading',
                worker_id              = $4,
                version                = version + 1,
                updated_at             = NOW()
          WHERE id      = $1
            AND version = $5`,
		id, sessionURIEncrypted, uploadedBytes, workerID, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("delivery session UpdateProgress: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delivery session UpdateProgress rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d expectedVersion=%d", ErrDeliverySessionVersionMismatch, id, expectedVersion)
	}
	return nil
}

// MarkCompleted is terminal-success: state='completed',
// remote_file_id + remote_url stamped, session_uri_encrypted cleared
// (the URI is now dead; nothing should reuse it). Bumps version.
func (r *DeliverySessionRepository) MarkCompleted(
	ctx context.Context,
	id int64,
	expectedVersion int,
	remoteFileID, remoteURL, workerID string,
) error {
	if id <= 0 {
		return errors.New("delivery session MarkCompleted: non-positive id")
	}
	if remoteFileID == "" {
		return errors.New("delivery session MarkCompleted: empty remote_file_id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE delivery_sessions
            SET remote_file_id        = $2,
                remote_url            = $3,
                session_uri_encrypted = NULL,
                state                 = 'completed',
                error_message         = NULL,
                error_code            = NULL,
                worker_id             = $4,
                lease_expires_at      = NULL,
                attempt_count         = attempt_count + 1,
                version               = version + 1,
                updated_at            = NOW()
          WHERE id      = $1
            AND version = $5`,
		id, remoteFileID, remoteURL, workerID, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("delivery session MarkCompleted: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delivery session MarkCompleted rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d expectedVersion=%d", ErrDeliverySessionVersionMismatch, id, expectedVersion)
	}
	return nil
}

// MarkFailed is terminal-fail: state='failed', error_message +
// error_code stamped, lease released, attempt_count incremented.
// The worker treats this as retry-eligible (next Deliver() with the
// same idempotency_key resumes from the persisted offset unless
// expires_at is in the past, which flips state to 'expired' first).
func (r *DeliverySessionRepository) MarkFailed(
	ctx context.Context,
	id int64,
	expectedVersion int,
	errorCode, errorMessage, workerID string,
) error {
	if id <= 0 {
		return errors.New("delivery session MarkFailed: non-positive id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE delivery_sessions
            SET state           = 'failed',
                error_code      = $2,
                error_message   = $3,
                worker_id       = $4,
                lease_expires_at = NULL,
                attempt_count   = attempt_count + 1,
                version         = version + 1,
                updated_at      = NOW()
          WHERE id      = $1
            AND version = $5`,
		id, errorCode, errorMessage, workerID, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("delivery session MarkFailed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delivery session MarkFailed rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d expectedVersion=%d", ErrDeliverySessionVersionMismatch, id, expectedVersion)
	}
	return nil
}

// DeleteByID removes a row outright. Used by the destination's
// TTL/hard-reset path: when a row reaches state="expired" OR the
// expires_at cursor leaks past the current time, the destination
// deletes the row and re-creates it with a fresh session URI +
// version=1 — the DB UNIQUE(deliverable_type, idempotency_key)
// constraint plus Create's ON CONFLICT DO NOTHING semantics
// would otherwise leave the expired row in place and the next
// re-Create would be a silent no-op. Deletion is the only way to
// guarantee the re-initiate path lands a usable row.
//
// CAS: the version-CAS clause guards against a peer worker's
// concurrent Delete on the same row (CAS loss → ErrDeliverySessionNotFound
// so we don't double-delete and don't accidentally delete a peer's
// fresh replacement row).
func (r *DeliverySessionRepository) DeleteByID(ctx context.Context, id int64, expectedVersion int) error {
	if id <= 0 {
		return errors.New("delivery session DeleteByID: non-positive id")
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM delivery_sessions
		  WHERE id      = $1
		    AND version = $2`,
		id, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("delivery session DeleteByID: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delivery session DeleteByID rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d expectedVersion=%d (no row deleted — peer already modified or row vanished)", ErrDeliverySessionVersionMismatch, id, expectedVersion)
	}
	return nil
}

// MarkExpired marks a row whose session_uri_encrypted has outlived
// Google's 7-day TTL. The next Deliver call discards the dead URI
// and re-issues POST /upload/drive/v3/files.
func (r *DeliverySessionRepository) MarkExpired(
	ctx context.Context,
	id int64,
	expectedVersion int,
) error {
	if id <= 0 {
		return errors.New("delivery session MarkExpired: non-positive id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE delivery_sessions
            SET state                 = 'expired',
                session_uri_encrypted = NULL,
                error_code            = 'drive_session_expired',
                error_message         = 'Drive resumable session exceeded 7d TTL; re-initiating',
                version               = version + 1,
                updated_at            = NOW()
          WHERE id      = $1
            AND version = $2`,
		id, expectedVersion,
	)
	if err != nil {
		return fmt.Errorf("delivery session MarkExpired: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delivery session MarkExpired rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d expectedVersion=%d", ErrDeliverySessionVersionMismatch, id, expectedVersion)
	}
	return nil
}

// scanDeliverySession is the private SELECT scanner shared between
// FindByIdempotencyKey and any future query. The 24-column list must
// stay in lockstep with the SELECT projection above; a mismatch
// causes sql.Scan to return an error on the first row hit.
//
// Columns past index 7 use sql.Null* wrappers because they're
// nullable on disk. Time columns use sql.NullTime to preserve the
// NULL-vs-zero distinction (a NULL lease_expires_at means "no
// lease held"; a zero-time stamp would mis-classify that as an
// expired-in-1970 lease).
func scanDeliverySession(row *sql.Row) (*models.DeliverySession, error) {
	var ds models.DeliverySession
	var rawState string
	var sessionURIEnc sql.NullString
	var folderID, filename, remoteFileID, remoteURL sql.NullString
	var workerID, errorMsg, errorCode sql.NullString
	var leaseExpiresAt, expiresAt sql.NullTime
	var appPropsJSON []byte

	err := row.Scan(
		&ds.ID,
		&ds.DeliverableType,
		&ds.IdempotencyKey,
		&rawState,
		&sessionURIEnc,
		&ds.UploadedBytes,
		&ds.TotalBytes,
		&ds.ChunkSize,
		&ds.MIMEType,
		&folderID,
		&filename,
		&appPropsJSON,
		&remoteFileID,
		&remoteURL,
		&workerID,
		&leaseExpiresAt,
		&expiresAt,
		&errorMsg,
		&errorCode,
		&ds.AttemptCount,
		&ds.Version,
		&ds.CreatedAt,
		&ds.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	ds.State = models.DeliverySessionState(rawState)
	if sessionURIEnc.Valid {
		ds.SessionURIEncrypted = sessionURIEnc.String
	}
	if folderID.Valid {
		ds.FolderID = folderID.String
	}
	if filename.Valid {
		ds.Filename = filename.String
	}
	if remoteFileID.Valid {
		ds.RemoteFileID = remoteFileID.String
	}
	if remoteURL.Valid {
		ds.RemoteURL = remoteURL.String
	}
	if workerID.Valid {
		ds.WorkerID = workerID.String
	}
	if errorMsg.Valid {
		ds.ErrorMessage = errorMsg.String
	}
	if errorCode.Valid {
		ds.ErrorCode = errorCode.String
	}
	if leaseExpiresAt.Valid {
		t := leaseExpiresAt.Time
		ds.LeaseExpiresAt = &t
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		ds.ExpiresAt = &t
	}
	if len(appPropsJSON) > 0 {
		if err := json.Unmarshal(appPropsJSON, &ds.AppProperties); err != nil {
			return nil, fmt.Errorf("unmarshal delivery session app_properties: %w", err)
		}
	}
	return &ds, nil
}

// deliverySessionClock is exposed at package level for tests to
// inject deterministic timeouts. Production uses time.Now via the
// value passed into GoogleDriveDestination's NewXxx function.
var deliverySessionClock = time.Now
