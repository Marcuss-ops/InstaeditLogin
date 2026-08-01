package outbox_test

import (
	"context"
	"errors"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
)

// --- Retry / backoff / re-claim ---------------------------------------------

// TestDispatcher_TransientFailure_MarkFailedWithBackoff covers the
// retry path: claim with attempt=1 → process returns transient error →
// MarkFailed with non-nil backoff (>0 since random source produces
// value in [base..prev*3]).
func TestDispatcher_TransientFailure_MarkFailedWithBackoff(t *testing.T) {
	store := &mockOutboxStore{
		claimResponses: []claimResponse{{ev: newEvent(50, 1)}},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(_ context.Context, _ *models.OutboxEvent) error {
			return errors.New("transient: network blip")
		},
		RandSource:   rand.NewSource(42), // deterministic
		BaseDelay:    1 * time.Second,
		CapDelay:     1 * time.Hour,
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.markFailed.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := store.lastFailed.Load(); got != 50 {
		t.Errorf("last failed id: want 50, got %d", got)
	}
	boNS := store.lastFailedBo.Load()
	if boNS <= 0 {
		t.Errorf("backoff: want >0, got %dns", boNS)
	}
	// First-attempt backoff range: rand(base=1s, prev=base*2^0=1s, temp=prev*3=3s) → [1s, 3s).
	bo := time.Duration(boNS)
	if bo < 1*time.Second || bo >= 3*time.Second {
		t.Errorf("backoff out of expected band [1s,3s): got %v", bo)
	}
	if n := store.markProcessed.Load(); n != 0 {
		t.Errorf("MarkProcessed fired on failure: count=%d", n)
	}
	if n := store.markDeadLetter.Load(); n != 0 {
		t.Errorf("MarkDeadLetter fired on attempt=1 transient: count=%d", n)
	}
}

// TestDispatcher_MaxAttemptsReached_MarkDeadLetter covers the
// "exhausted retries" path: a row reaching attempt count == maxAttempts
// goes to DLQ even on a generic (non-ErrTerminal) error.
func TestDispatcher_MaxAttemptsReached_MarkDeadLetter(t *testing.T) {
	const maxAttempts = 5
	// AttemptCount=5 means ClaimNext's increment leaves it at 5 → at MaxAttempts.
	store := &mockOutboxStore{
		claimResponses: []claimResponse{{ev: newEvent(70, maxAttempts)}},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(_ context.Context, _ *models.OutboxEvent) error {
			return errors.New("transient but exhausted")
		},
		MaxAttempts:  maxAttempts,
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.markDeadLetter.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := store.lastDeadLetter.Load(); got != 70 {
		t.Errorf("last DLQ id: want 70, got %d", got)
	}
	if msg := store.lastDeadLetterMsg.Load().(string); !contains(msg, "max attempts") {
		t.Errorf("DLQ message: want contains 'max attempts', got %q", msg)
	}
	if n := store.markFailed.Load(); n != 0 {
		t.Errorf("MarkFailed fired at max attempts: count=%d", n)
	}
}

// TestDispatcher_PartialPersistence_NextTickReclaimsAndSucceeds proves
// the at-least-once contract end-to-end: the FIRST tick's MarkProcessed
// fails (partial persistence) and the event's lease gets reclaimed
// again on the next tick. The idempotent ProcessFunc returns nil on
// every call, so the SECOND claim's MarkProcessed succeeds and the
// row is durably marked done. We assert the contract via atomic
// counters: ProcessFunc called EXACTLY twice (idempotent re-run) and
// MarkProcessed was called EXACTLY twice (once failed, once succeeded).
//
// Setup shortcut: the mock doesn't model real lease expiry; we
// enqueue the same event id twice in claimResponses to simulate the
// lease having expired and the row being re-claimable. The dispatcher
// sees (claim → fail → watermark) on the first pass and (claim →
// MarkProcessed → succeed) on the second pass.
func TestDispatcher_PartialPersistence_NextTickReclaimsAndSucceeds(t *testing.T) {
	var processCalls atomic.Int32
	var callNum atomic.Int32

	store := &mockOutboxStore{
		claimResponses: []claimResponse{
			{ev: newEvent(300, 1)}, // first claim: MarkProcessed fails
			{ev: newEvent(300, 1)}, // simulated re-claim on next tick
		},
		markProcessedFn: func() error {
			n := callNum.Add(1)
			if n == 1 {
				return errors.New("simulated partial: first-call DB blip")
			}
			return nil // second call succeeds (lease expired + re-claim)
		},
	}

	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(_ context.Context, _ *models.OutboxEvent) error {
			// Idempotent adapter: always succeeds.
			processCalls.Add(1)
			return nil
		},
		TickInterval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait until BOTH MarkProcessed calls fired AND the idempotent
	// adapter re-ran (so the contract is end-to-end verified).
	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.markProcessed.Load() == 2 && processCalls.Load() == 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := store.markProcessed.Load(); got != 2 {
		t.Errorf("markProcessed.Load: want 2 (one fail + one succeed across re-claim), got %d", got)
	}
	if got := processCalls.Load(); got != 2 {
		t.Errorf("ProcessFunc invocations: want 2 (idempotent adapter re-run on re-claim), got %d", got)
	}
	// MarkFailed and MarkDeadLetter must NEVER fire on this path.
	if n := store.markFailed.Load(); n != 0 {
		t.Errorf("markFailed.Load: want 0, got %d", n)
	}
	if n := store.markDeadLetter.Load(); n != 0 {
		t.Errorf("markDeadLetter.Load: want 0, got %d", n)
	}
}
