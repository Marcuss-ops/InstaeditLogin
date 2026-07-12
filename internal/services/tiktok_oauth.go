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
	cfg *config.Config
	*TokenHelper
	httpClient *http.Client
}

// NewTikTokOAuthService creates a new TikTokOAuthService.
func NewTikTokOAuthService(cfg *config.Config, tokenRepo *repository.TokenRepository) (*TikTokOAuthService, error) {
	encryptor, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	return &TikTokOAuthService{
		cfg:         cfg,
		TokenHelper: NewTokenHelper(encryptor, tokenRepo),
		httpClient:  NewHTTPClient(),
	}, nil
}

func (s *TikTokOAuthService) Name() string { return models.PlatformTikTok }

// maskClientKey restituisce una versione mascherata della client key per i log.
// Mostra solo i primi 4 caratteri per chiavi corte, oppure primi/ultimi 4 per
// chiavi lunghe, in modo da non esporre mai l'intera chiave.
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

// truncateForLog restituisce una versione troncata di una stringa per i log,
// evitando di riversare corpi di risposta potenzialmente grandi o sensibili.
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
	// user.info.basic: lettura profilo
	// video.publish: pubblicazione diretta su TikTok
	// Aggiungi video.upload se vuoi anche l'upload come draft da editare in app.
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

// Revoke is not supported by the TikTok API. The caller should proceed with
// local token deletion.
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

func (s *TikTokOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformTikTok, time.Now(), &err)
	if payload.VideoURL == "" {
		return nil, fmt.Errorf("tiktok requires a video_url for publishing")
	}

	slog.Info("TikTok: initiating video publish")

	// Step 1: Initialize upload.
	//
	// TikTok expects a nested `post_info` object that carries caption/title,
	// privacy_level, and the disable_* toggle flags. Caption (carried in
	// payload.Text per the shared PublishPayload) is truncated to TikTok's
	// 4000-character limit. Empty privacy_level falls back to
	// PUBLIC_TO_EVERYONE so callers don't have to set it explicitly.
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
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

// tikTokTitleMaxRunes is TikTok's documented per-post title/caption limit.
const tikTokTitleMaxRunes = 4000

// truncateTikTokTitle truncates a caption to TikTok's 4000-rune limit,
// counting Unicode code points so we don't accidentally slice a multi-byte
// sequence in the middle.
func truncateTikTokTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= tikTokTitleMaxRunes {
		return s
	}
	return string(runes[:tikTokTitleMaxRunes])
}

// normalizeTikTokPrivacyLevel returns a valid TikTok privacy_level enum.
// Empty input or unknown values fall back to PUBLIC_TO_EVERYONE — the
// permissive-but-safe default that's already what the open API returns for
// unconfigured creator accounts.
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

// modeIsDisabled interprets a user-facing "mode" string into the boolean
// flag TikTok expects. Empty / unknown / affirmative values default to
// disabled = false so calls without explicit configuration still behave as
// "comments allowed" and "duets allowed".
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
