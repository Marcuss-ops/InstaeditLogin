package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRenderConcurrencyRegistryLimitsActiveProcessesAndThreads(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(1, 3)
	first, err := registry.Acquire(context.Background(), RenderPriorityTranscode)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if first.Threads != 3 {
		t.Fatalf("threads: got %d, want 3", first.Threads)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secondReady := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondReady)
		lease, acquireErr := registry.Acquire(ctx, RenderPriorityPublish)
		if acquireErr == nil {
			lease.Release()
		}
		secondDone <- acquireErr
	}()
	<-secondReady
	deadline := time.Now().Add(time.Second)
	for registry.Queued() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if registry.Queued() != 1 {
		t.Fatalf("queued: got %d, want 1", registry.Queued())
	}
	first.Release()
	if err := <-secondDone; err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if got := registry.Active(); got != 0 {
		t.Fatalf("active after releases: got %d, want 0", got)
	}
}

func TestRenderConcurrencyRegistryPrioritizesInteractiveRequests(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(1, 1)
	first, err := registry.Acquire(context.Background(), RenderPriorityMaintenance)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	var mu sync.Mutex
	order := make([]RenderPriority, 0, 2)
	acquire := func(priority RenderPriority) <-chan error {
		done := make(chan error, 1)
		go func() {
			lease, acquireErr := registry.Acquire(context.Background(), priority)
			if acquireErr == nil {
				mu.Lock()
				order = append(order, priority)
				mu.Unlock()
				lease.Release()
			}
			done <- acquireErr
		}()
		return done
	}
	lowDone := acquire(RenderPriorityMaintenance)
	highDone := acquire(RenderPriorityInteractive)
	deadline := time.Now().Add(time.Second)
	for registry.Queued() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if registry.Queued() != 2 {
		t.Fatalf("queued: got %d, want 2", registry.Queued())
	}
	first.Release()
	if err := <-highDone; err != nil {
		t.Fatalf("interactive acquire: %v", err)
	}
	if err := <-lowDone; err != nil {
		t.Fatalf("maintenance acquire: %v", err)
	}
	if len(order) != 2 || order[0] != RenderPriorityInteractive || order[1] != RenderPriorityMaintenance {
		t.Fatalf("acquisition order: got %v, want [interactive maintenance]", order)
	}
}

func TestRenderConcurrencyRegistryReleaseHookCanReenter(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(1, 1)
	called := make(chan struct{}, 1)
	registry.SetHooks(nil, nil, func(RenderRegistryEvent) {
		_ = registry.Active()
		_ = registry.Queued()
		called <- struct{}{}
	})
	lease, err := registry.Acquire(context.Background(), RenderPriorityTranscode)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	lease.Release()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("release hook was not called without deadlocking")
	}
}

func TestRenderConcurrencyRegistryCancellationDoesNotLeakCapacity(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(1, 1)
	first, err := registry.Acquire(context.Background(), RenderPriorityTranscode)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		lease, acquireErr := registry.Acquire(ctx, RenderPriorityTranscode)
		if lease != nil {
			lease.Release()
		}
		result <- acquireErr
	}()
	deadline := time.Now().Add(time.Second)
	for registry.Queued() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire: got %v, want context.Canceled", err)
	}
	first.Release()
	if got := registry.Active(); got != 0 {
		t.Fatalf("active after cancelled waiter: got %d, want 0", got)
	}
	if got := registry.Queued(); got != 0 {
		t.Fatalf("queued after cancelled waiter: got %d, want 0", got)
	}
}

func TestRenderConcurrencyRegistryCancellationAfterGrantDoesNotLeakCapacity(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(1, 1)
	first, err := registry.Acquire(context.Background(), RenderPriorityTranscode)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		lease, acquireErr := registry.Acquire(ctx, RenderPriorityTranscode)
		if lease != nil {
			lease.Release()
		}
		result <- acquireErr
	}()
	deadline := time.Now().Add(time.Second)
	for registry.Queued() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if registry.Queued() != 1 {
		t.Fatal("waiter did not enqueue")
	}

	// Cancel and release together. Whichever select branch wins, the
	// registry must return the slot and never leave active capacity stuck.
	cancel()
	first.Release()
	if err := <-result; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire: unexpected error %v", err)
	}
	if got := registry.Active(); got != 0 {
		t.Fatalf("active after cancellation/grant race: got %d, want 0", got)
	}
	if got := registry.Queued(); got != 0 {
		t.Fatalf("queued after cancellation/grant race: got %d, want 0", got)
	}
}

func TestRenderConcurrencyRegistryRejectsInvalidPriority(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(1, 1)
	_, err := registry.Acquire(context.Background(), RenderPriorityMaintenance+1)
	if !errors.Is(err, ErrInvalidRenderPriority) {
		t.Fatalf("invalid priority error: got %v", err)
	}
}

func TestRenderConcurrencyRegistryConcurrentAcquireRelease(t *testing.T) {
	registry := NewRenderConcurrencyRegistry(2, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const callers = 24
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			lease, err := registry.Acquire(ctx, RenderPriorityTranscode)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			if lease.Threads != 2 {
				t.Errorf("threads: got %d, want 2", lease.Threads)
			}
			time.Sleep(time.Millisecond)
			lease.Release()
		}()
	}
	wg.Wait()
	if got := registry.Active(); got != 0 {
		t.Fatalf("active at end: got %d, want 0", got)
	}
	if got := registry.Queued(); got != 0 {
		t.Fatalf("queued at end: got %d, want 0", got)
	}
}
