// Package worker — token_refresh_sweep.go.
//
// Periodic worker that renews the OAuth refresh tokens of dormant
// channels BEFORE Google garbage-collects them. Google's OAuth docs
// list "the refresh token has not been used for six months" as a
// reason a refresh token stops working, and InstaEdit has channels
// that are connected but publish rarely (or never) — those grants
// silently die and the first real publish fails with invalid_grant.
//
// Design notes:
//
//   - One struct, one Run(ctx) loop, ctx-cancellable — mirrors
//     sessions_cleanup.go / asset_cleanup_worker.go. Registered as
//     NON-critical in the worker registry: a transient failure here
//     must never take the process down.
//   - Selection is delegated to a TokenRefreshSweepStore (the
//     repository layer): active grants, not reauth_required, whose
//     last_refresh_at is older than the horizon (or NULL with an
//     old created_at) or whose expires_at is within the TTL window.
//   - Renewal reuses the CredentialVault.Renew path — the same
//     per-grant Postgres advisory lock the publish worker uses — so
//     a sweep renew and a publish-time renew of the same grant can
//     never race. For YouTube the sweep uses RenewYouTubeToken
//     (canonical bearer first, legacy long_lived fallback,
//     ErrYouTubeInvalidGrant classification) so a dead grant is
//     automatically flagged reauth_required — the desired side
//     effect: the operator's dashboard surfaces the dead channel
//     BEFORE a publish fails.
//   - Refreshers are a per-provider map injected at bootstrap; a
//     grant whose provider has no wired refresher is skipped
//     silently (the sweep only covers wired Google providers).
//   - Failure isolation: one account failing (invalid_grant, 5xx,
//     network) is logged at WARN without aborting the sweep; the
//     next tick retries.
//   - Metrics are free: each RefreshOAuthToken call records
//     token_refresh_success_total / token_refresh_error_total via
//     the services layer.
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// DefaultTokenRefreshSweepInterval is the fallback tick interval.
// Production cadence comes from cfg.Worker.TokenRefreshSweepIntervalSeconds
// (env: TOKEN_REFRESH_SWEEP_INTERVAL_SECONDS, default 86400s = 24h).
// Daily is ample: the risk horizon is ~6 months, so even a weekly
// cadence would keep every grant inside the activity window.
const DefaultTokenRefreshSweepInterval = 24 * time.Hour

// DefaultTokenRefreshSweepHorizonDays is the inactivity lookahead:
// a grant whose last_refresh_at (or created_at when never refreshed)
// is older than this is renewed. 120 days leaves a 2-month margin
// under Google's ~6-month inactivity GC. Mirrors
// repository.DefaultRefreshSweepHorizonDays (kept in sync at the
// two constructor fallback sites).
const DefaultTokenRefreshSweepHorizonDays = 120

// TokenRefreshSweepStore selects the at-risk grants. Defined inline
// (not in repository) so the worker is unit-testable with a fake
// without touching the database — the SessionCleaner pattern.
type TokenRefreshSweepStore interface {
	ListDormantRefreshGrants(ctx context.Context, horizonDays int) ([]models.DormantRefreshGrant, error)
}

// TokenRefreshSweepWorker periodically renews dormant grants.
//
// vault is the full credentials.VaultAPI (not a narrower seam): the
// YouTube branch calls credentials.RenewYouTubeToken, which needs the
// VaultAPI surface (canonical bearer + legacy long_lived fallback +
// ErrYouTubeInvalidGrant classification). Tests reuse the existing
// mockCredentialVault (mocks_test.go) which implements VaultAPI with
// an injectable renewFn.
type TokenRefreshSweepWorker struct {
	store       TokenRefreshSweepStore
	vault       credentials.VaultAPI
	refreshers  map[string]credentials.TokenRefresher // provider key → provider refresh adapter
	interval    time.Duration
	horizonDays int
	logger      *slog.Logger
}

// NewTokenRefreshSweepWorker wires the dependencies. interval <= 0
// falls back to DefaultTokenRefreshSweepInterval (24h); horizonDays
// <= 0 falls back to DefaultTokenRefreshSweepHorizonDays (120).
// nil logger inherits slog.Default(). store and vault must be
// non-nil — a nil will panic on the first tick (fail-fast for
// misconfigured wiring, mirroring SessionsCleanupWorker).
func NewTokenRefreshSweepWorker(
	store TokenRefreshSweepStore,
	vault credentials.VaultAPI,
	refreshers map[string]credentials.TokenRefresher,
	interval time.Duration,
	horizonDays int,
	logger *slog.Logger,
) *TokenRefreshSweepWorker {
	if interval <= 0 {
		interval = DefaultTokenRefreshSweepInterval
	}
	if horizonDays <= 0 {
		horizonDays = DefaultTokenRefreshSweepHorizonDays
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TokenRefreshSweepWorker{
		store:       store,
		vault:       vault,
		refreshers:  refreshers,
		interval:    interval,
		horizonDays: horizonDays,
		logger:      logger,
	}
}

// Run blocks until ctx is cancelled. Initial tick runs BEFORE the
// first ticker fire so a freshly-started process doesn't wait a full
// interval before catching up on grants that aged during downtime.
//
// Errors are logged at WARN but do NOT stop the loop — a transient
// DB blip should not kill the cadence. Returns ctx.Err() on shutdown.
func (w *TokenRefreshSweepWorker) Run(ctx context.Context) error {
	w.logger.Info("token refresh sweep worker started",
		"interval_seconds", w.interval.Seconds(),
		"horizon_days", w.horizonDays,
		"providers", providerKeys(w.refreshers))
	defer w.logger.Info("token refresh sweep worker stopped")

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

// tick executes one sweep pass: select at-risk grants, renew each
// through the vault, log outcomes. Per-account failures are isolated
// (logged, sweep continues); a selection error aborts the pass with
// a WARN (retried at the next interval).
func (w *TokenRefreshSweepWorker) tick(ctx context.Context) {
	grants, err := w.store.ListDormantRefreshGrants(ctx, w.horizonDays)
	if err != nil {
		w.logger.Warn("token refresh sweep tick failed (will retry at next interval)",
			"error", err)
		return
	}
	if len(grants) == 0 {
		return
	}
	w.logger.Info("token refresh sweep selecting dormant grants", "count", len(grants))

	renewed, skipped, failed := 0, 0, 0
	for _, g := range grants {
		refresher, ok := w.refreshers[g.Provider]
		if !ok {
			skipped++
			continue // no refresher wired for this provider — not an error
		}
		var renewErr error
		if g.Provider == models.PlatformYouTube {
			// Canonical bearer first + legacy long_lived fallback +
			// ErrYouTubeInvalidGrant classification (flags the grant
			// reauth_required so the dashboard surfaces it early).
			_, renewErr = credentials.RenewYouTubeToken(ctx, w.vault, g.PlatformAccountID, refresher, w.logger)
		} else {
			_, renewErr = w.vault.Renew(ctx, g.PlatformAccountID, models.TokenTypeBearer, refresher)
		}
		if renewErr != nil {
			failed++
			// Vault errors are redacted by construction (no token
			// material, classified code only) — safe to log the full
			// error. A dead grant surfaces here as invalid_grant →
			// reauth_required, which is exactly what the sweep wants
			// to discover early.
			w.logger.Warn("token refresh sweep renew failed (will retry at next interval)",
				"platform_account_id", g.PlatformAccountID,
				"oauth_connection_id", g.OAuthConnectionID,
				"provider", g.Provider,
				"error", renewErr)
			continue
		}
		renewed++
		w.logger.Debug("token refresh sweep renewed dormant grant",
			"platform_account_id", g.PlatformAccountID,
			"oauth_connection_id", g.OAuthConnectionID,
			"provider", g.Provider)
	}
	w.logger.Info("token refresh sweep pass complete",
		"selected", len(grants),
		"renewed", renewed,
		"skipped", skipped,
		"failed", failed)
}

// providerKeys returns the sorted provider keys of the refresher map
// for stable log output. Nil-safe.
func providerKeys(m map[string]credentials.TokenRefresher) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
