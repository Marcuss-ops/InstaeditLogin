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

// PinterestOAuthService implements OAuthProvider and ContentPublisher for Pinterest.
type PinterestOAuthService struct {
	cfg        *config.Config
	userRepo   *repository.UserRepository
	*TokenHelper
	httpClient *http.Client
}

// NewPinterestOAuthService creates a new PinterestOAuthService.
func NewPinterestOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*PinterestOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &PinterestOAuthService{
		cfg:         cfg,
		userRepo:    userRepo,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *PinterestOAuthService) GetPlatform() string { return models.PlatformPinterest }

func (s *PinterestOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.PinterestAppID)
	params.Set("redirect_uri", s.cfg.PinterestRedirectURI)
	params.Set("state", state)
	params.Set("scope", "user_accounts:read,pins:read,pins:write")
	params.Set("response_type", "code")

	return "https://www.pinterest.com/oauth/?" + params.Encode()
}

func (s *PinterestOAuthService) HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Pinterest: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(code)
	if err != nil {
		return nil, nil, fmt.Errorf("pinterest token exchange: %w", err)
	}

	slog.Info("Pinterest: fetching user info")
	profile, err := s.getUserInfo(tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("pinterest user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken: tokenResp.AccessToken,
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   tokenResp.ExpiresIn,
		Scopes:      strings.Split(tokenResp.Scope, ","),
	}

	return profile, tokenData, nil
}

func (s *PinterestOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	if payload.ImageURL == "" {
		return nil, fmt.Errorf("pinterest requires image_url for pin creation")
	}

	slog.Info("Pinterest: creating pin")

	pinBody := map[string]interface{}{
		"title":       payload.Title,
		"description": payload.Text,
		"link":        payload.ImageURL,
		"media_source": map[string]string{
			"source_type": "image_url",
			"url":         payload.ImageURL,
		},
	}

	jsonBody, _ := json.Marshal(pinBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.pinterest.com/v5/pins",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pinterest pin creation failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("pinterest pin returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("pinterest response parse: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: result.ID,
	}, nil
}

// --- Private ---

type pinterestTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

func (s *PinterestOAuthService) exchangeCodeForToken(code string) (*pinterestTokenResponse, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", s.cfg.PinterestRedirectURI)

	req, err := http.NewRequest("POST", "https://api.pinterest.com/v5/oauth/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.cfg.PinterestAppID, s.cfg.PinterestAppSecret)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr pinterestTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *PinterestOAuthService) getUserInfo(accessToken string) (*models.PlatformProfile, error) {
	req, _ := http.NewRequest("GET", "https://api.pinterest.com/v5/user_account", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID: result.ID,
		Username:       result.Username,
		Name:           result.Username,
	}, nil
}
