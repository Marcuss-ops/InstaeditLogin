package worker

import (
	"context"
	"log/slog"
	"time"
)

// AssetCleaner is the narrow interface AssetCleanupWorker depends on.
// Defined inline (mirrors the package's other worker contracts) so
// the wiring in internal/bootstrap/app.go can pass a thin *Repository
// facade without dragging the *sql.DB-bound concrete type.
//
// Calls CleanupOnce(ctx, retentionDays) per tick; returns the
// number of media_assets rows hard-deleted by this invocation. A
// zero count is normal (no eligible rows this tick); a non-nil
// error surfaces as a WARN log at the worker level but does not
// break the Run loop.
type AssetCleaner interface {
	CleanupOnce(ctx context.Context, retentionDays int) (int64, error)
}

// AssetCleanupWorker periodically hard-deletes media_assets rows
// whose YouTube publish + post pipeline has fully run and aged past
// the configured retention buffer.
//
// Eligiblity predicate (single DELETE FROM ... USING ... statement,
// see internal/repository/asset_repo.go::DeleteEligibleAssets):
//
//   - The linked youtube_target_publications row has:
//     youtube_upload_status = 'youtube_uploaded'
//     AND published_at IS NOT NULL
//     AND published_at + retentionDays < NOW()
//
//   - AND no post_targets row on the same post is in {retrying, dlq}
//     (operator-triage state — asset is still load-bearing for
//     debugging/audit until the row is resolved).
//
//   - AND no youtube_target_publications row has a future
//     publish_at cursor (the asset is held until the publish lands,
//     even if the upload itself staged hours earlier).
//
// Workers lifecycle: an immediate, initial tick drains any backlog
// (e.g. after a long worker downtime) THEN a time.Ticker fires at
// the configured interval (default 24h; not as frequent as
// sessions_cleanup since publish windows are typically days).
//
// Critical=false: a hard error here MUST NOT take the process down
// — a single bad migration / transient DB issue must not kill the
// background job runner. The ticker loop is resilient: errors log
// WARN and the next tick retries with fresh state.
type AssetCleanupWorker struct {
	assetCleaner   AssetCleaner
	interval       time.Duration
	retentionDays  int
	logger        *slog.Logger
}

// NewAssetCleanupWorker wires the dependencies. interval <= 0 falls
// back to a production-safe default (24h) so a misconfigured env
// doesn't accidentally tighten the cleanup to a hot loop.
// retentionDays <= 0 falls back to 7 (the historical S3 retention
// floor used by the upload worker's media_asset create site).
func NewAssetCleanupWorker(cleaner AssetCleaner, interval time.Duration, retentionDays int, logger *slog.Logger) *AssetCleanupWorker {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if retentionDays <= 0 {
		retentionDays = 7
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AssetCleanupWorker{
		assetCleaner:  cleaner,
		interval:      interval,
		retentionDays: retentionDays,
		logger:        logger,
	}
}

// Run drains any backlog, then fires a tick on every interval. The
// loop exits cleanly on context cancellation; transient errors
// log WARN and the loop continues (no backoff — a 24h cadence
// already de-flates repeated transient failures).
func (w *AssetCleanupWorker) Run(ctx context.Context) error {
	w.tick(ctx) // immediate backlog drain at startup

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("asset cleanup worker: context cancelled, exiting")
			return nil
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick runs a single cleanup pass. Errors are caught + logged so
// the loop doesn't crash on a transient DB issue; the next tick
// will retry the same range.
func (w *AssetCleanupWorker) tick(ctx context.Context) {
	deleted, err := w.assetCleaner.CleanupOnce(ctx, w.retentionDays)
	if err != nil {
		w.logger.Warn("asset cleanup worker: CleanupOnce failed (will retry next tick)",
			"retention_days", w.retentionDays, "error", err)
		return
	}
	if deleted > 0 {
		w.logger.Info("asset cleanup worker: hard-deleted stale media assets",
			"deleted_count", deleted, "retention_days", w.retentionDays)
	}
}
