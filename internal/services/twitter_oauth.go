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

// TwitterOAuthService implements OAuthProvider and ContentPublisher for Twitter/X.
type TwitterOAuthService struct {
	cfg        *config.Config
	userRepo   *repository.UserRepository
	*TokenHelper
	httpClient *http.Client
}

// NewTwitterOAuthService creates a new TwitterOAuthService.
func NewTwitterOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*TwitterOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &TwitterOAuthService{
		cfg:         cfg,
		userRepo:    userRepo,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *TwitterOAuthService) GetPlatform() string { return models.PlatformTwitter }

func (s *TwitterOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.TwitterClientID)
	params.Set("redirect_uri", s.cfg.TwitterRedirectURI)
	params.Set("state", state)
	params.Set("scope", "tweet.read tweet.write users.read offline.access")
	params.Set("response_type", "code")
	params.Set("code_challenge", "challenge")
	params.Set("code_challenge_method", "plain")

	return "https://twitter.com/i/oauth2/authorize?" + params.Encode()
}

func (s *TwitterOAuthService) HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Twitter: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(code)
	if err != nil {
		return nil, nil, fmt.Errorf("twitter token exchange: %w", err)
	}

	slog.Info("Twitter: fetching user info")
	profile, err := s.getUserInfo(tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("twitter user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scopes:       strings.Split(tokenResp.Scope, " "),
	}

	return profile, tokenData, nil
}

// RefreshOAuthToken exchanges a Twitter refresh token for a new access token.
func (s *TwitterOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("twitter RefreshOAuthToken: empty refresh token")
	}
	slog.Info("Twitter: refreshing access token")
	body := url.Values{}
	body.Set("client_id", s.cfg.TwitterClientID)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.twitter.com/2/oauth2/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.cfg.TwitterClientID, s.cfg.TwitterClientSecret)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twitter refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr twitterTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("twitter refresh parse: %w", err)
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}
	return &models.TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: refresh,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tr.ExpiresIn,
		Scopes:       strings.Split(tr.Scope, " "),
	}, nil
}

func (s *TwitterOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	if payload.Text == "" && payload.ImageURL == "" {
		return nil, fmt.Errorf("twitter requires text or image_url")
	}

	slog.Info("Twitter: publishing tweet")

	tweetBody := map[string]interface{}{
		"text": payload.Text,
	}

	// If there's an image, we'd need to upload it first via media/upload
	// For now, just support text tweets
	jsonBody, _ := json.Marshal(tweetBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.twitter.com/2/tweets",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("twitter tweet request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter tweet failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("twitter tweet returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("twitter tweet parse: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: result.Data.ID,
		PlatformURL:     "https://twitter.com/i/status/" + result.Data.ID,
	}, nil
}

// --- Private ---

type twitterTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

func (s *TwitterOAuthService) exchangeCodeForToken(code string) (*twitterTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.TwitterClientID)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.TwitterRedirectURI)
	body.Set("code_verifier", "challenge")

	req, err := http.NewRequest("POST", "https://api.twitter.com/2/oauth2/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.cfg.TwitterClientID, s.cfg.TwitterClientSecret)

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr twitterTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *TwitterOAuthService) getUserInfo(accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequest("GET",
		"https://api.twitter.com/2/users/me?user.fields=id,name,username", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID: result.Data.ID,
		Username:       result.Data.Username,
		Name:           result.Data.Name,
	}, nil
}
