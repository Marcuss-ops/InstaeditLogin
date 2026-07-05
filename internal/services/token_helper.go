package services

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

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
func (h *TokenHelper) SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error {
	encrypted, err := h.encryptor.Encrypt(tokenData.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	expiresAt := time.Now().Add(time.Duration(tokenData.ExpiresIn) * time.Second)
	token := &models.Token{
		PlatformAccountID: platformAccountID,
		TokenType:         tokenData.TokenType,
		EncryptedToken:    encrypted,
		ExpiresAt:         &expiresAt,
		Scopes:            tokenData.Scopes,
	}

	return h.tokenRepo.SaveToken(token)
}

// GetDecryptedToken retrieves and decrypts the latest token for a platform account.
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
