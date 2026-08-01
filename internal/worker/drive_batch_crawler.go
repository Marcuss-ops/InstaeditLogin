package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// CrawlerBatchStore is the narrow repository interface the drive-batch
// crawler (P1#7) needs against the import_batches header table. It is
// INTENTIONALLY a separate interface from pkg/api.ImportBatchStore —
// the producer-side handler only needs Create + FindByID; the crawler-
// side worker needs the lease/CAS machinery as well. Splitting them
// keeps the producer's surface tiny (read-only + create) and the
// crawler's surface focused on terminal transitions + cursor
// checkpoints.
type CrawlerBatchStore interface {
	ClaimNextBatch(ctx context.Context, workerID string, lease time.Duration) (*models.ImportBatch, error)
	Heartbeat(ctx context.Context, id uuid.UUID, workerID string, lease time.Duration) error
	UpdateCursor(ctx context.Context, id uuid.UUID, workerID, pageToken string, cursorIndexedCount int) error
	IncrementCreatedCount(ctx context.Context, id uuid.UUID, workerID string, delta int) error
	MarkCompleted(ctx context.Context, id uuid.UUID, workerID string) error
	MarkFailed(ctx context.Context, id uuid.UUID, workerID, errorMessage string) error
	FindByID(id uuid.UUID) (*models.ImportBatch, error)
	ReclaimExpiredBatches(ctx context.Context, maxRows int) (int64, error)
}

// CrawlerUploadJobStore is the narrow repository interface the
// crawler needs to create one upload_job per Drive file. Only the
// Create word is used — Mark* / Claim* flows are owned by the
// existing upload_worker, which runs AFTER the crawler has fanned
// out the rows.
type CrawlerUploadJobStore interface {
	Create(job *models.UploadJob) error
}

// DriveBatchCrawlerOptions configures the crawler pool sizing +
// cadence. All fields are zero-value safe; defaults are applied in
// Run() so NewDriveBatchCrawler never panics on a half-initialised
// options struct.
type DriveBatchCrawlerOptions struct {
	// ClaimInterval is the cadence at which the crawler polls
	// import_batches for queued rows. Default 5s (Drive pagination
	// rounds are seconds-to-minutes, so sub-second ticks are noise).
	ClaimInterval time.Duration
	// LeaseTTL is the lifetime of a batch claim before
	// ReclaimExpiredBatches recovers it. Heartbeat must run at
	// leaseTTL/3 so the lease is renewed twice before expiry.
	// Default 5 minutes.
	LeaseTTL time.Duration
	// HeartbeatInterval is the cadence of the per-claimed-row
	// heartbeat; the crawler also checkpoints cursor_page_token
	// per page, which doubles as a stale-lease warning. Default
	// leaseTTL/3 = ~100s.
	HeartbeatInterval time.Duration
	// ReclaimInterval is the cadence of the background
	// ReclaimExpiredBatches ticker. Default 30s.
	ReclaimInterval time.Duration
	// ReclaimOnStart, when true, runs ReclaimExpiredBatches
	// synchronously BEFORE the first tick of the pool so the
	// crawler doesn't race against stale leases from a previous
	// crash. Default true.
	ReclaimOnStart bool
	// PublishHorizonDays (Blocco #3 P0) caps the EXACT projected
	// publish cursor at fan-out time. The producer-side heuristic
	// (handleDriveBatchImportV2) projects a worst-case 10_000-file
	// range BEFORE listing Drive; the crawler uses the actual file
	// count to reject batches whose projected cursor lands past
	// now + PublishHorizonDays. Default 30 = env PUBLISH_HORIZON_DAYS.
	// Zero / negative → the check is skipped (matches the producer's
	// "no cap" silent-truncation pre-Blocco #2 behaviour).
	PublishHorizonDays int
}

// DriveBatchCrawler is the P1#7 background consumer that drains
// import_batches rows. Each tick:
//  1. ClaimNextBatch (single-row contract; a crawler owns one
//     batch at a time because cross-page Drive pagination is the
//     long-running work — N concurrent batches would let one
//     batch starve the others).
//  2. For each page of source files: ListFolder, then loop over
//     the entries, writing one upload_job per file with the
//     batch_id FK stamped + stagger publish_at across the
//     schedule envelope (random uniform [min_gap,max_gap]).
//  3. After every page: UpdateCursor(cursor_page_token) so a
//     crash mid-batch resumes from the LAST produced page.
//  4. When Drive's nextPageToken is empty: MarkCompleted.
//
// Per the thinker's D5.b+cursor recommendation, the cursor pattern
// is per-page (NOT per-file) so crashed-restart does not double-
// write upload_jobs for any paginated range. The crawl also calls
// IncrementCreatedCount(delta) per file so the dashboard's
// "by-batch" gauge is live (no JOIN required).
type DriveBatchCrawler struct {
	batchRepo    CrawlerBatchStore
	uploadRepo   CrawlerUploadJobStore
	vault        credentials.VaultAPI
	capRouter    *services.CapabilityRouter
	workerPrefix string
	opts         DriveBatchCrawlerOptions
	logger       *slog.Logger
}

// NewDriveBatchCrawler wires a new crawler. opts fields default in
// Run() when zero; the bootstrap should pass an explicit options
// struct built from cfg so the operator-facing env vars take effect.
func NewDriveBatchCrawler(
	batchRepo CrawlerBatchStore,
	uploadRepo CrawlerUploadJobStore,
	vault credentials.VaultAPI,
	capRouter *services.CapabilityRouter,
	workerPrefix string,
	opts DriveBatchCrawlerOptions,
	logger *slog.Logger,
) *DriveBatchCrawler {
	if workerPrefix == "" {
		workerPrefix = "drive-batch-crawler"
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DriveBatchCrawler{
		batchRepo:    batchRepo,
		uploadRepo:   uploadRepo,
		vault:        vault,
		capRouter:    capRouter,
		workerPrefix: workerPrefix,
		opts:         opts,
		logger:       logger,
	}
}

func (c *DriveBatchCrawler) applyDefaults() {
	if c.opts.ClaimInterval <= 0 {
		c.opts.ClaimInterval = 5 * time.Second
	}
	if c.opts.LeaseTTL <= 0 {
		c.opts.LeaseTTL = 5 * time.Minute
	}
	if c.opts.HeartbeatInterval <= 0 {
		c.opts.HeartbeatInterval = c.opts.LeaseTTL / 3
	}
	if c.opts.ReclaimInterval <= 0 {
		c.opts.ReclaimInterval = 30 * time.Second
	}
	// Blocco #3 P0 — PublishHorizonDays defaults to the same 30
	// used by the HTTP layer's r.publishHorizonDays(). Zero would
	// skip the D6 exact horizon re-stamp (matching the legacy
	// silent-truncation behaviour pre-Blocco #2); tests / fixtures
	// that want to disable the check pass 0 explicitly.
	if c.opts.PublishHorizonDays < 0 {
		c.opts.PublishHorizonDays = 0
	} else if c.opts.PublishHorizonDays == 0 {
		c.opts.PublishHorizonDays = 30
	}
}

// publishHorizonDays returns the configured horizon with a safe
// fallback (matches the HTTP-layer r.publishHorizonDays() helper in
// pkg/api/limits.go so the worker and the API enforce identical caps).
func (c *DriveBatchCrawler) publishHorizonDays() int {
	if c.opts.PublishHorizonDays <= 0 {
		return 30
	}
	return c.opts.PublishHorizonDays
}

// Run orchestrates the crawler goroutines:
//  1. Apply lazy defaults on opts.
//  2. Synchronously reclaim stuck leases on startup.
//  3. Spawn the reclaimer ticker.
//  4. Spawn the claimer loop (single-row contract).
//  5. Block on ctx.Done() + waitGroup.Wait() for graceful shutdown.
//  6. The per-batch processing happens inline (one batch at a time).
func (c *DriveBatchCrawler) Run(ctx context.Context) error {
	c.applyDefaults()

	c.logger.Info("drive batch crawler started",
		"claim_interval_seconds", c.opts.ClaimInterval.Seconds(),
		"lease_ttl_seconds", c.opts.LeaseTTL.Seconds(),
		"heartbeat_interval_seconds", c.opts.HeartbeatInterval.Seconds(),
		"reclaim_interval_seconds", c.opts.ReclaimInterval.Seconds(),
		"reclaim_on_start", c.opts.ReclaimOnStart,
	)
	defer c.logger.Info("drive batch crawler stopped")

	if c.opts.ReclaimOnStart {
		n, err := c.batchRepo.ReclaimExpiredBatches(ctx, 10000)
		if err != nil {
			c.logger.Error("drive batch crawler: startup reclaim failed", "error", err)
		} else if n > 0 {
			c.logger.Info("drive batch crawler: startup reclaim recovered batches", "count", n)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.runReclaimerLoop(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		c.runClaimLoop(ctx)
	}()

	wg.Wait()
	return ctx.Err()
}

func (c *DriveBatchCrawler) runReclaimerLoop(ctx context.Context) {
	ticker := time.NewTicker(c.opts.ReclaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := c.batchRepo.ReclaimExpiredBatches(ctx, 50)
			if err != nil {
				c.logger.Error("drive batch crawler: reclaimer tick failed", "error", err)
			} else if n > 0 {
				c.logger.Info("drive batch crawler: reclaimer recovered batches", "count", n)
			}
		}
	}
}

func (c *DriveBatchCrawler) runClaimLoop(ctx context.Context) {
	workerID := uniqueWorkerID(c.workerPrefix)
	c.logger.Info("drive batch crawler: claimer loop running", "worker_id", workerID)
	c.runClaimTick(ctx, workerID)
	ticker := time.NewTicker(c.opts.ClaimInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runClaimTick(ctx, workerID)
		}
	}
}

// runClaimTick processes AT MOST one batch per tick (single-row
// contract). If processing takes longer than a tick, the next tick
// gracefully no-ops (no claim available) until processing returns.
func (c *DriveBatchCrawler) runClaimTick(ctx context.Context, workerID string) {
	batch, err := c.batchRepo.ClaimNextBatch(ctx, workerID, c.opts.LeaseTTL)
	if err != nil {
		if errors.Is(err, repository.ErrImportBatchLeaseLost) {
			return
		}
		c.logger.Error("drive batch crawler: claim failed", "worker_id", workerID, "error", err)
		return
	}
	if batch == nil {
		return
	}
	c.logger.Info("drive batch crawler: claimed batch",
		"batch_id", batch.ID, "user_id", batch.UserID, "workspace_id", batch.WorkspaceID,
		"source_provider", batch.SourceProvider, "source_folder_id", batch.SourceFolderID,
		"target_count", len(batch.TargetAccountIDs),
	)
	c.processBatch(ctx, batch, workerID)
}
