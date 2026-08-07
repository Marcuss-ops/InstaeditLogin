package services

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"io"
	"log/slog"
	"net/http"
)

func (s *YouTubeOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	return s.HandleCallbackWithClient(ctx, state, code, nil)
}

// HandleCallbackWithClient exchanges the authorization code using the
// given pool client (YouTube OAuth Client Pool). The callback MUST use
// the client that built the consent URL — the authorization code was
// issued against that client_id + redirect_uri and Google rejects an
// exchange against a different client. The pkg/api handler resolves the
// client from the signed state's oauth_client_key and passes it here;
// a nil client falls back to the legacy single-client config.
func (s *YouTubeOAuthService) HandleCallbackWithClient(ctx context.Context, state, code string, client *YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("YouTube: exchanging code for token", "oauth_client_key", clientKeyForLog(client))

	tokenResp, err := s.exchangeCodeForTokenWithClient(ctx, code, client)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube token exchange: %w", err)
	}

	slog.Info("YouTube: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:           tokenResp.AccessToken,
		RefreshToken:          tokenResp.RefreshToken,
		ProviderSubjectID:     profile.ProviderSubjectID,
		TokenType:             models.TokenTypeBearer,
		ExpiresIn:             tokenResp.ExpiresIn,
		RefreshTokenExpiresIn: tokenResp.RefreshTokenExpiresIn,
		Scopes:                nonEmptyScopes(tokenResp.Scope),
	}

	return profile, tokenData, nil
}

// clientKeyForLog returns a log-safe pool-client label: the client's
// Key when present, the legacy marker otherwise. Never includes
// credential material.
func clientKeyForLog(client *YouTubeOAuthClientConfig) string {
	if client == nil {
		return "legacy_single_client"
	}
	return client.Key
}

func (s *YouTubeOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
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
		return nil, fmt.Errorf("user info failed (status %d)", resp.StatusCode)
	}

	var result struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID:    result.ID,
		ProviderSubjectID: result.ID,
		Username:          result.Name,
		Name:              result.Name,
		Email:             result.Email,
	}, nil
}
