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

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ThreadsOAuthService implements the Meta-Threads provider. Threads uses
// its own OAuth endpoints (threads.net for authorization and
// graph.threads.net for token exchange) while still authenticating with the
// shared Meta App credentials. **Publishing is asynchronous-only**: the
// initial POST creates a media container whose status is polled via
// CheckPublishStatus,
// then the worker publishes via ContinuePublish. The state machine is
// driven by the AsyncPublisher interface (Taglio 4.2).
//
// Taglio 4.4 split: scope formally clarified to async-only via container.
// The legacy Publisher.Publish() surface is KEPT as a backwards-compat
// wrapper that runs the full async flow synchronously (createContainer +
// wait + publishContainer in the same call). New code should use the
// AsyncPublisher interface (StartPublish / CheckPublishStatus /
// ContinuePublish / Reconcile) so reconciliation state lives in
// post_targets.provider_state and is driven by the worker reconciler
// goroutine — not by a synchronous poll inside the request path.
//
// Capabilities exposed:
//   - OAuthProvider (Meta OAuth login flow)
//   - ContentValidator (text/image/video required)
//   - Publisher (DEPRECATED compat path — synchronous single-step
//     container create + immediate publish. Wraps the async flow
//     behind a blocking call. New code MUST prefer AsyncPublisher)
//   - AsyncPublisher (PRIMARY — StartPublish / CheckPublishStatus /
//     ContinuePublish / Reconcile. The worker reconciler goroutine
//     drives this on every tick.)
type ThreadsOAuthService struct {
	base        *MetaOAuthBase
	redirectURI string
}

// NewThreadsOAuthService creates a new ThreadsOAuthService. Returns
// nil when the Threads redirect URI is not configured (provider disabled).
// Threads reuses the shared Meta OAuth credentials (META_APP_ID / META_APP_SECRET)
// like Instagram and Facebook.
// Accepts optional ProviderDependencies for HTTP client injection.
func NewThreadsOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*ThreadsOAuthService, error) {
	if cfg.ThreadsRedirectURI == "" {
		return nil, nil // provider disabled
	}
	return &ThreadsOAuthService{
		base:        NewMetaOAuthBase(cfg, deps...),
		redirectURI: cfg.ThreadsRedirectURI,
	}, nil
}

// Name returns the platform identifier.
func (s *ThreadsOAuthService) Name() string { return models.PlatformThreads }

// GetLoginURL builds the Threads OAuth login URL with Threads scopes.
// Threads uses its own OAuth authorization endpoint (threads.net), while
// still authenticating with the shared Meta App credentials.
func (s *ThreadsOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.base.cfg.MetaAppID)
	params.Set("redirect_uri", s.redirectURI)
	params.Set("state", state)
	params.Set("scope", "threads_basic,threads_content_publish")
	params.Set("response_type", "code")
	return "https://threads.net/oauth/authorize?" + params.Encode()
}

// HandleCallback processes the full OAuth callback for Threads.
func (s *ThreadsOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Threads: exchanging code for short-lived token")
	shortLived, err := s.exchangeCodeForThreadsToken(ctx, code, s.redirectURI)
	if err != nil {
		return nil, nil, fmt.Errorf("step 1 (code exchange): %w", err)
	}
	slog.Info("Threads: exchanging for long-lived token")
	longLived, err := s.exchangeForLongLivedThreadsToken(ctx, shortLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 2 (long-lived exchange): %w", err)
	}
	slog.Info("Threads: fetching user info")
	profile, err := s.fetchThreadsUserInfo(ctx, longLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 3 (user info): %w", err)
	}
	return profile, &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
		Scopes:      []string{"threads_basic", "threads_content_publish"},
	}, nil
}

// exchangeCodeForThreadsToken trades an authorization code for a short-lived
// Threads access token. This is step 1 of the Threads OAuth flow and uses the
// Threads-specific endpoint graph.threads.net/oauth/access_token.
func (s *ThreadsOAuthService) exchangeCodeForThreadsToken(ctx context.Context, code, redirectURI string) (*models.MetaTokenResponse, error) {
	params := url.Values{}
	params.Set("client_id", s.base.cfg.MetaAppID)
	params.Set("client_secret", s.base.cfg.MetaAppSecret)
	params.Set("grant_type", "authorization_code")
	params.Set("redirect_uri", redirectURI)
	params.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://graph.threads.net/oauth/access_token", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create Threads token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Threads token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Threads token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Threads token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse Threads token response: %w", err)
	}

	return &tokenResp, nil
}

// exchangeForLongLivedThreadsToken extends a short-lived Threads token into a
// long-lived (~60 day) token via grant_type=th_exchange_token on
// graph.threads.net/access_token.
func (s *ThreadsOAuthService) exchangeForLongLivedThreadsToken(ctx context.Context, shortLivedToken string) (*models.MetaLongLivedTokenResponse, error) {
	params := url.Values{}
	params.Set("grant_type", "th_exchange_token")
	params.Set("client_secret", s.base.cfg.MetaAppSecret)
	params.Set("access_token", shortLivedToken)

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.threads.net/access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Threads long-lived token request: %w", err)
	}

	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Threads long-lived token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Threads long-lived token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Threads long-lived token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaLongLivedTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse Threads long-lived token response: %w", err)
	}

	return &tokenResp, nil
}

// fetchThreadsUserInfo fetches the authenticated Threads user's profile
// from graph.threads.net. This is distinct from MetaOAuthBase.GetUserInfo
// (which hits Facebook's /me) because Threads publishing endpoints require
// a Threads user id.
func (s *ThreadsOAuthService) fetchThreadsUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	params := url.Values{}
	params.Set("fields", "id,username,name")
	params.Set("access_token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.threads.net/v1.0/me?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("threads user info request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("threads user info request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read threads user info: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("threads user info failed (status %d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse threads user info: %w", err)
	}
	username := result.Username
	if username == "" {
		username = result.Name
	}
	return &models.PlatformProfile{
		PlatformUserID: result.ID,
		Username:       username,
		Name:           result.Name,
	}, nil
}

// RefreshOAuthToken refreshes a long-lived Threads token via
// graph.threads.net/refresh_access_token.
func (s *ThreadsOAuthService) RefreshOAuthToken(ctx context.Context, currentToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformThreads, &err)
	if currentToken == "" {
		return nil, fmt.Errorf("threads RefreshOAuthToken: empty current token")
	}
	slog.Info("Threads: refreshing long-lived token via refresh_access_token")
	refreshed, err := s.refreshThreadsAccessToken(ctx, currentToken)
	if err != nil {
		return nil, fmt.Errorf("threads refresh failed: %w", err)
	}
	return &models.TokenData{
		AccessToken: refreshed.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   refreshed.ExpiresIn,
	}, nil
}

// refreshThreadsAccessToken refreshes an existing long-lived Threads token.
func (s *ThreadsOAuthService) refreshThreadsAccessToken(ctx context.Context, currentToken string) (*models.MetaLongLivedTokenResponse, error) {
	params := url.Values{}
	params.Set("grant_type", "th_refresh_token")
	params.Set("access_token", currentToken)

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.threads.net/refresh_access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Threads refresh token request: %w", err)
	}

	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Threads refresh token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Threads refresh token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Threads refresh token failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaLongLivedTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse Threads refresh token response: %w", err)
	}

	return &tokenResp, nil
}

// ValidateContent enforces Threads' content requirements.
func (s *ThreadsOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.Text == "" && payload.ImageURL == "" && payload.VideoURL == "" {
		return fmt.Errorf("threads requires text or media")
	}
	return nil
}

// =========================================================================

func (s *ThreadsOAuthService) createContainer(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (string, error) {
	slog.Info("Threads: creating media container", "user_id", platformUserID)

	mediaType := "TEXT"
	if payload.VideoURL != "" {
		mediaType = "VIDEO"
	} else if payload.ImageURL != "" {
		mediaType = "IMAGE"
	}

	params := url.Values{}
	params.Set("media_type", mediaType)
	params.Set("text", payload.Text)
	params.Set("access_token", accessToken)
	if payload.VideoURL != "" {
		params.Set("video_url", payload.VideoURL)
	}
	if payload.ImageURL != "" {
		params.Set("image_url", payload.ImageURL)
	}

	reqURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads", platformUserID)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("threads container request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("threads container request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read container response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("threads container failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse container response: %w", err)
	}
	return result.ID, nil
}

func (s *ThreadsOAuthService) publishContainer(ctx context.Context, accessToken, platformUserID, containerID string) (*models.PublishResult, error) {
	params := url.Values{}
	params.Set("creation_id", containerID)
	params.Set("access_token", accessToken)

	reqURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s/threads_publish?%s", platformUserID, params.Encode())
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("threads publish request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("threads publish request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read publish response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("threads publish failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse publish response: %w", err)
	}
	slog.Info("Threads: published successfully", "media_id", result.ID)
	return &models.PublishResult{
		PlatformMediaID: result.ID,
		PlatformURL:     fmt.Sprintf("https://www.threads.net/t/%s", result.ID),
	}, nil
}
