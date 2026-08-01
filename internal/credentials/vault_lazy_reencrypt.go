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
	stored, err := v.store.FindLatestToken(oauthConnectionID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to find token: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("vault: no token for account %d (type: %s)", platformAccountID, tokenType)
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
	// Lazy re-encrypt: idempotent + race-safe (see godoc).
	if v.encryptor.NeedsRotation(accessCiphertext) {
		newCiphertext, reencErr := v.encryptor.Encrypt(decrypted)
		if reencErr != nil {
			// Best-effort: log and continue. The read still
			// succeeds; a future read will retry the re-encrypt.
			slog.Warn("vault: lazy re-encrypt failed (will retry on next read)",
				"token_id", stored.ID, "error", reencErr)
		} else if err := v.store.UpdateCiphertexts(stored.ID, accessCiphertext, newCiphertext); err != nil {
			// Log-level split (Blocco #2.2 follow-up):
			//   - "ciphertext stale" is the EXPECTED race-loser
			//     case (concurrent workers, only one wins the
			//     optimistic-concurrency UPDATE). High rate
			//     under load → Debug (operators can re-enable
			//     for forensic investigation, default off in prod).
			//   - Anything else is a real DB error worth a
			//     breadcrumb at Warn level.
			// The read still returns the decrypted value either
			// way — the persist is a best-effort background
			// upgrade, not part of the read contract.
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
