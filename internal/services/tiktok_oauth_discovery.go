package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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

// getUserInfo fetches the TikTok user profile for the given access token.
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
