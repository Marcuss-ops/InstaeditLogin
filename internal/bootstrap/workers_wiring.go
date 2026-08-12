package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

// workerSpecs is the single lifecycle plan. It deliberately returns the
// existing worker.WorkerSpec values rather than introducing another registry:
// RunWorkers registers these specs on App.WorkerRegistry in this exact order.
func (a *App) workerSpecs() []worker.WorkerSpec {
	return []worker.WorkerSpec{
		a.publishWorkerSpec(),
		a.reconcileWorkerSpec(),
		a.outboxWorkerSpec(),
		a.webhookWorkerSpec(),
		a.metricsWorkerSpec(),
		a.sessionsCleanupWorkerSpec(),
		a.assetCleanupWorkerSpec(),
		a.veloxDownloaderWorkerSpec(),
		a.uploadWorkerSpec(),
		a.contentPreparationWorkerSpec(),
		a.driveBatchCrawlerWorkerSpec(),
		a.driveInboxScannerWorkerSpec(),
		a.youtubeProcessingReconcilerWorkerSpec(),
		a.youtubeCopyrightWorkerSpec(),
		a.metadataGenerationWorkerSpec(),
		a.tokenRefreshSweepWorkerSpec(),
		a.snapshotRefreshSweepWorkerSpec(),
	}
}

// registerWorkerSpecs registers the lifecycle plan on the shared registry.
// The caller owns startup; keeping registration separate makes the wiring
// contract testable without launching database-backed workers.
func (a *App) registerWorkerSpecs() []worker.WorkerSpec {
	specs := a.workerSpecs()
	for _, spec := range specs {
		a.WorkerRegistry.Register(spec)
	}
	return specs
}

// RunWorkers starts the 17 background workers (publish, reconcile,
// outbox, webhook, metrics, sessions_cleanup, asset_cleanup,
// velox_downloader, upload, content_preparation, drive_batch_crawler, drive_inbox_scanner,
// youtube_processing_reconciler, youtube_copyright_checker, metadata_generation, token_refresh_sweep,
// snapshot_refresh_sweep) under the shared WorkerRegistry. The registry handles startup, heartbeat
// tracking, supervision, logging, and shutdown. A critical worker
// that exits with a non-context error aborts the whole process by
// returning the error from RunWorkers.
//
// The per-worker construction closures live in the *WorkerSpec factories
// above (one per goroutine, in registration order) so this function
// stays a thin orchestration loop: register all → StartAll → block on
// critical-error-or-cancel → StopAll with the shared 15s drain budget.
//
// On cancellation it cancels every goroutine concurrently and waits
// up to 15s total for their Run loops to drain gracefully.
func (a *App) RunWorkers(ctx context.Context) error {
	if a.WorkerRegistry == nil {
		return fmt.Errorf("RunWorkers called with nil App.WorkerRegistry")
	}
	if _, err := a.requireRuntime(); err != nil {
		return fmt.Errorf("RunWorkers capability validation failed: %w", err)
	}

	// The plan is the single source of truth for worker count, order and
	// criticality. Each spec still contains a lazy Run closure; no worker
	// dependency is constructed before StartAll invokes it.
	a.registerWorkerSpecs()

	slog.Info("17 background workers registered: publish / reconcile / outbox / webhook / metrics / sessions_cleanup / asset_cleanup / velox_downloader / upload / content_preparation / drive_batch_crawler / drive_inbox_scanner / youtube_processing_reconciler / youtube_copyright_checker / metadata_generation / token_refresh_sweep / snapshot_refresh_sweep")

	criticalErrCh := a.WorkerRegistry.StartAll(ctx)

	var criticalErr error
	select {
	case criticalErr = <-criticalErrCh:
		if criticalErr != nil {
			slog.Error("critical worker exited unexpectedly", "error", criticalErr)
		}
	case <-ctx.Done():
		slog.Info("context cancelled, broadcasting shutdown to all workers")
		// A critical worker may report just as cancellation arrives. Give
		// the buffered error channel one non-blocking turn before declaring
		// this a clean cancellation, so the failure reaches the caller.
		select {
		case criticalErr = <-criticalErrCh:
		default:
		}
	}

	shutdownErr := a.WorkerRegistry.StopAll(15 * time.Second)
	if criticalErr == nil {
		// StopAll waits for every supervisor, so any critical failure that
		// raced the cancellation has now had a chance to reach the buffer.
		select {
		case criticalErr = <-criticalErrCh:
		default:
		}
	}
	if criticalErr != nil {
		if a.OneTimeCodes != nil {
			a.OneTimeCodes.Stop()
		}
		return criticalErr
	}
	if shutdownErr != nil {
		slog.Warn("worker shutdown did not complete cleanly", "error", shutdownErr)
	} else {
		slog.Info("all background workers drained")
	}

	if a.OneTimeCodes != nil {
		a.OneTimeCodes.Stop()
		slog.Info("OneTimeCodeStore sweeper stopped")
	}

	return shutdownErr
}
