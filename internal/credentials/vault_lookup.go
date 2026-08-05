package credentials

// Grant lookup, refresh-token access, and revocation.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Token lookup, refresh-token access, and revocation remain keyed by the
// public platform account while resolving storage through oauth_connection_id.

// oauthConnectionIDForAccount resolves the canonical storage key for a
// platform account. The resolver is shared by every public vault operation;
// keeping it here makes the lookup boundary explicit without changing the
// compatibility-facing VaultAPI signatures.
func (v *CredentialVault) oauthConnectionIDForAccount(ctx context.Context, platformAccountID int64) (int64, error) {
	var oid int64
	if err := v.db.QueryRowContext(ctx,
		`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`,
		platformAccountID,
	).Scan(&oid); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("vault: no oauth_connection for platform_account %d (pre-043 attach or grant revoked)", platformAccountID)
		}
		return 0, fmt.Errorf("vault: resolve oauth_connection_id for platform_account %d: %w", platformAccountID, err)
	}
	return oid, nil
}

// GetRefreshToken decrypts the stored bearer refresh grant for Drive-style
// integrations without exposing storage details to the caller.
func (v *CredentialVault) GetRefreshToken(ctx context.Context, platformAccountID int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		return "", err
	}
	stored, err := v.store.FindLatestToken(oauthConnectionID, models.TokenTypeBearer)
	if err != nil || stored == nil {
		return "", fmt.Errorf("vault: refresh token not found for account %d: %w", platformAccountID, err)
	}
	if len(stored.EncryptedRefreshToken) == 0 {
		return "", fmt.Errorf("vault: refresh token empty for account %d", platformAccountID)
	}
	return v.encryptor.Decrypt(stored.EncryptedRefreshToken)
}

// GetRefreshTokenForOAuthConnectionTx decrypts a grant refresh token while the
// caller-owned transaction holds the grant lock. It is intentionally narrow:
// complete grant revocation uses it to coordinate provider revocation with the
// subsequent local cleanup.
func (v *CredentialVault) GetRefreshTokenForOAuthConnectionTx(ctx context.Context, tx *sql.Tx, oauthConnectionID int64) (string, error) {
	if tx == nil || oauthConnectionID <= 0 {
		return "", fmt.Errorf("vault: invalid OAuth connection transaction")
	}
	var encrypted []byte
	if err := tx.QueryRowContext(ctx,
		`SELECT encrypted_refresh_token
		   FROM tokens
		  WHERE oauth_connection_id = $1
		    AND token_type = $2
		  ORDER BY created_at DESC
		  LIMIT 1`,
		oauthConnectionID, models.TokenTypeBearer,
	).Scan(&encrypted); err != nil {
		return "", fmt.Errorf("vault: refresh token not found for OAuth connection %d: %w", oauthConnectionID, err)
	}
	if len(encrypted) == 0 {
		return "", fmt.Errorf("vault: refresh token empty for OAuth connection %d", oauthConnectionID)
	}
	return v.encryptor.Decrypt(encrypted)
}

// Revoke deletes all tokens for the resolved OAuth connection and remains
// idempotent for already-revoked token rows.
func (v *CredentialVault) Revoke(ctx context.Context, platformAccountID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		// Surface the pre-043-attach / grant-revoked state to the
		// caller; Save/Get/Renew use the same fail-loud contract.
		// The disconnect orchestrator (pkg/api) maps this to a 401
		// or "already-revoked" response — the silent-swallow moved up
		// the stack so the audit log has a single source of truth.
		return err
	}
	if err := v.store.DeleteAllTokensForOAuthConnection(oauthConnectionID); err != nil {
		// ErrTokenNotFound is the legitimate "already revoked" case.
		// The vault is idempotent, so we swallow it.
		if strings.Contains(err.Error(), "token not found") {
			return nil
		}
		return fmt.Errorf("vault: revoke failed: %w", err)
	}
	return nil
}
