package services

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// GetLoginURL returns the TikTok OAuth authorization URL.
func (s *TikTokOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

// GetLoginURLWithOptions builds the TikTok OAuth authorization URL.
func (s *TikTokOAuthService) GetLoginURLWithOptions(state string, _ OAuthLoginOptions) string {
	params := url.Values{}
	params.Set("client_key", s.cfg.TikTokClientID)
	params.Set("redirect_uri", s.cfg.TikTokRedirectURI)
	params.Set("state", state)
	// Mirrors the App Review submission: Login Kit = user.info.basic;
	// Content Posting API = video.publish (Direct Post) + video.upload
	// (Upload-as-Draft flow, used only when the client opts into
	// PULL_FROM_FILE via PublishPayload.Source). TikTok requires the
	// exact scope list at App Review — a drift between this string
	// and the dashboard's "Products / Scopes" causes App Review
	// rejection.
	params.Set("scope", "user.info.basic,video.publish,video.upload")
	params.Set("response_type", "code")

	loginURL := "https://www.tiktok.com/v2/auth/authorize/?" + params.Encode()
	slog.Info("TikTok: built login URL",
		"redirect_uri", s.cfg.TikTokRedirectURI,
		"client_key_prefix", maskClientKey(s.cfg.TikTokClientID),
		"scope", params.Get("scope"))
	return loginURL
}

// HandleCallback exchanges the OAuth authorization code for a user profile
// and token data.
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

// Revoke is not supported by the TikTok API.
func (s *TikTokOAuthService) Revoke(ctx context.Context, accessToken string) error {
	return ErrRevokeUnsupported
}
