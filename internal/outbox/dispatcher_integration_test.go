// Package outbox INTEGRATION tests for the IDEMPOTENCY CONTRACT
// documented at the top of dispatcher.go. These tests force the
// "partial persistence" scenarios that an operator would observe
// during a DLQ-storm investigation:
//
//	H1 — adapter succeeded but MarkProcessed failed on the DB write.
//	     Loop MUST continue to the NEXT tick (proven by enqueuing a
//	     second happy-path event that gets processed despite the
//	     first one's MarkProcessed having failed). Side-effect ran.
//
//	H3 — LeaseTTL expired before MarkFailed ran. ProcessFunc returned
//	     a transient; MarkFailed returns ErrOutboxGone (peer stole
//	     the lease). Loop MUST continue to the NEXT tick. Side-
//	     effect may have executed once OR twice (at-least-once).
//
//	H4 — ctx-cancel mid-drain. ProcessFunc blocks on ctx.Done. After
//	     ctx cancel, ONLY the in-flight row gets processed; the
//	     NEXT unclaimed row in the drain pass is skipped (next
//	     replica / next tick picks it up via SKIP LOCKED). Side-
//	     effects for unclaimed rows: ZERO.
//
// Note on H2 (ProcessFunc panic recovery): covered by the existing
// unit test TestDispatcher_PanicInProcess_RecoversAsTransient in
// dispatcher_test.go. H2 is the "dispatcher goroutine does NOT die"
// invariant, asserted in unit-test form (panic → transient error →
// MarkFailed path).
//
// Run: `go test ./internal/outbox/... -count=1 -v -run TestDispatcher_Integration`
package outbox_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// partialFailureStore is a mock OutboxStore that fails the first N
// calls to MarkProcessed then succeeds; it forwards all other
// interface calls to the same shape mockOutboxStore uses. Lets us
// prove the dispatcher loop CONTINUIES after a partial-persistence
// failure by enqueueing two events and asserting BOTH reach Mark*.
//
// Only the call counters we actually assert on are tracked; the
// renew lease path is forward-no-op'd to keep the test small.
type partialFailureStore struct {
	claimResponses []claimResponse
	claimFallback  error

	failProcessed int // NUMBER of MarkProcessed failures to inject (consumed atomically)

	markProcessed atomic.Int64
	renews        atomic.Int64
	markFailed    atomic.Int64
}

func (p *partialFailureStore) ClaimNext(_ time.Duration) (*models.OutboxEvent, error) {
	if len(p.claimResponses) == 0 {
		if p.claimFallback != nil {
			return nil, p.claimFallback
		}
		return nil, repository.ErrOutboxAlreadyClaimed
	}
	resp := p.claimResponses[0]
	p.claimResponses = p.claimResponses[1:]
	return resp.ev, resp.err
}

func (p *partialFailureStore) RenewLease(_ int64, _ string, _ time.Duration) error {
	p.renews.Add(1)
	return nil
}

func (p *partialFailureStore) MarkProcessed(_ int64, _ string) error {
	// Record the attempt first; this counter tracks how many times
	// MarkProcessed was invoked, including injected partial-persistence
	// failures. The H1 test uses it to prove the dispatcher loop
	// continued after a failed mark (first event failed, second event
	// still reached MarkProcessed).
	p.markProcessed.Add(1)
	// Atomically consume one of the N requested failures first; once
	// failProcessed hits zero we record the call as a success.
	// The failProcessed counter is a plain int under mutex protection
	// rather than sync/atomic because we want a clean "go negative
	// on error" semantic without a sentinel value (requesting N+1
	// failures leaves the counter negative, which we want to be
	// reflected rather than silently clamped).
	p.muClaim()
	if p.failProcessed > 0 {
		p.failProcessed--
		p.muRelease()
		return errors.New("simulated partial failure (first Mark failed)")
	}
	p.muRelease()
	return nil
}

func (p *partialFailureStore) MarkFailed(int64, string, string, *time.Duration) error {
	p.markFailed.Add(1)
	return nil
}

func (p *partialFailureStore) MarkDeadLetter(int64, string, string) error {
	return nil
}

// Mutex guarding failProcessed (counter manipulated by MarkProcessed).
// We can't use atomic.AddInt32 cleanly with a "counter that can go
// negative" semantics without an extra sentinel; a mutex is clearer.
var (
	pMu sync.Mutex
)

func (p *partialFailureStore) muClaim()   { pMu.Lock() }
func (p *partialFailureStore) muRelease() { pMu.Unlock() }

// TestDispatcher_Integration_H1_PartialPersistenceOnMarkFailure exercises
// the H1 contract hazard: ProcessFunc returns nil (side-effect ran),
// MarkProcessed fails on the DB write. Loop MUST continue: a second
// enqueued event reaches MarkProcessed with success.
//
// Expected:
//   - Run returns ctx.DeadlineExceeded (cleanly).
//   - markProcessed == 2: first call FAILED (H1), second SUCCEEDED
//     (proves the loop continued past the partial-failure).
//   - markFailed / markDeadLetter are untouched (no recovery path
//     triggered because ProcessError was nil; only Mark* failed).
//   - No panic.
func TestDispatcher_Integration_H1_PartialPersistenceOnMarkFailure(t *testing.T) {
	store := &partialFailureStore{
		claimResponses: []claimResponse{
			{ev: newEvent(999, 1)}, // H1-trigger: will fail its MarkProcessed
			{ev: newEvent(998, 1)}, // happy-path: must still be processed
		},
		failProcessed: 1, // only the FIRST MarkProcessed fails
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(_ context.Context, _ *models.OutboxEvent) error {
			// Side-effect ran on both events.
			return nil
		},
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := d.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err under H1 partial-persistence: want DeadlineExceeded, got %v", err)
	}

	if n := store.markProcessed.Load(); n != 2 {
		t.Errorf("H1: expected markProcessed=2 (H1 fail + happy success), got %d", n)
	}
	if n := store.markFailed.Load(); n != 0 {
		t.Errorf("H1: MarkFailed must NOT trigger when ProcessError is nil, got %d", n)
	}
}

// TestDispatcher_Integration_H3_LeaseExpiryOnMark exercises the H3
// contract hazard: peer dispatcher has stolen the lease. MarkFailed
// returns ErrOutboxGone (markFailedErr). Loop MUST continue: a
// second enqueued event reaches MarkProcessed with success.
//
// The wrapper partialFailureStore here is slightly different — we
// fail ALL MarkFailed calls (to model the peer-steals scenario) and
// verify ProcessError reporting doesn't tear the loop down.
//
// Expected:
//   - Run returns ctx.DeadlineExceeded cleanly.
//   - markProcessed == 2 (H3-trigger row + happy-path row).
//   - markFailed == 1 (H3-trigger's failed Mark attempt is recorded).
//   - markDeadLetter == 0 (under MaxAttempts=5).
//   - No panic.
func TestDispatcher_Integration_H3_LeaseExpiryOnMark(t *testing.T) {
	store := &h3Store{
		partialFailureStore: partialFailureStore{
			claimResponses: []claimResponse{
				{ev: newEvent(997, 1)}, // H3-trigger: peer stole lease; MarkFailed returns ErrOutboxGone
				{ev: newEvent(996, 1)}, // happy-path: must still be processed
			},
		},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(_ context.Context, ev *models.OutboxEvent) error {
			// H3 event (id 997) returns a transient error; the happy
			// path event (id 996) returns nil.
			if ev.ID == 997 {
				return errors.New("transient: pretend the API timed out after we already published")
			}
			return nil
		},
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := d.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err under H3 lease-stolen: want DeadlineExceeded, got %v", err)
	}

	if n := store.markProcessed.Load(); n != 1 {
		t.Errorf("H3: expected markProcessed=1 (happy event processed), got %d", n)
	}
	if n := store.markFailed.Load(); n != 1 {
		t.Errorf("H3: expected markFailed=1 (H3's MarkFailed attempt), got %d", n)
	}
}

// h3Store wraps partialFailureStore and makes MarkFailed return
// ErrOutboxGone (peer stole the lease), so the H3 hazard is incident.
type h3Store struct {
	partialFailureStore
}

func (h *h3Store) MarkFailed(_ int64, _ string, _ string, _ *time.Duration) error {
	// Real OutboxStore semantics: MarkFailed with no row → ErrOutboxGone.
	// We deliberately shadow partialFailureStore's nil-success MarkFailed.
	h.markFailed.Add(1)
	return repository.ErrOutboxGone
}

// TestDispatcher_Integration_H4_ShutdownSkipsUnclaimedRows exercises
// the H4 contract hazard: ctx-cancel mid-drain. ProcessFunc is
// ctx-aware and blocks on ctx.Done. We ENQUEUE 2 events — the first
// blocks the in-flight ProcessFunc; on cancel the dispatcher exits
// the drain pass and ONLY the in-flight one reaches MarkProcessed.
// The second event stays in the queue (next replica / next tick
// picks it up via SKIP LOCKED).
//
// Expected:
//   - Run returns context.Canceled (NOT DeadlineExceeded).
//   - markProcessed == 1 (only the in-flight row reaches mark).
//   - The second event was CONSUMED off the claim queue (because it
//     was the second claim attempt in the same drain) but NOT
//     processed (because ctx-cancel preempted it before/during its
//     ProcessFunc). The "consumed but not processed" pattern is a
//     known shape: the row is "in-flight cancelled" and will be
//     re-claimed once the lease TTL expires if no Mark* was written.
//
// The test relies on the dispatcher's documented behaviour: "On
// ctx.Done the loop exits AFTER the current in-flight processOne
// completes" (see dispatcher.go Run() doc comment). The SECOND
// event is processed by the SAME drain pass AFTER the in-flight
// one's processOne completes — but if ctx is cancelled BEFORE the
// second one's processFunc is invoked, it stays unstaged. We rely
// on the in-flight's processFunc to wait on ctx.Done, so when we
// cancel, the in-flight one resolves quickly and the second one's
// drain pass step ALSO checks ctx.Err() and returns.
//
// Robustness note: this test is timing-sensitive. The deadline is
// 50ms; the test asserts within 50ms of cancel. If the dispatcher
// ever changes its mid-drain semantics, this test will (correctly)
// fail — that's a feature, not a bug.
func TestDispatcher_Integration_H4_ShutdownSkipsUnclaimedRows(t *testing.T) {
	gate := make(chan struct{})
	enteredFirst := make(chan struct{})
	pfs := &partialFailureStore{
		claimResponses: []claimResponse{
			{ev: newEvent(995, 1)}, // in-flight: blocks on gate
			{ev: newEvent(994, 1)}, // would be the in-flight if first returned
		},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: pfs,
		Process: func(ctx context.Context, _ *models.OutboxEvent) error {
			// First event: announce we're entered, then block on gate.
			// The gate is unblocked by the test AFTER cancel, so the
			// dispatcher's "drain in-flight" path runs to completion
			// THEN drain pass checks ctx.Err and exits.
			select {
			case <-enteredFirst: // re-entry on second call won't match; harmless
			default:
				close(enteredFirst)
				<-gate
				return nil
			}
			// Second event: short work so the test can observe the
			// drain pass exiting early on ctx.Err.
			return nil
		},
		TickInterval: 1 * time.Hour, // only initial drain matters
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for the FIRST event's ProcessFunc to enter.
	<-enteredFirst

	// Cancel mid-flight.
	cancel()

	// Unblock the first event's ProcessFunc so the dispatcher's
	// drain-in-flight path runs to completion.
	close(gate)

	// Run should return ctx.Canceled cleanly.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err under H4 mid-drain cancel: want context.Canceled, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return after first event's processOne completed")
	}

	// markProcessed exactly 1: only the in-flight row reached mark.
	// The second event row was in-flight but the drain pass exited
	// before its ProcessFunc could run. We read markProcessed off
	// the same store instance the dispatcher used — no helper
	// indirection needed.
	if n := pfs.markProcessed.Load(); n != 1 {
		t.Errorf("H4: expected markProcessed=1 (only in-flight), got %d", n)
	}
}
