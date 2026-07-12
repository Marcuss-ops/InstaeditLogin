package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TwitterOAuthService implements the X / Twitter provider. Taglio 2.1:
//
// Capabilities exposed:
//   - OAuthProvider (OAuth 2.0 PKCE flow)
//   - ContentValidator (text + single image)
//   - Publisher (POST /2/tweets with user Bearer, text only)
//   - AccountManager (Validate / Revoke)
type TwitterOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewTwitterOAuthService creates a new TwitterOAuthService.
func NewTwitterOAuthService(cfg *config.Config) (*TwitterOAuthService, error) {
	if cfg.TwitterClientID == "" {
		return nil, nil // provider disabled
	}
	return &TwitterOAuthService{
		cfg:        cfg,
		httpClient: NewHTTPClient(),
	}, nil
}

func (s *TwitterOAuthService) Name() string { return models.PlatformTwitter }

func (s *TwitterOAuthService) GetLoginURL(state string) string {
	verifierBytes := make([]byte, 64)
	var verifier string
	if _, err := rand.Read(verifierBytes); err != nil {
		slog.Error("Twitter: failed to generate PKCE code_verifier, falling back to unsafe default", "error", err)
		verifier = "challenge"
	} else {
		verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)
	}

	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	params := url.Values{}
	params.Set("client_id", s.cfg.TwitterClientID)
	params.Set("redirect_uri", s.cfg.TwitterRedirectURI)
	params.Set("state", state+"."+verifier)
	params.Set("scope", "tweet.read tweet.write users.read offline.access")
	params.Set("response_type", "code")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")

	return "https://twitter.com/i/oauth2/authorize?" + params.Encode()
}

func (s *TwitterOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Twitter: exchanging code for token")

	verifier := ""
	if idx := strings.LastIndex(state, "."); idx != -1 {
		verifier = state[idx+1:]
	}
	if verifier == "" {
		return nil, nil, fmt.Errorf("twitter PKCE: missing code_verifier in state")
	}

	tokenResp, err := s.exchangeCodeForToken(ctx, code, verifier)
	if err != nil {
		return nil, nil, fmt.Errorf("twitter token exchange: %w", err)
	}

	slog.Info("Twitter: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
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

// ValidateContent enforces X/Twitter's minimum requirements: text.
// Taglio 5d: minimum feature set = text + single image.
func (s *TwitterOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.Text == "" {
		return fmt.Errorf("twitter requires text content")
	}
	return nil
}

// Validate calls the Twitter /2/users/me endpoint to verify the access token.
func (s *TwitterOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.twitter.com/2/users/me?user.fields=id", nil)
	if err != nil {
		return fmt.Errorf("twitter validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("twitter validate failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twitter validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Revoke calls Twitter's OAuth 2.0 token revocation endpoint.
func (s *TwitterOAuthService) Revoke(ctx context.Context, accessToken string) error {
	body := url.Values{}
	body.Set("token", accessToken)
	body.Set("client_id", s.cfg.TwitterClientID)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.twitter.com/2/oauth2/revoke",
		strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("twitter revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("twitter revoke failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("twitter revoke returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// RefreshOAuthToken exchanges a Twitter refresh token for a new access token.
func (s *TwitterOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformTwitter, &err)
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

func (s *TwitterOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformTwitter, time.Now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}

	slog.Info("Twitter: publishing tweet")

	tweetBody := map[string]interface{}{
		"text": payload.Text,
	}
	jsonBody, _ := json.Marshal(tweetBody)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.twitter.com/2/tweets",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("twitter tweet request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Taglio 1.3: X is OAuth 2.0 PKCE only. Every publish uses the
	// user-context Bearer token.
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitter tweet failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("twitter tweet returned status %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Data struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("twitter tweet parse: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: parsed.Data.ID,
		PlatformURL:     "https://twitter.com/i/status/" + parsed.Data.ID,
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

func (s *TwitterOAuthService) exchangeCodeForToken(ctx context.Context, code, verifier string) (*twitterTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.TwitterClientID)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.TwitterRedirectURI)
	body.Set("code_verifier", verifier)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.twitter.com/2/oauth2/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.cfg.TwitterClientID, s.cfg.TwitterClientSecret)

	resp, err := s.httpClient.Do(req)
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

func (s *TwitterOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.twitter.com/2/users/me?user.fields=id,name,username", nil)
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

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ Provider         = (*TwitterOAuthService)(nil)
	_ OAuthProvider    = (*TwitterOAuthService)(nil)
	_ ContentValidator = (*TwitterOAuthService)(nil)
	_ Publisher        = (*TwitterOAuthService)(nil)
)
