package credentials

// Legacy refresh fallback and shared token metadata compatibility helpers.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// extractRefreshMaterial preserves the legacy Meta behavior of using a
// long-lived access token as refresh material when no refresh grant exists.
func (v *CredentialVault) extractRefreshMaterial(stored *models.Token, tokenType string) (string, error) {
	if len(stored.EncryptedRefreshToken) > 0 {
		decrypted, err := v.encryptor.Decrypt(stored.EncryptedRefreshToken)
		if err != nil {
			return "", fmt.Errorf("vault: decrypt refresh token: %w", err)
		}
		return decrypted, nil
	}
	if tokenType == models.TokenTypeLongLived {
		// Meta fallback: the long-lived access token itself serves as
		// the "refresh token" for fb_exchange_token.
		accessCiphertext := stored.EncryptedAccessToken
		if len(accessCiphertext) == 0 {
			accessCiphertext = stored.EncryptedToken
		}
		decrypted, err := v.encryptor.Decrypt(accessCiphertext)
		if err != nil {
			return "", fmt.Errorf("vault: decrypt access for meta re-exchange: %w", err)
		}
		return decrypted, nil
	}
	return "", fmt.Errorf("vault: token expired and no refresh token available for account %d (type %s)", stored.PlatformAccountID, tokenType)
}

func classifyRefreshFailure(err error) (status, code string) {
	// Typed sentinel first (the YouTube/Drive services wrap
	// ErrInvalidGrant when the provider body says invalid_grant); the
	// string fallback keeps older call sites and test refreshers that
	// still emit the literal working.
	if errors.Is(err, ErrInvalidGrant) || strings.Contains(strings.ToLower(err.Error()), "invalid_grant") {
		return models.AccountStatusReauthRequired, "invalid_grant"
	}
	return "error", "refresh_failed"
}

func nonEmptyScopeValues(scopes []string) []string {
	filtered := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope = strings.TrimSpace(scope); scope != "" {
			filtered = append(filtered, scope)
		}
	}
	return filtered
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func isExpiryError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "expired")
}
