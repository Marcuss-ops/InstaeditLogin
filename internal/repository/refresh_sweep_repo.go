package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// RefreshSweepRepository drives the periodic token-refresh sweep
// (internal/worker/token_refresh_sweep.go): it selects the OAuth
// grants whose refresh tokens are at risk of provider
// garbage-collection so the worker can renew them BEFORE Google's
// ~6-month inactivity policy kills them.
type RefreshSweepRepository struct {
	db *sql.DB
}

// NewRefreshSweepRepository creates the sweep selection repository.
func NewRefreshSweepRepository(db *sql.DB) *RefreshSweepRepository {
	return &RefreshSweepRepository{db: db}
}

// refreshSweepTTLWindowDays is the lookahead for the
// oauth_connections.expires_at branch of the sweep selection. It
// mirrors the refresh_tokens_near_expiry collector window
// (pkg/metrics/collector.go::refreshTokensNearExpiryWindowSQL) and
// the vault.Renew warning window
// (internal/credentials/vault_refresh.go::refreshGrantExpiryWarningWindow)
// so gauge, sweep and log all agree on the same horizon.
const refreshSweepTTLWindowDays = 7

// SQLListDormantRefreshGrants is the sweep selection query. Exported
// as a const (same pattern as SQLListDeadLetterJobs in admin_ops.go)
// so the sqlmock test pins the production SQL byte-for-byte via
// QueryMatcherEqual — a production-side drift fires a test failure
// instead of a silent regexp mismatch.
const SQLListDormantRefreshGrants = `SELECT oc.id            AS oauth_connection_id,
        pa.id            AS platform_account_id,
        oc.provider      AS provider
   FROM oauth_connections oc
   JOIN platform_accounts pa ON pa.oauth_connection_id = oc.id
  WHERE oc.status = 'active'
    AND oc.reauth_required_at IS NULL
    AND (
          (oc.last_refresh_at IS NULL
           AND oc.created_at < NOW() - ($1 || ' days')::interval)
       OR (oc.last_refresh_at < NOW() - ($1 || ' days')::interval)
       OR (oc.expires_at IS NOT NULL
           AND oc.expires_at <= NOW() + $2::interval)
        )
  ORDER BY oc.id, pa.id`

// ListDormantRefreshGrants returns one row per (platform_account,
// oauth_connection) whose refresh grant is at risk:
//
//   - account is active AND not already awaiting reauthorization
//     (reauth_required accounts need a human, not a refresh — the
//     refresh would only re-stamp the same flag),
//   - AND at least one of:
//     a) last_refresh_at is NULL and the grant was created more
//     than horizonDays ago — connected once, never published,
//     the canonical "dormant" cohort (NULL-safe fallback uses
//     created_at because last_refresh_at is only stamped on a
//     successful refresh),
//     b) last_refresh_at is older than horizonDays — was active,
//     went quiet, and is within striking distance of the
//     6-month inactivity GC,
//     c) expires_at is within refreshSweepTTLWindowDays of now —
//     the provider gave the grant an explicit TTL (Google's
//     refresh_token_expires_in) and it is about to lapse.
//
// horizonDays is the inactivity lookahead; the publish worker's
// default sweep horizon is 120 days (~4 months, leaving a 2-month
// margin under Google's 6-month GC).
//
// The JOIN yields one row per platform_account (a single YouTube
// grant can span multiple channels); the worker renews per account
// and the vault's per-grant advisory lock serialises concurrent
// renewals of the same grant.
func (r *RefreshSweepRepository) ListDormantRefreshGrants(ctx context.Context, horizonDays int) ([]models.DormantRefreshGrant, error) {
	if horizonDays <= 0 {
		horizonDays = DefaultRefreshSweepHorizonDays
	}
	rows, err := r.db.QueryContext(ctx,
		SQLListDormantRefreshGrants,
		horizonDays, fmt.Sprintf("%d days", refreshSweepTTLWindowDays),
	)
	if err != nil {
		return nil, fmt.Errorf("refresh sweep: list dormant grants: %w", err)
	}
	defer rows.Close()

	var out []models.DormantRefreshGrant
	for rows.Next() {
		var g models.DormantRefreshGrant
		if err := rows.Scan(&g.OAuthConnectionID, &g.PlatformAccountID, &g.Provider); err != nil {
			return nil, fmt.Errorf("refresh sweep: scan dormant grant: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("refresh sweep: iterate dormant grants: %w", err)
	}
	return out, nil
}

// DefaultRefreshSweepHorizonDays is the fallback inactivity horizon
// for the sweep (4 months — 2 months of margin under Google's
// ~6-month refresh-token inactivity GC). Config-driven in production
// via TOKEN_REFRESH_SWEEP_HORIZON_DAYS; the worker and repository
// share this default so a zero-value config falls back consistently.
const DefaultRefreshSweepHorizonDays = 120
