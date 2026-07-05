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

// LinkedInOAuthService implements OAuthProvider and ContentPublisher for LinkedIn.
type LinkedInOAuthService struct {
	cfg        *config.Config
	userRepo   *repository.UserRepository
	*TokenHelper
	httpClient *http.Client
}

// NewLinkedInOAuthService creates a new LinkedInOAuthService.
func NewLinkedInOAuthService(cfg *config.Config, userRepo *repository.UserRepository, tokenRepo *repository.TokenRepository) (*LinkedInOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &LinkedInOAuthService{
		cfg:         cfg,
		userRepo:    userRepo,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (s *LinkedInOAuthService) GetPlatform() string { return models.PlatformLinkedIn }

func (s *LinkedInOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.LinkedInClientID)
	params.Set("redirect_uri", s.cfg.LinkedInRedirectURI)
	params.Set("state", state)
	params.Set("scope", "openid profile email w_member_social")
	params.Set("response_type", "code")

	return "https://www.linkedin.com/oauth/v2/authorization?" + params.Encode()
}

func (s *LinkedInOAuthService) HandleCallback(code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("LinkedIn: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(code)
	if err != nil {
		return nil, nil, fmt.Errorf("linkedin token exchange: %w", err)
	}

	slog.Info("LinkedIn: fetching user info")
	profile, err := s.getUserInfo(tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("linkedin user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken: tokenResp.AccessToken,
		TokenType:   models.TokenTypeBearer,
		ExpiresIn:   tokenResp.ExpiresIn,
		Scopes:      strings.Split(tokenResp.Scope, " "),
	}

	return profile, tokenData, nil
}

func (s *LinkedInOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	if payload.Text == "" {
		return nil, fmt.Errorf("linkedin requires text for sharing")
	}

	slog.Info("LinkedIn: creating share post")

	shareBody := map[string]interface{}{
		"author":     "urn:li:person:" + platformUserID,
		"lifecycleState": "PUBLISHED",
		"specificContent": map[string]interface{}{
			"com.linkedin.ugc.ShareContent": map[string]interface{}{
				"shareCommentary": map[string]string{
					"text": payload.Text,
				},
				"shareMediaCategory": "NONE",
			},
		},
		"visibility": map[string]string{
			"com.linkedin.ugc.MemberNetworkVisibility": "PUBLIC",
		},
	}

	jsonBody, _ := json.Marshal(shareBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.linkedin.com/v2/ugcPosts",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("linkedin share failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("linkedin share returned status %d: %s", resp.StatusCode, string(body))
	}

	// LinkedIn returns the URN in a header
	shareID := resp.Header.Get("X-Restli-Id")
	if shareID == "" {
		// Try body
		var result struct {
			ID string `json:"id"`
		}
		json.Unmarshal(body, &result)
		shareID = result.ID
	}

	return &models.PublishResult{
		PlatformMediaID: shareID,
	}, nil
}

// --- Private ---

type linkedinTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

func (s *LinkedInOAuthService) exchangeCodeForToken(code string) (*linkedinTokenResponse, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("client_id", s.cfg.LinkedInClientID)
	body.Set("client_secret", s.cfg.LinkedInClientSecret)
	body.Set("redirect_uri", s.cfg.LinkedInRedirectURI)

	req, err := http.NewRequest("POST", "https://www.linkedin.com/oauth/v2/accessToken",
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

	var tr linkedinTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *LinkedInOAuthService) getUserInfo(accessToken string) (*models.PlatformProfile, error) {
	req, _ := http.NewRequest("GET", "https://api.linkedin.com/v2/userinfo", nil)
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
		Sub   string `json:"sub"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID: result.Sub,
		Username:       result.Name,
		Name:           result.Name,
		Email:          result.Email,
	}, nil
}
