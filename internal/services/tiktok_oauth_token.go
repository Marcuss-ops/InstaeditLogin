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

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// tiktokTokenResponse mirrors the JSON returned by the TikTok
// /v2/oauth/token/ endpoint for both authorization_code and
// refresh_token grants.
type tiktokTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

// exchangeCodeForToken trades the OAuth authorization code for an access
// and refresh token pair.
func (s *TikTokOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*tiktokTokenResponse, error) {
	body := url.Values{}
	body.Set("client_key", s.cfg.TikTokClientID)
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
			"client_key_prefix", maskClientKey(s.cfg.TikTokClientID),
			"redirect_uri", s.cfg.TikTokRedirectURI)
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr tiktokTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

// RefreshOAuthToken exchanges a TikTok refresh token for a new access token.
func (s *TikTokOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformTikTok, &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("tiktok RefreshOAuthToken: empty refresh token")
	}
	slog.Info("TikTok: refreshing access token")
	body := url.Values{}
	body.Set("client_key", s.cfg.TikTokClientID)
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
