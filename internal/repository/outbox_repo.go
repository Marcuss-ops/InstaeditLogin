package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// OutboxRepository handles CRUD operations for the transactional outbox
// table (migration 023). The outbox is the source of truth for
// "events the dispatcher needs to act on"; the CreatePost handler in
// pkg/api/posts.go calls PostRepository.CreateWithOutbox which
// composes posts + post_targets + outbox_events in a single
// transaction so the dispatcher never misses an event (the canonical
// dual-write problem solved).
//
// Method style mirrors PostRepository / UserRepository / TokenRepository:
//   - no context.Context (synchronous signature only — the dispatcher
//     wraps in its own ctx-aware loops where it cares about cancellation).
//   - not-found returns (nil, nil) for queriers; markers explicitly
//     for writers (ErrOutboxAlreadyClaimed, ErrOutboxGone).
//   - typed pq.Error dispatch for SQLSTATE 23505 to map to a sentinel.
//
// Lifecycle of one row (see migration 023 header for full context):
//
//	Insert (no claim)   → claim (lease_id, lease_until)         →
//	MarkProcessed (terminal, processed_at set) OR
//	MarkFailed (next_attempt_at=jittered next) OR
//	MarkDeadLetter (terminal, status='dead_letter')
//
// The dispatcher MUST run heartbeats that renew lease_until while
// the row is being processed (otherwise a slow dispatch loses the
// row to a peer dispatcher at lease TTL). The repository exposes
// RenewLease for that purpose — heartbeat goroutine calls it every
// 20s on every actively-claimed row it owns.
type OutboxRepository struct {
	db *sql.DB
}

// NewOutboxRepository creates a new OutboxRepository.
func NewOutboxRepository(db *sql.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

// ErrOutboxAlreadyClaimed is returned by ClaimNext when the SELECT
// FOR UPDATE SKIP LOCKED returns no rows — typically because every
// pending row is held by an unlocked peer. The dispatcher treats
// this as "nothing to do right now" and just sleeps until the next
// tick. Distinct from a generic not-found so the retry-or-die loop
// doesn't mistake a transient "queue is empty" for a logic error.
//
// ErrOutboxRace is returned by ClaimNext when the SELECT picked a
// row but a concurrent dispatcher committed MarkProcessed/MarkFailed
// between our SELECT FOR UPDATE SKIP LOCKED and the lease-setting
// UPDATE. Semantically the row is "gone but not because the queue
// is empty" — a peer dispatcher finished it. We expose it as a
// distinct sentinel so the dispatcher can log it at DEBUG (rather
// than the misleading "queue empty" log) and immediately try the
// next SELECT. Without this sentinel, mapping UPDATE-no-rows to
// ErrOutboxAlreadyClaimed would put the loop into a sleep+retry
// cycle on a row that's already terminal and never visible again.
//
// ErrOutboxGone is returned by Mark* / RenewLease when the row
// is gone (deleted by operator, or claimed by peer that finished
// + deleted). The dispatcher treats as "drop the lease silently".
var (
	ErrOutboxAlreadyClaimed = errors.New("outbox: no pending row available to claim (queue may be empty or all rows leased)")
	ErrOutboxRace           = errors.New("outbox: row claimed-and-finished by a concurrent dispatcher between our SELECT and UPDATE")
	ErrOutboxGone           = errors.New("outbox: row not found")
)

// Insert writes a new outbox event row. Does NOT generate a lease —
// lease is set only by ClaimNext. Always returns the assigned id via
// the OutboxEvent pointer.
//
// Behaviour:
//   - payload must be valid JSON (caller-provided json.RawMessage is
//     assumed already-marshalled; the column is JSONB).
//   - status defaults to 'pending' (the SQL DEFAULT) — Insert here
//     is the dispatcher-discovery flow (rows that should NOT be
//     picked up don't go through Insert).
//   - AggregateID must be > 0 — the dispatcher locates the row by
//     aggregate_id alone; a zero-id row would be unresolvable
//     because no aggregate's BIGSERIAL ever starts at 0. This is a
//     hard invariant enforced at the repository surface so a malformed
//     caller (e.g. one that bails before target.ID is set) cannot
//     poison the queue with unrouteable events.
//   - insert failure on existing (id collides — shouldn't happen
//     with BIGSERIAL) is treated as a generic wrapped error.
func (r *OutboxRepository) Insert(ev *models.OutboxEvent) error {
	if ev.AggregateType == "" || ev.EventType == "" || len(ev.Payload) == 0 {
		return fmt.Errorf("outbox Insert: missing required fields (aggregate_type=%q event_type=%q payload len=%d)",
			ev.AggregateType, ev.EventType, len(ev.Payload))
	}
	if ev.AggregateID <= 0 {
		return fmt.Errorf("outbox Insert: aggregate_id must be > 0 (got %d; BIGSERIAL ids never start at zero)", ev.AggregateID)
	}
	err := r.db.QueryRow(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)
		 RETURNING id, created_at`,
		ev.AggregateType, ev.AggregateID, ev.EventType, string(ev.Payload),
	).Scan(&ev.ID, &ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("outbox Insert: %w", err)
	}
	// Status defaults to 'pending' at the DB; explicit mirror so
	// downstream code that reads ev without re-fetching sees the
	// canonical state.
	if ev.Status == "" {
		ev.Status = models.OutboxStatusPending
	}
	return nil
}

// ClaimNext atomically acquires the next pending outbox row for this
// dispatcher, stamping it with a fresh lease. Returns the claimed
// event (populated with all post-claim columns: id, lease_id,
// lease_until, status, attempt_count, etc.) or (nil, ErrOutboxAlreadyClaimed)
// when no work is available.
//
// The SELECT FOR UPDATE SKIP LOCKED + UPDATE pattern is the canonical
// Postgres queue-table idiom (Postgres 9.5+). It guarantees:
//  1. Multi-dispatcher safety: each concurrent dispatcher sees a
//     different available row (no double-claim).
//  2. Lease-column CAS: even if the SKIP LOCKED tx finishes the
//     claim before heartbeat renewal, the next dispatcher won't
//     steal the row until lease_until < now() AND no row is
//     actively leased elsewhere.
//
// `leaseTTL` is the duration the dispatcher wants the lease to be
// valid for; the heartbeat goroutine will renew it before expiry.
func (r *OutboxRepository) ClaimNext(leaseTTL time.Duration) (*models.OutboxEvent, error) {
	leaseID := uuid.NewString()
	leaseUntil := time.Now().Add(leaseTTL)

	// Step 1: pick a candidate row (SKIP LOCKED + order by next_attempt_at).
	// Step 2: UPDATE the SAME row with our lease IDs.
	// The two-step shape is needed because UPDATE ... WHERE id = (SELECT ...
	// FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING * works in one round-trip
	// but SKIP LOCKED applied to the UPDATE's WHERE filter doesn't
	// translate cleanly. Two statements inside an explicit tx gives
	// us the lock-and-claim guarantee.
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("outbox ClaimNext begin: %w", err)
	}
	defer func() {
		_ = tx.Rollback() // safe to call even after Commit
	}()

	row := tx.QueryRow(
		`SELECT id FROM outbox_events
		 WHERE status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		   AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		 ORDER BY next_attempt_at NULLS FIRST, created_at ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
	)
	var id int64
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOutboxAlreadyClaimed
		}
		return nil, fmt.Errorf("outbox ClaimNext SELECT: %w", err)
	}

	// Step 2: stamp the lease on the candidate row. The WHERE clause
	// still includes status='pending' as the secondary guard so a
	// concurrent tx that committed a MarkProcessed/MarkFailed between
	// our SELECT and the UPDATE won't cause us to overwrite a
	// terminal state.
	row = tx.QueryRow(
		`UPDATE outbox_events
		 SET lease_id = $1::uuid,
		     lease_until = $2,
		     attempt_count = attempt_count + 1
		 WHERE id = $3
		   AND status = 'pending'
		   AND (lease_until IS NULL OR lease_until < now())
		 RETURNING id, aggregate_type, aggregate_id, event_type, payload,
		           status, lease_id, lease_until, attempt_count,
		           next_attempt_at, last_error, created_at, processed_at`,
		leaseID, leaseUntil, id,
	)
	ev := &models.OutboxEvent{}
	var leaseIDOut sql.NullString
	var lastErrorOut sql.NullString
	if err := row.Scan(
		&ev.ID, &ev.AggregateType, &ev.AggregateID, &ev.EventType, &ev.Payload,
		&ev.Status, &leaseIDOut, &ev.LeaseUntil, &ev.AttemptCount,
		&ev.NextAttemptAt, &lastErrorOut, &ev.CreatedAt, &ev.ProcessedAt,
	); err != nil {
		// The UPDATE returned sql.ErrNoRows: a peer dispatcher committed
		// MarkProcessed/MarkFailed/DeadLetter between our SKIP LOCKED
		// SELECT and this UPDATE. This is NOT the same as "queue empty"
		// (which the SELECT-no-rows path already handled above) — this
		// is "queue had a row but it got away from us". Return
		// ErrOutboxRace so the dispatcher logs at DEBUG (not the
		// phantom queue-empty warning) and immediately re-runs SELECT
		// for the next available row.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrOutboxRace
		}
		return nil, fmt.Errorf("outbox ClaimNext UPDATE: %w", err)
	}
	// SQL NULL → sql.NullString.Valid == false → LeaseID stays nil.
	// (A previous version used `&leaseIDOut` on a plain string and
	// produced a non-nil pointer to "" on NULL, which misled any
	// `if ev.LeaseID != nil` claim check downstream.)
	if leaseIDOut.Valid {
		ev.LeaseID = &leaseIDOut.String
	}
	ev.LastError = lastErrorOut.String

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("outbox ClaimNext commit: %w", err)
	}
	return ev, nil
}

// RenewLease extends the lease TTL for an actively-claimed row. The
// heartbeat goroutine calls this every 20s on every row it's
// processing. Returns ErrOutboxGone if the row doesn't exist or
// wasn't claimed by the caller (lease_id mismatch — could mean a
// peer dispatcher stole it after a clock skew).
func (r *OutboxRepository) RenewLease(id int64, leaseID string, leaseTTL time.Duration) error {
	if leaseID == "" {
		return fmt.Errorf("outbox RenewLease: empty lease_id for id=%d", id)
	}
	leaseUntil := time.Now().Add(leaseTTL)
	result, err := r.db.Exec(
		`UPDATE outbox_events
		 SET lease_until = $1
		 WHERE id = $2 AND lease_id = $3::uuid`,
		leaseUntil, id, leaseID,
	)
	if err != nil {
		return fmt.Errorf("outbox RenewLease: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox RenewLease rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d (lease_id mismatch or row gone)", ErrOutboxGone, id)
	}
	return nil
}

// MarkProcessed transitions the row to terminal success state: clears
// the lease, sets processed_at = now(). Idempotent — calling twice
// stays at processed_at = first call's value because we don't bump it.
func (r *OutboxRepository) MarkProcessed(id int64, leaseID string) error {
	result, err := r.db.Exec(
		`UPDATE outbox_events
		 SET status = 'processed',
		     processed_at = now(),
		     lease_id = NULL,
		     lease_until = NULL,
		     next_attempt_at = NULL,
		     last_error = NULL
		 WHERE id = $1 AND lease_id = $2::uuid`,
		id, leaseID,
	)
	if err != nil {
		return fmt.Errorf("outbox MarkProcessed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox MarkProcessed rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d (lease_id mismatch — possibly stolen)", ErrOutboxGone, id)
	}
	return nil
}

// MarkFailed transitions the row for retry: clears the lease, persists
// the error, schedules next_attempt_at = now() + backoff via the
// caller's jitter formula (caller-computed so the dispatcher can
// enforce exponential growth + cap policies consistently across the
// codebase).
//
// `backoff` is the duration to wait before the next dispatcher
// pickup. nil backoff means "retry immediately on the next tick";
// this is intentional (caller decides urgency) — the dispatcher
// doesn't know whether the failure was transient or needs cooling
// down.
func (r *OutboxRepository) MarkFailed(id int64, leaseID string, lastError string, backoff *time.Duration) error {
	if lastError == "" {
		return fmt.Errorf("outbox MarkFailed: empty last_error for id=%d", id)
	}
	var nextAttemptAt *time.Time
	if backoff != nil {
		t := time.Now().Add(*backoff)
		nextAttemptAt = &t
	}
	result, err := r.db.Exec(
		`UPDATE outbox_events
		 SET lease_id = NULL,
		     lease_until = NULL,
		     last_error = $1,
		     next_attempt_at = $2
		 WHERE id = $3 AND lease_id = $4::uuid`,
		lastError, nextAttemptAt, id, leaseID,
	)
	if err != nil {
		return fmt.Errorf("outbox MarkFailed: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox MarkFailed rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d (lease_id mismatch — possibly stolen)", ErrOutboxGone, id)
	}
	return nil
}

// MarkDeadLetter transitions the row to terminal-fail state: status =
// 'dead_letter', processed_at = now() (so the operator can compute
// time-in-dlq), all retry scheduling cleared. Idempotent at the
// operator level — re-running with a different last_error overwrites
// the message but preserves status='dead_letter'.
//
// `leaseID` is the dispatcher's lease; if a peer stole it the row is
// already in a different terminal state and we report ErrOutboxGone.
// (We don't release-then-retry because the dlq decision is final.)
func (r *OutboxRepository) MarkDeadLetter(id int64, leaseID string, lastError string) error {
	if lastError == "" {
		return fmt.Errorf("outbox MarkDeadLetter: empty last_error for id=%d", id)
	}
	result, err := r.db.Exec(
		`UPDATE outbox_events
		 SET status = 'dead_letter',
		     processed_at = now(),
		     lease_id = NULL,
		     lease_until = NULL,
		     next_attempt_at = NULL
		 WHERE id = $1 AND lease_id = $2::uuid`,
		id, leaseID,
	)
	if err != nil {
		return fmt.Errorf("outbox MarkDeadLetter: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("outbox MarkDeadLetter rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d (lease_id mismatch — possibly stolen)", ErrOutboxGone, id)
	}
	return nil
}

// ReleaseLease clears the lease without changing status — used by
// the dispatcher on graceful shutdown when it has rows still in
// flight (so a peer dispatcher can pick them up immediately rather
// than waiting for lease expiry). Idempotent — re-running is a
// no-op if the lease is already gone.
//
// Used by:
//   - Dispatcher.Run graceful drain path
//   - Dispatcher ctx-cancel abort path on shutdown
func (r *OutboxRepository) ReleaseLease(id int64, leaseID string) error {
	result, err := r.db.Exec(
		`UPDATE outbox_events
		 SET lease_id = NULL,
		     lease_until = NULL
		 WHERE id = $1 AND lease_id = $2::uuid`,
		id, leaseID,
	)
	if err != nil {
		return fmt.Errorf("outbox ReleaseLease: %w", err)
	}
	// n == 0 is fine here (lease already gone — idempotent path).
	_, _ = result.RowsAffected()
	return nil
}

// ListPending returns a snapshot of all pending outbox rows for
// diagnostic / metric purposes. Does NOT claim. Use sparingly — a
// large queue can produce a big result set; metrics exporters
// should downsample before calling this.
func (r *OutboxRepository) ListPending(limit int) ([]models.OutboxEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(
		`SELECT id, aggregate_type, aggregate_id, event_type, payload, status,
		        lease_id, lease_until, attempt_count, next_attempt_at,
		        last_error, created_at, processed_at
		 FROM outbox_events
		 WHERE status = 'pending'
		 ORDER BY next_attempt_at NULLS FIRST, created_at ASC
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("outbox ListPending: %w", err)
	}
	defer rows.Close()

	var out []models.OutboxEvent
	for rows.Next() {
		ev := models.OutboxEvent{}
		// sql.NullString is required: scanning into a raw `*string`
		// collapses NULL and "" into the same nil-pointer-to-empty
		// value, which silently breaks any caller that asks
		// `if ev.LeaseID != nil` (an unleased row would look
		// "claimed"). sql.NullString distinguishes Valid==false (SQL
		// NULL → ev.LeaseID stays nil) from Valid==true (SQL UUID →
		// ev.LeaseID points at the populated string).
		var leaseIDOut sql.NullString
		var lastErrorOut sql.NullString
		if err := rows.Scan(
			&ev.ID, &ev.AggregateType, &ev.AggregateID, &ev.EventType, &ev.Payload,
			&ev.Status, &leaseIDOut, &ev.LeaseUntil, &ev.AttemptCount,
			&ev.NextAttemptAt, &lastErrorOut, &ev.CreatedAt, &ev.ProcessedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox ListPending scan: %w", err)
		}
		if leaseIDOut.Valid {
			s := leaseIDOut.String
			ev.LeaseID = &s
		}
		ev.LastError = lastErrorOut.String
		out = append(out, ev)
	}
	// pq import retained for future typed-error paths (e.g. on
	// future contention constraints). Currently unused at the
	// repository surface — the pq import is referenced via the
	// discovery Scan path in post_repo.go (via errors.As).
	_ = pq.Error{}
	return out, nil
}

// CountPending returns the number of pending rows (zero-leased ∪
// unleased) for metric dashboards. Cheap query that hits
// idx_outbox_pending.
func (r *OutboxRepository) CountPending() (int, error) {
	var n int
	if err := r.db.QueryRow(
		`SELECT count(*) FROM outbox_events WHERE status = 'pending'`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("outbox CountPending: %w", err)
	}
	return n, nil
}

// CountDeadLetter returns the number of rows currently in DLQ.
// Backs alerting dashboards — a non-zero count is "operator
// attention required".
func (r *OutboxRepository) CountDeadLetter() (int, error) {
	var n int
	if err := r.db.QueryRow(
		`SELECT count(*) FROM outbox_events WHERE status = 'dead_letter'`,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("outbox CountDeadLetter: %w", err)
	}
	return n, nil
}
