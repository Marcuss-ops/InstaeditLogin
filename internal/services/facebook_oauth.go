package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// FacebookOAuthService handles the OAuth 2.0 flow with Meta/Facebook.
type FacebookOAuthService struct {
	cfg          *config.Config
	userRepo     *repository.UserRepository
	tokenRepo    *repository.TokenRepository
	encryptor    *crypto.Encryptor
	httpClient   *http.Client
}

// NewFacebookOAuthService creates a new FacebookOAuthService.
// Returns an error if the encryption key is invalid.
func NewFacebookOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*FacebookOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &FacebookOAuthService{
		cfg:        cfg,
		userRepo:   userRepo,
		tokenRepo:  tokenRepo,
		encryptor:  encryptor,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// GetLoginURL builds the Meta OAuth login URL for user redirection.
func (s *FacebookOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.MetaAppID)
	params.Set("redirect_uri", s.cfg.MetaRedirectURI)
	params.Set("state", state)
	params.Set("scope", "instagram_basic,instagram_content_publish,pages_show_list,pages_read_engagement")
	params.Set("response_type", "code")

	return "https://www.facebook.com/v19.0/dialog/oauth?" + params.Encode()
}

// ExchangeCodeForToken exchanges an OAuth authorization code for a short-lived access token.
func (s *FacebookOAuthService) ExchangeCodeForToken(code string) (*models.MetaTokenResponse, error) {
	params := url.Values{}
	params.Set("client_id", s.cfg.MetaAppID)
	params.Set("client_secret", s.cfg.MetaAppSecret)
	params.Set("redirect_uri", s.cfg.MetaRedirectURI)
	params.Set("code", code)

	req, err := http.NewRequest("GET",
		"https://graph.facebook.com/v19.0/oauth/access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

// ExchangeForLongLivedToken exchanges a short-lived token for a long-lived one.
func (s *FacebookOAuthService) ExchangeForLongLivedToken(shortLivedToken string) (*models.MetaLongLivedTokenResponse, error) {
	params := url.Values{}
	params.Set("grant_type", "fb_exchange_token")
	params.Set("client_id", s.cfg.MetaAppID)
	params.Set("client_secret", s.cfg.MetaAppSecret)
	params.Set("fb_exchange_token", shortLivedToken)

	req, err := http.NewRequest("GET",
		"https://graph.facebook.com/v19.0/oauth/access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create long-lived token request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("long-lived token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read long-lived token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("long-lived token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaLongLivedTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse long-lived token response: %w", err)
	}

	return &tokenResp, nil
}

// GetUserInfo fetches the Meta user profile using an access token.
func (s *FacebookOAuthService) GetUserInfo(accessToken string) (*models.User, error) {
	params := url.Values{}
	params.Set("fields", "id,name,email")
	params.Set("access_token", accessToken)

	resp, err := s.httpClient.Get(
		"https://graph.facebook.com/v19.0/me?" + params.Encode(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read user info: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}

	return &models.User{
		MetaUserID: result.ID,
		Name:       result.Name,
		Email:      result.Email,
	}, nil
}

// GetInstagramAccounts fetches Instagram Business accounts linked to a Meta user.
func (s *FacebookOAuthService) GetInstagramAccounts(accessToken, metaUserID string) ([]*models.InstagramAccount, error) {
	params := url.Values{}
	params.Set("fields", "instagram_business_account{id,username}")
	params.Set("access_token", accessToken)

	resp, err := s.httpClient.Get(
		"https://graph.facebook.com/v19.0/" + metaUserID + "?" + params.Encode(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get instagram accounts: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read instagram accounts: %w", err)
	}

	slog.Debug("Instagram accounts response", "body", string(body))

	// Parse the nested response
	var result struct {
		InstagramBusinessAccount struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"instagram_business_account"`
	}

	// Try parsing; if the field is absent, return empty slice
	if err := json.Unmarshal(body, &result); err != nil {
		// Might not have an Instagram business account linked
		return nil, nil
	}

	if result.InstagramBusinessAccount.ID == "" {
		return nil, nil
	}

	return []*models.InstagramAccount{{
		InstagramUserID: result.InstagramBusinessAccount.ID,
		Username:        result.InstagramBusinessAccount.Username,
	}}, nil
}

// HandleCallback processes the full OAuth callback flow:
// 1. Exchange code for short-lived token
// 2. Exchange for long-lived token
// 3. Fetch user info
// 4. Fetch Instagram accounts
// 5. Encrypt and save tokens
// 6. Return the user
func (s *FacebookOAuthService) HandleCallback(code string) (*models.User, error) {
	// Step 1: Exchange code for short-lived token
	slog.Info("Exchanging code for short-lived token")
	shortLived, err := s.ExchangeCodeForToken(code)
	if err != nil {
		return nil, fmt.Errorf("step 1 (code exchange): %w", err)
	}

	// Step 2: Exchange for long-lived token
	slog.Info("Exchanging for long-lived token")
	longLived, err := s.ExchangeForLongLivedToken(shortLived.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("step 2 (long-lived exchange): %w", err)
	}

	// Step 3: Fetch user info
	slog.Info("Fetching user info")
	metaUser, err := s.GetUserInfo(longLived.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("step 3 (user info): %w", err)
	}

	// Step 4: Find or create user in DB
	existingUser, err := s.userRepo.FindByMetaUserID(metaUser.MetaUserID)
	if err != nil {
		return nil, fmt.Errorf("step 4 (find user): %w", err)
	}

	var user *models.User
	if existingUser != nil {
		// Update existing user
		existingUser.Name = metaUser.Name
		existingUser.Email = metaUser.Email
		if err := s.userRepo.Update(existingUser); err != nil {
			return nil, fmt.Errorf("step 4 (update user): %w", err)
		}
		user = existingUser
	} else {
		// Create new user
		metaUser.Email = coalesce(metaUser.Email, "")
		metaUser.Name = coalesce(metaUser.Name, "")
		if err := s.userRepo.Create(metaUser); err != nil {
			return nil, fmt.Errorf("step 4 (create user): %w", err)
		}
		user = metaUser
	}

	// Step 5: Encrypt and save long-lived token
	encryptedToken, err := s.encryptor.Encrypt(longLived.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("step 5 (encrypt token): %w", err)
	}

	expiresAt := time.Now().Add(time.Duration(longLived.ExpiresIn) * time.Second)
	token := &models.Token{
		UserID:         user.ID,
		TokenType:      models.TokenTypeLongLived,
		EncryptedToken: encryptedToken,
		ExpiresAt:      &expiresAt,
		Scopes:         []string{"instagram_basic", "instagram_content_publish", "pages_show_list"},
	}

	if err := s.tokenRepo.SaveToken(token); err != nil {
		return nil, fmt.Errorf("step 5 (save token): %w", err)
	}

	// Step 6: Fetch and save Instagram accounts
	accounts, err := s.GetInstagramAccounts(longLived.AccessToken, user.MetaUserID)
	if err != nil {
		slog.Warn("Failed to fetch Instagram accounts (non-fatal)", "error", err)
	} else {
		for _, acc := range accounts {
			acc.UserID = user.ID
			existing, _ := s.userRepo.FindInstagramAccount(acc.InstagramUserID)
			if existing == nil {
				if err := s.userRepo.CreateInstagramAccount(acc); err != nil {
					slog.Warn("Failed to save Instagram account", "error", err)
				}
			}
		}
	}

	slog.Info("OAuth callback completed successfully", "user_id", user.ID)
	return user, nil
}

// GetDecryptedToken retrieves and decrypts a user's token for API use.
func (s *FacebookOAuthService) GetDecryptedToken(userID int64, tokenType string) (*models.OAuthToken, error) {
	token, err := s.tokenRepo.FindLatestToken(userID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("failed to find token: %w", err)
	}
	if token == nil {
		return nil, fmt.Errorf("no token found for user %d (type: %s)", userID, tokenType)
	}

	// Check expiration
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

// coalesce returns the first non-empty string.
func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
