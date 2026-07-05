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

// YouTubeOAuthService implements OAuthProvider and ContentPublisher for YouTube.
type YouTubeOAuthService struct {
	cfg        *config.Config
	userRepo   *repository.UserRepository
	*TokenHelper
	httpClient *http.Client
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService.
func NewYouTubeOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*YouTubeOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &YouTubeOAuthService{
		cfg:         cfg,
		userRepo:    userRepo,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *YouTubeOAuthService) GetPlatform() string { return models.PlatformYouTube }

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.YouTubeClientID)
	params.Set("redirect_uri", s.cfg.YouTubeRedirectURI)
	params.Set("state", state)
	params.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/userinfo.profile")
	params.Set("response_type", "code")
	params.Set("access_type", "offline")

	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

func (s *YouTubeOAuthService) HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("YouTube: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(code)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube token exchange: %w", err)
	}

	slog.Info("YouTube: fetching user info")
	profile, err := s.getUserInfo(tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken: tokenResp.AccessToken,
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   tokenResp.ExpiresIn,
		Scopes:      strings.Split(tokenResp.Scope, " "),
	}
	if tokenResp.RefreshToken != "" {
		tokenData.Scopes = append(tokenData.Scopes, "refresh_token:"+tokenResp.RefreshToken)
	}

	return profile, tokenData, nil
}

func (s *YouTubeOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	if payload.VideoURL == "" {
		return nil, fmt.Errorf("youtube requires video_url for publishing")
	}

	slog.Info("YouTube: uploading video")

	// YouTube requires the video to be uploaded as multipart
	// For simplicity, we return the video URL as a placeholder
	// In production, this would stream the video to YouTube's resumable upload endpoint
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://www.googleapis.com/upload/youtube/v3/videos?part=snippet,status",
		strings.NewReader(""))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	metadata := map[string]interface{}{
		"snippet": map[string]string{
			"title":       payload.Title,
			"description": payload.Text,
		},
		"status": map[string]string{
			"privacyStatus": "public",
		},
	}
	jsonMeta, _ := json.Marshal(metadata)
	req.Body = io.NopCloser(strings.NewReader(string(jsonMeta)))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube upload failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("youtube upload returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("youtube response parse: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: result.ID,
		PlatformURL:     "https://www.youtube.com/watch?v=" + result.ID,
	}, nil
}

// --- Private ---

type youtubeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

func (s *YouTubeOAuthService) exchangeCodeForToken(code string) (*youtubeTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.YouTubeClientID)
	body.Set("client_secret", s.cfg.YouTubeClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.YouTubeRedirectURI)

	req, err := http.NewRequest("POST", "https://oauth2.googleapis.com/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *YouTubeOAuthService) getUserInfo(accessToken string) (*models.PlatformProfile, error) {
	req, _ := http.NewRequest("GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
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
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID: result.ID,
		Username:       result.Name,
		Name:           result.Name,
		Email:          result.Email,
	}, nil
}
