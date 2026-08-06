package worker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestRunPoolLoop_EmptyQueueUsesBoundedBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker := NewUploadWorker(nil, nil, nil, nil, nil, nil, nil, nil, nil, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)), UploadWorkerOptions{
		LeaseTTL:             time.Minute,
		EmptyQueueBackoffMin: 5 * time.Millisecond,
		EmptyQueueBackoffMax: 12 * time.Millisecond,
	})
	worker.applyDefaults()

	var mu sync.Mutex
	calls := make([]time.Time, 0, 3)
	claimer := func(context.Context, int, time.Duration) ([]*models.UploadJob, error) {
		mu.Lock()
		calls = append(calls, time.Now())
		count := len(calls)
		mu.Unlock()
		if count == 3 {
			cancel()
		}
		return nil, nil
	}

	started := time.Now()
	worker.runPoolLoop(ctx, "test", 1, claimer, nil, "worker-test")
	elapsed := time.Since(started)

	mu.Lock()
	got := append([]time.Time(nil), calls...)
	mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("claim calls: got %d, want exactly 3", len(got))
	}
	if got[1].Sub(got[0]) < 2*time.Millisecond {
		t.Errorf("first empty-queue delay was too short: %s", got[1].Sub(got[0]))
	}
	if got[2].Sub(got[1]) < 4*time.Millisecond {
		t.Errorf("second empty-queue delay was too short: %s", got[2].Sub(got[1]))
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("bounded backoff took too long: %s", elapsed)
	}
}
