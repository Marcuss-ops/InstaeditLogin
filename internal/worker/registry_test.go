package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestRegistry_StartAllHealthy(t *testing.T) {
	r := NewRegistry()
	var ran int32

	r.Register(WorkerSpec{
		Name:     "w1",
		Critical: true,
		Run: func(ctx context.Context) error {
			atomic.AddInt32(&ran, 1)
			<-ctx.Done()
			return ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.StartAll(ctx)

	// Give the supervisor time to mark the worker healthy.
	time.Sleep(100 * time.Millisecond)
	statuses := r.GetStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].State != StateHealthy && statuses[0].State != StateStarting {
		t.Errorf("expected starting or healthy, got %q", statuses[0].State)
	}
	if !r.AllHealthy() {
		t.Errorf("expected AllHealthy to be true")
	}

	cancel()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("unexpected critical error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not stop in time")
	}

	if atomic.LoadInt32(&ran) != 1 {
		t.Errorf("expected worker to run once, got %d", ran)
	}
}

func TestRegistry_CriticalFailurePropagates(t *testing.T) {
	r := NewRegistry()
	boomErr := errors.New("boom")

	r.Register(WorkerSpec{
		Name:     "failing",
		Critical: true,
		Run: func(ctx context.Context) error {
			return boomErr
		},
	})

	ch := r.StartAll(context.Background())
	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, boomErr) {
			t.Fatalf("expected error to wrap boom, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("critical error was not reported")
	}
}

func TestRegistry_NonCriticalFailureDoesNotPropagate(t *testing.T) {
	r := NewRegistry()

	r.Register(WorkerSpec{
		Name:     "failing",
		Critical: false,
		Run: func(ctx context.Context) error {
			return errors.New("boom")
		},
	})
	r.Register(WorkerSpec{
		Name:     "blocker",
		Critical: true,
		Run: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.StartAll(ctx)

	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("did not expect error for non-critical failure, got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		// expected: the non-critical failure does not abort the registry.
	}

	cancel()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("registry did not stop")
	}
}

func TestRegistry_Heartbeat(t *testing.T) {
	r := NewRegistry()
	r.Register(WorkerSpec{
		Name:     "heartbeater",
		Critical: true,
		Run: func(ctx context.Context) error {
			// Simulate a worker that does its own heartbeat calls.
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					r.Heartbeat("heartbeater")
				}
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.StartAll(ctx)

	time.Sleep(150 * time.Millisecond)
	statuses := r.GetStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].HeartbeatAt.IsZero() {
		t.Errorf("expected heartbeat to be recorded")
	}

	cancel()
	<-ch
}

func TestRegistry_RecordSuccess(t *testing.T) {
	r := NewRegistry()
	r.Register(WorkerSpec{
		Name:     "success",
		Critical: true,
		Run: func(ctx context.Context) error {
			r.RecordSuccess("success")
			<-ctx.Done()
			return ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.StartAll(ctx)
	time.Sleep(50 * time.Millisecond)

	statuses := r.GetStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].LastSuccessAt.IsZero() {
		t.Errorf("expected last success to be recorded")
	}

	cancel()
	<-ch
}

func TestRegistry_Ready_NoWorkers(t *testing.T) {
	r := NewRegistry()
	ready, _ := r.Ready()
	if ready {
		t.Fatal("expected empty registry to be not ready")
	}
}

func TestRegistry_Ready_CriticalFailure(t *testing.T) {
	r := NewRegistry()
	r.Register(WorkerSpec{
		Name:     "failing",
		Critical: true,
		Run: func(ctx context.Context) error {
			return errors.New("boom")
		},
	})

	ch := r.StartAll(context.Background())
	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected critical error")
		}
	case <-time.After(time.Second):
		t.Fatal("critical error was not reported")
	}

	ready, statuses := r.Ready()
	if ready {
		t.Fatal("expected readiness to fail after critical worker failure")
	}
	if len(statuses) != 1 || statuses[0].State != StateFailed {
		t.Fatalf("expected failed state, got %+v", statuses)
	}
}

func TestRegistry_Ready_NonCriticalFailureDoesNotAffectReadiness(t *testing.T) {
	r := NewRegistry()
	r.Register(WorkerSpec{
		Name:     "failing",
		Critical: false,
		Run: func(ctx context.Context) error {
			return errors.New("boom")
		},
	})
	r.Register(WorkerSpec{
		Name:     "blocker",
		Critical: true,
		Run: func(ctx context.Context) error {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					r.Heartbeat("blocker")
				}
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.StartAll(ctx)

	// Wait until the non-critical worker has failed and the critical
	// worker is healthy.
	time.Sleep(200 * time.Millisecond)

	ready, statuses := r.Ready()
	if !ready {
		t.Fatalf("expected readiness to stay ok when only non-critical worker failed: %+v", statuses)
	}

	cancel()
	<-ch
}

func TestRegistry_Collect(t *testing.T) {
	r := NewRegistry()
	r.Register(WorkerSpec{
		Name:     "healthy",
		Critical: true,
		Run: func(ctx context.Context) error {
			r.Heartbeat("healthy")
			<-ctx.Done()
			return ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := r.StartAll(ctx)

	time.Sleep(100 * time.Millisecond)

	gatherer := prometheus.NewRegistry()
	gatherer.MustRegister(r)
	mfs, err := gatherer.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	found := false
outerLoop:
	for _, mf := range mfs {
		if mf.GetName() != "worker_state" {
			continue
		}
		for _, m := range mf.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["worker"] == "healthy" && labels["state"] == string(StateHealthy) {
				found = true
				break outerLoop
			}
		}
	}
	if !found {
		t.Fatalf("expected worker_state metric for healthy worker, got %v", mfs)
	}

	cancel()
	<-ch
}
