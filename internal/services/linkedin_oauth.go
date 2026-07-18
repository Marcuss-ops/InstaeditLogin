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

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// LinkedInOAuthService implements the LinkedIn provider. Taglio 2.1:
//
// Capabilities exposed:
//   - OAuthProvider (OAuth 2.0 with OpenID Connect userinfo)
//   - ContentValidator (text required — text_only)
//   - Publisher (POST /rest/posts, text only — Taglio 3c: articleSource removed)
//   - AccountManager (Validate / Revoke)
//
// cfg is the OAuthConfig adapter (see oauth_config.go). The provider
// no longer imports internal/config directly — bootstrap constructs
// a ConfigAdapter once and passes it here.
type LinkedInOAuthService struct {
	cfg        OAuthConfig
	httpClient *http.Client
	clock      func() time.Time
}

// NewLinkedInOAuthService creates a new LinkedInOAuthService. Accepts
// optional ProviderDependencies for HTTP client injection. The cfg
// parameter is the OAuthConfig interface (see oauth_config.go); the
// concrete *config.Config never reaches provider internals.
func NewLinkedInOAuthService(cfg OAuthConfig, deps ...ProviderDependencies) (*LinkedInOAuthService, error) {
	if cfg.LinkedInClientID() == "" {
		return nil, nil // provider disabled
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	return &LinkedInOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
	}, nil
}

// now returns the current time via the injected clock, or time.Now as default.
func (s *LinkedInOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *LinkedInOAuthService) Name() string { return models.PlatformLinkedIn }

func (s *LinkedInOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", s.cfg.LinkedInClientID())
	params.Set("redirect_uri", s.cfg.LinkedInRedirectURI())
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

// ValidateContent enforces the text-only rule for a LinkedIn post
// and a mandatory visibility (privacy_level).
// Taglio 3c: LinkedIn is text_only — the articleSource block that
// pretended to upload media was removed.
// Taglio 4b: visibility is now required — one of PUBLIC, CONNECTIONS.
func (s *LinkedInOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.Text == "" {
		return fmt.Errorf("linkedin requires text content")
	}
	if payload.PrivacyLevel == "" {
		return fmt.Errorf("linkedin requires a privacy_level (visibility): PUBLIC or CONNECTIONS")
	}
	if err := validateLinkedInVisibility(payload.PrivacyLevel); err != nil {
		return err
	}
	return nil
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

// Revoke is not supported by the LinkedIn OAuth 2.0 implementation.
func (s *LinkedInOAuthService) Revoke(ctx context.Context, accessToken string) error {
	return ErrRevokeUnsupported
}

// RefreshOAuthToken is not applicable for LinkedIn with the current scopes
// (no offline_access). Returns a clear error so callers can handle it.
func (s *LinkedInOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	return nil, fmt.Errorf("linkedin: token refresh not available (offline_access scope not requested)")
}

func (s *LinkedInOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformLinkedIn, s.now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}

	slog.Info("LinkedIn: publishing post via /rest/posts")

	postBody := map[string]interface{}{
		"author":     "urn:li:person:" + platformUserID,
		"commentary": payload.Text,
		"visibility": normalizeLinkedInVisibility(payload.PrivacyLevel),
		"distribution": map[string]interface{}{
			"feedDistribution": "MAIN_FEED",
		},
		"lifecycleState": "PUBLISHED",
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

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		parsed.ID = resp.Header.Get("x-linkedin-id")
	}

	return &models.PublishResult{
		PlatformMediaID: parsed.ID,
	}, nil
}

// validateLinkedInVisibility returns an error if visibility is not one of the
// LinkedIn-recognized values. Used by ValidateContent.
// Taglio 4b: no default — empty/unrecognized causes validation_error.
func validateLinkedInVisibility(level string) error {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC", "CONNECTIONS":
		return nil
	default:
		return fmt.Errorf("linkedin visibility must be PUBLIC or CONNECTIONS (got %q)", level)
	}
}

// normalizeLinkedInVisibility canonicalizes the visibility value for the
// LinkedIn API. ValidateContent already guarantees the value is valid.
func normalizeLinkedInVisibility(level string) string {
	return strings.ToUpper(strings.TrimSpace(level))
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
	body.Set("redirect_uri", s.cfg.LinkedInRedirectURI())
	body.Set("client_id", s.cfg.LinkedInClientID())
	body.Set("client_secret", s.cfg.LinkedInClientSecret())

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

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ OAuthProvider    = (*LinkedInOAuthService)(nil)
	_ ContentValidator = (*LinkedInOAuthService)(nil)
	_ Publisher        = (*LinkedInOAuthService)(nil)
)
