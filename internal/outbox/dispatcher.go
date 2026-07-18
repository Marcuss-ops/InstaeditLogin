// Package outbox implements the dispatcher goroutine that drives the
// transactional outbox forward. STEP 1 (migration 023 + OutboxRepository
// + PostRepository.Create integration) wrote outbox_events atomically
// alongside posts + post_targets. STEP 2 (this file) reads them back:
//
//	claim → heartbeat → process → mark
//
// The dispatcher is the consumer half of the canonical outbox pattern:
// each claimed row is processed by a caller-supplied ProcessFunc
// (STEP 3 plugs in the publish_jobs materialisation; future event types
// — workspace.member.invited, api_key.rotated — plug in additional
// processors). Per-claim heartbeat keeps lease_until alive during
// slow dispatches; transport errors and terminal errors (via the
// ErrTerminal sentinel) drive retry-vs-DLQ decisions via
// decorrelated-jitter backoff up to MaxAttempts.
//
// Multi-replica safety is delegated to OutboxRepository.ClaimNext's
// SELECT FOR UPDATE SKIP LOCKED + lease-CAS UPDATE — see
// internal/repository/outbox_repo.go for the canonical two-statement
// tx. ErrOutboxAlreadyClaimed signals "queue empty" (sleep-and-retry);
// ErrOutboxRace signals "we got the row but a peer finished it"
// (immediate re-claim without sleep — no log spam).
//
// The dispatcher is intentionally a SINGLE goroutine per replica;
// multiple replicas in production parallelise via SKIP LOCKED.
// Adding worker concurrency within a single dispatcher would
// require either per-row ordering guarantees or shardable leases —
// the current shape is simpler and matches Medium scale (10s of
// events/sec).
//
// IDEMPOTENCY CONTRACT — the dispatcher implements AT-LEAST-ONCE
// delivery via the canonical outbox pattern. A function in this file
// RETURNS A NON-NIL ERROR to its caller (drainOnce) when the
// side-effect persisted but the outbox row's mark* step failed: the
// loop continues, no fallback Mark* fires, and the lease naturally
// expires so the row is re-claimable. The side-effect (HTTP POST to
// a provider, publish_jobs INSERT, etc.) may have already executed.
// Adapters MUST therefore be idempotent on the receiving side.
//
// Concrete partial-persistence hazards (an operator reading this
// during a DLQ-storm investigation can use these as a checklist):
//
//	H1 — Mark* failure AFTER side-effect SUCCEEDED:
//	  processOne invokes ProcessFunc, which returns nil. Then
//	  OutboxStore.Mark* fails (DB blip / connection drop). The
//	  dispatcher LOGS at ERROR, returns a wrapped error from
//	  processOne to drainOnce (which logs once at ERROR for the
//	  observability layer), and does NOT attempt a different
//	  Mark*. The lease naturally expires; the next peer replica
//	  OR the next tick re-claims the row and re-runs ProcessFunc.
//	  Adapter MUST be idempotent: the side-effect ran exactly
//	  once already, this contract guarantees it will run AT LEAST
//	  once. Provider-side idempotency_key / unique-constraint
//	  dedupe are the callers' invariant.
//
//	H2 — ProcessFunc PANIC recovery (safeProcess):
//	  A panicking adapter is converted into a transient error via
//	  safeProcess. runOnce continues the tick loop cleanly (the
//	  dispatcher goroutine does NOT die). The side-effect of the
//	  panicking adapter is undefined (may have partially executed
//	  before the panic). Re-delivery MAY re-run the partial side-
//	  effect against an idempotent receiver.
//
//	H3 — Lease EXPIRY mid-process:
//	  If ProcessFunc runs longer than LeaseTTL, the heartbeat
//	  goroutine's RenewLease fails (or never fires). A peer
//	  dispatcher may claim the row. We still call Mark* at the end;
//	  a peer that already marked first returns an error here, which
//	  we log at WARN and continue. The side-effect ran twice (once by
//	  us, once by the peer) — that is the at-least-once contract.
//	  Doing it ZERO times is unacceptable.
//
//	H4 — runOnce early-return on ctx.Err():
//	  On shutdown, drainOnce returns nil on ctx.Done. The CURRENT
//	  in-flight row finishes via processOne's own heartbeat/mark
//	  path, but any OTHER unclaimed rows in this drain pass are
//	  NOT processed this tick. The next replica picks them up via
//	  SKIP LOCKED. Side-effects for unclaimed rows: ZERO — by
//	  design (during shutdown we err toward safety).
//
// Operational guidance:
//   - Inspect DLQ rows for H1 patterns: MarkProcessed errors logged
//     shortly before the post landed on the user's timeline mean an
//     adapter was retried → unique content is the caller's invariant.
//   - Codify adapter idempotency: every ProcessFunc must accept the
//     same outbox event ID twice without producing duplicate side-
//     effects. The PublishJobs materialiser uses the
//     provider_idempotency_key column; future adapters must follow.
//   - Monitor `outbox processor tick duration` p99 — a rising tail
//     points to H3 risk (slow adapters tripping LeaseTTL).
//   - Alert on `outbox.ErrPartialPersistence` log rows: any match
//     indicates a partial-persistence event that an operator
//     should verify ended in re-claim + idempotent retry success.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ErrTerminal is a sentinel error that ProcessFunc implementations wrap
// via fmt.Errorf("%w: ...", ErrTerminal) when the failure is unrecoverable
// — schema mismatch, payload too large, business-rule violation, etc.
// The dispatcher treats these as "go straight to DLQ, do NOT retry".
//
// Anything else is transient: network blip, third-party rate limit,
// terminal container not yet ready, etc. → MarkFailed with the
// decorrelated-jitter backoff.
var ErrTerminal = errors.New("outbox: terminal error (do not retry — go to DLQ)")

// ErrPartialPersistence signals that the side-effect persisted but the
// follow-up outbox row mark* did NOT. The dispatcher returns it
// (wrapped) from processOne whenever any of the four mark* arms
// (MarkProcessed, MarkDeadLetter terminal, MarkDeadLetter max
// attempts, MarkFailed) fails. Operators and alerts can grep
// `errors.Is(err, outbox.ErrPartialPersistence)` to surface partial-
// persistence events without substring-matching the message. The
// underlying real error (DB blip, sql.ErrConnDone, etc.) is also
// preserved in the chain via a second %w so callers can still
// errors.As on the cause.
var ErrPartialPersistence = errors.New("outbox partial persistence (side-effect ran; outbox row's mark* failed)")

// OutboxStore is the narrow slice of OutboxRepository the dispatcher
// depends on. Defining it in the dispatcher's package (not the
// repository's) keeps the dispatcher testable with an in-memory mock
// without dragging in *sql.DB / sqlmock. The concrete *OutboxRepository
// satisfies it directly (duck-typed).
type OutboxStore interface {
	ClaimNext(leaseTTL time.Duration) (*models.OutboxEvent, error)
	RenewLease(id int64, leaseID string, leaseTTL time.Duration) error
	MarkProcessed(id int64, leaseID string) error
	MarkFailed(id int64, leaseID string, lastError string, backoff *time.Duration) error
	MarkDeadLetter(id int64, leaseID string, lastError string) error
}

// ProcessFunc handles a claimed outbox event. The implementation
// (e.g. the publish-jobs materialiser in STEP 3) reads the payload,
// does its work, and either:
//   - returns nil → dispatcher calls MarkProcessed
//   - wraps ErrTerminal → dispatcher calls MarkDeadLetter (skip retries)
//   - returns any other error → dispatcher calls MarkFailed with backoff
//
// The context passed in is the dispatcher's main ctx — implementations
// should respect cancellation. Long-running implementations should
// spawn their own goroutines for blocking work and pass the ctx down
// to support graceful shutdown.
type ProcessFunc func(ctx context.Context, ev *models.OutboxEvent) error

// --- Tunables ---------------------------------------------------------------

// Default tunables. Override via DispatcherConfig if the operator
// needs different behaviour. Constants exported so tests can
// reference them by identity (avoid magic numbers in test bodies).
const (
	DefaultLeaseTTL = 60 * time.Second
	// HeartbeatInterval = LeaseTTL / 3 keeps the lease fresh with
	// safety margin — a single missed tick still leaves a 2/3 lease
	// window before expiry.
	DefaultHeartbeatInterval = 20 * time.Second
	// MaxAttempts = 5 corresponds to cumulative backoff that
	// reaches capDelay (~1h) within the formula bounds. After 5
	// failed retries the row goes to DLQ for operator triage.
	DefaultMaxAttempts = 5
	DefaultBaseDelay   = 1 * time.Second
	DefaultCapDelay    = 1 * time.Hour
	// TickInterval is how often the dispatcher's outer loop polls
	// the queue when it's empty. Smaller = snappier pickup under
	// low load (sub-second latency for a fresh post), larger = less
	// idle DB load. 5s is a sweet spot for typical workloads.
	DefaultTickInterval = 5 * time.Second
)

// DispatcherConfig bundles the tunables. Zero value gets safe defaults
// applied in NewDispatcher.
type DispatcherConfig struct {
	OutboxStore OutboxStore
	Process     ProcessFunc
	Logger      *slog.Logger

	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	MaxAttempts       int
	BaseDelay         time.Duration
	CapDelay          time.Duration
	TickInterval      time.Duration

	// RandSource for tests; nil → rand.Default. Decorrelated jitter
	// uses Source.Int63n for reproducibility under test.
	RandSource rand.Source
}

// Dispatcher drives the outbox consumer loop. Construct via
// NewDispatcher; one Dispatcher per replica (multi-replica safety
// lives on Postgres's SKIP LOCKED, not in Go).
type Dispatcher struct {
	cfg  DispatcherConfig
	rand *rand.Rand
}

// NewDispatcher returns a Dispatcher with defaults applied to zero-valued
// config fields. Panics if OutboxStore or Process are nil — those
// represent misconfiguration at construction, not runtime configuration.
func NewDispatcher(cfg DispatcherConfig) *Dispatcher {
	if cfg.OutboxStore == nil {
		panic("outbox.NewDispatcher: OutboxStore is required")
	}
	if cfg.Process == nil {
		panic("outbox.NewDispatcher: Process is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.LeaseTTL <= 0 {
		cfg.LeaseTTL = DefaultLeaseTTL
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = cfg.LeaseTTL / 3
		if cfg.HeartbeatInterval <= 0 {
			cfg.HeartbeatInterval = DefaultHeartbeatInterval
		}
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = DefaultBaseDelay
	}
	if cfg.CapDelay <= 0 {
		cfg.CapDelay = DefaultCapDelay
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = DefaultTickInterval
	}
	var r *rand.Rand
	if cfg.RandSource != nil {
		r = rand.New(cfg.RandSource)
	} else {
		r = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Dispatcher{cfg: cfg, rand: r}
}

// Run is the dispatcher's lifecycle: tick ClaimNext in a loop, drain
// the queue on each tick (continue claiming until ErrOutboxAlreadyClaimed),
// sleep TickInterval between drains. On ctx.Done the loop exits AFTER
// the current in-flight processOne completes — drain semantics
// matching the existing PublishWorker.
//
// Returns ctx.Err() on shutdown. Errors inside processOne are escalated
// inside the dispatcher's logging layer (ERROR rows) but do NOT
// propagate up to Run — the at-least-once contract means the lease
// expiry is the recovery vector, not caller-visible errors.
func (d *Dispatcher) Run(ctx context.Context) error {
	d.cfg.Logger.Info("outbox dispatcher started",
		"tick_interval_seconds", d.cfg.TickInterval.Seconds(),
		"lease_ttl_seconds", d.cfg.LeaseTTL.Seconds(),
		"heartbeat_interval_seconds", d.cfg.HeartbeatInterval.Seconds(),
		"max_attempts", d.cfg.MaxAttempts)
	defer d.cfg.Logger.Info("outbox dispatcher stopped")

	// Initial drain — no wait for the first tick.
	d.drainOnce(ctx)

	ticker := time.NewTicker(d.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			d.drainOnce(ctx)
		}
	}
}

// drainOnce pulls rows one-at-a-time until the queue reports empty
// (repository.ErrOutboxAlreadyClaimed), races (repository.ErrOutboxRace —
// re-loop immediately), or the context is cancelled. Each row consumed
// goes through processOne (heartbeat → process → mark). A genuine DB
// error logs at WARN and breaks the drain (we don't want to spin on
// a persistent infra issue).
//
// On partial persistence (processOne returns a non-nil error), drainOnce
// logs once at ERROR and continues the drain loop. The lease stays held
// on the failed row until the next tick (or a peer's lease-expiry
// renewal) re-claims it; that is the at-least-once recovery vector.
func (d *Dispatcher) drainOnce(ctx context.Context) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		ev, err := d.cfg.OutboxStore.ClaimNext(d.cfg.LeaseTTL)
		if err != nil {
			// Queue empty — done draining until the next tick.
			if errors.Is(err, repository.ErrOutboxAlreadyClaimed) {
				return
			}
			// Peer race — try the next row immediately, no log spam.
			if errors.Is(err, repository.ErrOutboxRace) {
				continue
			}
			// Real DB error — log + break drain (next tick will retry).
			d.cfg.Logger.Warn("outbox dispatcher ClaimNext error", "error", err)
			return
		}
		if ev == nil {
			// Defensive: ClaimNext signature is documented to return
			// (nil, ErrOutboxAlreadyClaimed) on empty queue, but a
			// custom mock store might return (nil, nil). Treat as
			// "done draining" and don't loop infinitely.
			return
		}
		if perr := d.processOne(ctx, ev); perr != nil {
			// processOne already logged at ERROR inside the failing
			// arm (the inner log carries the mark-call duration and
			// the underlying error); this outer row anchors the
			// event_id to the drain cycle so observability can group
			// partial-persistence events with the tick window.
			d.cfg.Logger.Error("outbox partial persistence — lease will expire; next peer/tick re-claims to re-run idempotent adapter",
				"event_id", ev.ID, "error", perr)
		}
	}
}

// processOne handles a single claimed event: starts a heartbeat
// goroutine to keep lease_until fresh while the process work runs,
// invokes the user-supplied ProcessFunc, classifies the result,
// and calls the appropriate Mark* method.
//
// Return value (C3 contract):
//   - nil  → side-effect AND outbox row's mark* BOTH persisted. The
//     lease is cleared and the row is durable as processed.
//   - non-nil → the side-effect ran but the FOLLOW-UP mark* call
//     failed (H1 partial persistence). The lease is still held by
//     the row; it naturally expires so a peer OR the next tick
//     re-claims and re-runs the idempotent adapter. drainOnce
//     MUST observe the non-nil return to escalate visibility (Error
//     log row) and NOT break the drain (lease expiry is the
//     recovery vector). The returned error always satisfies
//     errors.Is(err, ErrPartialPersistence) so operators/alerting
//     can grep without substring-matching.
//
// Two processOne contracts worth calling out:
//
//  1. The Order of operations is heartbeat-start → process → mark.
//     Mark* clears the lease, which means the heartbeat goroutine
//     (if it hasn't been cancelled yet) will fail to renew the
//     lease on its next tick. That's fine — ErrOutboxGone is
//     expected after a Mark*, and RenewLease's call would simply
//     no-op. The goroutine will exit on its next tick through the
//     done-channel close.
//
//  2. Heartbeat-shutdown is decoupled from the main ctx. The
//     heartbeat's parent context is created INSIDE processOne
//     (not the dispatcher's main ctx) so a mid-process ctx
//     cancel still allows the final Mark* call to run cleanly.
//     Without this, a mark-after-cancel would itself fail on
//     released DB connections.
func (d *Dispatcher) processOne(ctx context.Context, ev *models.OutboxEvent) (err error) {
	if ev == nil || ev.LeaseID == nil {
		// Defensive: a malformed claim cannot be heartbeat-protected.
		// Surface as a non-nil error so observability/counters
		// (future iteration) capture this as a logged anomaly
		// rather than silent skip.
		d.cfg.Logger.Warn("outbox dispatcher processOne got event without lease_id; skipping",
			"event_id", evID(ev))
		return fmt.Errorf("outbox event without lease_id: ev-id=%d", evID(ev))
	}
	leaseID := *ev.LeaseID

	// Heartbeat goroutine uses its OWN context so a main-ctx cancel
	// doesn't trample the final Mark* call. The caller (drainOnce) is
	// responsible for ensuring the goroutine exits — we close the
	// done channel after the Mark* below.
	hbCtx, hbCancel := context.WithCancel(context.Background())
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		d.heartbeatLoop(hbCtx, ev.ID, leaseID)
	}()

	// Run the user's work. ProcessFunc can be slow (network calls,
	// rate-limited platforms, etc.); the heartbeat keeps the lease
	// alive in parallel.
	start := time.Now()
	processErr := d.safeProcess(ctx, ev)
	duration := time.Since(start)

	// Stop the heartbeat FIRST so it can't overwrite our Mark* with a
	// stale renew. The Mark* call itself doesn't care about the
	// heartbeat goroutine (its sql calls are independent).
	hbCancel()
	<-hbDone

	// Classify the result.
	//
	// C3 hardening: ANY failure to call the appropriate Mark* is
	// partial persistence (the side-effect ran but the outbox's
	// durable acknowledgement did NOT). We do NOT log-and-continue
	// from any branch — every Mark* failure escalates to ERROR and
	// returns a wrapped error from processOne wrapping
	// ErrPartialPersistence, letting drainOnce log the recovery
	// expectation. The lease stays held on the row; tick interval OR
	// a peer replica's lease expiry re-claims the row for another
	// pass through the idempotent adapter (the at-least-once
	// contract).
	switch {
	case processErr == nil:
		if err := d.cfg.OutboxStore.MarkProcessed(ev.ID, leaseID); err != nil {
			d.cfg.Logger.Error("outbox partial persistence: MarkProcessed failed AFTER side-effect success — lease will expire; next peer/tick re-claims to re-run idempotent adapter",
				"event_id", ev.ID, "duration", duration, "error", err)
			return fmt.Errorf("%w: MarkProcessed failed: %w", ErrPartialPersistence, err)
		}
		d.cfg.Logger.Info("outbox dispatcher processed event",
			"event_id", ev.ID, "duration", duration)
		return nil
	case errors.Is(processErr, ErrTerminal):
		// Terminal error → DLQ regardless of attempt count.
		if err := d.cfg.OutboxStore.MarkDeadLetter(ev.ID, leaseID, processErr.Error()); err != nil {
			d.cfg.Logger.Error("outbox partial persistence: MarkDeadLetter (terminal) failed — lease will expire; next peer/tick re-runs idempotent adapter to re-DLQ",
				"event_id", ev.ID, "duration", duration, "error", err)
			return fmt.Errorf("%w: MarkDeadLetter (terminal) failed: %w", ErrPartialPersistence, err)
		}
		d.cfg.Logger.Warn("outbox dispatcher sent event to DLQ (terminal error)",
			"event_id", ev.ID, "error", processErr.Error())
		return nil
	case ev.AttemptCount >= d.cfg.MaxAttempts:
		// Transient retries exhausted — DLQ.
		if err := d.cfg.OutboxStore.MarkDeadLetter(ev.ID, leaseID,
			fmt.Sprintf("max attempts (%d) reached: %s", d.cfg.MaxAttempts, processErr.Error()),
		); err != nil {
			d.cfg.Logger.Error("outbox partial persistence: MarkDeadLetter (max attempts) failed — lease will expire; next peer/tick re-runs idempotent adapter to re-DLQ",
				"event_id", ev.ID, "duration", duration, "error", err)
			return fmt.Errorf("%w: MarkDeadLetter (max attempts) failed: %w", ErrPartialPersistence, err)
		}
		d.cfg.Logger.Warn("outbox dispatcher sent event to DLQ (max attempts)",
			"event_id", ev.ID, "attempts", ev.AttemptCount, "error", processErr.Error())
		return nil
	default:
		// Transient failure — backoff and retry.
		backoff := d.computeBackoff(ev.AttemptCount)
		if err := d.cfg.OutboxStore.MarkFailed(ev.ID, leaseID, processErr.Error(), &backoff); err != nil {
			d.cfg.Logger.Error("outbox partial persistence: MarkFailed failed — lease will expire; next peer/tick re-runs idempotent adapter to re-schedule",
				"event_id", ev.ID, "duration", duration, "backoff", backoff, "error", err)
			return fmt.Errorf("%w: MarkFailed failed: %w", ErrPartialPersistence, err)
		}
		d.cfg.Logger.Info("outbox dispatcher retrying event",
			"event_id", ev.ID, "attempts", ev.AttemptCount, "backoff", backoff, "error", processErr.Error())
		return nil
	}
}

// safeProcess invokes the user-supplied ProcessFunc with panic
// recovery. A panicking ProcessFunc must NOT take down the entire
// dispatcher — we treat it as a transient error and let the row
// retry/timeout per the normal backoff path.
func (d *Dispatcher) safeProcess(ctx context.Context, ev *models.OutboxEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("processFunc panic: %v", r)
			d.cfg.Logger.Error("outbox ProcessFunc panic recovered",
				"event_id", ev.ID, "panic", r)
		}
	}()
	return d.cfg.Process(ctx, ev)
}

// heartbeatLoop renews lease_until every HeartbeatInterval until
// the parent ctx is done. RenewLease failures are logged at DEBUG
// (peer-dispatcher steals the row → expected in steady state) and
// the goroutine exits; the dispatcher's processOne proceeds to
// call Mark* which will also report repository.ErrOutboxGone in that case.
func (d *Dispatcher) heartbeatLoop(ctx context.Context, id int64, leaseID string) {
	ticker := time.NewTicker(d.cfg.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.cfg.OutboxStore.RenewLease(id, leaseID, d.cfg.LeaseTTL); err != nil {
				if !errors.Is(err, repository.ErrOutboxGone) {
					d.cfg.Logger.Debug("outbox heartbeat renew failed",
						"event_id", id, "error", err)
				}
				return
			}
		}
	}
}

// computeBackoff returns the next-attempt delay using AWS-style
// decorrelated jitter (Marcuss architecture blog "Exponential Backoff
// and Jitter"):
//
//	temp = min(cap, prev * 3)
//	sleep = uniform(base..temp)
//
// where `prev` is reconstructed from the attempt count as
// `base * 2^(attempt-1)`. The bound ensures retries don't
// synchronise across replicas after a transient outage
// (the canonical thundering-herd problem).
//
// Decorrelated jitter's lower bound (uniform base..temp) is much
// more aggressive than full-jitter (uniform 0..temp) — it preserves
// a minimum retry cadence even at large attempt counts. The
// alternative "equal jitter" (temp/2 + uniform 0..temp/2) is more
// conservative but drives longer retry tails.
//
// Whether prev is exact or a heuristic doesn't matter for correctness
// (the cap is enforced first) but the heuristic version matches
// what the dispatcher's loop will see when its own MarkFailed calls
// stamp the next_attempt_at column.
func (d *Dispatcher) computeBackoff(attempt int) time.Duration {
	base := d.cfg.BaseDelay
	cap := d.cfg.CapDelay
	if attempt < 1 {
		attempt = 1
	}
	// prev = base * 2^(attempt-1), capped. Use float64 for the
	// multiplication because base << cap and float precision is
	// fine for retry timings.
	prev := float64(base) * pow2(attempt-1)
	if prev > float64(cap) {
		prev = float64(cap)
	}
	temp := prev * 3
	if temp > float64(cap) {
		temp = float64(cap)
	}
	// uniform_int63n returns [0, n). We want [base, temp]. Compute
	// delta = uniform_int63n(temp - base) then sleep = base + delta.
	span := int64(temp) - int64(base)
	if span <= 0 {
		return base
	}
	delta := d.rand.Int63n(span)
	return time.Duration(int64(base) + delta)
}

// pow2 returns 2^n as a float64. Inlined helper to avoid pulling
// in math/bits or math.Pow for a simple doubling. Caller is bounds-
// aware (n in [0, ~30] practically).
func pow2(n int) float64 {
	if n <= 0 {
		return 1
	}
	r := 1.0
	for i := 0; i < n; i++ {
		r *= 2
	}
	return r
}

// evID safely reads ev.ID, returning 0 if ev is nil. Used only
// in log lines so we never panic in the dispatcher hot path on a
// malformed claim.
func evID(ev *models.OutboxEvent) int64 {
	if ev == nil {
		return 0
	}
	return ev.ID
}
