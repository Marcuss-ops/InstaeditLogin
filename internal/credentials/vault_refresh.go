package credentials

// Refresh coordination, advisory locking, and grant-status persistence.

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
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
		return nil, fmt.Errorf("vault: no stored token for account %d (oauth_connection=%d)", platformAccountID, oauthConnectionID)
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

	newTokenData, err := refresher(ctx, refreshToken)
	if err != nil {
		status, code := classifyRefreshFailure(err)
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
