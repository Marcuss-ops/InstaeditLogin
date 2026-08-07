package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// MetadataGenerationJobStore is the narrow slice of the repository the
// worker depends on. Defined here (not in the repository package) so the
// worker can be unit-tested with an in-memory fake without dragging in
// *sql.DB or sqlmock.
type MetadataGenerationJobStore interface {
	ClaimNext(leaseID string, leaseTTL time.Duration) (*models.MetadataGenerationJob, error)
	RenewLease(id int64, leaseID string, leaseTTL time.Duration) error
	MarkCompleted(id int64, leaseID string, result []byte) error
	MarkFailed(id int64, leaseID string, lastError string, backoff *time.Duration) error
	ReclaimExpired(leaseTTL time.Duration) (int64, error)
}

// MetadataGenerator is the NVIDIA metadata generation capability the
// worker needs. *services.MetadataGenerator satisfies it; the interface
// keeps the worker unit-testable without a live API key.
type MetadataGenerator interface {
	Generate(ctx context.Context, prompt string) (*services.NVIDIAMetadataResponse, error)
}

// MetadataGenerationWorker polls metadata_generation_jobs, calls
// MetadataGenerator.Generate for each claimed job, and marks the
// result. One goroutine per replica (multi-replica safety via
// Postgres SKIP LOCKED). The worker is intentionally simple:
// single-threaded drain loop, no concurrency within a replica.
type MetadataGenerationWorker struct {
	store    MetadataGenerationJobStore
	gen      MetadataGenerator
	workerID string
	leaseTTL time.Duration
	interval time.Duration
	logger   *slog.Logger
	rand     *rand.Rand
}

// NewMetadataGenerationWorker constructs the worker. interval <= 0
// defaults to 5s; the lease is deliberately longer than the
// NVIDIA API timeout so a slow request cannot be reclaimed mid-call.
func NewMetadataGenerationWorker(
	store MetadataGenerationJobStore,
	gen MetadataGenerator,
	interval time.Duration,
	logger *slog.Logger,
) *MetadataGenerationWorker {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	leaseTTL := 10 * time.Minute
	if logger == nil {
		logger = slog.Default()
	}
	workerID := fmt.Sprintf("metadata-gen-%d", time.Now().UnixNano())
	return &MetadataGenerationWorker{
		store:    store,
		gen:      gen,
		workerID: workerID,
		leaseTTL: leaseTTL,
		interval: interval,
		logger:   logger,
		rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Run is the blocking lifecycle. Call in a goroutine (via WorkerSpec).
func (w *MetadataGenerationWorker) Run(ctx context.Context) error {
	w.logger.Info("metadata generation worker started",
		"interval_seconds", w.interval.Seconds(),
		"lease_ttl_seconds", w.leaseTTL.Seconds())
	defer w.logger.Info("metadata generation worker stopped")

	// Reclaim expired leases on startup.
	if n, err := w.store.ReclaimExpired(w.leaseTTL); err != nil {
		w.logger.Warn("metadata generation worker: reclaim expired leases on startup", "error", err)
	} else if n > 0 {
		w.logger.Info("metadata generation worker: reclaimed expired leases", "count", n)
	}

	// Initial drain — no wait for the first tick.
	w.drainOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.drainOnce(ctx)
		}
	}
}

// drainOnce claims jobs one-at-a-time until the queue is empty.
func (w *MetadataGenerationWorker) drainOnce(ctx context.Context) {
	// Reclaim expired leases every tick (cheap).
	if n, err := w.store.ReclaimExpired(w.leaseTTL); err != nil {
		w.logger.Warn("metadata generation worker: reclaim expired leases", "error", err)
	} else if n > 0 {
		w.logger.Info("metadata generation worker: reclaimed expired leases", "count", n)
	}

	for {
		if err := ctx.Err(); err != nil {
			return
		}
		job, err := w.store.ClaimNext(w.workerID, w.leaseTTL)
		if err != nil {
			if errors.Is(err, repository.ErrMetadataGenAlreadyClaimed) {
				return // queue empty
			}
			w.logger.Warn("metadata generation worker: claim error", "error", err)
			return
		}
		w.processOne(ctx, job)
	}
}

// processOne handles a single claimed job: runs the NVIDIA generation
// with a heartbeat goroutine, then marks completed or failed.
func (w *MetadataGenerationWorker) processOne(ctx context.Context, job *models.MetadataGenerationJob) {
	// Heartbeat goroutine keeps the lease alive. Derived from
	// context.Background() (NOT the parent ctx) on purpose: on worker
	// shutdown the heartbeat must keep renewing while Generate drains
	// its 300s-bounded HTTP call, so a peer never reclaims the row
	// mid-generation. hbCancel() below stops it as soon as Generate
	// returns.
	hbCtx, hbCancel := context.WithCancel(context.Background())
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		w.heartbeatLoop(hbCtx, job.ID)
	}()

	start := time.Now()
	result, err := w.gen.Generate(ctx, job.Prompt)
	duration := time.Since(start)

	hbCancel()
	<-hbDone

	if err != nil {
		if errors.Is(err, services.ErrNVIDIANotConfigured) {
			w.logger.Warn("metadata generation worker: NVIDIA not configured, failing job permanently",
				"job_id", job.ID, "workspace_id", job.WorkspaceID)
			// Terminal — no retry for misconfiguration.
			if mErr := w.store.MarkFailed(job.ID, w.workerID, err.Error(), nil); mErr != nil {
				w.logger.Warn("metadata generation worker: MarkFailed (terminal) error", "job_id", job.ID, "error", mErr)
			}
			return
		}
		// Transient (or unknown) failure — requeue with backoff.
		backoff := w.computeBackoff(job.AttemptCount)
		w.logger.Warn("metadata generation worker: generation failed, retrying",
			"job_id", job.ID, "workspace_id", job.WorkspaceID,
			"attempt", job.AttemptCount+1, "max_attempts", job.MaxAttempts,
			"backoff_seconds", backoff.Seconds(), "duration_ms", duration.Milliseconds(),
			"error", err)
		if mErr := w.store.MarkFailed(job.ID, w.workerID, err.Error(), &backoff); mErr != nil {
			w.logger.Warn("metadata generation worker: MarkFailed error", "job_id", job.ID, "error", mErr)
		}
		return
	}

	// Success — marshal the full response as JSONB result.
	resultBytes, err := json.Marshal(result)
	if err != nil {
		w.logger.Error("metadata generation worker: marshal result failed",
			"job_id", job.ID, "error", err)
		if mErr := w.store.MarkFailed(job.ID, w.workerID, fmt.Sprintf("marshal result: %v", err), nil); mErr != nil {
			w.logger.Warn("metadata generation worker: MarkFailed error", "job_id", job.ID, "error", mErr)
		}
		return
	}
	if err := w.store.MarkCompleted(job.ID, w.workerID, resultBytes); err != nil {
		w.logger.Warn("metadata generation worker: MarkCompleted error", "job_id", job.ID, "error", err)
		return
	}
	w.logger.Info("metadata generation worker: completed",
		"job_id", job.ID, "workspace_id", job.WorkspaceID,
		"duration_ms", duration.Milliseconds())
}

// heartbeatLoop renews the lease every 60s until the context is done.
func (w *MetadataGenerationWorker) heartbeatLoop(ctx context.Context, jobID int64) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.store.RenewLease(jobID, w.workerID, w.leaseTTL); err != nil {
				w.logger.Debug("metadata generation worker: heartbeat renew failed",
					"job_id", jobID, "error", err)
				return
			}
		}
	}
}

// computeBackoff returns a decorrelated-jittered backoff for the given
// attempt number (0-based). Base: 5s, cap: 5min.
func (w *MetadataGenerationWorker) computeBackoff(attempt int) time.Duration {
	base := 5 * time.Second
	cap_ := 5 * time.Minute
	// Decorrelated jitter: sleep = min(cap, base * 2 * attempt)
	d := base * time.Duration(1<<uint(attempt)) // 5s, 10s, 20s, 40s, 80s...
	if d > cap_ {
		d = cap_
	}
	// Add jitter: [0, d)
	jitter := time.Duration(w.rand.Int63n(int64(d)))
	return jitter
}
