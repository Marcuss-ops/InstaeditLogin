package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// MetaOAuthBase holds the shared Meta Graph API OAuth infrastructure used by
// all three Meta-family providers: Instagram, Facebook, and Threads.
//
// The OAuth flow is identical across all three — they all go through
// facebook.com/dialog/oauth, exchange the code at graph.facebook.com, and
// fetch user info from /me. What differs is the scopes, redirect URI, and
// publishing logic, which each wrapper provider controls independently.
//
// Taglio 2.1: token persistence is handled by the central CredentialVault.
// MetaOAuthBase carries only OAuth-level helpers.
type MetaOAuthBase struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
}

// NewMetaOAuthBase creates the shared OAuth client. Accepts optional
// ProviderDependencies for HTTP client + clock injection.
func NewMetaOAuthBase(cfg *config.Config, deps ...ProviderDependencies) *MetaOAuthBase {
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	return &MetaOAuthBase{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
	}
}

// now returns the current time via the injected clock, or time.Now as default.
func (b *MetaOAuthBase) now() time.Time {
	if b.clock != nil {
		return b.clock()
	}
	return time.Now()
}

// ExchangeCodeForToken trades an OAuth authorization code for a short-lived
// access token. This is step 1 of the Meta OAuth flow.
func (b *MetaOAuthBase) ExchangeCodeForToken(ctx context.Context, code, redirectURI string) (*models.MetaTokenResponse, error) {
	params := url.Values{}
	params.Set("client_id", b.cfg.MetaAppID)
	params.Set("client_secret", b.cfg.MetaAppSecret)
	params.Set("redirect_uri", redirectURI)
	params.Set("code", code)

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.facebook.com/v19.0/oauth/access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

// ExchangeForLongLivedToken extends a short-lived token into a long-lived
// (~60 day) token via fb_exchange_token. Used both during initial login and
// for token refresh.
func (b *MetaOAuthBase) ExchangeForLongLivedToken(ctx context.Context, shortLivedToken string) (*models.MetaLongLivedTokenResponse, error) {
	params := url.Values{}
	params.Set("grant_type", "fb_exchange_token")
	params.Set("client_id", b.cfg.MetaAppID)
	params.Set("client_secret", b.cfg.MetaAppSecret)
	params.Set("fb_exchange_token", shortLivedToken)

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.facebook.com/v19.0/oauth/access_token?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create long-lived token request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("long-lived token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read long-lived token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("long-lived token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}

	var tokenResp models.MetaLongLivedTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse long-lived token response: %w", err)
	}

	return &tokenResp, nil
}

// GetUserInfo fetches the authenticated user's profile from /me.
func (b *MetaOAuthBase) GetUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	params := url.Values{}
	params.Set("fields", "id,name,email")
	params.Set("access_token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "GET", "https://graph.facebook.com/v19.0/me?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create user info request: %w", err)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get user info: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read user info: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse user info: %w", err)
	}

	return &models.PlatformProfile{
		PlatformUserID: result.ID,
		Username:       result.Name,
		Email:          result.Email,
		Name:           result.Name,
	}, nil
}

// PostJSON is a low-level helper that POSTs a JSON body to the given URL and
// decodes the response into result. Used by Instagram and Facebook providers
// for platform-specific Graph API calls.
func (b *MetaOAuthBase) PostJSON(ctx context.Context, urlStr string, body interface{}, result interface{}) (int, []byte, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", urlStr, bytes.NewReader(jsonBody))
	if err != nil {
		return 0, nil, fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, fmt.Errorf("read: %w", err)
	}

	if result != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := json.Unmarshal(respBody, result); err != nil {
			return resp.StatusCode, respBody, fmt.Errorf("unmarshal: %w", err)
		}
	}

	return resp.StatusCode, respBody, nil
}
