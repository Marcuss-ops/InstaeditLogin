package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// UploadJobStore is the narrow repository interface the upload worker needs.
// P1 step 2 — ingest + upload pools:
//   - ClaimBatch          ingest pool claims status IN ('pending','retry_wait').
//   - ClaimBatchForPublish upload pool claims status = 'ready_to_publish' (the
//     ingest pool's MarkIngested output).
//   - MarkIngested         ingest pool's terminal-for-ingest: leased →
//     ready_to_publish + asset_id stamp + total_bytes/progress_bytes
//     set to the streamed size.
//   - ReclaimExpiredLeases reaper: returned leased rows past lease_expires_at
//     (5-min heartbeat grace window) back to 'pending'. Called both
//     synchronously on startup (ReclaimOnStart) and on a background
//     ticker cadence.
type UploadJobStore interface {
	ClaimBatch(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error)
	ClaimBatchForPublish(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error)
	Heartbeat(ctx context.Context, jobID int64, workerID string, lease time.Duration) error
	MarkCompleted(ctx context.Context, id int64, workerID string, postID int64, assetID string) error
	MarkFailed(ctx context.Context, id int64, workerID, errorCode, errMessage string) error
	MarkRetry(ctx context.Context, id int64, workerID, errorCode, errMessage string, nextAttemptAt time.Time) error
	MarkDeadLetter(ctx context.Context, id int64, workerID, errorCode, errMessage string) error
	MarkIngested(ctx context.Context, id int64, workerID, assetID string, totalBytes int64) error
	ReclaimExpiredLeases(ctx context.Context, maxRows int) (int64, error)
	// P1#5 — YouTube resumable session persistence. Called per-chunk
	// (Save) and once at terminal-success / session-expired (Clear).
	SaveYouTubeSession(ctx context.Context, id int64, workerID, sessionURI string, offset, chunkSize int64, expiresAt time.Time) error
	ClearYouTubeSession(ctx context.Context, id int64, workerID string) error
}

// UploadMediaStore is the narrow media asset repository interface.
type UploadMediaStore interface {
	Create(asset *models.MediaAsset) error
	MarkReady(id, sha256 string, sizeBytes int64, contentType string) error
	MarkFailed(id, reason string) error
	// MarkFailedWithReason: same as pkg/api MediaStore — caller passes
	// `cause` so the persist failure path emits a structured log
	// line. Replaces the historical `_ = store.MarkFailed(id, err.Error())`
	// pattern that silently lost errors on the failure-of-the-failure.
	MarkFailedWithReason(id, reason string, cause error) error
}

// UploadPostStore is the narrow post repository interface.
type UploadPostStore interface {
	Create(post *models.Post, targets []*models.PostTarget) error
	PublishPost(postID int64) error
}

// UploadUserStore resolves platform accounts for target validation.
type UploadUserStore interface {
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
}

// UploadWorkerOptions configures the worker pool sizing + cadence.
// All fields are zero-value safe; defaults are applied in Run() so
// NewUploadWorker never panics on a half-initialised options struct.
type UploadWorkerOptions struct {
	// IngestConcurrency caps the per-tick concurrent goroutines
	// the ingest pool can run (Drive → S3 streaming). The valutazione
	// doc recommends 2–3 on a dev box; default 3.
	IngestConcurrency int
	// UploadConcurrency caps the per-tick concurrent goroutines
	// the upload pool can run (videos.insert per-channel). The
	// valutazione doc recommends 3–4 on a dev box; default 4.
	UploadConcurrency int
	// LeaseTTL is the lifetime of a claim before ReclaimExpiredLeases
	// recovers it. Heartbeat must run at leaseTTL/3 so the lease
	// is renewed twice before expiry. Default 60s.
	LeaseTTL time.Duration
	// HeartbeatInterval is the cadence of the per-claimed-row
	// heartbeat goroutine. Default LeaseTTL/3 (e.g. 20s for a 60s
	// lease); three renewals before expiry is the safety margin.
	HeartbeatInterval time.Duration
	// ReclaimInterval is the cadence of the background
	// ReclaimExpiredLeases ticker (separate goroutine from the
	// per-row heartbeats). Default 30s.
	ReclaimInterval time.Duration
	// ReclaimOnStart, when true, runs ReclaimExpiredLeases
	// synchronously BEFORE the first tick of the pools so workers
	// don't race against any leases left over by a previous
	// crash. Default true.
	ReclaimOnStart bool
}

// UploadWorker processes upload_jobs in the background. It downloads
// videos from public or authenticated Google Drive, uploads them to S3,
// creates posts + targets, and triggers publishing. Jobs survive server
// restarts because they are persisted in the upload_jobs table.
//
// P1 step 2 — the worker is split into an ingest pool (Drive → S3)
// and an upload pool (S3 → posts → YouTube videos.insert). Both
// pools share the lease + heartbeat machinery added in P1 step 1
// (commit 4888c40). Per-claimed-row heartbeat goroutines keep the
// lease alive during the long streaming phases.
type UploadWorker struct {
	jobRepo          UploadJobStore
	mediaStore       UploadMediaStore
	postStore        UploadPostStore
	userRepo         UploadUserStore
	storage          services.StorageProvider
	capRouter        *services.CapabilityRouter
	vault            credentials.VaultAPI
	sourceRegistry   *ArtifactSourceRegistry
	deliveryVerifier ExternalDeliveryVerifier
	interval         time.Duration
	logger           *slog.Logger
	uploadTimeout    time.Duration
	opts             UploadWorkerOptions
}

// NewUploadWorker wires a new UploadWorker. opts fields default in
// Run() when zero; the bootstrap should pass an explicit options
// struct built from cfg so the operator-facing env vars take effect.
func NewUploadWorker(
	jobRepo UploadJobStore,
	mediaStore UploadMediaStore,
	postStore UploadPostStore,
	userStore UploadUserStore,
	storage services.StorageProvider,
	capRouter *services.CapabilityRouter,
	vault credentials.VaultAPI,
	sourceRegistry *ArtifactSourceRegistry,
	deliveryVerifier ExternalDeliveryVerifier,
	interval time.Duration,
	logger *slog.Logger,
	opts UploadWorkerOptions,
) *UploadWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UploadWorker{
		jobRepo:          jobRepo,
		mediaStore:       mediaStore,
		postStore:        postStore,
		userRepo:         userStore,
		storage:          storage,
		capRouter:        capRouter,
		vault:            vault,
		sourceRegistry:   sourceRegistry,
		deliveryVerifier: deliveryVerifier,
		interval:         interval,
		logger:           logger,
		uploadTimeout:    30 * time.Minute,
		opts:             opts,
	}
}

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

	// Run once immediately so we don't wait `interval` on the
	// first tick after startup.
	w.runPoolTick(ctx, poolName, sem, claimer, processor, poolWorkerID)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runPoolTick(ctx, poolName, sem, claimer, processor, poolWorkerID)
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
) {
	jobs, err := claimer(ctx, cap(sem), w.opts.LeaseTTL)
	if err != nil {
		w.logger.Error("upload worker: claim batch failed", "pool", poolName, "error", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	w.logger.Info("upload worker: claimed batch", "pool", poolName, "count", len(jobs), "worker_id", poolWorkerID)

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
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

// handleProcessingError classifies the error and routes MarkRetry
// vs MarkDeadLetter based on attempt_count vs max_attempts.
// ErrUploadJobLeaseLost is treated as "drop silently" (peer owns
// the row).
func (w *UploadWorker) handleProcessingError(
	ctx context.Context,
	poolName string,
	workerID string,
	job *models.UploadJob,
	processErr error,
) {
	if errors.Is(processErr, repository.ErrUploadJobLeaseLost) {
		w.logger.Warn("upload worker: lease lost mid-processing; dropping",
			"pool", poolName, "job_id", job.ID, "worker_id", workerID)
		return
	}

	w.logger.Error("upload worker: job failed",
		"pool", poolName, "job_id", job.ID,
		"attempt_count", job.AttemptCount, "max_attempts", job.MaxAttempts,
		"error", processErr,
	)

	errorCode := classifyUploadError(processErr)
	// Task 5/10 — permanent-error fast-path. Drive files with
	// capabilities.canDownload=false (and SHA / size / MIME mismatch
	// failures from artifact_verify) wrap PermanentError via errors.Join
	// upstream so the canDownload false case matches the same sentinel.
	// Short-circuit to MarkDeadLetter WITHOUT consuming the retry
	// budget — a non-downloadable file will not become downloadable on
	// retry; burning attempt_count for ~5 min × 8 attempts (max_attempts
	// envelope) before dead-letter triggers anyway is purely wasted
	// wall-clock + DB log noise. Routed BEFORE the attempt-count gate
	// so a single canDownload=false rejection lands the row in
	// 'dead_letter' (= 'perm_error' per the docs/OPERATIONS.md
	// runbook) on the very first failed tick.
	if errors.Is(processErr, ErrPermanent) {
		if markErr := w.jobRepo.MarkDeadLetter(ctx, job.ID, workerID, errorCode, processErr.Error()); markErr != nil {
			w.logger.Error("upload worker: MarkDeadLetter (permanent) failed",
				"pool", poolName, "job_id", job.ID, "error", markErr)
		}
		return
	}
	if job.AttemptCount >= job.MaxAttempts {
		if markErr := w.jobRepo.MarkDeadLetter(ctx, job.ID, workerID, errorCode, processErr.Error()); markErr != nil {
			w.logger.Error("upload worker: MarkDeadLetter failed",
				"pool", poolName, "job_id", job.ID, "error", markErr)
		}
		return
	}

	backoff := computeUploadBackoff(job.AttemptCount)
	if markErr := w.jobRepo.MarkRetry(ctx, job.ID, workerID, errorCode, processErr.Error(), time.Now().Add(backoff)); markErr != nil {
		w.logger.Error("upload worker: MarkRetry failed",
			"pool", poolName, "job_id", job.ID, "error", markErr)
	}
}

// processIngestJob handles the per-source ingest path. On success
// transitions the row to ready_to_publish via MarkIngested so the
// upload pool can claim it next.
//
// Phase 1 (registry refactor): the legacy switch over source_type is
// REPLACED by `sourceRegistry.Resolve(job.SourceType)`. The worker is
// generic — every per-source concern (OAuth refresh for Drive, signed
// URL GET for Velox, deprecation for PublicDrive) lives in the
// corresponding ArtifactSource implementation invoked here via the
// registry key.
//
// Worker-layer invariants (force-fail BEFORE storage.Upload):
//   - job.SourceType must be registered (else "unsupported source type")
//   - Inspect pre-flight surfaces size + mime used to size the asset + S3 PUT
//   - Open returns an io.ReadCloser that the worker drains through S3
//   - The downstream storage.Upload path is unchanged from the prior
//     revision; the only thing that moved is the bytestream source.
func (w *UploadWorker) processIngestJob(ctx context.Context, job *models.UploadJob, workerID string) error {
	// (1) Resolve the source via the registry. ok=false means the
	// worker doesn't recognise this SourceType — caller bug if we
	// ever see one (an upload_job's SourceType comes from the producer
	// and matches an enum value the worker must have a source for).
	src, ok := w.sourceRegistry.Resolve(job.SourceType)
	if !ok {
		return fmt.Errorf("unsupported source type: %s", job.SourceType)
	}

	// (2) Optional Inspect for pre-flight metadata. Most sources
	// implement it (Velox HEAD, Drive GetFileMetadata); the deprecated
	// PublicDrive source returns the actionable error verbatim. The
	// worker treats Inspect as best-effort: tolerate ErrInspectNotImplemented
	// as a soft no-op (no metadata means Open is the only source of
	// truth for ingest invariants).
	//
	// `md` is lifted to outer scope (Task 4/10) so the build-policy
	// block below can use SHA256Hex (Drive's sha256Checksum) when
	// RequireSHA is gated on the surface-declared value.
	var sizeBytes int64
	var contentType string
	var md *SourceMetadata
	if inspectMd, inspectErr := src.Inspect(ctx, job); inspectErr == nil && inspectMd != nil {
		md = inspectMd
		sizeBytes = md.SizeBytes
		contentType = md.MimeType
	} else if inspectErr != nil && !errors.Is(inspectErr, ErrInspectNotImplemented) {
		// PublicDrive's deprecation error (or any non-soft-Inspect
		// error from another source) bubbles up so the operator sees
		// the same guidance regardless of which entry point surfaced
		// the rejection.
		return fmt.Errorf("inspect source: %w", inspectErr)
	}

	// (3) Open the byte stream. The worker drains this through S3;
	// per-source OAuth refresh / signed URL GET / deprecation gates
	// live inside the source.
	srcBody, err := src.Open(ctx, job)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcBody.Close()

	if sizeBytes <= 0 {
		return fmt.Errorf("source returned unknown or zero size for job %d; cannot import", job.ID)
	}

	// (3.5) GENERIC ArtifactVerificationPolicy AT THE WORKER LAYER
	// (Task 4/10). The prior Velox-only VeloxVerifyReader is replaced
	// by the unified artifactVerifyReader used by BOTH Velox and
	// Drive source paths. The policy is built per-source:
	//   * Velox: canonical ExpectedSize + ExpectedSHA256 from the
	//     external_deliveries row (via deliveryVerifier); RequireSHA=true
	//     unless the row is missing/legacy (skip-or-best-effort path).
	//   * Drive: ExpectedSize + ExpectedMIME from Inspect; ExpectedSHA256
	//     from sha256Checksum when present, RequireSHA accordingly.
	// "Drive verification is a follow-up" is no longer true as of
	// Task 4/10 — Drive with declared sha256Checksum now feeds the
	// policy and a mismatch causes MarkFailed + the post never
	// publishes.
	policy := models.ArtifactVerificationPolicy{
		ExpectedSize: sizeBytes,
		ExpectedMIME: contentType,
	}
	switch job.SourceType {
	case models.UploadJobSourceVeloxArtifact:
		if w.deliveryVerifier != nil {
			expSize, expSHA, vErr := w.deliveryVerifier.GetExpectedTripleByUploadJobID(ctx, job.ID)
			switch {
			case vErr == nil && expSize > 0:
				// Prefer the canonical external_deliveries row over
				// Inspect's HEAD — they're the producer's authoritative
				// triple; Inspect is the network probe (best-effort).
				policy.ExpectedSize = expSize
				policy.ExpectedSHA256 = expSHA
				policy.RequireSHA = true
			case IsDeliveryVerificationSkipErr(vErr):
				// peek-ordering race / legacy row — best-effort no-op
			default:
				return fmt.Errorf("velox: load expected triple: %w", vErr)
			}
		}
	case models.UploadJobSourceAuthenticatedDrive:
		if md != nil {
			policy.ExpectedSHA256 = md.SHA256Hex
			policy.RequireSHA = md.SHA256Hex != ""
		}
	default:
		// best-effort no-op for unmapped / future sources
	}
	verifyReader, err := NewArtifactVerifyReader(srcBody, policy)
	if err != nil {
		return fmt.Errorf("wrap body for verification: %w", err)
	}
	srcBody = verifyReader // S3 PUT now reads via the verify wrapper

	// Build S3 key and create pending media asset.
	key := services.BuildUploadKey(job.UserID, job.SourceID)
	asset := &models.MediaAsset{
		UserID:      job.UserID,
		UploadKey:   key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		Status:      models.MediaAssetStatusPending,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	}
	if err := w.mediaStore.Create(asset); err != nil {
		return fmt.Errorf("create media asset: %w", err)
	}

	// Sign S3 PUT and stream.
	grant, err := w.storage.SignUpload(ctx, job.UserID, key, contentType, sizeBytes, 15*time.Minute)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("sign s3 upload: %w", err)
	}

	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.UploadURL, srcBody)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("build s3 upload request: %w", err)
	}
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.ContentLength = sizeBytes

	s3Client := &http.Client{Timeout: w.uploadTimeout}
	uploadResp, err := s3Client.Do(uploadReq)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("upload to s3: %w", err)
	}
	uploadResp.Body.Close()

	// POST-stream artifact verification (Task 4/10). MUST run AFTER
	// s3Client.Do has fully drained srcBody + BEFORE MarkReady /
	// MarkIngested so a SHA or size mismatch fails loud before the
	// row transitions to ready_to_publish. Both Velox and Drive
	// paths share this single gate. The defer srcBody.Close() above
	// covers verifyReader.Close() since `srcBody = verifyReader`.
	if vErr := verifyReader.Verify(); vErr != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, vErr.Error(), vErr)
		return fmt.Errorf("artifact verification: %w", vErr)
	}
	if uploadResp.StatusCode >= 300 {
		reason := fmt.Sprintf("s3 upload returned %d", uploadResp.StatusCode)
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, reason, errors.New(reason))
		return fmt.Errorf("%s", reason)
	}

	// Verify upload.
	verifiedContentType, verifiedSize, err := w.storage.VerifyUpload(ctx, key)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("verify s3 upload: %w", err)
	}
	// Boundary MIME check: S3-reported content_type must match the
	// policy's ExpectedMIME (typically the upstream-declared mime).
	// A mismatch means the upstream lied about the bytes — fail loud
	// instead of marking the asset ready so the operator-triage
	// dashboard can surface the upstream-side regression.
	if policy.ExpectedMIME != "" && verifiedContentType != policy.ExpectedMIME {
		reason := fmt.Sprintf("mime mismatch (expected %q, S3 returned %q)", policy.ExpectedMIME, verifiedContentType)
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, reason, errors.New(reason))
		return fmt.Errorf("%s", reason)
	}
	// MarkReady now receives the LOCALLY-COMPUTED SHA — always,
	// even when RequireSHA=false — so media_assets.sha256 stores the
	// authoritative hash for downstream re-verification. The repo
	// already handles "COALESCE(NULLIF($2, ''), sha256)" so a
	// non-empty local SHA always overwrites the existing row's
	// empty sha256 with the truth source.
	if err := w.mediaStore.MarkReady(asset.ID, verifyReader.ActualSHA256Hex(), verifiedSize, verifiedContentType); err != nil {
		return fmt.Errorf("mark media asset ready: %w", err)
	}

	// P2 — ops dashboard throughput counter. Increment BEFORE
	// the MarkIngested CAS so a worker crash between the
	// successful S3 verify and the DB stamp doesn't double-count
	// the bytes on retry. The "ingest phase" gate is implicit:
	// the upload worker only reaches this point on the ingest
	// pool's hot path, never on publish.
	if verifiedSize > 0 {
		metrics.RecordUploadBytes(models.PlatformYouTube, "ingest", verifiedSize)
	}

	// Transition the row: leased → ready_to_publish + asset_id +
	// total_bytes/progress_bytes (CAS against workerID that
	// ClaimBatch stamped on the row).
	if err := w.jobRepo.MarkIngested(ctx, job.ID, workerID, asset.ID, verifiedSize); err != nil {
		return fmt.Errorf("mark ingested: %w", err)
	}

	w.logger.Info("upload worker: ingest done",
		"pool", "ingest", "job_id", job.ID, "asset_id", asset.ID, "size", verifiedSize)
	return nil
}

// processPublishJob handles the S3 → post → YouTube publish path.
// Assumes the row is in 'ready_to_publish' state with asset_id set.
func (w *UploadWorker) processPublishJob(ctx context.Context, job *models.UploadJob, workerID string) error {
	if job.AssetID == nil || *job.AssetID == "" {
		return fmt.Errorf("publish job %d missing asset_id; ingest did not complete", job.ID)
	}
	assetID := *job.AssetID

	key := services.BuildUploadKey(job.UserID, job.SourceID)
	mediaURL := w.storage.AssetURL(key)

	post := &models.Post{
		WorkspaceID: job.WorkspaceID,
		Title:       job.Title,
		Caption:     job.Caption,
		MediaURL:    mediaURL,
		Status:      models.PostStatusQueued,
		// P1#4 — IngestAfter is server-side DEFAULT NOW() at SQL
		// level; we pass job.IngestAfter through so a queued
		// ingest-after-future row preserves its ingest schedule.
		IngestAfter: job.IngestAfter,
		// PublishAt stamps the user-facing "what time should this
		// fire" cursor onto the created post. The publish_worker
		// ListPending predicate (queries.go::qSelectPendingTargets)
		// gates on publish_at <= NOW(), so the post stays queued
		// until the cursor elapses.
		PublishAt: job.PublishAt,
		// P1 (migration 053) — propagate the inherited batch default
		// onto the post. The publish_worker uses this as the middle
		// term of the precedence cascade:
		//   payload override (post.PrivacyLevel) > post.DefaultPrivacyLevel
		//   > "unlisted" (YouTube fallback) > PUBLIC_TO_EVERYONE (other platforms)
		// post.PrivacyLevel is left empty by this flow — the operator
		// sets it explicitly via the post-update endpoint when they want a
		// per-post override.
		DefaultPrivacyLevel: job.DefaultPrivacyLevel,
	}
	targets := make([]*models.PostTarget, 0, len(job.Targets))
	for _, accountID := range job.Targets {
		targets = append(targets, &models.PostTarget{
			PlatformAccountID: accountID,
			Status:            models.PostStatusQueued,
		})
	}
	if err := w.postStore.Create(post, targets); err != nil {
		return fmt.Errorf("create post: %w", err)
	}

	// Trigger publishing only for jobs that should publish NOW.
	// Future-scheduled jobs (job.PublishAt > now) stay in the
	// `status='queued'` state and the publish_worker picks them up
	// when publish_at <= now(). Calling PublishPost on a future post
	// would race the scheduler and risk an out-of-order publish.
	//
	// P1#4 — defense-in-depth keep this go-level gate: ingest and
	// publish pools are separate goroutines; the publish pool's
	// ClaimBatchForPublish CTE also gates on (publish_at IS NULL OR
	// publish_at <= NOW()) so under normal conditions a row claimed
	// here already has publish_at <= now. The go-level check stays
	// for legacy single-file flows (POST /posts direct + cmd
	// binaries) where rows bypass the upload_jobs batching path and
	// the publish pool's CTE has no claim opportunity. A future
	// Taskilino can remove this check once every flow routes through
	// ClaimBatchForPublish.
	if job.PublishAt == nil || !job.PublishAt.After(time.Now()) {
		if err := w.postStore.PublishPost(post.ID); err != nil {
			return fmt.Errorf("trigger publish: %w", err)
		}
	} else {
		w.logger.Info("upload worker: post scheduled for future publish",
			"job_id", job.ID, "post_id", post.ID, "publish_at", job.PublishAt.Format(time.RFC3339))
	}

	// P2 — publish-phase throughput counter. Increment on the
	// hot path after the post + targets are persisted but BEFORE
	// the MarkCompleted CAS so a worker crash between persist
	// and the CAS double-counts on retry (acceptable: the
	// operator's "throughput" is a 5-minute rate over a counter,
	// not a strict sum, so a one-byte overcount per failed
	// transition is invisible at the dashboard).
	if assetID != "" && job.TotalBytes != nil && *job.TotalBytes > 0 {
		metrics.RecordUploadBytes(models.PlatformYouTube, "publish", *job.TotalBytes)
	}

	// Mark job completed. CAS against workerID ensures a peer that
	// stole the lease (reaper release + peer's ClaimBatch
	// re-claim) cannot overwrite a peer's terminal write.
	if err := w.jobRepo.MarkCompleted(ctx, job.ID, workerID, post.ID, assetID); err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}

	w.logger.Info("upload worker: publish done",
		"pool", "upload", "job_id", job.ID, "post_id", post.ID, "asset_id", assetID)
	return nil
}

// classifyUploadError maps a process-time error onto a stable taxonomy
// used by error_code (migration 046) for dashboard filtering and retry
// routing. Empty string means "unclassified" — the repository will
// store NULL via NULLIF.
func classifyUploadError(err error) string {
	s := err.Error()
	switch {
	case containsAny(s, "drive", "googleapis.com/upload/drive"):
		return "drive_error"
	case containsAny(s, "s3", "tigris", "minio", "presigned"):
		return "s3_error"
	case containsAny(s, "youtube", "videos.insert"):
		return "youtube_error"
	case containsAny(s, "oauth", "401", "403", "unauthorized"):
		return "auth_error"
	case containsAny(s, "context deadline", "timeout"):
		return "timeout"
	default:
		return ""
	}
}

// containsAny is the cheap substring-or helper for classifyUploadError.
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		for i := 0; i+len(n) <= len(s); i++ {
			if s[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}

// computeUploadBackoff implements a deterministic decorrelated-jitter
// curve for the upload worker. AWS-style: temp = min(cap, prev * 3),
// sleep = base + (temp - base) / 2. Capped at 1h. Production polish
// in a follow-up commit replaces this with math/rand-based uniform
// sampling (mirroring internal/outbox/dispatcher.go::computeBackoff).
func computeUploadBackoff(attempt int) time.Duration {
	const (
		base = 5 * time.Second
		cap  = 1 * time.Hour
	)
	if attempt < 1 {
		attempt = 1
	}
	prev := base
	for i := 1; i < attempt; i++ {
		prev *= 3
		if prev > cap {
			prev = cap
			break
		}
	}
	temp := prev
	if temp > cap {
		temp = cap
	}
	jitter := time.Duration(int64(temp) - int64(base))
	if jitter < 0 {
		jitter = 0
	}
	return base + jitter/2
}
