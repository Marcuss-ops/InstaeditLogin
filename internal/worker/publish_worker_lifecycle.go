package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Run blocks until ctx is cancelled, executing one tick per interval
// period. Performs a graceful drain: when ctx.Done() fires while a
// tick is mid-flight, the current tick completes naturally and Run
// returns only after that. Returns ctx.Err() on shutdown; logs
// non-nil errors and continues otherwise.
//
// Taglio 5.x: Run only drives tick() now. The publishing→published
// transition is owned by ReconcileWorker.Run on its own goroutine
// (see reconcile_worker.go). The two goroutines share the publish-
// state at the post_targets.status column; the publish driver's
// lease-aware claim (ClaimQueuedTargetWithLease) is the only writer
// for queued→publishing, and the reconciler is the only writer for
// publishing→published|failed.
func (w *PublishWorker) Run(ctx context.Context) error {
	w.logger.Info("publish worker started",
		"interval_seconds", w.interval.Seconds(),
		"worker_id", w.workerID)
	defer w.logger.Info("publish worker stopped", "worker_id", w.workerID)

	// Initial tick — no wait for the first sweep.
	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce executes one tick and logs the result. The Reconciler is
// no longer called from runOnce — Taglio 5.x split it into its own
// goroutine (ReconcileWorker.Run, reconcile_worker.go).
func (w *PublishWorker) runOnce(ctx context.Context) {
	if processed, ok, ko, err := w.tick(ctx); err != nil {
		w.logger.Warn("publish worker tick failed", "error", err)
	} else if processed > 0 {
		w.logger.Info("publish worker tick done",
			"processed", processed, "succeeded", ok, "failed", ko)
	}
} // tick processes a bounded, fair batch of independent child targets exactly
// once. Each target owns its own claim, lease, retry budget, error state and
// idempotency key; no parent can hold a worker slot while its siblings run.
// Returns (processed, succeeded, failed, err).
//
// Burst parallelization: the batch is drained by a fixed worker pool
// (publishConcurrency, default 4) instead of a sequential for-loop, so
// 30 videos scheduled at the same minute are published concurrently
// instead of queuing behind one slow target. Each target still runs its
// own atomic claim (ClaimQueuedTargetWithLease), so the platform
// throttle + the DB row locks — not the pool — serialize the actual API
// calls. publishConcurrency <= 1 (or a single pending target) keeps the
// legacy sequential path.
//
// Per-target errors are LOGGED and counted but do not abort the tick;
// the worker should keep trying other targets even if Meta/Twitter/etc.
// are flapping. Counters are atomic — workers update them concurrently.
func (w *PublishWorker) tick(ctx context.Context) (processed, succeeded, failed int, err error) {
	pending, err := w.postRepo.ListPending(time.Now())
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list pending: %w", err)
	}
	if len(pending) == 0 {
		return 0, 0, 0, nil
	}

	concurrency := w.publishConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > len(pending) {
		concurrency = len(pending)
	}
	if concurrency == 1 {
		// Sequential fast path — single target (or operator opt-out).
		for i := range pending {
			// Index-based loop (not `for _, target`): we mutate &pending[i]
			// inside publishTarget and the local copy must reflect those
			// mutations when we pass it to UpdateStatus.
			if err := w.publishTarget(ctx, &pending[i]); err != nil {
				w.logger.Warn("publish target failed",
					"target_id", pending[i].ID,
					"post_id", pending[i].PostID,
					"error", err)
				failed++
			} else {
				succeeded++
			}
			processed++
		}
		return processed, succeeded, failed, nil
	}

	// Parallel path: `concurrency` workers drain the pending batch from a
	// channel. Each target pointer is unique (distinct &pending[i]), so
	// workers never share a target; the per-target claim CAS is the row
	// lock. Counters are atomically updated by all workers.
	var processedCount, succeededCount, failedCount atomic.Int32
	targets := make(chan *models.PostTarget)
	var done sync.WaitGroup
	done.Add(concurrency)
	for worker := 0; worker < concurrency; worker++ {
		go func() {
			defer done.Done()
			for t := range targets {
				if err := w.publishTarget(ctx, t); err != nil {
					w.logger.Warn("publish target failed",
						"target_id", t.ID,
						"post_id", t.PostID,
						"error", err)
					failedCount.Add(1)
				} else {
					succeededCount.Add(1)
				}
				processedCount.Add(1)
			}
		}()
	}
	for i := range pending {
		select {
		case targets <- &pending[i]:
		case <-ctx.Done():
			// Graceful drain on shutdown: stop feeding new targets; the
			// in-flight workers still finish their current target (their
			// internal ctx checks abort the API call) and drain the
			// channel.
		}
	}
	close(targets)
	done.Wait()
	return int(processedCount.Load()), int(succeededCount.Load()), int(failedCount.Load()), nil
}
