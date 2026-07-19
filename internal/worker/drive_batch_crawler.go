package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
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
}

// DriveBatchCrawler is the P1#7 background consumer that drains
// import_batches rows. Each tick:
//   1. ClaimNextBatch (single-row contract; a crawler owns one
//      batch at a time because cross-page Drive pagination is the
//      long-running work — N concurrent batches would let one
//      batch starve the others).
//   2. For each page of source files: ListFolder, then loop over
//      the entries, writing one upload_job per file with the
//      batch_id FK stamped + stagger publish_at across the
//      schedule envelope (random uniform [min_gap,max_gap]).
//   3. After every page: UpdateCursor(cursor_page_token) so a
//      crash mid-batch resumes from the LAST produced page.
//   4. When Drive's nextPageToken is empty: MarkCompleted.
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
}

// Run orchestrates the crawler goroutines:
//   1. Apply lazy defaults on opts.
//   2. Synchronously reclaim stuck leases on startup.
//   3. Spawn the reclaimer ticker.
//   4. Spawn the claimer loop (single-row contract).
//   5. Block on ctx.Done() + waitGroup.Wait() for graceful shutdown.
//   6. The per-batch processing happens inline (one batch at a time).
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

// processBatch runs the per-batch fold: paginate source, fan out
// upload_jobs, checkpoint per page, mark terminal.
//
// Heartbeat is spawned in a per-batch goroutine with its own
// context so the per-page work's slower-than-leaseTTL cost never
// loses the row to the reaper.
func (c *DriveBatchCrawler) processBatch(ctx context.Context, batch *models.ImportBatch, workerID string) {
	hbCtx, cancelHB := context.WithCancel(context.Background())
	var hbWG sync.WaitGroup

	hbWG.Add(1)
	go func() {
		defer hbWG.Done()
		ticker := time.NewTicker(c.opts.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := c.batchRepo.Heartbeat(hbCtx, batch.ID, workerID, c.opts.LeaseTTL); err != nil {
					if errors.Is(err, repository.ErrImportBatchLeaseLost) {
						c.logger.Warn("drive batch crawler: heartbeat lost lease", "batch_id", batch.ID)
						return
					}
					c.logger.Error("drive batch crawler: heartbeat failed", "batch_id", batch.ID, "error", err)
				}
			}
		}
	}()

	// Defer teardown in the right order: stop the heartbeat first
	// (otherwise wg.Wait blocks forever waiting on a tick we
	// already abandoned), then mark terminal.
	var (
		terminalErr error
		completed   bool
	)
	defer func() {
		cancelHB()
		hbWG.Wait()
		if completed {
			return
		}
		if markErr := c.batchRepo.MarkFailed(context.Background(), batch.ID, workerID, terminalMsg(terminalErr)); markErr != nil {
			c.logger.Error("drive batch crawler: MarkFailed failed",
				"batch_id", batch.ID, "worker_id", workerID, "error", markErr)
		}
	}()

	// P0 hardening refactor: every drive-batch job requires an
	// authenticated Drive account. The legacy public_drive path
	// (unauthenticated drive.google.com/uc scraping) has been
	// removed from the Drive service entirely, so a batch with
	// SourceDriveAccountID=nil can never be processed — fail it
	// here with a clear operator-facing message rather than letting
	// the per-page worker surface a confusing 5xx later.
	if batch.SourceDriveAccountID == nil {
		terminalErr = fmt.Errorf(
			"drive batch %s: SourceDriveAccountID is required (the public_drive download path was removed in the Drive pipeline hardening refactor; re-import via POST /api/v1/media/import/drive/folder/async with a connected Drive account)",
			batch.ID,
		)
		c.logger.Error("drive batch crawler: missing drive account (legacy public_drive path removed)",
			"batch_id", batch.ID,
			"source_provider", batch.SourceProvider,
			"source_folder_id", batch.SourceFolderID,
		)
		return
	}

	// Provider-specific folder lister. Today: google_drive.
	lister, accessToken, err := c.resolveFolderLister(ctx, batch)
	if err != nil {
		terminalErr = fmt.Errorf("resolve folder lister: %w", err)
		c.logger.Error("drive batch crawler: resolve lister failed",
			"batch_id", batch.ID, "source_provider", batch.SourceProvider,
			"source_drive_account_id", batch.SourceDriveAccountID, "error", err)
		return
	}

	// Per-page pagination. After every page write we checkpoint
	// the cursor so a crash resumes here.
	cursorToken := ""
	if batch.CursorPageToken != nil {
		cursorToken = *batch.CursorPageToken
	}
	indexed := batch.CursorIndexedCount

	// Stagger publish_at from the user-supplied schedule envelope.
	// The first job publishes AT start_at; subsequent jobs at
	// prev + random_uniform(min_gap, max_gap).
	currentPublishAt := batch.PublishScheduleStartAt
	if currentPublishAt.Before(time.Now()) {
		// Defensive: producer-side validation already rejected this,
		// but a misconfigured client could land here. Pin to NOW()
		// so the schedule still produces a workable rhythm.
		currentPublishAt = time.Now()
	}

	pageCount := 0
	const maxPages = 200 // sanity cap: 200 pages × 200 = 40k files
	for {
		select {
		case <-ctx.Done():
			terminalErr = ctx.Err()
			return
		default:
		}
		pageCount++
		if pageCount > maxPages {
			terminalErr = fmt.Errorf("exceeded max pages cap %d (folder_id=%q)", maxPages, batch.SourceFolderID)
			c.logger.Error("drive batch crawler: page cap hit",
				"batch_id", batch.ID, "page_count", pageCount, "max_pages", maxPages)
			return
		}

		files, nextPageToken, listErr := lister.ListFolder(ctx, batch.SourceFolderID, "" /*driveID — see ListFolder godoc*/, accessToken, cursorToken)
		if listErr != nil {
			terminalErr = fmt.Errorf("ListFolder page %d: %w", pageCount, listErr)
			c.logger.Error("drive batch crawler: ListFolder failed",
				"batch_id", batch.ID, "page_count", pageCount, "error", listErr)
			return
		}

		// Filter to video-shaped mime types so the folder crawler
		// doesn't enqueue a Google Doc or PDF as an upload_job.
		// VideoMimePrefixes is a conservative allowlist; a future
		// Image / Carousel rollout extends this.
		var pageVideoCount int
		for _, f := range files {
			if !IsVideoMime(f.MimeType) {
				continue
			}
			// P0 hardening refactor: every job in a drive-batch
			// is authenticated_drive (the public_drive path was
			// removed from the Drive service). SourceDriveAccountID
			// is guaranteed non-nil by the guard at the top of
			// processBatch, so the dereference is safe.
			job := &models.UploadJob{
				UserID:         batch.UserID,
				WorkspaceID:    batch.WorkspaceID,
				SourceType:     models.UploadJobSourceAuthenticatedDrive,
				SourceID:       f.ID,
				DriveAccountID: batch.SourceDriveAccountID, // pointer alias — safe per the guard above
				FolderID:       &batch.SourceFolderID,
				Title:          f.Name,
				Caption:        "",
				Targets:        append([]int64{}, batch.TargetAccountIDs...),
				Status:         models.UploadJobStatusPending,
				IngestAfter:    time.Now(),
				PublishAt:      &currentPublishAt,
				BatchID:        &batch.ID,
			}
			if err := c.uploadRepo.Create(job); err != nil {
				terminalErr = fmt.Errorf("Create upload_job at page %d for file %s: %w", pageCount, f.ID, err)
				c.logger.Error("drive batch crawler: upload_job create failed",
					"batch_id", batch.ID, "page_count", pageCount, "file_id", f.ID, "error", err)
				return
			}
			pageVideoCount++
			indexed++

			// Advance the schedule.
			gap := c.randomGap(batch.PublishScheduleMinGap, batch.PublishScheduleMaxGap)
			currentPublishAt = currentPublishAt.Add(gap)
		}
		// Increment the cumulative counter so the dashboard's
		// "by-batch" gauge updates without polling upload_jobs.
		if pageVideoCount > 0 {
			if err := c.batchRepo.IncrementCreatedCount(ctx, batch.ID, workerID, pageVideoCount); err != nil {
				if errors.Is(err, repository.ErrImportBatchLeaseLost) {
					terminalErr = err
					return
				}
				c.logger.Error("drive batch crawler: IncrementCreatedCount failed",
					"batch_id", batch.ID, "page_count", pageCount, "delta", pageVideoCount, "error", err)
				terminalErr = err
				return
			}
		}
		// Checkpoint cursor (per-page write so a crash restarts
		// here; see D5.b+cursor in the design notes).
		if err := c.batchRepo.UpdateCursor(ctx, batch.ID, workerID, nextPageToken, indexed); err != nil {
			if errors.Is(err, repository.ErrImportBatchLeaseLost) {
				terminalErr = err
				return
			}
			c.logger.Error("drive batch crawler: UpdateCursor failed",
				"batch_id", batch.ID, "page_count", pageCount, "error", err)
			terminalErr = err
			return
		}

		if nextPageToken == "" {
			break
		}
		cursorToken = nextPageToken
	}

	if err := c.batchRepo.MarkCompleted(ctx, batch.ID, workerID); err != nil {
		if errors.Is(err, repository.ErrImportBatchLeaseLost) {
			terminalErr = err
			return
		}
		c.logger.Error("drive batch crawler: MarkCompleted failed",
			"batch_id", batch.ID, "error", err)
		terminalErr = err
		return
	}
	completed = true
	c.logger.Info("drive batch crawler: batch done",
		"batch_id", batch.ID,
		"pages", pageCount,
		"indexed", indexed,
	)
}

// terminalMsg returns a safe error string for the MarkFailed
// error_message column. Empty err → "process exited without success".
func terminalMsg(err error) string {
	if err == nil {
		return "process exited without success"
	}
	return err.Error()
}

// VideoMimePrefixes is the conservative allowlist of Google Drive
// mime prefixes the crawler treats as video-shaped. Non-video items
// (docs, sheets, images) are skipped so a folder containing a
// mixed-content tree doesn't enqueue broken upload_jobs.
//
// Conservative on purpose: a misclassified file at this stage
// silently fails at Drive download time anyway, but we'd rather
// skip in advance to keep the upload_job queue clean.
var VideoMimePrefixes = []string{
	"video/",
}

// IsVideoMime returns true if mimeType is in the VideoMimePrefixes
// allowlist. Empty strings and unknown prefixes are rejected.
func IsVideoMime(mimeType string) bool {
	for _, prefix := range VideoMimePrefixes {
		if len(mimeType) >= len(prefix) && mimeType[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

// randomGap returns a uniformly-random duration between [min, max]
// seconds. min > max is silently swapped (defensive; the producer-
// side validation already enforces the invariant).
//
// math/rand is appropriate here: the schedule jitter is not security-
// sensitive, the per-process seed is fine for jittering, and seeding
// per-crawl with time.Now().UnixNano() avoids the conventionalist
// concern about math/rand predictability across replicas.
func (c *DriveBatchCrawler) randomGap(minSec, maxSec int) time.Duration {
	if minSec < 0 {
		minSec = 0
	}
	if maxSec < minSec {
		minSec, maxSec = maxSec, minSec
	}
	if minSec == maxSec {
		return time.Duration(minSec) * time.Second
	}
	span := int64(maxSec - minSec)
	offset := rand.Int63n(span + 1) // [0, span]
	return time.Duration(minSec)*time.Second + time.Duration(offset)*time.Second
}

// resolveFolderLister returns the Drive folder lister + access
// token for the batch. Today only "google_drive" is supported;
// a future Dropbox source registers here.
//
// For authenticated access (batch.SourceDriveAccountID != nil) we
// fetch the long-lived OAuth bearer token from the vault. For
// public folders the lister's ListFolder-with-empty-accessToken
// path uses the server-side GOOGLE_DRIVE_API_KEY via the service
// implementation; the handler verified the configuration exists at
// user-OAuth time so we surface a typed error here if not.
func (c *DriveBatchCrawler) resolveFolderLister(ctx context.Context, batch *models.ImportBatch) (services.DriveFolderLister, string, error) {
	provider, ok := c.capRouter.Get(batch.SourceProvider)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q not configured", batch.SourceProvider)
	}
	lister, ok := provider.(services.DriveFolderLister)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q does not implement DriveFolderLister", batch.SourceProvider)
	}
	if batch.SourceDriveAccountID == nil {
		// Public folder path — lister uses the server's GOOGLE_DRIVE_API_KEY
		// when access_token is empty.
		return lister, "", nil
	}
	importer, ok := provider.(services.DriveImporter)
	if !ok {
		return nil, "", fmt.Errorf("source_provider %q does not implement DriveImporter (needed to read the bearer token)", batch.SourceProvider)
	}
	token, err := c.vault.Renew(ctx, *batch.SourceDriveAccountID, models.TokenTypeBearer,
		func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
			return importer.RefreshOAuthToken(ctx, refreshToken)
		})
	if err != nil {
		return nil, "", fmt.Errorf("refresh drive bearer token: %w", err)
	}
	return lister, token.AccessToken, nil
}
