package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// --- Dispatch-loop behaviour ------------------------------------------------

// TestDispatcher_HappyPath_MarkProcessed covers the canonical
// success path: claim → process returns nil → MarkProcessed.
// Asserts that ONLY MarkProcessed fires (no MarkFailed, no
// MarkDeadLetter, no heartbeat renews since process is instant).
func TestDispatcher_HappyPath_MarkProcessed(t *testing.T) {
	store := &mockOutboxStore{
		claimResponses: []claimResponse{{ev: newEvent(42, 1)}},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore:  store,
		Process:      func(_ context.Context, _ *models.OutboxEvent) error { return nil },
		TickInterval: 50 * time.Millisecond, // not used; we drive drain directly via Run
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run on a goroutine; cancel via the timeout so Run returns.
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for the dispatcher to call MarkProcessed.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.markProcessed.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := store.lastProcessed.Load(); got != 42 {
		t.Errorf("last processed id: want 42, got %d", got)
	}
	if n := store.markFailed.Load(); n != 0 {
		t.Errorf("MarkFailed fired on happy path: count=%d", n)
	}
	if n := store.markDeadLetter.Load(); n != 0 {
		t.Errorf("MarkDeadLetter fired on happy path: count=%d", n)
	}
}

// TestDispatcher_RaceErr_LoopContinues covers the peer-race branch:
// ClaimNext returns ErrOutboxRace → drainOnce continues (no log, no
// panic). We enqueue race + happy to verify both are consumed in
// sequence on the same drain.
func TestDispatcher_RaceErr_LoopContinues(t *testing.T) {
	store := &mockOutboxStore{
		claimResponses: []claimResponse{
			{err: repository.ErrOutboxRace},
			{ev: newEvent(80, 1)},
			// Then empty to terminate the drain.
		},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore:  store,
		Process:      func(_ context.Context, _ *models.OutboxEvent) error { return nil },
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for the MarkProcessed (after the race) to fire.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.markProcessed.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	// The race must NOT increment MarkProcessed (peer dispatcher
	// completed it). Only the second claim should produce a mark.
	if got := store.lastProcessed.Load(); got != 80 {
		t.Errorf("last processed id: want 80 (after race), got %d", got)
	}
	if n := store.markProcessed.Load(); n != 1 {
		t.Errorf("MarkProcessed count: want 1, got %d (race path leaked)", n)
	}
}

// TestDispatcher_QueueEmpty_StopsDraining covers the empty-queue /
// already-claimed branch: ClaimNext returns ErrOutboxAlreadyClaimed →
// drainOnce returns. Asserts no Mark* and no panic.
func TestDispatcher_QueueEmpty_StopsDraining(t *testing.T) {
	store := &mockOutboxStore{
		claimFallback: repository.ErrOutboxAlreadyClaimed,
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore:  store,
		Process:      func(_ context.Context, _ *models.OutboxEvent) error { return nil },
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := d.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run err: want DeadlineExceeded, got %v", err)
	}
	if n := store.markProcessed.Load() + store.markFailed.Load() + store.markDeadLetter.Load(); n != 0 {
		t.Errorf("Mark calls on empty queue: want 0, got %d", n)
	}
}

// TestDispatcher_RealDBError_LogsBreaksDrain covers the genuine
// infrastructure error path: ClaimNext returns a non-sentinel error →
// drainOnce logs warn and returns without going into panic. The
// dispatcher should KEEP ticking (test ends via ctx-cancel).
func TestDispatcher_RealDBError_LogsBreaksDrain(t *testing.T) {
	store := &mockOutboxStore{
		claimResponses: []claimResponse{
			{err: errors.New("connection lost")},
			{ev: newEvent(90, 1)}, // second claim succeeds; verifies loop wasn't broken long-term
		},
	}
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore:  store,
		Process:      func(_ context.Context, _ *models.OutboxEvent) error { return nil },
		TickInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for the second claim's MarkProcessed (proves the loop
	// continued past the broken-db-error path on the next tick).
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.markProcessed.Load() > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if got := store.lastProcessed.Load(); got != 90 {
		t.Errorf("last processed: want 90, got %d", got)
	}
}
