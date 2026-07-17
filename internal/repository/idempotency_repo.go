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

// IdempotencyRepository handles persistence for both
// idempotency_records (migration 021, lookup hot-path) AND
// idempotency_batch_replays (migration 039, drive_batch cached
// response payload). The two tables are linked 1:1 by
// idempotency_records.id → idempotency_batch_replays.idempotency_record_id;
// the CASCADE FK ensures the side row dies with its parent so the
// post-039 CRON sweeper doesn't need special-casing for batch
// records.
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

// FindBatchReplay returns the cached response payload for a
// drive_batch idempotent POST. The lookup uses the parent
// idempotency_record_id (a primary key on idempotency_batch_replays).
// Returns (nil, nil) when no side row matches — a drive_batch record
// exists but its replay row wasn't written for some reason (best-effort
// insert failed at write time). The replay path treats a nil replay
// as "no cached response available; surface 500 to operator".
//
// The side table is appended to only AFTER the parent
// idempotency_records row has been inserted (since the side row
// requires the parent's generated id). The CASCADE FK ensures the
// side row dies with its parent.
func (r *IdempotencyRepository) FindBatchReplay(idempotencyRecordID int64) (*models.BatchReplay, error) {
	if idempotencyRecordID <= 0 {
		return nil, nil
	}
	rec := &models.BatchReplay{}
	err := r.db.QueryRow(
		`SELECT idempotency_record_id, response_payload, created_at
		 FROM idempotency_batch_replays
		 WHERE idempotency_record_id = $1`,
		idempotencyRecordID,
	).Scan(&rec.IdempotencyRecordID, &rec.ResponsePayload, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find batch replay: %w", err)
	}
	return rec, nil
}

// InsertBatchReplay persists the cached response payload for a
// drive_batch idempotent POST. The parent idempotency_record_id is
// the PK on idempotency_batch_replays; a duplicate insert returns
// ErrIdempotencyKeyCollided's sibling ErrBatchReplayCollision so
// the caller can distinguish a duplicate-side-row insert from a
// generic error. In practice this shouldn't happen (Insert is only
// called from insertBatchIdempotentRecord AFTER a successful parent
// insert) — the dispatch here is paranoia for future refactors.
func (r *IdempotencyRepository) InsertBatchReplay(rec *models.BatchReplay) error {
	if rec == nil {
		return errors.New("nil batch replay record")
	}
	if rec.IdempotencyRecordID <= 0 {
		return errors.New("idempotency_record_id is required")
	}
	if len(rec.ResponsePayload) == 0 {
		return errors.New("response_payload is required")
	}
	err := r.db.QueryRow(
		`INSERT INTO idempotency_batch_replays
		   (idempotency_record_id, response_payload)
		 VALUES ($1, $2)
		 RETURNING created_at`,
		rec.IdempotencyRecordID, rec.ResponsePayload,
	).Scan(&rec.CreatedAt)
	if err != nil {
		// Same collision-sentinel pattern as the parent table. The
		// PRIMARY KEY on idempotency_batch_replays is idempotency_record_id
		// so a duplicate-side-row collision is a PK violation.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return fmt.Errorf("batch replay already exists for record %d: %w",
				rec.IdempotencyRecordID, ErrIdempotencyKeyCollided)
		}
		return fmt.Errorf("failed to insert batch replay: %w", err)
	}
	return nil
}

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
