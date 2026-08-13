package worker

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
		if completed || errors.Is(terminalErr, context.Canceled) {
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

	// Task 6/10 — Shared Drive auto-resolve. Resolve the folder's
	// driveId ONCE before the pagination loop (the folder's driveId
	// is stable; per-page resolve would burn quota for no gain).
	// Best-effort: a failure here just falls back to the My Drive
	// corpus and logs a warn-level remediation hint so a future
	// operator dashboard can group these. The resolveFolderLister
	// helper returns `(lister, token, err)` where `lister` is the
	// narrowed DriveFolderLister interface — re-type-assert to
	// DriveFolderInspector for the resolver call (Inspector is a
	// narrow subset of DriveImporter, satisfied by the same provider).
	provider, _ := c.capRouter.Get(batch.SourceProvider)
	inspector, _ := provider.(services.DriveFolderInspector)
	resolvedDriveID, resolveErr := services.ResolveFolderDriveID(ctx, inspector, batch.SourceFolderID, accessToken)
	if resolveErr != nil {
		c.logger.Warn("drive batch crawler: folder metadata fetch failed; falling back to My Drive corpus",
			"batch_id", batch.ID,
			"folder_id", batch.SourceFolderID,
			"error", resolveErr,
		)
		resolvedDriveID = ""
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

		files, nextPageToken, listErr := lister.ListFolder(ctx, batch.SourceFolderID, resolvedDriveID, accessToken, cursorToken)
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
			// P1 (migration 053) — propagate the per-batch YouTube
			// default (set by pkg/api/drive_batch_v2.go handler
			// allowlist-validated at producer boundary) onto every
			// upload_job we fan out. The upload_worker then copies
			// this onto post.default_privacy_level; the publish_worker
			// uses it as the middle term in its precedence cascade.
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
				// Prepare before the public cursor: the upload worker claims
				// at this time, downloads Drive and uploads YouTube privately.
				IngestAfter:         currentPublishAt.Add(-c.opts.PrepareLeadTime),
				PublishAt:           &currentPublishAt,
				BatchID:             &batch.ID,
				DefaultPrivacyLevel: batch.DefaultPrivacyLevel,
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

			// Blocco #3 P0 — D6 exact horizon re-stamp. The producer's
			// heuristic check (handleDriveBatchImportV2) projects a
			// 10_000-file worst-case BEFORE the actual listing; the
			// real file count is now known. Reject the batch when the
			// EXACT projected cursor would land past now + horizon so
			// the SPA gets a clear 422 instead of the rows silently
			// being clamped to a date in the past at publish-time. The
			// BatchID/folder/page context stays in the error message
			// for the operator-triage dashboard.
			//
			// Without this check, a Drive folder with 10_001 videos
			// scheduled 1 minute apart would pass the 10k-day producer
			// guard (which uses the WORST-CASE projection) but then
			// discover at fan-out time that file #10_001 lands on
			// day 10k+1 — silently squashing into the horizon limit on
			// the last few rows. The Reject path means the SPA can
			// prompt the operator to widen the gap and resubmit.
			horizonDays := c.publishHorizonDays()
			if horizonDays > 0 {
				horizon := time.Now().Add(time.Duration(horizonDays) * 24 * time.Hour)
				if currentPublishAt.After(horizon) {
					terminalErr = fmt.Errorf(
						"publish schedule exceeds %d-day horizon at video #%d (projected publish_at=%s, folder_id=%q). Widen the gap and resubmit the batch.",
						horizonDays, indexed, currentPublishAt.UTC().Format(time.RFC3339), batch.SourceFolderID,
					)
					c.logger.Error("drive batch crawler: exact horizon re-stamp exceeded",
						"batch_id", batch.ID,
						"folder_id", batch.SourceFolderID,
						"indexed_count", indexed,
						"projected_publish_at", currentPublishAt.UTC().Format(time.RFC3339),
						"horizon_days", horizonDays,
						"horizon_at", horizon.UTC().Format(time.RFC3339),
					)
					return
				}
			}
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

// terminalMsg returns a stable, provider-independent diagnostic for the
// MarkFailed error_message column. The crawler currently has no durable
// requeue transition, so every non-cancellation failure remains terminal,
// but its retry/auth/permanent class is still preserved for operators.
func terminalMsg(err error) string {
	if err == nil {
		return "permanent: process exited without success"
	}
	classification := services.ClassifyErrorFor("google_drive", "batch_crawl", err)
	if classification == nil {
		return "permanent: drive batch failed"
	}
	return classification.Error()
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
