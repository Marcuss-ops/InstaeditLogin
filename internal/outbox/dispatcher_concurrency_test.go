package outbox_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
)

// --- Concurrency: heartbeat + graceful shutdown -----------------------------

// TestDispatcher_Heartbeat_RenewsLease verifies that RenewLease is
// called while ProcessFunc is in flight (i.e. lease_until is being
// kept fresh during a slow dispatch).
func TestDispatcher_Heartbeat_RenewsLease(t *testing.T) {
	store := &mockOutboxStore{
		claimResponses: []claimResponse{{ev: newEvent(100, 1)}},
	}
	// ProcessFunc blocks for ~120ms; heartbeat interval is 20ms → ~6
	// ticks before process completes.
	started := make(chan struct{})
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(ctx context.Context, _ *models.OutboxEvent) error {
			close(started)
			select {
			case <-time.After(120 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		HeartbeatInterval: 20 * time.Millisecond,
		TickInterval:      1 * time.Hour,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	<-started
	// Wait until at least one heartbeat tick has fired.
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if store.renews.Load() >= 1 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	if n := store.renews.Load(); n < 1 {
		t.Errorf("renews: want >=1 during in-flight process, got %d", n)
	}
}

// TestDispatcher_GracefulShutdown_DrainsInFlight covers the user's
// "graceful shutdown al worker esistente" requirement: when ctx is
// cancelled, the dispatcher stops claiming new rows but lets the
// in-flight one finish. ProcessFunc is gated by a channel; we cancel
// mid-flight and then unblock to verify the drain path runs to
// completion (MarkProcessed on the in-flight, no leaked claims).
//
// IMPORTANT — processFunc gates ONLY on the `gate` channel, NOT on
// ctx.Done(). A ctx.Done()-aware ProcessFunc would short-circuit
// on cancellation and return ctx.Err(), defeating the test's
// intent (which is "Run stays blocked on the in-flight"). The
// dispatcher's drain-on-shutdown semantics only apply to long-running
// ProcessFunc implementations that explicitly respect ctx; the
// test's ProcessFunc ignores ctx to model the worst case where the
// caller doesn't propagate cancellation.
func TestDispatcher_GracefulShutdown_DrainsInFlight(t *testing.T) {
	store := &mockOutboxStore{
		claimResponses: []claimResponse{{ev: newEvent(110, 1)}},
	}
	gate := make(chan struct{})    // test→process signal
	entered := make(chan struct{}) // process→test signal
	d := outbox.NewDispatcher(outbox.DispatcherConfig{
		OutboxStore: store,
		Process: func(_ context.Context, _ *models.OutboxEvent) error {
			close(entered)
			<-gate // block strictly on the test-driven gate; ctx is ignored
			return nil
		},
		TickInterval: 1 * time.Hour, // only the initial drain matters
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	// Wait for ProcessFunc to be entered (in-flight).
	<-entered
	// Cancel the dispatcher — Run should not return yet (draining).
	cancel()
	// Confirm Run is still blocked on the in-flight process.
	select {
	case err := <-done:
		t.Fatalf("Run returned prematurely with %v (in-flight should drain)", err)
	case <-time.After(50 * time.Millisecond):
	}
	// Unblock the process; Run should return ctx.Canceled now.
	close(gate)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err: want context.Canceled, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return after gate closed")
	}

	// ProcessFunc returned nil → MarkProcessed must have fired.
	if n := store.markProcessed.Load(); n != 1 {
		t.Errorf("MarkProcessed after graceful drain: want 1, got %d", n)
	}
}
