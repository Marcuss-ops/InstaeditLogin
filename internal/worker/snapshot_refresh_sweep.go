// Package worker — snapshot_refresh_sweep.go.
//
// Periodic worker that drains account_resource_snapshots refreshes in
// the background — the second half of the Fase 4 strict rule:
//
//	opening a channel page reads PostgreSQL ONLY (no provider calls),
//	serves the cached snapshot immediately, stamps refresh_pending_at,
//	and THIS worker refreshes the snapshot asynchronously.
//
// Design notes:
//
//   - One struct, one Run(ctx) loop, ctx-cancellable — mirrors
//     token_refresh_sweep.go / sessions_cleanup.go. Registered as
//     NON-critical in the worker registry: a transient failure here
//     must never take the process down.
//   - Selection is delegated to a SnapshotRefreshStore (the repository
//     layer): accounts whose snapshot row has refresh_pending_at set
//     (stamped by the read path when serving a stale cached snapshot).
//     MarkSnapshotRefreshPending upserts a placeholder row even for
//     never-fetched accounts, so every stamped account is claimable.
//     The claim (FOR UPDATE SKIP LOCKED) makes the drain safe across
//     multiple replicas.
//   - Refresh reuses the SAME token path as handleSyncAccount: renew
//     via the platform's OAuth provider when wired (vault.Renew), with
//     the Get bearer → long_lived → short_lived fallback for platforms
//     without a refresher or legacy tokens.
//   - Concurrency is bounded by accountSnapshotRefreshConcurrency (4):
//     N pending accounts never become N concurrent provider calls —
//     the DoD cap is 3-5. A pass drains at most
//     repository.SnapshotRefreshBatchLimit (25) accounts, so even a
//     100-channel fleet is spread over a few passes instead of one
//     fan-out.
//   - Failure isolation: one account failing (invalid_grant, 5xx,
//     network) is logged at WARN without aborting the pass; the next
//     tick retries. A selection error aborts the pass with a WARN
//     (retried at the next interval).
//   - The upsert clears refresh_pending_at + the claim lease and resets
//     the attempt counter (UpsertSnapshot sets all three on conflict), so
//     a refreshed snapshot drops out of the queue naturally.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// DefaultSnapshotRefreshSweepInterval is the fallback tick interval.
// Production cadence comes from cfg.Worker.SnapshotRefreshSweepIntervalSeconds
// (env: SNAPSHOT_REFRESH_SWEEP_INTERVAL_SECONDS, default 60s). One minute
// keeps freshly-stamped refresh_pending_at rows draining promptly after a
// page load without hammering the provider on a large fleet.
const DefaultSnapshotRefreshSweepInterval = 60 * time.Second

// accountSnapshotRefreshConcurrency bounds how many provider fetches run
// at once during one sweep pass. The Fase 4 Definition of Done caps this
// at 3-5 — 4 is the middle of the range.
const accountSnapshotRefreshConcurrency = 4

// Keep provider work below the two-minute database claim lease so a slow
// request cannot outlive its claim and be processed by another worker.
const snapshotRefreshProviderTimeout = 90 * time.Second

// SnapshotRefreshStore selects the accounts due for a background snapshot
// refresh and persists the refreshed snapshot. Defined inline (not in
// repository) so the worker is unit-testable with a fake without touching
// the database — the SessionCleaner / TokenRefreshSweepStore pattern.
type SnapshotRefreshStore interface {
	ClaimPendingSnapshotRefreshes(ctx context.Context, limit int, lease time.Duration) ([]repository.PendingSnapshotRefresh, error)
	UpsertSnapshot(snap *repository.AccountResourceSnapshot) error
	RescheduleSnapshotRefresh(ctx context.Context, accountID int64, next time.Time, errText string) error
	MarkSnapshotRefreshTerminal(ctx context.Context, accountID int64, code, message string) error
}

// DailyMetricHistoryStore is the persistence seam for the daily numeric
// series consumed by Dashboard and Channel Performance. A fresh remote
// snapshot must update this history row as well, otherwise the UI keeps
// serving the previous day's values.
type DailyMetricHistoryStore interface {
	UpsertDaily(platformAccountID int64, date time.Time, point repository.AccountMetricPoint) error
}

// AccountDetailsFetcher is the narrow provider surface the worker needs:
// fetch rich account details for a platform account. The services
// AccountDetailsProvider satisfies this structurally; defined inline so
// the worker doesn't drag in the full capability router.
type AccountDetailsFetcher interface {
	GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error)
}

// SnapshotRefreshSweepWorker periodically drains pending snapshot
// refreshes. NON-critical worker: a failure here must never take the
// process down.
//
// vault is the full credentials.VaultAPI because the refresh path reuses
// the same token resolution as handleSyncAccount (Renew with per-provider
// refresher, then Get bearer → long_lived → short_lived fallback).
// refreshers maps platform → credentials.TokenRefresher (the OAuth
// provider's RefreshOAuthToken adapter), exactly like the token refresh
// sweep. fetchers maps platform → AccountDetailsFetcher; a platform
// without a fetcher is skipped (nothing to refresh into a snapshot).
type SnapshotRefreshSweepWorker struct {
	store       SnapshotRefreshStore
	metricStore DailyMetricHistoryStore
	vault       credentials.VaultAPI
	refreshers  map[string]credentials.TokenRefresher
	fetchers    map[string]AccountDetailsFetcher
	interval    time.Duration
	logger      *slog.Logger
}

// NewSnapshotRefreshSweepWorker wires the dependencies. interval <= 0
// falls back to DefaultSnapshotRefreshSweepInterval (60s). nil logger
// inherits slog.Default(). store and vault must be non-nil — a nil will
// panic on the first tick (fail-fast for misconfigured wiring, mirroring
// SessionsCleanupWorker).
func NewSnapshotRefreshSweepWorker(
	store SnapshotRefreshStore,
	metricStore DailyMetricHistoryStore,
	vault credentials.VaultAPI,
	refreshers map[string]credentials.TokenRefresher,
	fetchers map[string]AccountDetailsFetcher,
	interval time.Duration,
	logger *slog.Logger,
) *SnapshotRefreshSweepWorker {
	if interval <= 0 {
		interval = DefaultSnapshotRefreshSweepInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SnapshotRefreshSweepWorker{
		store:       store,
		metricStore: metricStore,
		vault:       vault,
		refreshers:  refreshers,
		fetchers:    fetchers,
		interval:    interval,
		logger:      logger,
	}
}

// Run blocks until ctx is cancelled. Initial tick runs BEFORE the first
// ticker fire so a freshly-started process doesn't wait a full interval
// before draining refresh_pending rows that accumulated during downtime.
//
// Errors are logged at WARN but do NOT stop the loop — a transient DB
// blip should not kill the cadence. Returns ctx.Err() on shutdown.
func (w *SnapshotRefreshSweepWorker) Run(ctx context.Context) error {
	w.logger.Info("snapshot refresh sweep worker started",
		"interval_seconds", w.interval.Seconds(),
		"concurrency", accountSnapshotRefreshConcurrency,
		"batch_limit", repository.SnapshotRefreshBatchLimit)
	defer w.logger.Info("snapshot refresh sweep worker stopped")

	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick executes one sweep pass: select pending snapshot refreshes and
// refresh each with bounded concurrency. Per-account failures are
// isolated (logged, sweep continues); a selection error aborts the pass
// with a WARN (retried at the next interval).
func (w *SnapshotRefreshSweepWorker) tick(ctx context.Context) {
	pending, err := w.store.ClaimPendingSnapshotRefreshes(ctx, accountSnapshotRefreshConcurrency, repository.SnapshotRefreshClaimLease)
	if err != nil {
		w.logger.Warn("snapshot refresh sweep tick failed (will retry at next interval)",
			"error", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	w.logger.Info("snapshot refresh sweep selecting accounts",
		"count", len(pending),
		"batch_limit", repository.SnapshotRefreshBatchLimit)

	// Bounded concurrency (DoD: 3-5 concurrent refreshes). A plain
	// semaphore-sized pool keeps at most
	// accountSnapshotRefreshConcurrency provider fetches in flight.
	// refreshOne logs every failure internally (WARN, isolated), so the
	// goroutines only need to signal completion — a WaitGroup suffices.
	sem := make(chan struct{}, accountSnapshotRefreshConcurrency)
	var wg sync.WaitGroup
	for _, p := range pending {
		wg.Add(1)
		go func(p repository.PendingSnapshotRefresh) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := w.refreshOne(ctx, p); err != nil {
				nextAttempt := nextSnapshotRefreshAttempt(p.Attempts)
				if errors.Is(err, credentials.ErrInvalidGrant) {
					// A revoked grant cannot be repaired by retrying. Clear
					// the durable queue marker; a successful reauthorization
					// will enqueue the account again through the read path.
				}
				if errors.Is(err, credentials.ErrInvalidGrant) {
					if terminalErr := w.store.MarkSnapshotRefreshTerminal(ctx, p.PlatformAccountID, "OAUTH_INVALID_GRANT", "OAuth grant requires reauthorization"); terminalErr != nil {
						w.logger.Warn("snapshot refresh sweep: failed to persist terminal OAuth state", "platform_account_id", p.PlatformAccountID, "error", terminalErr)
					}
				} else if scheduleErr := w.store.RescheduleSnapshotRefresh(ctx, p.PlatformAccountID, nextAttempt, snapshotRefreshErrorSummary(err)); scheduleErr != nil {
					w.logger.Warn("snapshot refresh sweep: failed to persist retry schedule", "platform_account_id", p.PlatformAccountID, "error", scheduleErr)
				}
			}
		}(p)
	}
	wg.Wait()
}

// nextSnapshotRefreshAttempt applies bounded exponential backoff after a
// failed refresh. The claim is released by RescheduleSnapshotRefresh, so a
// provider outage cannot hot-loop on every worker tick.
func snapshotRefreshErrorSummary(err error) string {
	if errors.Is(err, credentials.ErrInvalidGrant) {
		return "oauth grant requires reauthorization"
	}
	if errors.Is(err, credentials.ErrModernGrantMissing) {
		return "oauth grant token is missing"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "provider request timed out"
	}
	return "snapshot refresh failed"
}

func nextSnapshotRefreshAttempt(attempts int) time.Time {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 6 {
		attempts = 6
	}
	backoff := 30 * time.Second
	backoff *= time.Duration(1 << attempts)
	if backoff > 30*time.Minute {
		backoff = 30 * time.Minute
	}
	return time.Now().Add(backoff)
}

// refreshOne fetches fresh details for a single account and persists them
// as the new snapshot (UpsertSnapshot clears refresh_pending_at, dropping
// the row out of the queue). Token resolution mirrors handleSyncAccount.
func (w *SnapshotRefreshSweepWorker) refreshOne(ctx context.Context, p repository.PendingSnapshotRefresh) error {
	fetcher, ok := w.fetchers[p.Platform]
	if !ok {
		// No details fetcher wired for this platform. Return an error so
		// the durable queue backs off instead of retrying every tick
		// (RescheduleSnapshotRefresh applies exponential backoff).
		return errors.New("no account details fetcher wired for platform " + p.Platform)
	}

	providerCtx, cancel := context.WithTimeout(ctx, snapshotRefreshProviderTimeout)
	defer cancel()

	token, err := resolveAccountToken(providerCtx, w.vault, w.refreshers, p.Platform, p.PlatformAccountID)
	if err != nil {
		// Token material is never logged: resolveAccountToken returns
		// only the classified error, and we log the account id +
		// platform only.
		w.logger.Warn("snapshot refresh sweep: no valid token for account (will retry at next interval)",
			"platform_account_id", p.PlatformAccountID,
			"platform", p.Platform,
			"error", snapshotRefreshErrorSummary(err))
		return err
	}

	details, err := fetcher.GetAccountDetails(providerCtx, token.AccessToken, p.PlatformUserID)
	if err != nil {
		w.logger.Warn("snapshot refresh sweep: provider fetch failed (will retry at next interval)",
			"platform_account_id", p.PlatformAccountID,
			"platform", p.Platform,
			"error", snapshotRefreshErrorSummary(err))
		return err
	}

	snap := buildSnapshotFromDetails(p.PlatformAccountID, details)
	if err := w.store.UpsertSnapshot(snap); err != nil {
		w.logger.Warn("snapshot refresh sweep: failed to persist snapshot (will retry at next interval)",
			"platform_account_id", p.PlatformAccountID,
			"platform", p.Platform,
			"error", "snapshot persistence failed")
		return err
	}
	if w.metricStore != nil {
		if err := w.metricStore.UpsertDaily(p.PlatformAccountID, details.FetchedAt, metricPointFromDetails(details.Metrics)); err != nil {
			w.logger.Warn("snapshot refresh sweep: failed to persist daily metrics (will retry at next interval)",
				"platform_account_id", p.PlatformAccountID,
				"platform", p.Platform,
				"error", "daily metric persistence failed")
			return err
		}
	}
	w.logger.Debug("snapshot refresh sweep: snapshot refreshed",
		"platform_account_id", p.PlatformAccountID,
		"platform", p.Platform)
	return nil
}

func metricPointFromDetails(metrics []models.AccountMetric) repository.AccountMetricPoint {
	point := repository.AccountMetricPoint{}
	for _, metric := range metrics {
		switch metric.Key {
		case "subscribers":
			point.Subscribers = metric.Value
		case "views":
			point.Views = metric.Value
		case "videos":
			point.Videos = metric.Value
		}
	}
	return point
}

// resolveAccountToken resolves a usable access token for the account,
// mirroring handleSyncAccount's chain: renew via the platform's OAuth
// refresher when wired, then fall back to Get bearer → long_lived →
// short_lived (platforms without a refresher or legacy tokens written by
// older releases). Errors are classified (no token material) so callers
// can log them safely.
func resolveAccountToken(
	ctx context.Context,
	vault credentials.VaultAPI,
	refreshers map[string]credentials.TokenRefresher,
	platform string,
	accountID int64,
) (*models.OAuthToken, error) {
	var token *models.OAuthToken
	var err error
	if refresher, ok := refreshers[platform]; ok {
		token, err = vault.Renew(ctx, accountID, models.TokenTypeBearer, refresher)
		if err != nil && !errors.Is(err, credentials.ErrModernGrantMissing) {
			return nil, err
		}
	} else {
		err = errors.New("no OAuth refresher wired for platform " + platform)
	}
	if err != nil {
		token, err = vault.Get(ctx, accountID, models.TokenTypeBearer)
	}
	if err != nil {
		token, err = vault.Get(ctx, accountID, models.TokenTypeLongLived)
	}
	if err != nil {
		token, err = vault.Get(ctx, accountID, models.TokenTypeShortLived)
	}
	if err != nil {
		return nil, err
	}
	return token, nil
}

// buildSnapshotFromDetails maps provider AccountDetails into the
// account_resource_snapshots shape — the exact same field mapping as
// handleSyncAccount so both paths persist identical payloads.
func buildSnapshotFromDetails(accountID int64, details *models.AccountDetails) *repository.AccountResourceSnapshot {
	snap := &repository.AccountResourceSnapshot{
		PlatformAccountID: accountID,
		ResourceType:      details.ResourceType,
		Profile: map[string]any{
			"display_name": details.DisplayName,
			"handle":       details.Handle,
			"description":  details.Description,
			"avatar_url":   details.AvatarURL,
			"banner_url":   details.BannerURL,
			"public_url":   details.PublicURL,
			"external_id":  details.ExternalID,
		},
		FetchedAt: details.FetchedAt,
	}
	stats := make(map[string]any)
	for _, m := range details.Metrics {
		stats[m.Key] = map[string]any{
			"label":         m.Label,
			"value":         m.Value,
			"display_value": m.DisplayValue,
		}
	}
	snap.Statistics = stats
	if details.Properties != nil {
		snap.Content = details.Properties
	}
	return snap
}
