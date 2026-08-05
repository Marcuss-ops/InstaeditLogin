package credentials

// Refresh coordination, advisory locking, and grant-status persistence.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// refreshGrantExpiryWarningWindow is the lookahead before a provider-issued
// refresh-token expiry at which Renew emits a warning. It matches the 7-day
// default window used by the admin token-rotation health view so operators
// see a single consistent horizon.
const refreshGrantExpiryWarningWindow = 7 * 24 * time.Hour

// Renew returns a fresh token or refreshes it under the per-grant advisory
// lock, preserving the existing error classification and status updates.
func (v *CredentialVault) Renew(ctx context.Context, platformAccountID int64, tokenType string, refresher TokenRefresher) (*models.OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Fast path: token is already fresh, no DB lock needed. Get handles
	// the oauth_connection_id lookup internally.
	if tok, err := v.Get(ctx, platformAccountID, tokenType); err == nil {
		if tok.ExpiresAt == nil || tok.ExpiresAt.Sub(v.clock()) > 60*time.Second {
			return tok, nil
		}
		// Within grace window: fall through to refresh.
	} else if !isExpiryError(err) {
		// Non-expiry error (decrypt failure, DB unreachable, …): surface it.
		return nil, err
	}

	// Slow path: open a short-lived tx so the advisory lock is
	// transaction-scoped. Inside the tx we (a) look up the canonical
	// oauth_connection_id with a row-level lock on platform_accounts
	// (so a concurrent grant swap is blocked), (b) acquire the advisory
	// lock keyed on the resolved oid.
	lockTx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("vault: begin lock tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = lockTx.Rollback()
		}
	}()
	var oauthConnectionID int64
	if err := lockTx.QueryRowContext(ctx,
		`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`,
		platformAccountID,
	).Scan(&oauthConnectionID); err != nil {
		return nil, fmt.Errorf("vault: resolve oauth_connection_id for renew: %w", err)
	}
	if _, err := lockTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", oauthConnectionID); err != nil {
		return nil, fmt.Errorf("vault: acquire advisory lock: %w", err)
	}

	// Re-read inside the lock. Another worker may have just refreshed.
	// Reuse the resolved oid — same acq, no extra SELECT.
	stored, err := v.store.FindLatestToken(oauthConnectionID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("vault: find inside lock: %w", err)
	}
	if stored != nil && (stored.ExpiresAt == nil || stored.ExpiresAt.Sub(v.clock()) > 60*time.Second) {
		if err := lockTx.Commit(); err != nil {
			return nil, fmt.Errorf("vault: commit lock tx: %w", err)
		}
		committed = true
		return v.toOAuthToken(stored)
	}
	if stored == nil {
		return nil, fmt.Errorf("vault: %w for account %d (oauth_connection=%d)", ErrModernGrantMissing, platformAccountID, oauthConnectionID)
	}

	// P3 observability: warn when the stored refresh grant is close to its
	// provider-issued expiry so operators can reconnect before Google
	// garbage-collects it. Access-token freshness is decided above; this is
	// a pure signal that never alters the refresh path. Deliberately
	// slow-path only: a fresh access token returns early without reading
	// the stored row, and the refresh grant is only exercised once the
	// access token goes stale anyway.
	if stored.RefreshTokenExpiresAt != nil {
		if remaining := stored.RefreshTokenExpiresAt.Sub(v.clock()); remaining <= refreshGrantExpiryWarningWindow {
			if v.logger != nil {
				v.logger.Warn("oauth refresh grant is nearing provider expiry",
					"platform_account_id", platformAccountID,
					"oauth_connection_id", oauthConnectionID,
					"refresh_token_expires_in_hours", int64(remaining.Hours()),
				)
			}
		}
	}

	// We own the refresh. Read the stored row is already in `stored`
	// from the re-read above — pass it directly to extractRefreshMaterial.
	// Re-finding would just re-pay the same lookup cost for no new info.
	refreshToken, err := v.extractRefreshMaterial(stored, tokenType)
	if err != nil {
		return nil, err
	}

	// R4 — YouTube OAuth Client Pool: resolve the grant's pool client
	// key inside the lock tx (platform_account_id → oauth_connection_id
	// → oauth_client_key) and stamp it on the ctx handed to the
	// refresher. The refresher (services layer) Resolves that key
	// through the pool registry and refreshes with the SAME client_id +
	// client_secret that issued the token — never a different one.
	// Best-effort: a pre-migration-099 database has no oauth_client_key
	// column and falls back to the legacy label at DEBUG without
	// failing the refresh (mirrors recordInvalidGrantMetric).
	clientKey := v.resolveOAuthClientKey(ctx, lockTx, oauthConnectionID)
	refreshCtx := WithOAuthClientKey(ctx, clientKey)

	newTokenData, err := refresher(refreshCtx, refreshToken)
	if err != nil {
		status, code := classifyRefreshFailure(err)
		if errors.Is(err, ErrInvalidGrant) {
			// Observability: bump youtube_oauth_invalid_grant_total for
			// the pool client that issued this grant. Best-effort — on a
			// pre-migration-099 database the oauth_client_key column is
			// missing and the increment is skipped (DEBUG) rather than
			// failing the propagation below.
			v.recordInvalidGrantMetric(ctx, lockTx, oauthConnectionID)
			statusStore, ok := v.store.(InvalidGrantTxStore)
			if !ok {
				_ = lockTx.Rollback()
				committed = true
				return nil, fmt.Errorf("vault: invalid_grant propagation unavailable: %w", err)
			}
			if statusErr := statusStore.MarkInvalidGrantTx(ctx, lockTx, oauthConnectionID, SharedGrantReauthRequiredCode, InvalidGrantAccountErrorMessage); statusErr != nil {
				return nil, fmt.Errorf("vault: propagate invalid_grant state: %w", statusErr)
			}
			if err := lockTx.Commit(); err != nil {
				return nil, fmt.Errorf("vault: commit invalid_grant state: %w", err)
			}
			committed = true
			return nil, fmt.Errorf("vault: refresh failed: %w", err)
		}
		_ = lockTx.Rollback()
		committed = true
		if statusErr := v.updateGrantStatus(ctx, oauthConnectionID, status, code); statusErr != nil {
			return nil, fmt.Errorf("vault: refresh failed: %w (grant status update failed: %v)", err, statusErr)
		}
		return nil, fmt.Errorf("vault: refresh failed: %w", err)
	}

	// Save via the lookup-free sibling — the resolved oid is the
	// canonical key for this row.
	if err := v.saveForOAuthConnectionTx(ctx, lockTx, oauthConnectionID, platformAccountID, newTokenData, false, stored); err != nil {
		_ = lockTx.Rollback()
		committed = true
		if statusErr := v.updateGrantStatus(ctx, oauthConnectionID, "error", "persist_failed"); statusErr != nil {
			return nil, fmt.Errorf("vault: persist refreshed token: %w (grant status update failed: %v)", err, statusErr)
		}
		return nil, fmt.Errorf("vault: persist refreshed token: %w", err)
	}

	statusInTx := false
	if statusStore, ok := v.store.(GrantStatusTxStore); ok {
		if err := statusStore.UpdateOAuthConnectionStatusTx(ctx, lockTx, oauthConnectionID, models.AccountStatusActive, ""); err != nil {
			_ = lockTx.Rollback()
			committed = true
			return nil, fmt.Errorf("vault: update refresh status in lock tx: %w", err)
		}
		statusInTx = true
	}
	if err := lockTx.Commit(); err != nil {
		return nil, fmt.Errorf("vault: commit lock tx: %w", err)
	}
	committed = true
	if !statusInTx {
		if err := v.updateGrantStatus(ctx, oauthConnectionID, models.AccountStatusActive, ""); err != nil {
			return nil, fmt.Errorf("vault: refreshed token committed but grant status update failed: %w", err)
		}
	}

	// Final read — fresh ciphertext was just persisted; the stored row
	// is now the latest write by THIS transaction. Pass the just-written
	// id via a sealed re-read through Get (which resolves the oid again
	// — one extra SELECT, but kept simple and consistent with the read
	// contract for callers).
	return v.Get(ctx, platformAccountID, tokenType)
}

func (v *CredentialVault) toOAuthToken(stored *models.Token) (*models.OAuthToken, error) {
	accessCiphertext := stored.EncryptedAccessToken
	if len(accessCiphertext) == 0 {
		accessCiphertext = stored.EncryptedToken
	}
	decrypted, err := v.encryptor.Decrypt(accessCiphertext)
	if err != nil {
		return nil, fmt.Errorf("vault: decrypt stored token inside lock: %w", err)
	}
	accessExpiresAt := stored.AccessTokenExpiresAt
	if accessExpiresAt == nil {
		accessExpiresAt = stored.ExpiresAt
	}
	scopes := stored.GrantedScopes
	if len(scopes) == 0 {
		scopes = stored.Scopes
	}
	return &models.OAuthToken{
		AccessToken: decrypted,
		TokenType:   stored.TokenType,
		ExpiresAt:   accessExpiresAt,
		Scopes:      scopes,
	}, nil
}

func (v *CredentialVault) updateGrantStatus(ctx context.Context, oauthConnectionID int64, status, lastError string) error {
	store, ok := v.store.(GrantStatusStore)
	if !ok {
		return nil
	}
	return store.UpdateOAuthConnectionStatus(ctx, oauthConnectionID, status, lastError)
}

// resolveOAuthClientKey returns the oauth_client_key of the grant
// being refreshed, resolved inside the caller's lock tx. The key is
// part of the grant identity (migration 099): a refresh token must
// always be renewed with the SAME OAuth client that issued it, so the
// vault hands the key to the refresher instead of letting the
// refresher pick a client on its own.
//
// Best-effort and never allowed to fail the refresh path: on a
// pre-migration-099 database the oauth_client_key column does not
// exist and the lookup falls back to the legacy label (DEBUG).
func (v *CredentialVault) resolveOAuthClientKey(ctx context.Context, tx *sql.Tx, oauthConnectionID int64) string {
	var clientKey string
	err := tx.QueryRowContext(ctx,
		`SELECT oc.oauth_client_key
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`,
		oauthConnectionID,
	).Scan(&clientKey)
	if errors.Is(err, sql.ErrNoRows) {
		// Non-YouTube grant (the vault's Renew path is shared by
		// TikTok, Instagram, X, Drive, …): not this metric's
		// jurisdiction, no stamp, no log noise.
		return defaultYouTubeOAuthClientKey
	}
	if err != nil {
		if v.logger != nil {
			v.logger.Debug("oauth client key resolution skipped (oauth_client_key column may not exist on pre-migration-099 database)",
				"oauth_connection_id", oauthConnectionID, "error", err)
		}
		return defaultYouTubeOAuthClientKey
	}
	if clientKey == "" {
		return defaultYouTubeOAuthClientKey
	}
	return clientKey
}

// recordInvalidGrantMetric bumps youtube_oauth_invalid_grant_total for
// the grant's pool client (oauth_client_key). YouTube-ONLY: the vault's
// Renew path is platform-agnostic (shared by TikTok, Instagram, X, …),
// so the increment is gated on the connection's provider to avoid
// polluting a YouTube metric with other platforms' invalid_grants.
// Best-effort and never allowed to fail the refresh path: on a
// pre-migration-099 database the column does not exist (DEBUG skip),
// and a non-YouTube connection is skipped silently.
func (v *CredentialVault) recordInvalidGrantMetric(ctx context.Context, tx *sql.Tx, oauthConnectionID int64) {
	clientKey := "youtube_pool_a"
	err := tx.QueryRowContext(ctx,
		`SELECT oc.oauth_client_key
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`,
		oauthConnectionID,
	).Scan(&clientKey)
	if errors.Is(err, sql.ErrNoRows) {
		// Non-YouTube grant (or row already gone): not this metric's
		// jurisdiction. No increment, no log noise.
		return
	}
	if err != nil {
		if v.logger != nil {
			v.logger.Debug("youtube oauth invalid_grant metric skipped (oauth_client_key column may not exist on pre-migration-099 database)",
				"oauth_connection_id", oauthConnectionID, "error", err)
		}
		return
	}
	if clientKey == "" {
		clientKey = "youtube_pool_a"
	}
	metrics.RecordYouTubeOAuthInvalidGrant(clientKey)
}
