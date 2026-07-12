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

// FacebookOAuthService implements the Meta-Facebook provider as a set of
// small capabilities. Taglio 2.1: each provider only carries the
// methods it actually supports — no more composition onto a single
// monolithic PlatformService.
//
// Capabilities exposed:
//   - OAuthProvider (login flow)
//   - AccountDiscoverer (Facebook Pages the user manages)
//   - ContentValidator (text or image required)
//   - Publisher (Page feed / Page photo)
//   - AccountManager (Validate / Revoke — non-interface helpers used by
//     the handlers' account lifecycle methods).
type FacebookOAuthService struct {
	base        *MetaOAuthBase
	redirectURI string
}

// NewFacebookOAuthService creates a new FacebookOAuthService. Returns
// nil when the redirect URI is not configured (provider disabled).
// Taglio 2.1: the constructor no longer takes a tokenRepo — token
// persistence is the TokenService's job, not the provider's.
func NewFacebookOAuthService(cfg *config.Config) (*FacebookOAuthService, error) {
	if cfg.FacebookRedirectURI == "" {
		return nil, nil // provider disabled
	}

	base := NewMetaOAuthBase(cfg)

	return &FacebookOAuthService{
		base:        base,
		redirectURI: cfg.FacebookRedirectURI,
	}, nil
}

// Name returns the platform identifier.
func (s *FacebookOAuthService) Name() string { return models.PlatformFacebook }

// GetLoginURL builds the Meta OAuth login URL with Facebook Page scopes.
func (s *FacebookOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.base.cfg.MetaAppID)
	params.Set("redirect_uri", s.redirectURI)
	params.Set("state", state)
	params.Set("scope", "pages_manage_posts,pages_read_engagement,pages_show_list")
	params.Set("response_type", "code")

	return "https://www.facebook.com/v19.0/dialog/oauth?" + params.Encode()
}

// HandleCallback processes the full OAuth callback for Facebook.
func (s *FacebookOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Facebook: exchanging code for short-lived token")
	shortLived, err := s.base.ExchangeCodeForToken(ctx, code, s.redirectURI)
	if err != nil {
		return nil, nil, fmt.Errorf("step 1 (code exchange): %w", err)
	}

	slog.Info("Facebook: exchanging for long-lived token")
	longLived, err := s.base.ExchangeForLongLivedToken(ctx, shortLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 2 (long-lived exchange): %w", err)
	}

	slog.Info("Facebook: fetching user info")
	profile, err := s.base.GetUserInfo(ctx, longLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 3 (user info): %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
		Scopes:      []string{"pages_manage_posts", "pages_read_engagement", "pages_show_list"},
	}

	return profile, tokenData, nil
}

// RefreshOAuthToken extends a long-lived token via fb_exchange_token.
func (s *FacebookOAuthService) RefreshOAuthToken(ctx context.Context, currentToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformFacebook, &err)
	if currentToken == "" {
		return nil, fmt.Errorf("facebook RefreshOAuthToken: empty current token")
	}
	slog.Info("Facebook: refreshing long-lived token via fb_exchange_token")
	longLived, err := s.base.ExchangeForLongLivedToken(ctx, currentToken)
	if err != nil {
		return nil, fmt.Errorf("facebook refresh failed: %w", err)
	}
	return &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
	}, nil
}

// ValidateContent enforces Facebook's "text OR image" rule before
// dispatching the publish call. Empty payloads would otherwise fail
// deep inside the Graph API with a 400.
func (s *FacebookOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.Text == "" && payload.ImageURL == "" {
		return fmt.Errorf("facebook requires either text or an image_url")
	}
	return nil
}

// DiscoverAccounts returns the Facebook Pages the user manages.
// Required scope: pages_show_list. The publish flow then uses the first
// page's access token (a Page Access Token is distinct from the user
// access token returned by the OAuth callback).
func (s *FacebookOAuthService) DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*models.PlatformAccount, error) {
	pages, err := s.getPages(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("facebook pages lookup: %w", err)
	}
	accounts := make([]*models.PlatformAccount, 0, len(pages))
	for _, p := range pages {
		accounts = append(accounts, &models.PlatformAccount{
			Platform:       models.PlatformFacebook,
			PlatformUserID: p.ID,
			Username:       p.Name,
		})
	}
	return accounts, nil
}

// Validate is a non-interface helper used by the handlers' account
// lifecycle methods (Taglio 1.4 will route the 501 stubs through here).
func (s *FacebookOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.facebook.com/v19.0/me?fields=id&access_token="+url.QueryEscape(accessToken), nil)
	if err != nil {
		return fmt.Errorf("facebook validate request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("facebook validate failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("facebook validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Revoke calls Meta's DELETE /me/permissions endpoint to invalidate
// the user access token. Note: Page Access Tokens obtained via
// /me/accounts are independent and survive the user-token revoke.
func (s *FacebookOAuthService) Revoke(ctx context.Context, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		fmt.Sprintf("https://graph.facebook.com/v19.0/me/permissions?access_token=%s", url.QueryEscape(accessToken)), nil)
	if err != nil {
		return fmt.Errorf("facebook revoke request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("facebook revoke failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("facebook revoke returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Publish publishes content to a Facebook Page.
// Supports text-only posts and single-image posts. Videos, albums, groups,
// and personal profiles are not supported yet.
func (s *FacebookOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformFacebook, time.Now(), &err)

	pages, err := s.getPages(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("facebook pages lookup: %w", err)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no Facebook Page found for this user — grant pages_show_list and pages_manage_posts permissions")
	}

	page := pages[0]
	slog.Info("Facebook: publishing to page", "page_id", page.ID, "page_name", page.Name)

	var mediaID string
	if payload.ImageURL != "" {
		mediaID, err = s.publishPagePhoto(ctx, page.AccessToken, page.ID, payload.ImageURL, payload.Text)
	} else if payload.Text != "" {
		mediaID, err = s.publishPageFeed(ctx, page.AccessToken, page.ID, payload.Text)
	} else {
		return nil, fmt.Errorf("facebook requires text content or an image_url")
	}

	if err != nil {
		return nil, fmt.Errorf("facebook publish failed: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: mediaID,
		PlatformURL:     fmt.Sprintf("https://www.facebook.com/%s", mediaID),
	}, nil
}

// --- Facebook-specific methods ---

func (s *FacebookOAuthService) getPages(ctx context.Context, accessToken string) ([]models.MetaPage, error) {
	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/me/accounts?access_token=%s", accessToken)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("accounts request: %w", err)
	}

	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("accounts request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read accounts response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("accounts request returned status %d: %s", resp.StatusCode, truncateForLog(string(body), 200))
	}

	var accountsResp models.MetaAccountsResponse
	if err := json.Unmarshal(body, &accountsResp); err != nil {
		return nil, fmt.Errorf("parse accounts response: %w", err)
	}

	return accountsResp.Data, nil
}

func (s *FacebookOAuthService) publishPageFeed(ctx context.Context, pageAccessToken, pageID, message string) (string, error) {
	params := url.Values{}
	params.Set("message", message)
	params.Set("access_token", pageAccessToken)

	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/feed", pageID)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("feed request: %w", err)
	}

	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("feed request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read feed response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("feed publish failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 200))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse feed response: %w", err)
	}

	return result.ID, nil
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// The blank identifiers cost zero runtime; they pin the interface set
// the constructor advertises. Drop a line here if a capability is removed
// from the implementation; drop the assertion if a new capability lands.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ Provider           = (*FacebookOAuthService)(nil)
	_ OAuthProvider      = (*FacebookOAuthService)(nil)
	_ ResourceDiscoverer = (*FacebookOAuthService)(nil)
	_ ContentValidator   = (*FacebookOAuthService)(nil)
	_ Publisher          = (*FacebookOAuthService)(nil)
)

func (s *FacebookOAuthService) publishPagePhoto(ctx context.Context, pageAccessToken, pageID, imageURL, caption string) (string, error) {
	params := url.Values{}
	params.Set("url", imageURL)
	params.Set("caption", caption)
	params.Set("access_token", pageAccessToken)

	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/photos", pageID)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("photos request: %w", err)
	}

	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("photos request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read photos response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("photos publish failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 200))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse photos response: %w", err)
	}

	return result.ID, nil
}
