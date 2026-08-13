package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// uniqueWorkerID derives a per-pod, per-restart lease identity.
// Format: "{prefix}-{host}-{pid}-{shortUUID}". Hostname + PID + a
// short UUID suffix avoids collisions across replicas / restarts
// on the same pod (Kubernetes always gives PID 1; multiple replicas
// of the same pool on the same host is rare but possible).
func uniqueWorkerID(prefix string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "upload-worker"
	}
	shortUUID := uuid.NewString()[:8] // first 8 chars of UUIDv4
	return fmt.Sprintf("%s-%s-%d-%s", prefix, host, os.Getpid(), shortUUID)
}

// applyDefaults fills zero-valued opts fields with conservative
// defaults. Called once at Run start.
func (w *UploadWorker) applyDefaults() {
	if w.opts.IngestConcurrency <= 0 {
		w.opts.IngestConcurrency = 3
	}
	if w.opts.UploadConcurrency <= 0 {
		w.opts.UploadConcurrency = 4
	}
	if w.opts.LeaseTTL <= 0 {
		w.opts.LeaseTTL = 60 * time.Second
	}
	if w.opts.HeartbeatInterval <= 0 {
		w.opts.HeartbeatInterval = w.opts.LeaseTTL / 3 // three renewals before expiry
	}
	if w.opts.ReclaimInterval <= 0 {
		w.opts.ReclaimInterval = 30 * time.Second
	}
	if w.opts.EmptyQueueBackoffMin <= 0 {
		w.opts.EmptyQueueBackoffMin = time.Second
	}
	if w.opts.EmptyQueueBackoffMax <= 0 {
		w.opts.EmptyQueueBackoffMax = 30 * time.Second
	}
	if w.opts.EmptyQueueBackoffMax < w.opts.EmptyQueueBackoffMin {
		w.opts.EmptyQueueBackoffMax = w.opts.EmptyQueueBackoffMin
	}
	// Blocco #2 P0 — VideoRetentionBufferDays defaults to 7 (mirrors
	// env VIDEO_RETENTION_BUFFER_DAYS=7). Zero would compute
	// expires_at = now (already-expired asset → /complete 410 forever).
	if w.opts.VideoRetentionBufferDays <= 0 {
		w.opts.VideoRetentionBufferDays = 7
	}
}

// Run orchestrates the upload-worker-pool goroutines:
//
//  1. Apply lazy defaults on opts.
//  2. Synchronously reclaim stuck leases on startup (if ReclaimOnStart).
//  3. Spawn the reclaimer ticker (background cadence reclaim).
//  4. Spawn the ingest pool (N ingest goroutines, per-row heartbeat).
//  5. Spawn the upload pool (M upload goroutines, per-row heartbeat).
//  6. Block on ctx.Done() + waitGroup.Wait() for graceful shutdown.
//
// Each top-level goroutine exits cleanly on ctx.Done(); the per-row
// heartbeat goroutines exit via their own context cancel when
// processIngestJob / processPublishJob returns.
func (w *UploadWorker) Run(ctx context.Context) error {
	w.applyDefaults()

	w.logger.Info("upload worker pool started",
		"interval_seconds", w.interval.Seconds(),
		"ingest_concurrency", w.opts.IngestConcurrency,
		"upload_concurrency", w.opts.UploadConcurrency,
		"lease_ttl_seconds", w.opts.LeaseTTL.Seconds(),
		"heartbeat_interval_seconds", w.opts.HeartbeatInterval.Seconds(),
		"reclaim_interval_seconds", w.opts.ReclaimInterval.Seconds(),
		"empty_queue_backoff_min_seconds", w.opts.EmptyQueueBackoffMin.Seconds(),
		"empty_queue_backoff_max_seconds", w.opts.EmptyQueueBackoffMax.Seconds(),
		"reclaim_on_start", w.opts.ReclaimOnStart,
	)
	defer w.logger.Info("upload worker pool stopped")

	// (2) Startup reclaim synchronous — recover any rows left
	// 'leased' by a previous crash before the pools start claiming
	// so workers don't race against leases with dead heartbeats.
	if w.opts.ReclaimOnStart {
		n, err := w.jobRepo.ReclaimExpiredLeases(ctx, 10000)
		if err != nil {
			w.logger.Error("upload worker: startup reclaim failed", "error", err)
		} else if n > 0 {
			w.logger.Info("upload worker: startup reclaim recovered rows", "count", n)
		}
	}

	var wg sync.WaitGroup

	// (3) Reclaimer ticker — background, separate from per-row heartbeats.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runReclaimerLoop(ctx)
	}()

	// (4) Ingest pool — claims status IN ('pending','retry_wait'),
	// transitions rows to 'ready_to_publish' via MarkIngested.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runIngestPool(ctx)
	}()

	// (5) Upload pool — claims status = 'ready_to_publish',
	// completes rows via MarkCompleted.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runUploadPool(ctx)
	}()

	// (6) YouTube delivery pool — claims individual (video, channel)
	// rows from youtube_target_publications (state='ready_to_upload' /
	// 'preflight' / due retry_wait | quota_wait) and runs the private
	// upload per row. Concurrency is bounded by the same
	// YOUTUBE_UPLOAD_CONCURRENCY knob as the upload pool: the fan-out
	// of a single job with N channels is N independent rows consumed
	// by this GLOBAL pool, so a slow channel never blocks its
	// siblings. Disabled (logs once, rows wait) when ytPubStore or the
	// delivery post store are not wired.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runYouTubeDeliveryPool(ctx)
	}()

	// (7) Delivery-lease reclaimer — recovers delivery rows stuck in
	// 'uploading' with an expired lease (worker crash / network
	// partition without a heartbeat) back to 'ready_to_upload'.
	wg.Add(1)
	go func() {
		defer wg.Done()
		w.runDeliveryReclaimerLoop(ctx)
	}()

	wg.Wait()
	return ctx.Err()
}

// runReclaimerLoop ticks on opts.ReclaimInterval, calling
// runReclaimerTick on each tick. The ticker-driven wrapper is
// the production hot path; the per-tick body is extracted into
// runReclaimerTick so tests (Task 10.10.x polish #1) can drive
// the recovery wire-up directly without spinning a real
// time.NewTicker. The metric increment lives inside runReclaimerTick
// itself — removing the metrics.RecordLeaseExpiry call from
// runReclaimerTick causes the assembly-line tick to silently
// flatten the counter while still "reclaiming" rows, and the
// polish #1 test asserts the wire-up is in place.
func (w *UploadWorker) runReclaimerLoop(ctx context.Context) {
	ticker := time.NewTicker(w.opts.ReclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runReclaimerTick(ctx)
		}
	}
}

// runReclaimerTick performs ONE reclaimer tick: outsourced from
// runReclaimerLoop so tests can call it directly. Recovers expired
// leases (status='leased' AND lease_expires_at<NOW() AND
// heartbeat_at<NOW()-5m) back to status='pending' with a 100-row
// per-tick cap so a backlog can't tie up the DB. Each successful
// reclaim increments metrics.lease_expiry_total{upload} by the
// row-count so the metric preserves per-row fidelity (a tick that
// recovers 7 rows shows +7, not +1).
//
// Failure modes:
//   - err from jobRepo.ReclaimExpiredLeases → log + return (the
//     next tick retries)
//   - n == 0 → no metric increment, no log (zero rows is the
//     steady-state and we don't want a metric spam during normal
//     operation)
//   - n > 0 → metrics.RecordLeaseExpiry("upload", n) + log
//
// Test 1 of Task 10.10.x polish #1
// (internal/worker/task_10_10_recovery_test.go) asserts
// construction + runReclaimerTick invocation + LeaseExpiryCount
// delta = the configured reclaim count, so a regression that
// deletes the RecordLeaseExpiry call line below trips the test.
func (w *UploadWorker) runReclaimerTick(ctx context.Context) {
	n, err := w.jobRepo.ReclaimExpiredLeases(ctx, 100)
	if err != nil {
		w.logger.Error("upload worker: reclaimer tick failed", "error", err)
		return
	}
	if n > 0 {
		w.logger.Info("upload worker: reclaimer recovered rows", "count", n)
		metrics.RecordLeaseExpiry("upload", n)
	}
}

// runIngestPool is the ingest side of the worker: Drive → S3
// streaming, transitions to ready_to_publish. Pool's workerID is
// "ingest-..." so a Mark* CAS can never collide with the upload
// pool's leases.
func (w *UploadWorker) runIngestPool(ctx context.Context) {
	poolWorkerID := uniqueWorkerID("ingest")
	w.runPoolLoop(ctx, "ingest", w.opts.IngestConcurrency,
		func(c context.Context, limit int, lease time.Duration) ([]*models.UploadJob, error) {
			return w.jobRepo.ClaimBatch(c, poolWorkerID, limit, lease)
		},
		w.processIngestJob,
		poolWorkerID,
	)
}

// runUploadPool is the upload side: S3 → post → YouTube
// videos.insert. Pool's workerID is "upload-...".
func (w *UploadWorker) runUploadPool(ctx context.Context) {
	poolWorkerID := uniqueWorkerID("upload")
	w.runPoolLoop(ctx, "upload", w.opts.UploadConcurrency,
		func(c context.Context, limit int, lease time.Duration) ([]*models.UploadJob, error) {
			return w.jobRepo.ClaimBatchForPublish(c, poolWorkerID, limit, lease)
		},
		w.processPublishJob,
		poolWorkerID,
	)
}

// claimFn is the per-pool signature: returns rows claimed for the
// calling worker's workerID. Each pool binds its own concrete
// implementation (ClaimBatch for ingest, ClaimBatchForPublish for
// upload).
type claimFn func(ctx context.Context, limit int, lease time.Duration) ([]*models.UploadJob, error)

// processFn is the per-row processing: returns nil on success or
// an error wrapped with a typed sentinel where appropriate.
type processFn func(ctx context.Context, job *models.UploadJob, workerID string) error

// runPoolLoop is the generic pool loop. Tick cadence is
// w.interval (legacy shared cadence). Concurrency is bounded by a
// semaphore of size `concurrency`. Per claimed row, spawn a
// goroutine that wraps processFn in a per-row heartbeat. The
// poolWorkerID is the same string for every claim made by this
// pool during the process — all rows in a single ClaimBatch share
// it as their lease_owner.
func (w *UploadWorker) runPoolLoop(
	ctx context.Context,
	poolName string,
	concurrency int,
	claimer claimFn,
	processor processFn,
	poolWorkerID string,
) {
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	backoff := w.opts.EmptyQueueBackoffMin

	for {
		if ctx.Err() != nil {
			return
		}
		claimed := w.runPoolTick(ctx, poolName, sem, claimer, processor, poolWorkerID)
		if claimed {
			backoff = w.opts.EmptyQueueBackoffMin
		} else {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > w.opts.EmptyQueueBackoffMax {
				backoff = w.opts.EmptyQueueBackoffMax
			}
		}
	}
}

func (w *UploadWorker) runPoolTick(
	ctx context.Context,
	poolName string,
	sem chan struct{},
	claimer claimFn,
	processor processFn,
	poolWorkerID string,
) bool {
	jobs, err := claimer(ctx, cap(sem), w.opts.LeaseTTL)
	if err != nil {
		w.logger.Error("upload worker: claim batch failed", "pool", poolName, "error", err)
		return false
	}
	if len(jobs) == 0 {
		return false
	}
	w.logger.Info("upload worker: claimed batch", "pool", poolName, "count", len(jobs), "worker_id", poolWorkerID)

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return false
		case sem <- struct{}{}:
		}
		go func(j *models.UploadJob) {
			defer func() { <-sem }()

			w.logger.Info("upload worker: processing job",
				"pool", poolName, "job_id", j.ID, "source_type", j.SourceType,
				"attempt_count", j.AttemptCount, "max_attempts", j.MaxAttempts,
			)

			if err := w.runWithHeartbeat(ctx, j, poolWorkerID, poolName, processor); err != nil {
				w.handleProcessingError(ctx, poolName, poolWorkerID, j, err)
			}
		}(job)
	}
	return true
}

// runWithHeartbeat spawns a per-row heartbeat goroutine that ticks
// every opts.HeartbeatInterval calling Heartbeat; the goroutine
// exits via hbCtx cancel when processFn returns. If Heartbeat
// returns ErrUploadJobLeaseLost (peer stole the lease during
// processing), the heartbeat goroutine logs + exits silently — the
// worker has already lost the row to a peer.
//
// Defer ordering — single defer matters:
// Go defers run LIFO. We intentionally keep cancel + wg.Wait +
// recover in ONE defer so the execution order on return is:
//  1. recover()                  catches a panic from processor().
//  2. MarkDeadLetter + err wrap  persists the dead-letter row.
//  3. cancel()                   signals hbCtx.Done() to the goroutine.
//  4. wg.Wait()                  blocks until the goroutine exits.
//
// Without this consolidation, splitting the three into separate
// defers creates a deadlock — wg.Wait must run AFTER cancel or it
// can never return (the goroutine only exits on hbCtx.Done()), but
// LIFO forces the cancel defer (declared first) to run LAST.
//
// Panic safety: processFn can panic (third-party SDK bug, nil-deref
// in a model field, etc.). Without recover() the goroutine crash
// would propagate to the runtime and terminate the entire process —
// taking down BOTH pools (ingest + upload) and the reclaimer. The
// named-return + defer/recover catches every panic, logs it with
// stack trace, and routes the row to dead_letter (error_code =
// 'panic') so the operator-triage dashboard surfaces it instead of
// letting the row sit in 'leased' forever.
func (w *UploadWorker) runWithHeartbeat(
	ctx context.Context,
	job *models.UploadJob,
	workerID string,
	poolName string,
	processor processFn,
) (err error) {
	hbCtx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(w.opts.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := w.jobRepo.Heartbeat(hbCtx, job.ID, workerID, w.opts.LeaseTTL); err != nil {
					if errors.Is(err, repository.ErrUploadJobLeaseLost) {
						w.logger.Warn("upload worker: heartbeat lost lease", "job_id", job.ID, "pool", poolName)
						return
					}
					w.logger.Error("upload worker: heartbeat failed", "job_id", job.ID, "pool", poolName, "error", err)
				}
			}
		}
	}()

	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			w.logger.Error("upload worker: processFn PANIC; routing to MarkDeadLetter",
				"pool", poolName, "job_id", job.ID, "worker_id", workerID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(stack),
			)
			// Use a fresh context for the MarkDeadLetter call: the
			// parent ctx might be cancelled (graceful shutdown in
			// flight when the panic fired). Worst case is that the
			// mark fails to persist and the reaper recovers the row
			// once the lease expires.
			bgCtx, bgCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer bgCancel()
			if markErr := w.jobRepo.MarkDeadLetter(bgCtx, job.ID, workerID, "panic",
				fmt.Sprintf("processFn panicked for job %d: %v", job.ID, r)); markErr != nil {
				w.logger.Error("upload worker: MarkDeadLetter after panic failed",
					"pool", poolName, "job_id", job.ID, "error", markErr)
			}
			err = fmt.Errorf("processFn panicked for job %d: %v", job.ID, r)
		}
		// Cancel first to signal hbCtx.Done(), THEN wait for the
		// goroutine to exit. Inverted order = deadlock.
		cancel()
		wg.Wait()
	}()

	return processor(ctx, job, workerID)
}

// runYouTubeDeliveryPool is the GLOBAL delivery pool: it claims
// individual (video, channel) rows from youtube_target_publications
// and runs the per-delivery private upload. Bounded by
// w.opts.UploadConcurrency (YOUTUBE_UPLOAD_CONCURRENCY) — the same
// knob that bounds the upload pool — so the total number of
// concurrent videos.insert calls is capped fleet-wide, regardless of
// how many jobs/targets the upload pool materialized.
//
// The loop mirrors runPoolLoop (semaphore + exponential empty-queue
// backoff) but claims *models.YouTubeTargetPublication rows instead of
// *models.UploadJob: the queue unit here is ONE (video, channel)
// delivery, so a single upload_job with N YouTube targets fans out to
// N independent rows claimed concurrently by different pool workers.
func (w *UploadWorker) runYouTubeDeliveryPool(ctx context.Context) {
	if w.ytPubStore == nil {
		w.logger.Warn("upload worker: ytPubStore not wired — youtube delivery pool disabled")
		return
	}
	if w.deliveryPostStore == nil {
		w.logger.Warn("upload worker: delivery post store not wired — youtube delivery pool disabled")
		return
	}
	poolWorkerID := uniqueWorkerID("delivery")
	concurrency := w.opts.UploadConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	backoff := w.opts.EmptyQueueBackoffMin

	w.logger.Info("upload worker: youtube delivery pool started",
		"delivery_concurrency", concurrency, "worker_id", poolWorkerID)
	defer w.logger.Info("upload worker: youtube delivery pool stopped")

	for {
		if ctx.Err() != nil {
			return
		}
		claimed := w.runYouTubeDeliveryTick(ctx, sem, poolWorkerID)
		if claimed {
			backoff = w.opts.EmptyQueueBackoffMin
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > w.opts.EmptyQueueBackoffMax {
			backoff = w.opts.EmptyQueueBackoffMax
		}
	}
}

// runYouTubeDeliveryTick claims one batch of ready deliveries (up to
// the semaphore size) and spawns one goroutine per row. Each row gets
// its own heartbeat goroutine via runDeliveryWithHeartbeat; the
// processor itself owns the retry/dead-letter transitions
// (MarkDeliveryFailed) so a slow or failed channel never blocks the
// batch's other rows.
func (w *UploadWorker) runYouTubeDeliveryTick(
	ctx context.Context,
	sem chan struct{},
	poolWorkerID string,
) bool {
	deliveries, err := w.ytPubStore.ClaimReadyDeliveries(ctx, poolWorkerID, cap(sem), w.opts.LeaseTTL)
	if err != nil {
		w.logger.Error("upload worker: claim ready deliveries failed", "error", err)
		return false
	}
	if len(deliveries) == 0 {
		return false
	}
	w.logger.Info("upload worker: claimed youtube deliveries",
		"pool", "delivery", "count", len(deliveries), "worker_id", poolWorkerID)

	for _, delivery := range deliveries {
		select {
		case <-ctx.Done():
			return false
		case sem <- struct{}{}:
		}
		go func(d *models.YouTubeTargetPublication) {
			defer func() { <-sem }()
			if err := w.runDeliveryWithHeartbeat(ctx, d, poolWorkerID); err != nil {
				w.logger.Warn("upload worker: youtube delivery failed",
					"delivery_id", d.ID, "post_target_id", d.PostTargetID,
					"platform_account_id", d.PlatformAccountID, "error", err)
			}
		}(delivery)
	}
	return true
}

// runDeliveryWithHeartbeat wraps one claimed delivery in a heartbeat
// goroutine (ticking every opts.HeartbeatInterval) plus panic recovery,
// mirroring runWithHeartbeat's defer-ordering contract: recover first,
// then cancel, then wg.Wait — LIFO means cancel must be declared after
// the recover block to run before wg.Wait returns. If HeartbeatDelivery
// reports the lease lost (peer stole it via the reclaimer), the
// heartbeat goroutine exits and the processor's next state write fails
// its CAS — the peer owns the row.
func (w *UploadWorker) runDeliveryWithHeartbeat(
	ctx context.Context,
	delivery *models.YouTubeTargetPublication,
	workerID string,
) (err error) {
	hbCtx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(w.opts.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				ok, hErr := w.ytPubStore.HeartbeatDelivery(ctx, delivery.ID, workerID, w.opts.LeaseTTL)
				if hErr != nil {
					w.logger.Warn("upload worker: delivery heartbeat failed",
						"delivery_id", delivery.ID, "error", hErr)
					continue
				}
				if !ok {
					w.logger.Warn("upload worker: delivery lease lost", "delivery_id", delivery.ID)
					return
				}
			}
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("delivery processing panic for delivery %d: %v", delivery.ID, r)
			w.logger.Error("upload worker: delivery processing panic",
				"delivery_id", delivery.ID, "panic", r, "stack", string(debug.Stack()))
		}
		cancel()
		wg.Wait()
	}()
	return w.processYouTubeDelivery(ctx, delivery, workerID)
}

// runDeliveryReclaimerLoop ticks on opts.ReclaimInterval and returns
// delivery rows stuck in 'uploading' with an expired lease back to
// 'ready_to_upload' so the delivery pool re-claims them (idempotent:
// already-uploaded rows are skipped on the next claim).
func (w *UploadWorker) runDeliveryReclaimerLoop(ctx context.Context) {
	if w.ytPubStore == nil {
		return
	}
	ticker := time.NewTicker(w.opts.ReclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := w.ytPubStore.ReclaimExpiredDeliveryLeases(ctx, 100)
			if err != nil {
				w.logger.Error("upload worker: delivery reclaimer tick failed", "error", err)
				continue
			}
			if n > 0 {
				w.logger.Info("upload worker: delivery reclaimer recovered rows", "count", n)
			}
		}
	}
}
