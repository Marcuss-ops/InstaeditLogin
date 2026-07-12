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

// TikTokOAuthService implements the TikTok provider. Taglio 2.1: each
// provider only carries the methods it actually supports — no more
// composition onto a single monolithic PlatformService.
//
// Capabilities exposed:
//   - OAuthProvider (login flow)
//   - ContentValidator (video_url required; caption ≤ 4000 runes)
//   - Publisher (async video init + poll)
//   - PublishReconciler (poll the publish status post-init)
//   - AccountManager (Validate / Revoke — non-interface helpers)
type TikTokOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewTikTokOAuthService creates a new TikTokOAuthService. Taglio 2.1:
// the constructor no longer takes a tokenRepo.
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

// ValidateContent enforces TikTok's hard requirements: a video_url and
// caption not exceeding 4000 runes. Privacy/comment/duet modes are
// validated separately by the public-API normalisation functions below.
func (s *TikTokOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("tiktok requires a video_url for publishing")
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

// Publish initiates a TikTok video publish and returns the publish_id.
// TikTok's flow is async: the initial POST returns immediately and the
// caller (the publish worker) reconciles via ReconcilePublish.
func (s *TikTokOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformTikTok, time.Now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}

	slog.Info("TikTok: initiating video publish")

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

	// Return publish_id as the platform_media_id so the publish worker
	// can reconcile later. The PublishReconciler contract says callers
	// pass the platform_media_id back to ReconcilePublish.
	return &models.PublishResult{
		PlatformMediaID: publishID,
	}, nil
}

// ReconcilePublish polls the TikTok status endpoint for a previously
// initiated publish_id. The publish worker calls this when a target
// has been moved to status='publishing' but Publish returned a
// publish_id (no terminal platform_media_id yet).
//
// Behaviour:
//   - PUBLISH_COMPLETE → returns the publish_id as platform_media_id
//   - FAILED → returns an error
//   - any other state → returns an error indicating "still in flight"
//     (the worker re-tries on the next tick)
func (s *TikTokOAuthService) ReconcilePublish(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("tiktok reconcile cancelled: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}

		req, _ := http.NewRequestWithContext(ctx, "GET",
			"https://open.tiktokapis.com/v2/post/publish/status/fetch/", nil)
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

		switch statusResult.Data.Status {
		case "PUBLISH_COMPLETE":
			return &models.PublishResult{PlatformMediaID: publishID}, nil
		case "FAILED":
			return nil, fmt.Errorf("tiktok publish failed: %s", string(body))
		}
	}

	return nil, fmt.Errorf("tiktok reconcile timed out for publish_id %s", publishID)
}

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
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE":
		return "PUBLIC_TO_EVERYONE"
	case "MUTUAL_FOLLOW_FRIENDS":
		return "MUTUAL_FOLLOW_FRIENDS"
	case "SELF_ONLY":
		return "SELF_ONLY"
	case "":
		return "PUBLIC_TO_EVERYONE"
	default:
		slog.Warn("TikTok: unrecognized privacy_level, defaulting to PUBLIC_TO_EVERYONE", "input", level)
		return "PUBLIC_TO_EVERYONE"
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
