package services

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// OAuthProvider handles the OAuth authentication flow for a social platform.
type OAuthProvider interface {
	// GetPlatform returns the platform identifier (e.g., "meta", "tiktok").
	GetPlatform() string

	// GetLoginURL builds the OAuth authorization URL for user redirection.
	GetLoginURL(state string) string

	// HandleCallback processes the full OAuth callback flow:
	// 1. Exchange code for token
	// 2. Fetch user profile
	// 3. Fetch platform accounts (if applicable)
	// Returns the profile and token data.
	HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error)
}

// ContentPublisher publishes content to a social platform.
type ContentPublisher interface {
	// Publish publishes content and returns the platform media ID.
	Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
}

// TokenManager handles token encryption, storage, and retrieval.
type TokenManager interface {
	// SaveEncryptedToken encrypts and persists a token for a platform account.
	SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error

	// GetDecryptedToken retrieves and decrypts the latest token for a platform account.
	GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error)
}

// PlatformService combines all platform capabilities into one interface.
type PlatformService interface {
	OAuthProvider
	ContentPublisher
	TokenManager
}
