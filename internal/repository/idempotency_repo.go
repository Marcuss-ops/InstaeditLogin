// Idempotency repository — the persistence layer for idempotency_records.
//
// Level 1 of the two-level idempotency design (migration 021). The
// middleware (pkg/api/idempotency.go) calls FindActiveByKey on every
// POST that carries an Idempotency-Key header, and Insert once the
// handler has successfully produced the resource.
//
// Style mirrors OrganizationRepository / ApiKeyRepository: no
// context.Context, not-found returns (nil, nil), errors wrapped with
// fmt.Errorf("%w", err), ErrIdempotencyKeyCollided sentinel for the
// UNIQUE collision (workspace_id, idempotency_key) — a realistic
// retry-on-conflict path.
//
// Why no UPDATE / DELETE here: the table is append-only from the
// application's perspective. The only legitimate writes are Insert
// (new request) and a CRON sweep that DELETE-expires records
// (deferred to a later Taglio — the middleware ignores expired rows
// regardless, so a runaway table is a memory/storage concern, not a
// correctness concern).
//
// Why RequestHash is []byte and not hex string: the SHA-256 output
// is 32 bytes. Storing as BYTEA (instead of TEXT hex) halves the
// row size on a hot column, and bytes.Equal over []byte in Go is
// constant-time with crypto/subtle but we don't need that hardening
// here — key reuse doesn't leak entropy.

package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// IdempotencyRepository handles persistence for idempotency_records.
type IdempotencyRepository struct {
	db *sql.DB
}

// NewIdempotencyRepository constructs a new repository bound to db.
func NewIdempotencyRepository(db *sql.DB) *IdempotencyRepository {
	return &IdempotencyRepository{db: db}
}

// ErrIdempotencyKeyCollided is the sentinel for UNIQUE-violation on
// (workspace_id, idempotency_key). A duplicate insert happens when two
// concurrent requests race the same (workspace, key) tuple. The
// handler should NOT retry on this — the second request's idempotency
// record was already being inserted by the first; the second
// request should re-fetch and replay. Today there's no caller path
// that retries unconditionally, but the sentinel is exported so a
// future handler can dispatch on it.
var ErrIdempotencyKeyCollided = errors.New("idempotency key collided on insert")

// FindActiveByKey looks up an unexpired idempotency record for
// (workspaceID, key). Returns (nil, nil) when no active row matches
// (either no row, or a row exists but its expires_at is in the
// past). Stale rows are filtered at SQL level to avoid loading
// expired entries into Go memory.
//
// The middleware treats expired records as misses so a record that
// outlived its TTL is harmless for correctness; the (UNIQUE
// workspace_id, idempotency_key) constraint + the expires filter are
// what make Insert idempotent-or-replaceable across the CRON
// sweeper's TTL boundaries.
func (r *IdempotencyRepository) FindActiveByKey(workspaceID int64, key string, now time.Time) (*models.IdempotencyRecord, error) {
	if key == "" {
		return nil, nil // no key → no lookup. (Middleware treats this as a miss too.)
	}
	rec := &models.IdempotencyRecord{}
	err := r.db.QueryRow(
		`SELECT id, workspace_id, idempotency_key, resource_type, resource_id,
		        request_hash, response_status, expires_at, created_at
		 FROM idempotency_records
		 WHERE workspace_id = $1 AND idempotency_key = $2 AND expires_at > $3`,
		workspaceID, key, now,
	).Scan(&rec.ID, &rec.WorkspaceID, &rec.IdempotencyKey, &rec.ResourceType,
		&rec.ResourceID, &rec.RequestHash, &rec.ResponseStatus,
		&rec.ExpiresAt, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find idempotency record: %w", err)
	}
	return rec, nil
}

// Insert persists a new idempotency record. The record's ID,
// CreatedAt, ExpiresAt fields are assigned on return (created_at +
// expires_at come from SQL DEFAULTs; expires_at is overwritten with
// the value the caller passed, so the caller is responsible for
// setting it to the desired TTL).
//
// Returns ErrIdempotencyKeyCollided on the (workspace_id,
// idempotency_key) UNIQUE violation (pq.Error SQLSTATE 23505 +
// constraint name). A collision here means another concurrent
// request raced the same key into Insert; the handler should treat
// that as the other side winning the race, fall through to the
// lookup again, and replay the same response.
func (r *IdempotencyRepository) Insert(rec *models.IdempotencyRecord) error {
	if rec == nil {
		return errors.New("nil idempotency record")
	}
	if rec.WorkspaceID <= 0 {
		return errors.New("workspace_id is required")
	}
	if rec.IdempotencyKey == "" {
		return errors.New("idempotency_key is required")
	}
	if len(rec.RequestHash) != 32 {
		return fmt.Errorf("request_hash must be 32 bytes (sha256); got %d", len(rec.RequestHash))
	}
	if rec.ResourceType == "" {
		return errors.New("resource_type is required")
	}
	if rec.ResourceID <= 0 {
		return errors.New("resource_id is required")
	}
	if rec.ResponseStatus < 100 || rec.ResponseStatus > 599 {
		return fmt.Errorf("response_status %d out of HTTP range", rec.ResponseStatus)
	}
	if rec.ExpiresAt.IsZero() {
		return errors.New("expires_at is required")
	}
	err := r.db.QueryRow(
		`INSERT INTO idempotency_records
		    (workspace_id, idempotency_key, resource_type, resource_id,
		     request_hash, response_status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at`,
		rec.WorkspaceID, rec.IdempotencyKey, rec.ResourceType, rec.ResourceID,
		rec.RequestHash, rec.ResponseStatus, rec.ExpiresAt,
	).Scan(&rec.ID, &rec.CreatedAt)
	if err != nil {
		// Typed pq.Error dispatch: SQLSTATE 23505 (unique_violation)
		// + the named constraint. Constraint name is the auto-generated
		// one from migration 021 (Postgres pattern: <table>_<columns>_key
		// for column-list UNIQUE). Mirrors the dispatch in
		// api_key_repo.go's Create.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" &&
			pqErr.Constraint == "idempotency_records_workspace_id_idempotency_key_key" {
			return fmt.Errorf("%w", ErrIdempotencyKeyCollided)
		}
		return fmt.Errorf("failed to insert idempotency record: %w", err)
	}
	return nil
}
