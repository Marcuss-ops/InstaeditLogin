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
	cfg *config.Config
	*TokenHelper
	httpClient *http.Client
}

// NewLinkedInOAuthService creates a new LinkedInOAuthService.
func NewLinkedInOAuthService(cfg *config.Config, tokenRepo *repository.TokenRepository) (*LinkedInOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("linkedin: failed to create encryptor: %w", err)
	}

	return &LinkedInOAuthService{
		cfg:         cfg,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  NewHTTPClient(),
	}, nil
}

func (s *LinkedInOAuthService) Name() string { return models.PlatformLinkedIn }

func (s *LinkedInOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", s.cfg.LinkedInClientID)
	params.Set("redirect_uri", s.cfg.LinkedInRedirectURI)
	params.Set("state", state)
	params.Set("scope", "openid profile email w_member_social")

	return "https://www.linkedin.com/oauth/v2/authorization?" + params.Encode()
}

func (s *LinkedInOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("LinkedIn: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("linkedin token exchange: %w", err)
	}

	slog.Info("LinkedIn: fetching user info via OpenID Connect")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
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

// Validate calls the LinkedIn OpenID Connect userinfo endpoint to verify
// the access token.
func (s *LinkedInOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.linkedin.com/v2/userinfo", nil)
	if err != nil {
		return fmt.Errorf("linkedin validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("linkedin validate failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("linkedin validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Revoke is not supported by the LinkedIn OAuth 2.0 implementation. The
// caller should proceed with local token deletion.
func (s *LinkedInOAuthService) Revoke(ctx context.Context, accessToken string) error {
	return ErrRevokeUnsupported
}

// RefreshOAuthToken is not applicable for LinkedIn with the current scopes
// (no offline_access). Returns a clear error so callers can handle it.
func (s *LinkedInOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	return nil, fmt.Errorf("linkedin: token refresh not available (offline_access scope not requested)")
}

func (s *LinkedInOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformLinkedIn, time.Now(), &err)
	if payload.Text == "" {
		return nil, fmt.Errorf("linkedin requires text content")
	}

	slog.Info("LinkedIn: publishing post via /rest/posts")

	// LinkedIn Posts API: POST https://api.linkedin.com/rest/posts
	// Version 202606, main-feed distribution, public visibility.
	postBody := map[string]interface{}{
		"author":     "urn:li:person:" + platformUserID,
		"commentary": payload.Text,
		"visibility": "PUBLIC",
		"distribution": map[string]interface{}{
			"feedDistribution": "MAIN_FEED",
		},
		"lifecycleState": "PUBLISHED",
	}

	// When a media_url is provided, attach it as an article link (infoproduct page).
	// The router maps pubReq.MediaURL to payload.ImageURL (for image/photo content_type)
	// or payload.VideoURL (for video/reel). Use whichever is non-empty.
	articleSource := payload.ImageURL
	if articleSource == "" {
		articleSource = payload.VideoURL
	}
	if articleSource != "" {
		article := map[string]interface{}{
			"source":      articleSource,
			"description": payload.Text,
		}
		if payload.Title != "" {
			article["title"] = payload.Title
		}
		postBody["content"] = map[string]interface{}{
			"article": article,
		}
	}

	jsonBody, err := json.Marshal(postBody)
	if err != nil {
		return nil, fmt.Errorf("linkedin marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.linkedin.com/rest/posts",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("linkedin post request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Restli-Protocol-Version", "2.0.0")
	req.Header.Set("LinkedIn-Version", "202606")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("linkedin post failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("linkedin post returned status %d: %s", resp.StatusCode, string(body))
	}

	// The /rest/posts response includes the post URN in the x-linkedin-id header
	// and the response body.
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Fall back to the header if body parse fails.
		parsed.ID = resp.Header.Get("x-linkedin-id")
	}

	return &models.PublishResult{
		PlatformMediaID: parsed.ID,
	}, nil
}

// --- Private ---

type linkedinTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

func (s *LinkedInOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*linkedinTokenResponse, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", s.cfg.LinkedInRedirectURI)
	body.Set("client_id", s.cfg.LinkedInClientID)
	body.Set("client_secret", s.cfg.LinkedInClientSecret)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://www.linkedin.com/oauth/v2/accessToken",
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

// getUserInfo fetches the LinkedIn profile via OpenID Connect userinfo endpoint.
// Returns sub as PlatformUserID, name, email.
func (s *LinkedInOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.linkedin.com/v2/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("userinfo failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Sub   string `json:"sub"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("userinfo parse: %w", err)
	}

	return &models.PlatformProfile{
		PlatformUserID: result.Sub,
		Username:       result.Sub,
		Name:           result.Name,
		Email:          result.Email,
	}, nil
}
