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

// TokenStorage is the narrow contract the HTTP router and publish worker
// use to talk to the token-encryption/refresh layer. It is implemented
// by *TokenService in production and by test mocks in pkg/api and
// internal/worker. Defining the interface here (alongside the concrete
// type) lets the consumers depend on behaviour, not on the concrete
// encryptor + token-repo wiring that *TokenService carries internally.
//
// Taglio 2.1 motivation: the old PlatformService composition included a
// TokenManager, forcing every provider to carry an embedded *TokenHelper
// and every consumer to depend on the composite interface. Splitting
// the token-encryption concern out of the per-provider concern is what
// makes CapabilityRouter possible.
type TokenStorage interface {
	// SaveEncryptedToken encrypts and persists a token for a platform account.
	SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error
	// GetDecryptedToken retrieves and decrypts the latest token for a platform
	// account. Expired tokens return an error containing "expired" so callers
	// can react by refreshing.
	GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error)
	// EnsureFreshToken returns a valid (non-expired) decrypted token, calling
	// the platform's RefreshOAuthToken when within the expiry grace window.
	EnsureFreshToken(ctx context.Context, accountID int64, tokenType string, refresh OAuthProvider) (*models.OAuthToken, error)
}

// Compile-time check: *TokenService must satisfy TokenStorage so consumers
// can pass it directly without an adapter. A drift here (e.g. a signature
// change that doesn't propagate) is a build error, not a runtime panic.
var _ TokenStorage = (*TokenService)(nil)

// TokenService is the shared, infrastructure-level token encryption and
// retrieval service. It is NOT a per-provider capability — every provider
// shares the same encrypted-token schema, so the logic is centralised here
// and injected wherever it's needed (the OAuth callback handler, the
// publish worker, and the per-provider constructor that no longer embeds
// the old TokenHelper).
//
// Taglio 2.1 lifts this out of the per-provider concern: the old
// PlatformService composition included TokenManager, forcing every
// provider to carry an embedded *TokenHelper. With the new design, each
// provider gets only the 5 user-facing capabilities it actually
// supports, and the OAuth callback handler / publish worker call this
// service directly.
type TokenService struct {
	encryptor *crypto.Encryptor
	tokenRepo *repository.TokenRepository
}

// NewTokenService constructs a TokenService.
func NewTokenService(encryptor *crypto.Encryptor, tokenRepo *repository.TokenRepository) *TokenService {
	return &TokenService{encryptor: encryptor, tokenRepo: tokenRepo}
}

// SaveEncryptedToken encrypts and persists a token for a platform account.
// The refresh token, when present in tokenData, is encrypted separately
// and stored in the same row to keep refresh semantics atomic with access
// tokens.
func (s *TokenService) SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error {
	encrypted, err := s.encryptor.Encrypt(tokenData.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to encrypt token: %w", err)
	}

	var encryptedRefresh []byte
	if tokenData.RefreshToken != "" {
		encryptedRefresh, err = s.encryptor.Encrypt(tokenData.RefreshToken)
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

	return s.tokenRepo.SaveToken(token)
}

// GetDecryptedToken retrieves and decrypts the latest token for a platform
// account. Expired tokens return an error containing "expired" so callers
// can react by refreshing.
func (s *TokenService) GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	token, err := s.tokenRepo.FindLatestToken(platformAccountID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("failed to find token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token found for platform account %d (type: %s)", platformAccountID, tokenType)
	}

	if token.ExpiresAt != nil && time.Now().After(*token.ExpiresAt) {
		return nil, fmt.Errorf("token expired at %s", token.ExpiresAt.Format(time.RFC3339))
	}

	decrypted, err := s.encryptor.Decrypt(token.EncryptedToken)
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
// stored token is within the 60s grace window of expiry, it calls the
// platform's RefreshOAuthToken, persists the result, and returns the
// freshly-decrypted value.
//
// Taglio 2.1: the refresher argument is now an OAuthProvider (looked up
// from the CapabilityRouter by the caller) rather than a TokenRefresher
// closure. This keeps the per-provider concern focused on its 5
// capabilities — token-encryption logic doesn't leak into the platform
// code anymore.
func (s *TokenService) EnsureFreshToken(
	ctx context.Context,
	accountID int64,
	tokenType string,
	refresh OAuthProvider,
) (*models.OAuthToken, error) {
	oauthToken, err := s.GetDecryptedToken(accountID, tokenType)
	if err == nil {
		if oauthToken.ExpiresAt == nil || time.Until(*oauthToken.ExpiresAt) > 60*time.Second {
			return oauthToken, nil
		}
		// Within grace window: refresh proactively.
	} else if !isExpiryError(err) {
		return nil, err
	}

	// Fetch the latest token row to retrieve the encrypted refresh token.
	stored, err := s.tokenRepo.FindLatestToken(accountID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch stored token for refresh: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("no stored token for account %d", accountID)
	}

	var refreshToken string
	if len(stored.EncryptedRefreshToken) > 0 {
		refreshToken, err = s.encryptor.Decrypt(stored.EncryptedRefreshToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt refresh token: %w", err)
		}
	} else if tokenType == models.TokenTypeLongLived {
		// Meta fallback: the long-lived access token itself serves as the
		// "refresh token" for the fb_exchange_token endpoint.
		refreshToken, err = s.encryptor.Decrypt(stored.EncryptedToken)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt current access token for meta re-exchange: %w", err)
		}
	} else {
		return nil, fmt.Errorf("token expired and no refresh token available for account %d (type %s)", accountID, tokenType)
	}

	newTokenData, err := refresh.RefreshOAuthToken(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("refresh failed: %w", err)
	}

	if err := s.SaveEncryptedToken(accountID, newTokenData); err != nil {
		return nil, fmt.Errorf("failed to persist refreshed token: %w", err)
	}

	return s.GetDecryptedToken(accountID, tokenType)
}

func isExpiryError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "expired")
}
