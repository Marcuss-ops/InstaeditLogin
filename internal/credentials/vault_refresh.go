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

const refreshOperationTimeout = 30 * time.Second

// refreshGrantExpiryWarningWindow is the lookahead before a provider-issued
// refresh-token expiry at which Renew emits a warning. It matches the 7-day
// default window used by the admin token-rotation health view so operators
// see a single consistent horizon.
const refreshGrantExpiryWarningWindow = 7 * 24 * time.Hour

// Renew returns a fresh token or refreshes it under the per-grant advisory
// lock. Application-level singleflight runs before the lock so concurrent
// requests for one oauth_connection_id do not queue duplicate transactions.
func (v *CredentialVault) Renew(ctx context.Context, platformAccountID int64, tokenType string, refresher TokenRefresher) (*models.OAuthToken, error) {
	return v.renew(ctx, platformAccountID, tokenType, refresher, nil)
}

// renew carries the optional synchronization observer used by package tests
// to prove concurrent callers joined the grant-keyed flight. Production calls
// Renew, which passes nil and has no observer side effects.
func (v *CredentialVault) renew(ctx context.Context, platformAccountID int64, tokenType string, refresher TokenRefresher, observer func(string)) (*models.OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Resolve the canonical grant before singleflight. This small lookup is
	// required to key the process-local flight by oauth_connection_id rather
	// than by platform account (several channels may share one grant).
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		return nil, err
	}
	window := RefreshWindow(oauthConnectionID)

	if tok, err := v.getByOAuthConnection(ctx, oauthConnectionID, platformAccountID, tokenType); err == nil {
		if tokenFreshForWindow(tok, v.clock(), window) {
			return tok, nil
		}
	} else if !isExpiryError(err) && !errors.Is(err, ErrModernGrantMissing) {
		return nil, err
	}

	key := fmt.Sprintf("renew:%d:%s", oauthConnectionID, tokenType)
	resultCh := v.renewFlight.DoChan(key, func() (any, error) {
		// Detach cancellation from the leader so its disconnect does not
		// abort work needed by concurrent waiters, while retaining any
		// request deadline as an upper bound.
		workCtx, cancel := contextWithoutCancelWithDeadline(ctx)
		defer cancel()
		return nil, v.renewUnderGrantLock(workCtx, platformAccountID, tokenType, refresher, oauthConnectionID, window)
	})
	if observer != nil {
		observer(key)
	}
	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		// Do not return decrypted token material from singleflight. Read the
		// committed row after completion, keeping secret lifetime out of the
		// flight result and honoring the caller's own cancellation. Use the
		// public read path so lazy ciphertext re-encryption remains intact.
		return v.Get(ctx, platformAccountID, tokenType)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func contextWithoutCancelWithDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	// WithoutCancel preserves request-scoped values while preventing the
	// leader's cancellation from aborting work shared with waiters. Keep
	// the caller deadline when present, but cap detached work so a provider
	// without a response cannot leave the flight resident forever.
	base := context.WithoutCancel(ctx)
	maxDeadline := time.Now().Add(refreshOperationTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(maxDeadline) {
		maxDeadline = deadline
	}
	return context.WithDeadline(base, maxDeadline)
}

func tokenFreshForWindow(tok *models.OAuthToken, now time.Time, window time.Duration) bool {
	return tok != nil && (tok.ExpiresAt == nil || tok.ExpiresAt.Sub(now) > window)
}

// renewUnderGrantLock owns the transaction-scoped PostgreSQL advisory lock.
func (v *CredentialVault) renewUnderGrantLock(ctx context.Context, platformAccountID int64, tokenType string, refresher TokenRefresher, oauthConnectionID int64, window time.Duration) error {
	lockTx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vault: begin lock tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = lockTx.Rollback()
		}
	}()

	// Lock the platform row while confirming the same canonical grant. A
	// reconnect/grant swap cannot race the refresh decision.
	var lockedConnectionID int64
	if err := lockTx.QueryRowContext(ctx,
		`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`,
		platformAccountID,
	).Scan(&lockedConnectionID); err != nil {
		return fmt.Errorf("vault: resolve oauth_connection_id for renew: %w", err)
	}
	if lockedConnectionID != oauthConnectionID {
		return fmt.Errorf("vault: OAuth connection changed during refresh")
	}
	if _, err := lockTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", oauthConnectionID); err != nil {
		return fmt.Errorf("vault: acquire advisory lock: %w", err)
	}

	stored, err := v.store.FindLatestToken(oauthConnectionID, tokenType)
	if err != nil {
		return fmt.Errorf("vault: find inside lock: %w", err)
	}
	if tokenFreshForStored(stored, v.clock(), window) {
		if err := lockTx.Commit(); err != nil {
			return fmt.Errorf("vault: commit lock tx: %w", err)
		}
		committed = true
		return nil
	}
	if stored == nil {
		return fmt.Errorf("vault: %w for account %d (oauth_connection=%d)", ErrModernGrantMissing, platformAccountID, oauthConnectionID)
	}

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

	refreshToken, err := v.extractRefreshMaterial(stored, tokenType)
	if err != nil {
		return err
	}
	clientKey := v.resolveOAuthClientKey(ctx, lockTx, oauthConnectionID)
	refreshCtx := WithOAuthClientKey(ctx, clientKey)

	newTokenData, err := refresher(refreshCtx, refreshToken)
	if err != nil {
		status, code := classifyRefreshFailure(err)
		if errors.Is(err, ErrInvalidGrant) {
			v.recordInvalidGrantMetric(ctx, lockTx, oauthConnectionID)
			statusStore, ok := v.store.(InvalidGrantTxStore)
			if !ok {
				_ = lockTx.Rollback()
				committed = true
				return fmt.Errorf("vault: invalid_grant propagation unavailable: %w", err)
			}
			if statusErr := statusStore.MarkInvalidGrantTx(ctx, lockTx, oauthConnectionID, SharedGrantReauthRequiredCode, InvalidGrantAccountErrorMessage); statusErr != nil {
				return fmt.Errorf("vault: propagate invalid_grant state: %w", statusErr)
			}
			if err := lockTx.Commit(); err != nil {
				return fmt.Errorf("vault: commit invalid_grant state: %w", err)
			}
			committed = true
			return fmt.Errorf("vault: refresh failed: %w", err)
		}
		_ = lockTx.Rollback()
		committed = true
		if statusErr := v.updateGrantStatus(ctx, oauthConnectionID, status, code); statusErr != nil {
			return fmt.Errorf("vault: refresh failed: %w (grant status update failed: %v)", err, statusErr)
		}
		return fmt.Errorf("vault: refresh failed: %w", err)
	}

	if err := v.saveForOAuthConnectionTx(ctx, lockTx, oauthConnectionID, platformAccountID, newTokenData, false, stored); err != nil {
		_ = lockTx.Rollback()
		committed = true
		if statusErr := v.updateGrantStatus(ctx, oauthConnectionID, "error", "persist_failed"); statusErr != nil {
			return fmt.Errorf("vault: persist refreshed token: %w (grant status update failed: %v)", err, statusErr)
		}
		return fmt.Errorf("vault: persist refreshed token: %w", err)
	}

	statusInTx := false
	if statusStore, ok := v.store.(GrantStatusTxStore); ok {
		if err := statusStore.UpdateOAuthConnectionStatusTx(ctx, lockTx, oauthConnectionID, models.AccountStatusActive, ""); err != nil {
			_ = lockTx.Rollback()
			committed = true
			return fmt.Errorf("vault: update refresh status in lock tx: %w", err)
		}
		statusInTx = true
	}
	if err := lockTx.Commit(); err != nil {
		return fmt.Errorf("vault: commit lock tx: %w", err)
	}
	committed = true
	if !statusInTx {
		if err := v.updateGrantStatus(ctx, oauthConnectionID, models.AccountStatusActive, ""); err != nil {
			return fmt.Errorf("vault: refreshed token committed but grant status update failed: %w", err)
		}
	}
	return nil
}

func tokenFreshForStored(stored *models.Token, now time.Time, window time.Duration) bool {
	if stored == nil {
		return false
	}
	expiresAt := stored.AccessTokenExpiresAt
	if expiresAt == nil {
		expiresAt = stored.ExpiresAt
	}
	return expiresAt == nil || expiresAt.Sub(now) > window
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
// the grant's (google subject, pool client). Best-effort and never allowed
// to fail the refresh path.
func (v *CredentialVault) recordInvalidGrantMetric(ctx context.Context, tx *sql.Tx, oauthConnectionID int64) {
	clientKey := "youtube_pool_a"
	var subject sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT oc.oauth_client_key, oc.provider_subject_id
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`,
		oauthConnectionID,
	).Scan(&clientKey, &subject)
	if errors.Is(err, sql.ErrNoRows) {
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
	metrics.RecordYouTubeOAuthInvalidGrant(subject.String, clientKey)
}
