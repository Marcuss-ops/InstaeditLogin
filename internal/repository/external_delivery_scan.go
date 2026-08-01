package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// scanExternalDeliveryByKey is the SELECT companion used by Insert
// to look up an existing row. Returns ErrExternalDeliveryNotFound
// when no row matches so the caller can errors.Is-dispatch on it
// without sql.ErrNoRows noise leaking out of the repo boundary.
//
// `q` is interface{ QueryRowContext } — both *sql.Tx and *sql.DB
// satisfy it, so the same helper serves Insert (in-tx) and the
// public GetByIdempotencyKey (out-of-tx). This mirrors the
// scanUploadJob / scanImportBatch helpers in the same package.
func scanExternalDeliveryByKey(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, sourceSystem, idempotencyKey string) (*models.ExternalDelivery, error) {
	if sourceSystem == "" || idempotencyKey == "" {
		return nil, errors.New("scanExternalDeliveryByKey: empty key")
	}
	r, err := scanExternalDeliveryByRow(q.QueryRowContext(ctx,
		`SELECT id, source_system, external_delivery_id, idempotency_key, external_destination_id,
		        source_artifact_id, expected_sha256, expected_size_bytes, expected_mime_type,
		        download_url, metadata, publish_at, callback_url,
		        status, request_sha256,
		        upload_job_id, post_id,
		        platform_media_id, platform_url,
		        last_error_code, last_error_message,
		        created_at, updated_at, completed_at,
		        attempt_count, max_attempts,
		        lease_expires_at, next_attempt_at, leased_by_worker_id
		 FROM external_deliveries
		 WHERE source_system = $1 AND idempotency_key = $2
		 LIMIT 1`,
		sourceSystem, idempotencyKey,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExternalDeliveryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scanExternalDeliveryByKey: %w", err)
	}
	return r, nil
}

// scanExternalDeliveryByRow is the shared column-list scanner used by
// Insert + every read method. Column-list-vs-Scan-list is a manual
// invariant in this codebase — every method that lists one of these
// columns must list all of them in the same order. The scan helper
// is the single source of truth; adding a column to the
// external_deliveries table requires extending this helper AND the
// SELECT/INSERT statements that list the column.
func scanExternalDeliveryByRow(row *sql.Row) (*models.ExternalDelivery, error) {
	var (
		e                   models.ExternalDelivery
		rawStatus           string
		rawDownloadURL      sql.NullString
		rawCallbackURL      sql.NullString
		rawPublishAt        sql.NullTime
		rawUploadJobID      sql.NullInt64
		rawPostID           sql.NullInt64
		rawPlatformMediaID  sql.NullString
		rawPlatformURL      sql.NullString
		rawLastErrorCode    sql.NullString
		rawLastErrorMessage sql.NullString
		rawMetadata         []byte
		rawCompletedAt      sql.NullTime
		rawAttemptCount     sql.NullInt64
		rawMaxAttempts      sql.NullInt64
		rawLeaseExpiresAt   sql.NullTime
		rawNextAttemptAt    sql.NullTime
		rawLeasedByWorkerID sql.NullString
	)
	err := row.Scan(
		&e.ID, &e.SourceSystem, &e.ExternalDeliveryID, &e.IdempotencyKey, &e.ExternalDestinationID,
		&e.SourceArtifactID, &e.ExpectedSHA256, &e.ExpectedSizeBytes, &e.ExpectedMimeType,
		&rawDownloadURL, &rawMetadata, &rawPublishAt, &rawCallbackURL,
		&rawStatus, &e.RequestSHA256,
		&rawUploadJobID, &rawPostID,
		&rawPlatformMediaID, &rawPlatformURL,
		&rawLastErrorCode, &rawLastErrorMessage,
		&e.CreatedAt, &e.UpdatedAt, &rawCompletedAt,
		&rawAttemptCount, &rawMaxAttempts,
		&rawLeaseExpiresAt, &rawNextAttemptAt, &rawLeasedByWorkerID,
	)
	if err != nil {
		return nil, err
	}
	e.Status = models.ExternalDeliveryStatus(rawStatus)
	if rawDownloadURL.Valid {
		s := rawDownloadURL.String
		e.DownloadURL = &s
	}
	if rawCallbackURL.Valid {
		s := rawCallbackURL.String
		e.CallbackURL = &s
	}
	if rawPublishAt.Valid {
		t := rawPublishAt.Time
		e.PublishAt = &t
	}
	if rawCompletedAt.Valid {
		t := rawCompletedAt.Time
		e.CompletedAt = &t
	}
	if rawUploadJobID.Valid {
		v := rawUploadJobID.Int64
		e.UploadJobID = &v
	}
	if rawPostID.Valid {
		v := rawPostID.Int64
		e.PostID = &v
	}
	if rawPlatformMediaID.Valid {
		s := rawPlatformMediaID.String
		e.PlatformMediaID = &s
	}
	if rawPlatformURL.Valid {
		s := rawPlatformURL.String
		e.PlatformURL = &s
	}
	if rawLastErrorCode.Valid {
		s := rawLastErrorCode.String
		e.LastErrorCode = &s
	}
	if rawLastErrorMessage.Valid {
		s := rawLastErrorMessage.String
		e.LastErrorMessage = &s
	}
	if len(rawMetadata) > 0 {
		e.Metadata = json.RawMessage(rawMetadata)
	}
	if rawAttemptCount.Valid {
		e.AttemptCount = int(rawAttemptCount.Int64)
	}
	if rawMaxAttempts.Valid {
		e.MaxAttempts = int(rawMaxAttempts.Int64)
	}
	if rawLeaseExpiresAt.Valid {
		t := rawLeaseExpiresAt.Time
		e.LeaseExpiresAt = &t
	}
	if rawNextAttemptAt.Valid {
		t := rawNextAttemptAt.Time
		e.NextAttemptAt = &t
	}
	if rawLeasedByWorkerID.Valid {
		s := rawLeasedByWorkerID.String
		e.LeasedByWorkerID = &s
	}
	return &e, nil
}

// scanExternalDeliveryByRowFromRows bridges sql.Rows → the
// shared column-list scanner. Mirrors scanUploadJobRows in the
// upload_job_repo (which has the same ergonomic concern: rows.Scan
// and row.Scan share the same arg list; reusing the helper is
// mechanical but adds a layer of indirection).
func scanExternalDeliveryByRowFromRows(rows *sql.Rows) (*models.ExternalDelivery, error) {
	var (
		e                   models.ExternalDelivery
		rawStatus           string
		rawDownloadURL      sql.NullString
		rawCallbackURL      sql.NullString
		rawPublishAt        sql.NullTime
		rawUploadJobID      sql.NullInt64
		rawPostID           sql.NullInt64
		rawPlatformMediaID  sql.NullString
		rawPlatformURL      sql.NullString
		rawLastErrorCode    sql.NullString
		rawLastErrorMessage sql.NullString
		rawMetadata         []byte
		rawCompletedAt      sql.NullTime
		rawAttemptCount     sql.NullInt64
		rawMaxAttempts      sql.NullInt64
		rawLeaseExpiresAt   sql.NullTime
		rawNextAttemptAt    sql.NullTime
		rawLeasedByWorkerID sql.NullString
	)
	err := rows.Scan(
		&e.ID, &e.SourceSystem, &e.ExternalDeliveryID, &e.IdempotencyKey, &e.ExternalDestinationID,
		&e.SourceArtifactID, &e.ExpectedSHA256, &e.ExpectedSizeBytes, &e.ExpectedMimeType,
		&rawDownloadURL, &rawMetadata, &rawPublishAt, &rawCallbackURL,
		&rawStatus, &e.RequestSHA256,
		&rawUploadJobID, &rawPostID,
		&rawPlatformMediaID, &rawPlatformURL,
		&rawLastErrorCode, &rawLastErrorMessage,
		&e.CreatedAt, &e.UpdatedAt, &rawCompletedAt,
		&rawAttemptCount, &rawMaxAttempts,
		&rawLeaseExpiresAt, &rawNextAttemptAt, &rawLeasedByWorkerID,
	)
	if err != nil {
		return nil, err
	}
	e.Status = models.ExternalDeliveryStatus(rawStatus)
	if rawDownloadURL.Valid {
		s := rawDownloadURL.String
		e.DownloadURL = &s
	}
	if rawCallbackURL.Valid {
		s := rawCallbackURL.String
		e.CallbackURL = &s
	}
	if rawPublishAt.Valid {
		t := rawPublishAt.Time
		e.PublishAt = &t
	}
	if rawCompletedAt.Valid {
		t := rawCompletedAt.Time
		e.CompletedAt = &t
	}
	if rawUploadJobID.Valid {
		v := rawUploadJobID.Int64
		e.UploadJobID = &v
	}
	if rawPostID.Valid {
		v := rawPostID.Int64
		e.PostID = &v
	}
	if rawPlatformMediaID.Valid {
		s := rawPlatformMediaID.String
		e.PlatformMediaID = &s
	}
	if rawPlatformURL.Valid {
		s := rawPlatformURL.String
		e.PlatformURL = &s
	}
	if rawLastErrorCode.Valid {
		s := rawLastErrorCode.String
		e.LastErrorCode = &s
	}
	if rawLastErrorMessage.Valid {
		s := rawLastErrorMessage.String
		e.LastErrorMessage = &s
	}
	if len(rawMetadata) > 0 {
		e.Metadata = json.RawMessage(rawMetadata)
	}
	if rawAttemptCount.Valid {
		e.AttemptCount = int(rawAttemptCount.Int64)
	}
	if rawMaxAttempts.Valid {
		e.MaxAttempts = int(rawMaxAttempts.Int64)
	}
	if rawLeaseExpiresAt.Valid {
		t := rawLeaseExpiresAt.Time
		e.LeaseExpiresAt = &t
	}
	if rawNextAttemptAt.Valid {
		t := rawNextAttemptAt.Time
		e.NextAttemptAt = &t
	}
	if rawLeasedByWorkerID.Valid {
		s := rawLeasedByWorkerID.String
		e.LeasedByWorkerID = &s
	}
	return &e, nil
}
