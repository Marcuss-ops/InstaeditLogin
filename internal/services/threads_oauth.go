package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ThreadsOAuthService implements the Meta-Threads provider. Threads uses
// Meta's Graph API with the same OAuth infrastructure as Instagram/Facebook
// (MetaOAuthBase). **Publishing is asynchronous-only**: the initial POST
// creates a media container whose status is polled via CheckPublishStatus,
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
// nil when the redirect URI is not configured (provider disabled).
func NewThreadsOAuthService(cfg *config.Config) (*ThreadsOAuthService, error) {
	if cfg.ThreadsRedirectURI == "" {
		return nil, nil // provider disabled
	}
	return &ThreadsOAuthService{
		base:        NewMetaOAuthBase(cfg),
		redirectURI: cfg.ThreadsRedirectURI,
	}, nil
}

// Name returns the platform identifier.
func (s *ThreadsOAuthService) Name() string { return models.PlatformThreads }

// GetLoginURL builds the Meta OAuth login URL with Threads scopes.
func (s *ThreadsOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.base.cfg.MetaAppID)
	params.Set("redirect_uri", s.redirectURI)
	params.Set("state", state)
	params.Set("scope", "threads_basic,threads_content_publish")
	params.Set("response_type", "code")
	return "https://www.facebook.com/v19.0/dialog/oauth?" + params.Encode()
}

// HandleCallback processes the full OAuth callback for Threads.
func (s *ThreadsOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Threads: exchanging code for short-lived token")
	shortLived, err := s.base.ExchangeCodeForToken(ctx, code, s.redirectURI)
	if err != nil {
		return nil, nil, fmt.Errorf("step 1 (code exchange): %w", err)
	}
	slog.Info("Threads: exchanging for long-lived token")
	longLived, err := s.base.ExchangeForLongLivedToken(ctx, shortLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 2 (long-lived exchange): %w", err)
	}
	slog.Info("Threads: fetching user info")
	profile, err := s.base.GetUserInfo(ctx, longLived.AccessToken)
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

// RefreshOAuthToken extends a long-lived token via fb_exchange_token.
func (s *ThreadsOAuthService) RefreshOAuthToken(ctx context.Context, currentToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformThreads, &err)
	if currentToken == "" {
		return nil, fmt.Errorf("threads RefreshOAuthToken: empty current token")
	}
	slog.Info("Threads: refreshing long-lived token via fb_exchange_token")
	longLived, err := s.base.ExchangeForLongLivedToken(ctx, currentToken)
	if err != nil {
		return nil, fmt.Errorf("threads refresh failed: %w", err)
	}
	return &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
	}, nil
}

// ValidateContent enforces Threads' content requirements.
func (s *ThreadsOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.Text == "" && payload.ImageURL == "" && payload.VideoURL == "" {
		return fmt.Errorf("threads requires text, an image_url, or a video_url")
	}
	return nil
}

// =========================================================================
// Publisher — synchronous path (creates container + publishes immediately)
// =========================================================================

func (s *ThreadsOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformThreads, time.Now(), &err)
	containerID, err := s.createContainer(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return nil, err
	}
	return s.publishContainer(ctx, accessToken, platformUserID, containerID)
}

// =========================================================================
// AsyncPublisher — Zernio 2.1 two-step async flow
// =========================================================================

// threadStates are the provider_state values written to post_targets.
const (
	threadsStateInProgress = "IN_PROGRESS"
	threadsStateFinished   = "FINISHED"
	threadsStateError      = "ERROR"
	threadsStateExpired    = "EXPIRED"
)

type threadsProviderState struct {
	UserID      string `json:"user_id"`
	ContainerID string `json:"container_id"`
}

// StartPublish creates a media container and returns immediately.
// The caller stores containerID + userID as provider_state so
// ContinuePublish can derive the platform user ID.
func (s *ThreadsOAuthService) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error) {
	defer RecordPublishMetrics(models.PlatformThreads, time.Now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return "", "", err
	}

	containerID, err := s.createContainer(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return "", "", err
	}

	slog.Info("Threads: async container created", "container_id", containerID)
	// Store user_id so ContinuePublish can derive it without a round-trip.
	ps := threadsProviderState{UserID: platformUserID, ContainerID: containerID}
	psJSON, _ := json.Marshal(ps)
	return containerID, string(psJSON), nil
}

// CheckPublishStatus does a single GET to the Threads container status endpoint.
func (s *ThreadsOAuthService) CheckPublishStatus(ctx context.Context, accessToken, containerID string) (state string, err error) {
	reqURL := fmt.Sprintf("https://graph.threads.net/v1.0/%s?fields=status&access_token=%s",
		containerID, url.QueryEscape(accessToken))

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("threads status request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("threads status request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read status response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("threads status returned %d: %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse status response: %w", err)
	}
	return result.Status, nil
}

// ContinuePublish publishes the container once CheckPublishStatus reports FINISHED.
// The publishID is the container ID; we recover the platform user ID from the
// Graph API /me endpoint because AsyncPublisher.ContinuePublish receives only
// publishID, not the post_target row that holds provider_state.
func (s *ThreadsOAuthService) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	profile, err := s.base.GetUserInfo(ctx, accessToken)
	if err != nil {
		return fmt.Errorf("threads ContinuePublish: get user info: %w", err)
	}
	_, err = s.publishContainer(ctx, accessToken, profile.PlatformUserID, publishID)
	return err
}

// Reconcile closes the AsyncPublisher loop: it queries the container status
// and applies the transition decided in provider_state.
//
//   IN_PROGRESS → (nil, nil)  caller retries on next tick
//   FINISHED    → publish the container via /threads_publish and return *PublishResult
//                  with the real media ID (captured from publishContainer)
//   ERROR       → error (terminal — worker transitions target.status='failed')
//   EXPIRED     → error (terminal)
//
// Taglio 4.2 / Zernio 2.1: without Reconcile, ThreadsOAuthService does not
// satisfy the AsyncPublisher interface and the registry will register it
// only as a sync Publisher — meaning the reconciler goroutine in the worker
// would never pick up Threads targets. Adding Reconcile unblocks the async
// state machine for Threads.
func (s *ThreadsOAuthService) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	state, err := s.CheckPublishStatus(ctx, accessToken, publishID)
	if err != nil {
		return nil, fmt.Errorf("threads reconcile: status check: %w", err)
	}
	slog.Info("Threads: reconcile status", "container_id", publishID, "status", state)
	switch state {
	case threadsStateInProgress:
		return nil, nil
	case threadsStateFinished:
		profile, err := s.base.GetUserInfo(ctx, accessToken)
		if err != nil {
			return nil, fmt.Errorf("threads reconcile: get user info: %w", err)
		}
		return s.publishContainer(ctx, accessToken, profile.PlatformUserID, publishID)
	case threadsStateError:
		return nil, fmt.Errorf("threads container in error state (container_id=%s)", publishID)
	case threadsStateExpired:
		return nil, fmt.Errorf("threads container expired (container_id=%s)", publishID)
	default:
		return nil, fmt.Errorf("threads reconcile: unknown status %q", state)
	}
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Threads implements OAuthProvider + ContentValidator + Publisher
// (sync compat path) + AsyncPublisher (Taglio 4.2, added Reconcile
// in this PR). Without AsyncPublisher, the worker reconciler would
// never pick up Threads targets — the Publish() return value alone
// is not enough to drive the state machine.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ Provider         = (*ThreadsOAuthService)(nil)
	_ OAuthProvider    = (*ThreadsOAuthService)(nil)
	_ ContentValidator = (*ThreadsOAuthService)(nil)
	_ Publisher        = (*ThreadsOAuthService)(nil)
	_ AsyncPublisher   = (*ThreadsOAuthService)(nil)
)

// =========================================================================
// Helpers
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
