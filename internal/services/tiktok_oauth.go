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
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TikTokOAuthService implements the TikTok provider. Taglio 2.1:
//
// Capabilities exposed (Taglio 4.2):
//   - OAuthProvider (login flow)
//   - ContentValidator (video required; caption ≤ 4000 runes)
//   - Publisher (Publisher.Publish = thin wrapper that calls StartPublish
//     and returns immediately with the publish_id, for backward compat
//     with the existing Publisher contract used by the worker's tick)
//   - AsyncPublisher (the 4-step state machine: StartPublish /
//     CheckPublishStatus / ContinuePublish / Reconcile) — this is the
//     new surface that the reconciler goroutine drives instead of
//     calling a synchronous polling loop inside the request path.
//   - AccountManager (Validate / Revoke — non-interface helpers)
type TikTokOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewTikTokOAuthService creates a new TikTokOAuthService.
func NewTikTokOAuthService(cfg *config.Config) (*TikTokOAuthService, error) {
	if cfg.TikTokClientKey == "" {
		return nil, nil // provider disabled
	}
	return &TikTokOAuthService{
		cfg:        cfg,
		httpClient: NewHTTPClient(),
	}, nil
}

// Name returns the platform identifier.
func (s *TikTokOAuthService) Name() string { return models.PlatformTikTok }

// maskClientKey restituisce una versione mascherata della client key per i log.
func maskClientKey(key string) string {
	if len(key) <= 8 {
		return "***"
	}
	if len(key) <= 16 {
		return key[:4] + "..."
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// maskCode restituisce i primi caratteri di un OAuth code per i log.
func maskCode(code string) string {
	if len(code) <= 8 {
		return "***"
	}
	return code[:4] + "..."
}

// truncateForLog restituisce una versione troncata di una stringa per i log.
func truncateForLog(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 200
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (s *TikTokOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_key", s.cfg.TikTokClientKey)
	params.Set("redirect_uri", s.cfg.TikTokRedirectURI)
	params.Set("state", state)
	params.Set("scope", "user.info.basic,video.publish")
	params.Set("response_type", "code")

	loginURL := "https://www.tiktok.com/v2/auth/authorize/?" + params.Encode()
	slog.Info("TikTok: built login URL",
		"redirect_uri", s.cfg.TikTokRedirectURI,
		"client_key_prefix", maskClientKey(s.cfg.TikTokClientKey),
		"scope", params.Get("scope"))
	return loginURL
}

func (s *TikTokOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("TikTok: exchanging code for token", "code_prefix", maskCode(code))

	tokenResp, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("tiktok token exchange: %w", err)
	}

	slog.Info("TikTok: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("tiktok user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scopes:       strings.Split(tokenResp.Scope, ","),
	}

	return profile, tokenData, nil
}

// ValidateContent enforces TikTok's hard requirements: a video,
// a privacy_level (mandatory — no default), and caption ≤ 4000 runes.
// Taglio 4b: privacy_level is now required — empty/unrecognized values
// return a validation_error instead of silently defaulting to PUBLIC_TO_EVERYONE.
func (s *TikTokOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("tiktok requires a video for publishing")
	}
	if payload.PrivacyLevel == "" {
		return fmt.Errorf("tiktok requires a privacy_level: one of PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, SELF_ONLY")
	}
	if err := validateTikTokPrivacyLevel(payload.PrivacyLevel); err != nil {
		return err
	}
	if n := len([]rune(payload.Text)); n > tikTokTitleMaxRunes {
		return fmt.Errorf("tiktok caption exceeds %d-rune limit (got %d)", tikTokTitleMaxRunes, n)
	}
	return nil
}

// Validate calls the TikTok user info endpoint to verify the access token.
func (s *TikTokOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://open.tiktokapis.com/v2/user/info/?fields=open_id,display_name", nil)
	if err != nil {
		return fmt.Errorf("tiktok validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("tiktok validate failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tiktok validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Revoke is not supported by the TikTok API.
func (s *TikTokOAuthService) Revoke(ctx context.Context, accessToken string) error {
	return ErrRevokeUnsupported
}

// RefreshOAuthToken exchanges a TikTok refresh token for a new access token.
func (s *TikTokOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformTikTok, &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("tiktok RefreshOAuthToken: empty refresh token")
	}
	slog.Info("TikTok: refreshing access token")
	body := url.Values{}
	body.Set("client_key", s.cfg.TikTokClientKey)
	body.Set("client_secret", s.cfg.TikTokClientSecret)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, "POST", "https://open.tiktokapis.com/v2/oauth/token/",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tiktok refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tiktok refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr tiktokTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("tiktok refresh parse: %w", err)
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
		Scopes:       strings.Split(tr.Scope, ","),
	}, nil
}

// Publish (Taglio 4.2) is a thin wrapper that calls StartPublish and
// returns the publish_id. Kept on the Publisher interface for backward
// compat with the worker's existing tick() call site — the worker's
// publishTarget() calls publisher.Publish(ctx, token, account.PlatformUserID,
// payload) and expects a *models.PublishResult. The reconciler goroutine
// (new in Taglio 4.2) drives the async state machine via the AsyncPublisher
// capability (CheckPublishStatus / Reconcile) instead of this method.
func (s *TikTokOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformTikTok, time.Now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}
	publishID, state, err := s.StartPublish(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return nil, err
	}
	slog.Info("TikTok: publish initialized (worker will store publish_id + state, reconciler will poll)", "publish_id", publishID, "state", state)
	return &models.PublishResult{PlatformMediaID: publishID}, nil
}

// StartPublish (Taglio 4.2) is the first step of the async state machine.
// It calls the TikTok /v2/post/publish/video/init/ endpoint and returns
// immediately with the publish_id and the platform's initial state.
// No polling — the reconciler goroutine will call CheckPublishStatus
// on the next tick to advance the state.
func (s *TikTokOAuthService) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error) {
	if err := s.ValidateContent(payload); err != nil {
		return "", "", err
	}

	slog.Info("TikTok: starting async publish (init)")

	postInfo := map[string]interface{}{
		"title":           truncateTikTokTitle(payload.Text),
		"privacy_level":   normalizeTikTokPrivacyLevel(payload.PrivacyLevel),
		"disable_comment": modeIsDisabled(payload.CommentMode),
		"disable_duet":    modeIsDisabled(payload.DuetMode),
	}

	initBody := map[string]interface{}{
		"source_info": map[string]string{
			"source":    "PULL_FROM_URL",
			"video_url": payload.VideoURL,
		},
		"post_info": postInfo,
	}

	jsonBody, _ := json.Marshal(initBody)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.tiktokapis.com/v2/post/publish/video/init/",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", "", fmt.Errorf("tiktok init request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("tiktok init failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("tiktok init returned status %d: %s", resp.StatusCode, string(body))
	}

	var initResult struct {
		Data struct {
			PublishID string `json:"publish_id"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &initResult); err != nil {
		return "", "", fmt.Errorf("tiktok init parse: %w", err)
	}

	publishID = initResult.Data.PublishID
	state = initResult.Data.Status
	slog.Info("TikTok: async publish initialized", "publish_id", publishID, "state", state)
	return publishID, state, nil
}

// CheckPublishStatus (Taglio 4.2) does a SINGLE GET to the TikTok status
// endpoint. Returns the platform's current state string. Does NOT poll.
// The reconciler goroutine calls this on every tick to advance the
// post_target through the async state machine.
//
// Expected state values (from TikTok API docs):
//   - PROCESSING_UPLOAD — TikTok is fetching the video from the URL
//   - PENDING_PUBLISH   — video received, waiting for processing
//   - IN_REVIEW         — TikTok is reviewing the video
//   - PUBLISH_COMPLETE  — video is live
//   - FAILED            — publish failed
func (s *TikTokOAuthService) CheckPublishStatus(ctx context.Context, accessToken, publishID string) (state string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://open.tiktokapis.com/v2/post/publish/status/fetch/", nil)
	if err != nil {
		return "", fmt.Errorf("tiktok status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	q := req.URL.Query()
	q.Set("publish_id", publishID)
	req.URL.RawQuery = q.Encode()

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tiktok status fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tiktok status returned status %d: %s", resp.StatusCode, string(body))
	}

	var statusResult struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &statusResult); err != nil {
		return "", fmt.Errorf("tiktok status parse: %w", err)
	}
	return statusResult.Data.Status, nil
}

// ContinuePublish (Taglio 4.2) is a no-op for PULL_FROM_URL. The platform
// fetches the video directly from the URL set in StartPublish. Provided
// for forward-compat with PULL_FROM_FILE flows that would do chunked
// upload here.
func (s *TikTokOAuthService) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	// PULL_FROM_URL: TikTok already has the video from StartPublish.
	// No continuation needed.
	return nil
}

// Reconcile (Taglio 4.2) is the terminal-state detector the reconciler
// goroutine calls. It combines CheckPublishStatus with transition logic:
//
//   PUBLISH_COMPLETE → returns *PublishResult (success, terminal)
//   FAILED          → returns error (terminal)
//   in-flight       → returns (nil, nil) — caller should retry next tick
//
// The reconciler in the worker uses this contract: nil result + nil err
// means "leave the target alone, check again next tick". A non-nil result
// means "transition to published". A non-nil err means "transition to failed".
func (s *TikTokOAuthService) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	state, err := s.CheckPublishStatus(ctx, accessToken, publishID)
	if err != nil {
		return nil, err
	}
	switch state {
	case "PUBLISH_COMPLETE":
		return &models.PublishResult{PlatformMediaID: publishID}, nil
	case "FAILED":
		return nil, fmt.Errorf("tiktok publish failed: publish_id=%s state=%s", publishID, state)
	default:
		// PROCESSING_UPLOAD, PENDING_PUBLISH, IN_REVIEW — still in flight.
		// Caller (reconciler goroutine) leaves the target as-is and
		// checks again on the next tick.
		return nil, nil
	}
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// TikTok implements both Publisher (sync legacy path / direct publish)
// AND AsyncPublisher (Taglio 4.2 four-step state machine). The router
// uses AsyncPublisher when present, falling back to Publisher only on
// platforms that don't satisfy the async state machine.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ Provider         = (*TikTokOAuthService)(nil)
	_ OAuthProvider    = (*TikTokOAuthService)(nil)
	_ ContentValidator = (*TikTokOAuthService)(nil)
	_ Publisher        = (*TikTokOAuthService)(nil)
	_ AsyncPublisher   = (*TikTokOAuthService)(nil)
)

// --- Private ---

type tiktokTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

// tikTokTitleMaxRunes is TikTok's documented per-post title/caption limit.
const tikTokTitleMaxRunes = 4000

func truncateTikTokTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= tikTokTitleMaxRunes {
		return s
	}
	return string(runes[:tikTokTitleMaxRunes])
}

func normalizeTikTokPrivacyLevel(level string) string {
	// Taglio 4b: ValidateContent already rejected empty/unrecognized
	// values, so this switch always matches. No default fallback.
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE":
		return "PUBLIC_TO_EVERYONE"
	case "MUTUAL_FOLLOW_FRIENDS":
		return "MUTUAL_FOLLOW_FRIENDS"
	case "SELF_ONLY":
		return "SELF_ONLY"
	default:
		return ""
	}
}

// validateTikTokPrivacyLevel returns an error if level is not one of the
// three TikTok-recognized privacy values. Used by ValidateContent.
func validateTikTokPrivacyLevel(level string) error {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE", "MUTUAL_FOLLOW_FRIENDS", "SELF_ONLY":
		return nil
	default:
		return fmt.Errorf("tiktok privacy_level must be one of PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, SELF_ONLY (got %q)", level)
	}
}

func modeIsDisabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "no_comments", "no_duet", "disabled", "off", "false", "0":
		return true
	default:
		return false
	}
}

func (s *TikTokOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*tiktokTokenResponse, error) {
	body := url.Values{}
	body.Set("client_key", s.cfg.TikTokClientKey)
	body.Set("client_secret", s.cfg.TikTokClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.TikTokRedirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://open.tiktokapis.com/v2/oauth/token/",
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
		slog.Error("TikTok: token exchange failed",
			"status", resp.StatusCode,
			"response", truncateForLog(string(respBody), 200),
			"client_key_prefix", maskClientKey(s.cfg.TikTokClientKey),
			"redirect_uri", s.cfg.TikTokRedirectURI)
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr tiktokTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *TikTokOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://open.tiktokapis.com/v2/user/info/?fields=open_id,display_name", nil)
	if err != nil {
		return nil, fmt.Errorf("user info request creation: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
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
