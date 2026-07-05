package services

import (
	"context"
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

// FacebookOAuthService implements OAuthProvider and ContentPublisher for Meta/Facebook.
type FacebookOAuthService struct {
	cfg        *config.Config
	userRepo   *repository.UserRepository
	*TokenHelper
	httpClient *http.Client
}

// NewFacebookOAuthService creates a new FacebookOAuthService.
// Returns an error if the encryption key is invalid.
func NewFacebookOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*FacebookOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &FacebookOAuthService{
		cfg:         cfg,
		userRepo:    userRepo,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// GetPlatform returns the platform identifier.
func (s *FacebookOAuthService) GetPlatform() string { return models.PlatformMeta }

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

// HandleCallback processes the full OAuth callback for Meta/Facebook.
func (s *FacebookOAuthService) HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error) {
	// Step 1: Exchange code for short-lived token
	slog.Info("Meta: exchanging code for short-lived token")
	shortLived, err := s.exchangeCodeForToken(code)
	if err != nil {
		return nil, nil, fmt.Errorf("step 1 (code exchange): %w", err)
	}

	// Step 2: Exchange for long-lived token
	slog.Info("Meta: exchanging for long-lived token")
	longLived, err := s.exchangeForLongLivedToken(shortLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 2 (long-lived exchange): %w", err)
	}

	// Step 3: Fetch user info
	slog.Info("Meta: fetching user info")
	metaUser, err := s.getUserInfo(longLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 3 (user info): %w", err)
	}

	profile := &models.PlatformProfile{
		PlatformUserID: metaUser.PlatformUserID,
		Username:       metaUser.Username,
		Email:          metaUser.Email,
		Name:           metaUser.Name,
	}

	// Meta does not issue OAuth refresh tokens; TokenHelper falls back to
	// fb_exchange_token using the current access token when refresh is needed.
	tokenData := &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
		Scopes:      []string{"instagram_basic", "instagram_content_publish", "pages_show_list"},
	}

	return profile, tokenData, nil
}

// RefreshOAuthToken extends a long-lived Meta token using fb_exchange_token.
// The argument is the current long-lived access token; a fresh long-lived
// token (~60 days) is returned. If Meta rejects the exchange (e.g. the
// previous token has been expired for > 24 hours) the caller must re-authenticate.
func (s *FacebookOAuthService) RefreshOAuthToken(ctx context.Context, currentToken string) (*models.TokenData, error) {
	if currentToken == "" {
		return nil, fmt.Errorf("meta RefreshOAuthToken: empty current token")
	}
	slog.Info("Meta: refreshing long-lived token via fb_exchange_token")
	longLived, err := s.exchangeForLongLivedToken(currentToken)
	if err != nil {
		return nil, fmt.Errorf("meta refresh failed: %w", err)
	}
	return &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
	}, nil
}

// Publish publishes content to Instagram via the Meta Graph API.
func (s *FacebookOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	// First, get the Instagram business account for this user
	accounts, err := s.getInstagramAccounts(ctx, accessToken, platformUserID)
	if err != nil || len(accounts) == 0 {
		return nil, fmt.Errorf("no Instagram business account found for user %s", platformUserID)
	}

	instagramUserID := accounts[0].PlatformUserID
	slog.Info("Meta: publishing content", "instagram_user_id", instagramUserID)

	var mediaID string

	if payload.VideoURL != "" {
		mediaID, err = s.publishVideo(ctx, accessToken, instagramUserID, payload.VideoURL, payload.Text)
	} else if payload.ImageURL != "" {
		mediaID, err = s.publishPhoto(ctx, accessToken, instagramUserID, payload.ImageURL, payload.Text)
	} else {
		return nil, fmt.Errorf("media url required (image_url or video_url)")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to publish: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: mediaID,
	}, nil
}

// --- Private Meta-specific methods ---

func (s *FacebookOAuthService) exchangeCodeForToken(code string) (*models.MetaTokenResponse, error) {
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

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
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

func (s *FacebookOAuthService) exchangeForLongLivedToken(shortLivedToken string) (*models.MetaLongLivedTokenResponse, error) {
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

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
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

func (s *FacebookOAuthService) getUserInfo(accessToken string) (*models.PlatformProfile, error) {
	params := url.Values{}
	params.Set("fields", "id,name,email")
	params.Set("access_token", accessToken)

	req, err := http.NewRequest("GET", "https://graph.facebook.com/v19.0/me?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create user info request: %w", err)
	}

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
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

	return &models.PlatformProfile{
		PlatformUserID: result.ID,
		Username:       result.Name,
		Email:          result.Email,
		Name:           result.Name,
	}, nil
}

func (s *FacebookOAuthService) getInstagramAccounts(ctx context.Context, accessToken, metaUserID string) ([]*models.PlatformAccount, error) {
	params := url.Values{}
	params.Set("fields", "instagram_business_account{id,username}")
	params.Set("access_token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "GET", "https://graph.facebook.com/v19.0/"+metaUserID+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create instagram accounts request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get instagram accounts: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read instagram accounts: %w", err)
	}

	var result struct {
		InstagramBusinessAccount struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"instagram_business_account"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil
	}

	if result.InstagramBusinessAccount.ID == "" {
		return nil, nil
	}

	return []*models.PlatformAccount{{
		Platform:       models.PlatformMeta,
		PlatformUserID: result.InstagramBusinessAccount.ID,
		Username:       result.InstagramBusinessAccount.Username,
	}}, nil
}

func (s *FacebookOAuthService) publishPhoto(ctx context.Context, accessToken, instagramUserID, imageURL, caption string) (string, error) {
	body := map[string]string{
		"media_type": "IMAGE",
		"image_url":  imageURL,
		"caption":    caption,
	}

	containerID, err := s.createMediaContainer(ctx, accessToken, instagramUserID, body)
	if err != nil {
		return "", err
	}

	return s.publishMediaContainer(ctx, accessToken, instagramUserID, containerID)
}

func (s *FacebookOAuthService) publishVideo(ctx context.Context, accessToken, instagramUserID, videoURL, caption string) (string, error) {
	body := map[string]string{
		"media_type": "REELS",
		"video_url":  videoURL,
		"caption":    caption,
	}

	containerID, err := s.createMediaContainer(ctx, accessToken, instagramUserID, body)
	if err != nil {
		return "", err
	}

	if err := s.waitForContainerReady(ctx, accessToken, containerID); err != nil {
		return "", err
	}

	return s.publishMediaContainer(ctx, accessToken, instagramUserID, containerID)
}

func (s *FacebookOAuthService) createMediaContainer(ctx context.Context, accessToken, instagramUserID string, body map[string]string) (string, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("failed to marshal body: %w", err)
	}

	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/media?access_token=%s", instagramUserID, accessToken)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", fmt.Errorf("failed to create media container request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("media container request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read media container response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media container failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse media container response: %w", err)
	}

	return result.ID, nil
}

func (s *FacebookOAuthService) publishMediaContainer(ctx context.Context, accessToken, instagramUserID, containerID string) (string, error) {
	body := map[string]string{"creation_id": containerID}
	jsonBody, _ := json.Marshal(body)

	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/media_publish?access_token=%s", instagramUserID, accessToken)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", fmt.Errorf("failed to create media publish request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("media publish request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read media publish response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("media publish failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to parse media publish response: %w", err)
	}

	return result.ID, nil
}

func (s *FacebookOAuthService) waitForContainerReady(ctx context.Context, accessToken, containerID string) error {
	maxAttempts := 30
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return fmt.Errorf("container polling cancelled: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}

		reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s?fields=status_code&access_token=%s", containerID, accessToken)
		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			slog.Warn("Container status check request failed, retrying", "error", err, "attempt", i+1)
			continue
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			slog.Warn("Container status check failed, retrying", "error", err, "attempt", i+1)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result struct {
			StatusCode string `json:"status_code"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			continue
		}

		switch result.StatusCode {
		case "FINISHED":
			return nil
		case "ERROR":
			return fmt.Errorf("container processing failed: %s", string(body))
		}
	}

	return fmt.Errorf("container not ready after %d attempts", maxAttempts)
}
