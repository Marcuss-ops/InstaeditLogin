package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TokenRefresher is defined in provider.go (same package). EnsureFreshToken
// takes one as a closure parameter so the platform-agnostic helper can call
// back into the provider's platform-specific refresh logic.

// TokenHelper provides shared token encryption, storage, and retrieval
// for all platform providers. Embed this in provider structs to satisfy
// the TokenManager interface without duplicating code.
type TokenHelper struct {
	encryptor *crypto.Encryptor
	tokenRepo *repository.TokenRepository
}

// NewTokenHelper creates a new TokenHelper.
func NewTokenHelper(encryptor *crypto.Encryptor, tokenRepo *repository.TokenRepository) *TokenHelper {
	return &TokenHelper{encryptor: encryptor, tokenRepo: tokenRepo}
}

// SaveEncryptedToken encrypts and persists a token for a platform account.
// The refresh token, when present in tokenData, is encrypted separately and
// stored in the same row to keep refresh semantics atomic with access tokens.
func (h *TokenHelper) SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error {
	encrypted, err := h.encryptor.Encrypt(tokenData.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	var encryptedRefresh []byte
	if tokenData.RefreshToken != "" {
		encryptedRefresh, err = h.encryptor.Encrypt(tokenData.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed to encrypt refresh token: %w", err)
		}
	}

	expiresAt := time.Now().Add(time.Duration(tokenData.ExpiresIn) * time.Second)
	token := &models.Token{
		PlatformAccountID:     platformAccountID,
		TokenType:             tokenData.TokenType,
		EncryptedToken:        encrypted,
		EncryptedRefreshToken: encryptedRefresh,
		ExpiresAt:             &expiresAt,
		Scopes:                tokenData.Scopes,
	}

	return h.tokenRepo.SaveToken(token)
}

// GetDecryptedToken retrieves and decrypts the latest token for a platform account.
// Expired tokens return an error containing "expired" so callers can react by refreshing.
func (h *TokenHelper) GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	token, err := h.tokenRepo.FindLatestToken(platformAccountID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("failed to find token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token found for platform account %d (type: %s)", platformAccountID, tokenType)
	}

	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		return nil, fmt.Errorf("token expired at %s", token.ExpiresAt.Format(time.RFC3339))
	}

	decrypted, err := h.encryptor.Decrypt(token.EncryptedToken)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt token: %w", err)
	}

	return &models.OAuthToken{
		AccessToken: decrypted,
		TokenType:   token.TokenType,
		ExpiresAt:   token.ExpiresAt,
		Scopes:      token.Scopes,
	}, nil
}

// EnsureFreshToken returns a valid (non-expired) decrypted token. If the
// stored token has expired, it calls the provided refresher, persists the new
// token + refresh token, and returns the freshly decrypted value.
//
// The 60s grace window before expiry avoids races where a publish request
// lands seconds before the official expiry.
func (h *TokenHelper) EnsureFreshToken(
	ctx context.Context,
	accountID int64,
	tokenType string,
	refresh TokenRefresher,
) (*models.OAuthToken, error) {
	oauthToken, err := h.GetDecryptedToken(accountID, tokenType)
	if err == nil {
		if oauthToken.ExpiresAt == nil || time.Until(*oauthToken.ExpiresAt) > 60*time.Second {
			return oauthToken, nil
		}
		// Within grace window: refresh proactively.
	} else if !isExpiryError(err) {
		return nil, err
	}

	// Fetch the latest token row to retrieve the encrypted refresh token.
	stored, err := h.tokenRepo.FindLatestToken(accountID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stored token for refresh: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("no stored token for account %d", accountID)
	}

	var refreshToken string
	if len(stored.EncryptedRefreshToken) > 0 {
		refreshToken, err = h.encryptor.Decrypt(stored.EncryptedRefreshToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt refresh token: %w", err)
		}
	} else if tokenType == models.TokenTypeLongLived {
		// Meta fallback: the long-lived access token itself serves as the
		// "refresh token" for the fb_exchange_token endpoint.
		refreshToken, err = h.encryptor.Decrypt(stored.EncryptedToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt current access token for meta re-exchange: %w", err)
		}
	} else {
		return nil, fmt.Errorf("token expired and no refresh token available for account %d (type %s)", accountID, tokenType)
	}

	newTokenData, err := refresh(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh failed: %w", err)
	}

	if err := h.SaveEncryptedToken(accountID, newTokenData); err != nil {
		return nil, fmt.Errorf("failed to persist refreshed token: %w", err)
	}

	return h.GetDecryptedToken(accountID, tokenType)
}

func isExpiryError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "expired")
}
