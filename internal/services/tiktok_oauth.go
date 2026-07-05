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

// TikTokOAuthService implements OAuthProvider and ContentPublisher for TikTok.
type TikTokOAuthService struct {
	cfg        *config.Config
	userRepo   *repository.UserRepository
	*TokenHelper
	httpClient *http.Client
}

// NewTikTokOAuthService creates a new TikTokOAuthService.
func NewTikTokOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*TikTokOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &TikTokOAuthService{
		cfg:         cfg,
		userRepo:    userRepo,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *TikTokOAuthService) GetPlatform() string { return models.PlatformTikTok }

func (s *TikTokOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_key", s.cfg.TikTokClientKey)
	params.Set("redirect_uri", s.cfg.TikTokRedirectURI)
	params.Set("state", state)
	params.Set("scope", "user.info.basic,video.publish")
	params.Set("response_type", "code")

	return "https://www.tiktok.com/v2/auth/authorize/?" + params.Encode()
}

func (s *TikTokOAuthService) HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("TikTok: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(code)
	if err != nil {
		return nil, nil, fmt.Errorf("tiktok token exchange: %w", err)
	}

	slog.Info("TikTok: fetching user info")
	profile, err := s.getUserInfo(tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("tiktok user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken: tokenResp.AccessToken,
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   tokenResp.ExpiresIn,
		Scopes:      strings.Split(tokenResp.Scope, ","),
	}

	return profile, tokenData, nil
}

func (s *TikTokOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	if payload.VideoURL == "" {
		return nil, fmt.Errorf("tiktok requires a video_url for publishing")
	}

	slog.Info("TikTok: initiating video publish")

	// Step 1: Initialize upload
	initBody := map[string]interface{}{
		"source_info": map[string]string{
			"source":      "PULL_FROM_URL",
			"video_url":   payload.VideoURL,
		},
	}

	jsonBody, _ := json.Marshal(initBody)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.tiktokapis.com/v2/post/publish/video/init/",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("tiktok init request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tiktok init failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tiktok init returned status %d: %s", resp.StatusCode, string(body))
	}

	var initResult struct {
		Data struct {
			PublishID string `json:"publish_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &initResult); err != nil {
		return nil, fmt.Errorf("tiktok init parse: %w", err)
	}

	publishID := initResult.Data.PublishID
	slog.Info("TikTok: publish initialized", "publish_id", publishID)

	// Poll for status
	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("tiktok publish cancelled: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}

		req, _ := http.NewRequestWithContext(ctx, "GET",
			"https://open.tiktokapis.com/v2/post/publish/status/fetch/",
			nil)
		req.Header.Set("Authorization", "Bearer "+accessToken)
		q := req.URL.Query()
		q.Set("publish_id", publishID)
		req.URL.RawQuery = q.Encode()

		resp, err := s.httpClient.Do(req)
		if err != nil {
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var statusResult struct {
			Data struct {
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &statusResult); err != nil {
			continue
		}

		if statusResult.Data.Status == "PUBLISH_COMPLETE" {
			return &models.PublishResult{PlatformMediaID: publishID}, nil
		}
		if statusResult.Data.Status == "FAILED" {
			return nil, fmt.Errorf("tiktok publish failed: %s", string(body))
		}
	}

	return nil, fmt.Errorf("tiktok publish timed out")
}

// --- Private ---

type tiktokTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

func (s *TikTokOAuthService) exchangeCodeForToken(code string) (*tiktokTokenResponse, error) {
	body := url.Values{}
	body.Set("client_key", s.cfg.TikTokClientKey)
	body.Set("client_secret", s.cfg.TikTokClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.TikTokRedirectURI)

	req, err := http.NewRequest("POST", "https://open.tiktokapis.com/v2/oauth/token/",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr tiktokTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *TikTokOAuthService) getUserInfo(accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequest("GET", "https://open.tiktokapis.com/v2/user/info/?fields=open_id,display_name", nil)
	if err != nil {
		return nil, fmt.Errorf("user info request creation: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req.WithContext(context.Background()))
	if err != nil {
		return nil, fmt.Errorf("user info request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data struct {
			User struct {
				OpenID      string `json:"open_id"`
				DisplayName string `json:"display_name"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("user info parse: %w", err)
	}

	return &models.PlatformProfile{
		PlatformUserID: result.Data.User.OpenID,
		Username:       result.Data.User.DisplayName,
		Name:           result.Data.User.DisplayName,
	}, nil
}
