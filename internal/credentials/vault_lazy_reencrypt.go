package credentials

// Access-token lookup, decryption, and best-effort lazy re-encryption.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Get decrypts the latest token and upgrades stale ciphertext best-effort;
// re-encryption failures never change the read contract.
func (v *CredentialVault) Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		return nil, err
	}
	return v.getByOAuthConnection(ctx, oauthConnectionID, platformAccountID, tokenType)
}

// getByOAuthConnection is shared by the refresh coordinator after it has
// already resolved the grant. It retains the public Get path's lazy
// re-encryption behavior while avoiding a second account-to-grant lookup.
func (v *CredentialVault) getByOAuthConnection(ctx context.Context, oauthConnectionID, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	stored, err := v.store.FindLatestToken(oauthConnectionID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to find token: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("vault: %w for account %d (type: %s)", ErrModernGrantMissing, platformAccountID, tokenType)
	}
	accessExpiresAt := stored.AccessTokenExpiresAt
	if accessExpiresAt == nil {
		accessExpiresAt = stored.ExpiresAt
	}
	if accessExpiresAt != nil && v.clock().After(*accessExpiresAt) {
		return nil, fmt.Errorf("vault: token expired at %s", accessExpiresAt.Format(time.RFC3339))
	}
	accessCiphertext := stored.EncryptedAccessToken
	if len(accessCiphertext) == 0 {
		accessCiphertext = stored.EncryptedToken
	}
	decrypted, err := v.encryptor.Decrypt(accessCiphertext)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to decrypt access token: %w", err)
	}
	if v.encryptor.NeedsRotation(accessCiphertext) {
		newCiphertext, reencErr := v.encryptor.Encrypt(decrypted)
		if reencErr != nil {
			slog.Warn("vault: lazy re-encrypt failed (will retry on next read)",
				"token_id", stored.ID, "error", reencErr)
		} else if err := v.store.UpdateCiphertexts(stored.ID, accessCiphertext, newCiphertext); err != nil {
			if strings.Contains(err.Error(), "ciphertext stale") {
				slog.Debug("vault: lazy re-encrypt race-loser (another worker already upgraded)",
					"token_id", stored.ID)
			} else {
				slog.Warn("vault: lazy re-encrypt persist failed (read still returned)",
					"token_id", stored.ID, "error", err)
			}
		}
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
