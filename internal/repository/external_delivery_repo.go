package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ExternalDeliveryLockNamespace is the advisory-lock namespace used
// by Insert to serialise concurrent inserts/replays of the same
// (source_system, idempotency_key) pair. Released automatically on
// COMMIT/ROLLBACK. Intentionally distinct from any other advisory
// lock in the codebase (idempotency_repo.go uses 0xA11D17 for its
// ErrIdempotencyKeyCollided namespace) so the two-key pg_advisory_xact_lock(int4,int4)
// form doesn't accidentally share a slot — Postgres hashtext
// returns int4 so 2^32-1 is the addressable range; overlapping
// namespaces would let a Velox insert and an idempotency-key insert
// serialise on each other, which is wrong.
//
// 0xB8111E is the chosen value: stays within Postgres' int4 limit
// and visually distinct from the other repo's hex. Not
// security-sensitive — the value merely discriminates from
// other tenants of pg_advisory_xact_lock in the same database.
const ExternalDeliveryLockNamespace int32 = 0xB8111E

// ExternalDeliveryRepository handles persistence for the
// external_deliveries table (migration 055_external_deliveries.sql).
//
// This is the most idempotency-sensitive repo in the codebase. The
// Velox ↔ InstaEdit delivery contract has a precise three-way
// outcome on POST /internal/v1/deliveries:
//
//	(a) first-ever POST for idempotency_key K → INSERT a new row,
//	    return (new record, nil)
//	(b) replay of POST for K + SAME body SHA-256 → return the
//	    existing row unchanged (no second INSERT), to honour
//	    "at-most-once" semantics across Velox retries
//	(c) replay of POST for K + DIFFERENT body SHA-256 → return
//	    ErrIdempotencyConflict; the handler maps this to 409 so the
//	    upstream sees "your retry carries a different payload — fix
//	    your code, don't retry with new body" without ever
//	    persisting the conflicting record
//
// All three outcomes share a single transaction:
//
//  1. SELECT pg_advisory_xact_lock($1, hashtext($2 || ':' || $3))
//     — serialises concurrent inserts/replays of the same key
//     (release on COMMIT/ROLLBACK). $1 is the namespace
//     ExternalDeliveryLockNamespace (0xB8111E). hashtext() is
//     Postgres' stable 32-bit hash; pg_advisory_xact_lock(int4,
//     int4) is the two-key form.
//  2. SELECT existing row by (source_system, idempotency_key).
//     Compare request_sha256 →
//     - match → reuse, COMMIT, return existing
//     - mismatch → ROLLBACK, return ErrIdempotencyConflict
//     - no row → INSERT (the advisory lock guarantees no peer
//     can race an INSERT for the same key in this window)
//  3. INSERT the new row, COMMIT, return the new row.
//
// ON CONFLICT DO NOTHING is intentionally NOT used here: the lock
// already guarantees no peer can race, so ON CONFLICT would be
// defensive noise and would obscure the "there is no row missing"
// failure mode in production logs (an INSERT that silently no-ops
// is harder to diagnose than one that hits the unique_violation).
//
// Authz is upstream of this layer: the handler verifies the
// external_destination referenced by the request body belongs to a
// workspace the caller's JWT user owns; the repo therefore trusts
// the supplied external_destination_id.
type ExternalDeliveryRepository struct {
	db *sql.DB
}

// NewExternalDeliveryRepository creates a repo bound to db.
func NewExternalDeliveryRepository(db *sql.DB) *ExternalDeliveryRepository {
	return &ExternalDeliveryRepository{db: db}
}

// ErrExternalDeliveryNotFound is the typed sentinel returned by
// GetByID / GetByIdempotencyKey / GetByExternalDeliveryID when no
// row matches. Maps to HTTP 404 via errors.Is at the API layer.
// Mirrors ErrWorkspaceNotFound / ErrExternalDestinationNotFound.
var ErrExternalDeliveryNotFound = errors.New("external delivery not found")

// Insert is the core idempotency-aware write path. The three
// outcome semantics are documented on the type doc-comment.
//
// The record's ID is supplied (application-side ULID with `sdel_`
// prefix per the spec). When the caller has not computed
// RequestSHA256 yet, pass rawBody and the function will compute the
// 64-char hex SHA-256 from rawBody — this is the recommended
// pattern from the Veloxes → InstaEdit contract, where the handler
// reads the raw body ONCE (for the body-hash) and then re-uses the
// body for the JSON decode; the repo Save signature keeps the
// two-step ergonomics clean.
//
// When the caller has already computed request_sha256 (e.g. the
// handler passed pre-parsed + the hash via a previous sha256.Sum256()
// call to avoid double-reading), pass rawBody=nil and set
// e.RequestSHA256 directly. Both paths converge on the same
// commit-byte storage layout.
//
// Returns:
//   - (*ExternalDelivery, nil) on fresh insert OR same-body replay
//   - (*ExternalDelivery, ErrIdempotencyConflict) on different-body replay (409)
//   - (nil, error) on validation / DB errors
func (r *ExternalDeliveryRepository) Insert(ctx context.Context, e *models.ExternalDelivery, rawBody []byte) (*models.ExternalDelivery, error) {
	if e == nil {
		return nil, errors.New("external delivery Insert: nil record")
	}
	if e.ID == "" {
		return nil, errors.New("external delivery Insert: id is required (application-side ULID with sdel_ prefix)")
	}
	if e.SourceSystem == "" || e.IdempotencyKey == "" {
		return nil, errors.New("external delivery Insert: source_system and idempotency_key are required")
	}
	if e.ExternalDeliveryID == "" {
		return nil, errors.New("external delivery Insert: external_delivery_id is required (upstream's own id)")
	}
	if e.ExternalDestinationID == "" {
		return nil, errors.New("external delivery Insert: external_destination_id is required")
	}
	if e.SourceArtifactID == "" || e.ExpectedSHA256 == "" || e.ExpectedMimeType == "" {
		return nil, errors.New("external delivery Insert: source_artifact_id, expected_sha256, expected_mime_type are required")
	}
	if e.ExpectedSizeBytes <= 0 {
		return nil, errors.New("external delivery Insert: expected_size_bytes must be positive")
	}
	if len(rawBody) > 0 && e.RequestSHA256 == "" {
		sum := sha256.Sum256(rawBody)
		e.RequestSHA256 = hex.EncodeToString(sum[:])
	} else if e.RequestSHA256 == "" && len(rawBody) == 0 {
		return nil, errors.New("external delivery Insert: either RequestSHA256 or rawBody must be supplied")
	}
	if len(e.Metadata) == 0 {
		e.Metadata = json.RawMessage("{}")
	}
	if !json.Valid(e.Metadata) {
		return nil, fmt.Errorf("external delivery Insert: metadata is not valid JSON: %s", string(e.Metadata))
	}

	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("external delivery Insert begin tx: %w", err)
	}
	defer tx.Rollback()

	// Serialise concurrent inserts/replays for the same key. Released
	// on COMMIT or ROLLBACK. See type doc-comment.
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock($1, hashtext($2 || ':' || $3))`,
		ExternalDeliveryLockNamespace, e.SourceSystem, e.IdempotencyKey,
	); err != nil {
		return nil, fmt.Errorf("external delivery Insert advisory lock: %w", err)
	}

	// Look up an existing row that matches the (source_system,
	// idempotency_key) pair.
	existing, lookupErr := scanExternalDeliveryByKey(ctx, tx, e.SourceSystem, e.IdempotencyKey)
	if lookupErr == nil {
		// Row exists. Compare SHA-256 — case-insensitive (the SHA hex
		// from sha256.Sum256 is lowercase; upstream callers may submit
		// uppercase, so a fold comparison is the right safety net).
		if !strings.EqualFold(existing.RequestSHA256, e.RequestSHA256) {
			return existing, ErrIdempotencyConflict
		}
		// Same SHA — replay. Commit is technically a no-op for an
		// empty tx but keeps the operation traceable in pg_stat_activity.
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, fmt.Errorf("external delivery Insert commit (replay): %w", commitErr)
		}
		return existing, nil
	} else if !errors.Is(lookupErr, ErrExternalDeliveryNotFound) {
		return nil, fmt.Errorf("external delivery Insert lookup: %w", lookupErr)
	}

	// No existing row. Insert. The advisory lock guarantees no peer
	// can race a same-key INSERT into existence during this window;
	// if a future migration removes the lock (or the lock is bypassed
	// by a peer-thread using a different code path) the UNIQUE
	// constraint will surface a 23505 anyway.
	if len(e.Metadata) == 0 {
		e.Metadata = json.RawMessage("{}")
	}

	// Bind args. Nullable columns use sql.NullT via interface{} holding nil.
	// upload_job_id and post_id are intentionally NOT bound at INSERT
	// time — the canonical pattern is: Worker → Insert external_delivery →
	// create upload_job (with batch_id or external_delivery link) →
	// LinkUploadJob stamps the FK after creation through a separate
	// single-shot UPDATE (filters on upload_job_id IS NULL). INSERTing
	// here with either or both would be silently overwritten by the
	// later LinkUploadJob stamp OR create a 2-step stale FK state
	// (insert with FK + link with NEW FK) that the new
	// WHERE upload_job_id IS NULL clause cannot recover from. The
	// INSERT-then-LINK order keeps the NOT NULL partial index for the
	// publish-pool claim scan free of half-stamped rows
	// (upload_job_id IS NULL → claim pool skips).
	var (
		downloadURL interface{}
		callbackURL interface{}
		publishAt   interface{}
	)
	if e.DownloadURL != nil {
		downloadURL = *e.DownloadURL
	}
	if e.CallbackURL != nil {
		callbackURL = *e.CallbackURL
	}
	if e.PublishAt != nil {
		publishAt = *e.PublishAt
	}

	inserted, insertErr := scanExternalDeliveryByRow(tx.QueryRowContext(ctx,
		`INSERT INTO external_deliveries
		    (id, source_system, external_delivery_id, idempotency_key,
		     external_destination_id,
		     source_artifact_id, expected_sha256, expected_size_bytes, expected_mime_type,
		     download_url, metadata, publish_at, callback_url,
		     status, request_sha256,
		     created_at, updated_at)
		 VALUES ($1, $2, $3, $4,
		         $5,
		         $6, $7, $8, $9,
		         $10, $11, $12, $13,
		         $14, $15,
		         NOW(), NOW())		RETURNING id, source_system, external_delivery_id, idempotency_key, external_destination_id,
		           source_artifact_id, expected_sha256, expected_size_bytes, expected_mime_type,
		           download_url, metadata, publish_at, callback_url,
		           status, request_sha256,
		           upload_job_id, post_id,
		           platform_media_id, platform_url,
		           last_error_code, last_error_message,
		           created_at, updated_at, completed_at,
		           attempt_count, max_attempts,
		           lease_expires_at, next_attempt_at, leased_by_worker_id`,
		e.ID, e.SourceSystem, e.ExternalDeliveryID, e.IdempotencyKey,
		e.ExternalDestinationID,
		e.SourceArtifactID, e.ExpectedSHA256, e.ExpectedSizeBytes, e.ExpectedMimeType,
		downloadURL, []byte(e.Metadata), publishAt, callbackURL,
		string(models.ExternalDeliveryStatusAccepted), e.RequestSHA256,
	))
	if insertErr != nil {
		// 23505 from the locked-twice race-window (should not happen
		// in practice but the constraint is defence in depth).
		var pqErr *pq.Error
		if errors.As(insertErr, &pqErr) && pqErr.Code == "23505" {
			return nil, fmt.Errorf("%w: source_system=%s idempotency_key=%s",
				ErrIdempotencyConflict, e.SourceSystem, e.IdempotencyKey)
		}
		return nil, fmt.Errorf("external delivery Insert: %w", insertErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return nil, fmt.Errorf("external delivery Insert commit: %w", commitErr)
	}
	return inserted, nil
}

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

// GetByID returns the row with the supplied application-issued id
// (the `sdel_01J...` opaque social-delivery id). Returns (nil, nil)
// when no row matches — distinct from ErrExternalDeliveryNotFound
// which is reserved for typed-dispatch sentinel matching.
//
// Mirrors the (nil, nil) convention of GetByIdempotencyKey /
// GetByExternalDeliveryID; the public-methods return (nil, nil) for
// not-found and the internal helper scanExternalDeliveryByKey
// returns ErrExternalDeliveryNotFound for the same condition so the
// idempotency semantics in Insert can errors.Is-dispatch cleanly.
func (r *ExternalDeliveryRepository) GetByID(ctx context.Context, id string) (*models.ExternalDelivery, error) {
	if id == "" {
		return nil, errors.New("external delivery GetByID: empty id")
	}
	r2, err := scanExternalDeliveryByRow(r.db.QueryRowContext(ctx,
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
		 WHERE id = $1`,
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("external delivery GetByID: %w", err)
	}
	return r2, nil
}

// GetByIdempotencyKey returns the row matching the
// (source_system, idempotency_key) pair. Used by:
//   - The dashboard's "trace this Velox retry" lookup
//   - The /internal/v1/deliveries/{social_delivery_id} GET handler
//     when the upstream passes a delivery id that was a remap of an
//     earlier idempotency key (rare; Velox is supposed to send the
//     same idempotency_key on retry, but if it sends a DIFFERENT key
//     while reusing the social_delivery_id, the handler uses index
//     (source_system, external_delivery_id) to disambiguate).
func (r *ExternalDeliveryRepository) GetByIdempotencyKey(ctx context.Context, sourceSystem, idempotencyKey string) (*models.ExternalDelivery, error) {
	if sourceSystem == "" || idempotencyKey == "" {
		return nil, errors.New("external delivery GetByIdempotencyKey: empty key")
	}
	r2, err := scanExternalDeliveryByKey(ctx, r.db, sourceSystem, idempotencyKey)
	if errors.Is(err, ErrExternalDeliveryNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("external delivery GetByIdempotencyKey: %w", err)
	}
	return r2, nil
}

// GetByExternalDeliveryID returns the row matching the upstream's
// own delivery id (e.g. the `delivery_8cc0f...` Velox id). Used by:
//   - The /internal/v1/destinations/{id}/validate cross-trace lookup
//   - The /admin/health dashboard for "did Velox actually hand us
//     delivery X?" — operator-grade audit lookup.
//   - The callback dispatcher when the upstream sends a status event
//     keyed on its own delivery id AND we want to bridge to our
//     social_delivery_id without a separate reconciliation layer.
func (r *ExternalDeliveryRepository) GetByExternalDeliveryID(ctx context.Context, sourceSystem, externalDeliveryID string) (*models.ExternalDelivery, error) {
	if sourceSystem == "" || externalDeliveryID == "" {
		return nil, errors.New("external delivery GetByExternalDeliveryID: empty key")
	}
	r2, err := scanExternalDeliveryByRow(r.db.QueryRowContext(ctx,
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
		 WHERE source_system = $1 AND external_delivery_id = $2`,
		sourceSystem, externalDeliveryID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("external delivery GetByExternalDeliveryID: %w", err)
	}
	return r2, nil
}

// ListByStatus returns every row whose status matches the supplied
// filter, ordered by created_at ASC (claim-ready first). The
// `limit` argument bounds the result set to keep a single tick
// cheap even at 100k rows; the partial index
// idx_external_deliveries_worker_pool serves the active set in
// O(active-row count).
//
// limit <= 0 returns up to 100 (matches the upload_worker pool
// ClaimBatch default).
func (r *ExternalDeliveryRepository) ListByStatus(ctx context.Context, status models.ExternalDeliveryStatus, limit int) ([]models.ExternalDelivery, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx,
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
		 WHERE status = $1
		 ORDER BY created_at ASC
		 LIMIT $2`,
		string(status), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("external delivery ListByStatus: %w", err)
	}
	defer rows.Close()

	out := make([]models.ExternalDelivery, 0, 16)
	for rows.Next() {
		// sql.Rows type doesn't satisfy *sql.Row directly; use a
		// local helper closure to delegate to scanExternalDeliveryByRow's
		// underlying column list.
		e, scanErr := scanExternalDeliveryByRowFromRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("external delivery ListByStatus scan: %w", scanErr)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("external delivery ListByStatus iterate: %w", err)
	}
	return out, nil
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

// UpdateStatus transitions a delivery row to a new status alongside
// optional error metadata (last_error_code + last_error_message) and
// optional platform identifiers (platform_media_id + platform_url).
// All fields except id and status are nullable pointers — nil
// preserves the existing value via COALESCE.
//
// Sets completed_at = NOW() automatically when transitioning to a
// terminal state (Published / Failed / DeadLetter / BlockedAuth).
// Non-terminal state transitions leave completed_at untouched.
//
// The CAS contract: zero rows affected means the row was deleted
// between the caller-side Lookup and this Update (rare; possible
// only via a manual operator DELETE). Returns
// ErrExternalDeliveryNotFound wrapped with id context.
//
// This method does NOT take an advisory lock — concurrent state
// transitions are not idempotent in the same way the Insert is;
// the worker that wins is the worker that gets rows_affected = 1.
// State-machine correctness should be enforced one level up
// (publish_worker's state transition guard in
// ingest_fsm_state.go); this repo is the SQL surface.
func (r *ExternalDeliveryRepository) UpdateStatus(ctx context.Context, id string, newStatus models.ExternalDeliveryStatus, lastErrorCode, lastErrorMessage, platformMediaID, platformURL *string) error {
	if id == "" {
		return errors.New("external delivery UpdateStatus: empty id")
	}
	if newStatus == "" {
		return errors.New("external delivery UpdateStatus: empty newStatus")
	}

	// COALESCE-friendly nil-resolving for the optional fields.
	var codeArg, msgArg, midArg, purlArg interface{}
	if lastErrorCode != nil {
		codeArg = *lastErrorCode
	}
	if lastErrorMessage != nil {
		msgArg = *lastErrorMessage
	}
	if platformMediaID != nil {
		midArg = *platformMediaID
	}
	if platformURL != nil {
		purlArg = *platformURL
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status              = $2,
		     last_error_code     = COALESCE($3, last_error_code),
		     last_error_message  = COALESCE($4, last_error_message),
		     platform_media_id   = COALESCE($5, platform_media_id),
		     platform_url        = COALESCE($6, platform_url),
		     updated_at          = NOW(),
		     completed_at        = CASE
		         WHEN $2 IN ('published', 'failed', 'dead_letter', 'blocked_auth')
		              AND completed_at IS NULL
		         THEN NOW()
		         ELSE completed_at
		     END
		 WHERE id = $1`,
		id, string(newStatus),
		codeArg, msgArg, midArg, purlArg,
	)
	if err != nil {
		return fmt.Errorf("external delivery UpdateStatus: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery UpdateStatus rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
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

// ErrExternalDeliveryNotLinked is the typed sentinel callers (the
// worker via errors.Is dispatch) match against when no
// external_delivery row is linked to the upload_job yet.
var ErrExternalDeliveryNotLinked = errors.New("external delivery not linked to upload job")

// ErrExternalDeliveryAlreadyClaimed is returned when a worker tries to
// atomically create an upload_job for a delivery row that another worker
// already claimed (status != 'accepted' or upload_job_id already set).
var ErrExternalDeliveryAlreadyClaimed = errors.New("external delivery already claimed")

// ErrExternalDeliveryNoExpectedTriple is the typed sentinel
// when the external_delivery row exists but (size, sha) fields
// are empty/zero.
var ErrExternalDeliveryNoExpectedTriple = errors.New("external delivery has no expected triple")

// GetExpectedTripleByUploadJobID returns (expected_size_bytes,
// expected_sha256_hex) for the external_delivery row linked to
// uploadJobID. Sentinel dispatch is via errors.Is.
// CreateUploadJobAndLink creates an upload_jobs row and atomically claims the
// external_deliveries row for it in a single transaction. The claim UPDATE
// filters on status='accepted' AND upload_job_id IS NULL, so only one worker
// can win the race. If the claim fails (0 rows affected) the transaction is
// rolled back and ErrExternalDeliveryAlreadyClaimed is returned, leaving the
// delivery row untouched for the winner. On success the delivery row is
// left with status='downloading' and upload_job_id set, and the new upload
// job ID is returned.
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
		   AND status       = 'accepted'
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
// the calling worker. Eligible rows are in status 'accepted' whose lease has
// expired (or never existed) and whose next_attempt_at window has opened (or
// is unset). The selected row is locked with FOR UPDATE SKIP LOCKED, then its
// attempt_count is incremented, lease_expires_at is set to NOW() + lease,
// leased_by_worker_id is stamped, and next_attempt_at is cleared. The
// status remains 'accepted' so that CreateUploadJobAndLink can perform the
// final state transition to 'downloading' while still verifying the lease.
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
		     WHERE status = 'accepted'
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

// MarkRetry releases the lease on a delivery and schedules a retry. The
// worker calls this when processing fails with a transient error. If the
// delivery has exhausted its retry budget, the row is instead marked as
// dead-letter via MarkDeadLetter.
func (r *ExternalDeliveryRepository) MarkRetry(ctx context.Context, id string, nextAttemptAt time.Time, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkRetry: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'retry_wait',
		     next_attempt_at    = $2,
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     last_error_code    = COALESCE($3, last_error_code),
		     last_error_message = COALESCE($4, last_error_message),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, nextAttemptAt, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkRetry: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkRetry rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkFailed moves a delivery to the terminal failed state, clears its
// lease, and records the failure reason. Used for non-retryable processing
// errors.
func (r *ExternalDeliveryRepository) MarkFailed(ctx context.Context, id string, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkFailed: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'failed',
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     next_attempt_at    = NULL,
		     last_error_code    = COALESCE($2, last_error_code),
		     last_error_message = COALESCE($3, last_error_message),
		     completed_at       = COALESCE(completed_at, NOW()),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkFailed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkFailed rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkBlockedAuth moves a delivery to the terminal blocked_auth state,
// clears its lease, and records the failure reason. Used when the delivery
// cannot proceed without operator intervention (e.g. metadata-only payload).
func (r *ExternalDeliveryRepository) MarkBlockedAuth(ctx context.Context, id string, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkBlockedAuth: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'blocked_auth',
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     next_attempt_at    = NULL,
		     last_error_code    = COALESCE($2, last_error_code),
		     last_error_message = COALESCE($3, last_error_message),
		     completed_at       = COALESCE(completed_at, NOW()),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkBlockedAuth: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkBlockedAuth rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

// MarkDeadLetter moves a delivery to the terminal dead_letter state, clears
// its lease, and records the failure reason. Called when the retry budget is
// exhausted.
func (r *ExternalDeliveryRepository) MarkDeadLetter(ctx context.Context, id string, errorCode, errorMessage string) error {
	if id == "" {
		return errors.New("external delivery MarkDeadLetter: empty id")
	}
	res, err := r.db.ExecContext(ctx,
		`UPDATE external_deliveries
		 SET status             = 'dead_letter',
		     lease_expires_at   = NULL,
		     leased_by_worker_id = NULL,
		     next_attempt_at    = NULL,
		     last_error_code    = COALESCE($2, last_error_code),
		     last_error_message = COALESCE($3, last_error_message),
		     completed_at       = COALESCE(completed_at, NOW()),
		     updated_at         = NOW()
		 WHERE id = $1`,
		id, errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("external delivery MarkDeadLetter: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("external delivery MarkDeadLetter rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%s", ErrExternalDeliveryNotFound, id)
	}
	return nil
}

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
