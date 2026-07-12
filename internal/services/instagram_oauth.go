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

// InstagramOAuthService implements the Meta-Instagram provider as a set of
// small capabilities. Taglio 4.4: split out from the legacy composite
// Meta-OAuth service. Each Meta-family provider now stands alone — Instagram
// here, Facebook in facebook_oauth.go, Threads in threads_oauth.go — covering
// only the capabilities its platform actually exposes.
//
// Capabilities exposed (Taglio 4.4):
//   - OAuthProvider (Meta OAuth login flow with IG-specific scopes)
//   - ResourceDiscoverer (Instagram Business Accounts linked to the user's
//     Facebook Pages — IG publishing is page-tied; you cannot publish to
//     a personal IG account without it being linked to a Page)
//   - ContentValidator (media-only — Instagram does NOT support text-only
//     posts; ValidateContent requires image_url OR video_url)
//   - Publisher (synchronous — POST /media + POST /media_publish; IG's
//     media_publish returns the final media_id synchronously so no
//     AsyncPublisher is needed)
//   - AccountManager (Validate / Revoke — non-interface helpers)
type InstagramOAuthService struct {
	base        *MetaOAuthBase
	redirectURI string
}

// NewInstagramOAuthService creates a new InstagramOAuthService. Returns
// nil when the redirect URI is not configured (provider disabled).
// Taglio 4.4: split out from the monolithic meta-OAuth provider. Same
// constructor posture as Facebook / Threads: nil = disabled, err = failed.
func NewInstagramOAuthService(cfg *config.Config) (*InstagramOAuthService, error) {
	if cfg.InstagramRedirectURI == "" {
		return nil, nil // provider disabled
	}
	return &InstagramOAuthService{
		base:        NewMetaOAuthBase(cfg),
		redirectURI: cfg.InstagramRedirectURI,
	}, nil
}

// Name returns the platform identifier.
func (s *InstagramOAuthService) Name() string { return models.PlatformInstagram }

// GetLoginURL builds the Meta OAuth login URL with Instagram-specific
// scopes. Instagram Graph API publishing requires:
//   - instagram_basic: read the IG profile
//   - instagram_content_publish: post media on behalf of the user
//   - pages_show_list: needed to discover the IG Business Accounts linked
//     to the user's Pages (a personal IG without a linked Page cannot
//     be published to via the Graph API)
func (s *InstagramOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.base.cfg.MetaAppID)
	params.Set("redirect_uri", s.redirectURI)
	params.Set("state", state)
	params.Set("scope", "instagram_basic,instagram_content_publish,pages_show_list")
	params.Set("response_type", "code")

	return "https://www.facebook.com/v19.0/dialog/oauth?" + params.Encode()
}

// HandleCallback processes the full OAuth callback for Instagram. The
// short-lived → long-lived token exchange is identical to Facebook/Threads.
// The user info from /me is the Facebook user; the IG business accounts
// (the actual publish targets) are discovered separately in
// DiscoverAccounts so the caller can create one PlatformAccount per IG
// business account at OAuth-connect time.
func (s *InstagramOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("Instagram: exchanging code for short-lived token")
	shortLived, err := s.base.ExchangeCodeForToken(ctx, code, s.redirectURI)
	if err != nil {
		return nil, nil, fmt.Errorf("step 1 (code exchange): %w", err)
	}

	slog.Info("Instagram: exchanging for long-lived token")
	longLived, err := s.base.ExchangeForLongLivedToken(ctx, shortLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 2 (long-lived exchange): %w", err)
	}

	slog.Info("Instagram: fetching user info")
	profile, err := s.base.GetUserInfo(ctx, longLived.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("step 3 (user info): %w", err)
	}

	return profile, &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
		Scopes:      []string{"instagram_basic", "instagram_content_publish", "pages_show_list"},
	}, nil
}

// RefreshOAuthToken extends a long-lived token via fb_exchange_token. Same
// flow as Facebook/Threads because Meta long-lived exchanges are not
// platform-specific.
func (s *InstagramOAuthService) RefreshOAuthToken(ctx context.Context, currentToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformInstagram, &err)
	if currentToken == "" {
		return nil, fmt.Errorf("instagram RefreshOAuthToken: empty current token")
	}
	slog.Info("Instagram: refreshing long-lived token via fb_exchange_token")
	longLived, err := s.base.ExchangeForLongLivedToken(ctx, currentToken)
	if err != nil {
		return nil, fmt.Errorf("instagram refresh failed: %w", err)
	}
	return &models.TokenData{
		AccessToken: longLived.AccessToken,
		TokenType:   models.TokenTypeLongLived,
		ExpiresIn:   longLived.ExpiresIn,
	}, nil
}

// ValidateContent enforces Instagram's media-only rule: a post MUST carry
// image_url OR video_url. Text-only posts are NOT supported by the Instagram
// Graph API — the only "text" first-class entity is the caption on a media
// post, and a post with no media is rejected at the /media endpoint.
//
// Note: caption (payload.Text) is OPTIONAL — IG allows image-only and
// video-only posts with no caption. only the media is required.
func (s *InstagramOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.ImageURL == "" && payload.VideoURL == "" {
		return fmt.Errorf("instagram requires image_url or video_url (text-only posts are not supported by the IG Graph API)")
	}
	return nil
}

// DiscoverAccounts returns the Instagram Business Accounts linked to the
// user's Facebook Pages. IG publishing is gated by the IG <-> Page linking —
// a personal IG cannot receive Graph API publish calls. The OAuth-callback
// caller picks the IG account to publish to and stores its id in
// PlatformAccount.PlatformUserID, which the worker then passes as
// platformUserID to Publish.
//
// Why this is a ResourceDiscoverer (not a one-shot publisher dispatch):
// Publishing reads PlatformAccount.PlatformUserID as the dispatch key
// (the IG business account id). The OAuth callback returns ONE Facebook
// user; we expand that into N PlatformAccounts (one per linked IG business
// account) here so the worker can later route by IG id.
func (s *InstagramOAuthService) DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*models.PlatformAccount, error) {
	igAccounts, err := s.discoverInstagramBusinessAccounts(ctx, accessToken)
	if err != nil {
		return nil, fmt.Errorf("instagram business account lookup: %w", err)
	}
	accounts := make([]*models.PlatformAccount, 0, len(igAccounts))
	for _, ig := range igAccounts {
		accounts = append(accounts, &models.PlatformAccount{
			Platform:       models.PlatformInstagram,
			PlatformUserID: ig.ID,
			Username:       ig.Username,
		})
	}
	return accounts, nil
}

// Validate checks that the long-lived token is still valid for IG business
// calls via the Meta debug_token endpoint. The endpoint requires app-level
// auth — we pass the Meta app_id|secret pair as the access_token parameter
// (this is documented Meta behaviour for app-level diagnostics).
func (s *InstagramOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	appToken := s.base.cfg.MetaAppID + "|" + s.base.cfg.MetaAppSecret
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://graph.facebook.com/v19.0/debug_token?input_token="+url.QueryEscape(accessToken)+
			"&access_token="+url.QueryEscape(appToken), nil)
	if err != nil {
		return fmt.Errorf("instagram validate request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("instagram validate failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("instagram validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Revoke calls Meta's DELETE /me/permissions endpoint to invalidate the
// user access token. The Facebook-user-level revoke invalidates every
// downstream Page Access Token (and thus every IG Business Account Access
// Token derived from this OAuth grant).
func (s *InstagramOAuthService) Revoke(ctx context.Context, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		fmt.Sprintf("https://graph.facebook.com/v19.0/me/permissions?access_token=%s", url.QueryEscape(accessToken)), nil)
	if err != nil {
		return fmt.Errorf("instagram revoke request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("instagram revoke failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("instagram revoke returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Publish creates an IG media container and immediately publishes it. The
// two-step flow (container_create + media_publish) is the documented
// Instagram Graph API pattern; both calls return synchronously and the
// final media_id is available after media_publish — so this is a sync
// Publisher (no AsyncPublisher needed).
//
// Per ValidateContent the caller has already ensured image_url OR
// video_url is set. caption (payload.Text) is optional and forwarded to
// the container_create call.
//
// platformUserID must be the IG business account id (NOT the Facebook user
// id). The OAuth-connect flow populates PlatformAccount.PlatformUserID
// with this value via DiscoverAccounts.
func (s *InstagramOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformInstagram, time.Now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}
	if platformUserID == "" {
		return nil, fmt.Errorf("instagram Publish: empty platform_user_id (IG business account id)")
	}

	// Step 1: create the media container.
	containerID, err := s.createMediaContainer(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return nil, fmt.Errorf("instagram container create failed: %w", err)
	}
	slog.Info("Instagram: media container created", "ig_user_id", platformUserID, "container_id", containerID)

	// Step 2: publish the container. IG's media_publish can return 400 if
	// the container is not yet finished processing server-side (rare but
	// observed on slow uploads). Without a real polling loop we'd block
	// the worker here; for the v1 implementation we publish immediately
	// and surface the 400 as an error. Future Taglio (>4.4) will move
	// this to an AsyncPublisher pattern for IG specifically.
	mediaID, err := s.publishMediaContainer(ctx, accessToken, platformUserID, containerID)
	if err != nil {
		return nil, fmt.Errorf("instagram media_publish failed: %w", err)
	}

	return &models.PublishResult{
		PlatformMediaID: mediaID,
		PlatformURL:     fmt.Sprintf("https://www.instagram.com/p/%s", mediaID),
	}, nil
}

// -----------------------------------------------------------------------------
// Instagram-specific helpers (private)
// -----------------------------------------------------------------------------

// igBusinessAccount is the minimal shape needed from
// GET /{page_id}?fields=instagram_business_account.
type igBusinessAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

// discoverInstagramBusinessAccounts walks the user's Pages and returns
// the IG business account linked to each. A user may have multiple Pages
// with their own IG business accounts; the publish flow will be called
// once per (post × IG account) tuple.
//
// Pages without a linked IG account are skipped silently (warn-logged)
// rather than aborting the whole discovery — a user managing 5 Pages
// where 2 lack IG should still see the 3 that have it.
func (s *InstagramOAuthService) discoverInstagramBusinessAccounts(ctx context.Context, accessToken string) ([]igBusinessAccount, error) {
	pagesReqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/me/accounts?fields=id,name&access_token=%s", accessToken)
	pagesReq, err := http.NewRequestWithContext(ctx, "GET", pagesReqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("pages list request: %w", err)
	}
	pagesResp, err := s.base.httpClient.Do(pagesReq)
	if err != nil {
		return nil, fmt.Errorf("pages list failed: %w", err)
	}
	defer pagesResp.Body.Close()
	pagesBody, _ := io.ReadAll(pagesResp.Body)
	if pagesResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pages list returned status %d: %s", pagesResp.StatusCode, truncateForLog(string(pagesBody), 200))
	}
	var pagesResp2 models.MetaAccountsResponse
	if err := json.Unmarshal(pagesBody, &pagesResp2); err != nil {
		return nil, fmt.Errorf("pages list parse: %w", err)
	}

	out := make([]igBusinessAccount, 0, len(pagesResp2.Data))
	for _, p := range pagesResp2.Data {
		igReqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s?fields=instagram_business_account&access_token=%s",
			p.ID, accessToken)
		igReq, err := http.NewRequestWithContext(ctx, "GET", igReqURL, nil)
		if err != nil {
			slog.Warn("Instagram: failed to expand IG business account for page", "page_id", p.ID, "error", err)
			continue
		}
		igResp, err := s.base.httpClient.Do(igReq)
		if err != nil {
			slog.Warn("Instagram: failed to expand IG business account for page", "page_id", p.ID, "error", err)
			continue
		}
		igBody, _ := io.ReadAll(igResp.Body)
		igResp.Body.Close()
		if igResp.StatusCode != http.StatusOK {
			slog.Warn("Instagram: linked IG account lookup failed for page", "page_id", p.ID, "status", igResp.StatusCode, "body", truncateForLog(string(igBody), 200))
			continue
		}
		var pageWith struct {
			InstagramBusinessAccount struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"instagram_business_account"`
		}
		if err := json.Unmarshal(igBody, &pageWith); err != nil {
			slog.Warn("Instagram: linked IG account parse failed for page", "page_id", p.ID, "error", err)
			continue
		}
		if pageWith.InstagramBusinessAccount.ID == "" {
			// Page has no IG business account linked — skip silently.
			continue
		}
		out = append(out, igBusinessAccount{
			ID:       pageWith.InstagramBusinessAccount.ID,
			Username: pageWith.InstagramBusinessAccount.Username,
		})
	}
	return out, nil
}

// createMediaContainer POSTs to /{igUserID}/media with image_url or
// video_url (mutually exclusive — ValidateContent enforces at least one).
// Returns the container_id which is then published via publishMediaContainer.
func (s *InstagramOAuthService) createMediaContainer(ctx context.Context, accessToken, igUserID string, payload models.PublishPayload) (string, error) {
	params := url.Values{}
	params.Set("access_token", accessToken)
	if payload.ImageURL != "" {
		params.Set("image_url", payload.ImageURL)
	}
	if payload.VideoURL != "" {
		params.Set("video_url", payload.VideoURL)
	}
	if payload.Text != "" {
		params.Set("caption", payload.Text)
	}

	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/media", igUserID)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("instagram container request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("instagram container request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("instagram container failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("instagram container parse: %w", err)
	}
	return result.ID, nil
}

// publishMediaContainer POSTs to /{igUserID}/media_publish with the
// container_id, returning the final media_id (the permanent identifier
// of the published IG post).
func (s *InstagramOAuthService) publishMediaContainer(ctx context.Context, accessToken, igUserID, containerID string) (string, error) {
	params := url.Values{}
	params.Set("creation_id", containerID)
	params.Set("access_token", accessToken)

	reqURL := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/media_publish", igUserID)
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("instagram publish request: %w", err)
	}
	resp, err := s.base.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("instagram publish request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("instagram publish failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 200))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("instagram publish parse: %w", err)
	}
	return result.ID, nil
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Instagram implements per its actual capability surface:
//   - Provider (Name via Name())
//   - OAuthProvider (login flow + refresh)
//   - ResourceDiscoverer (IG Business Accounts linked to Pages)
//   - ContentValidator (media-only — rejects text-only)
//   - Publisher (sync container_create + media_publish)
// NOT AsyncPublisher — Instagram's publish returns the final media_id
// synchronously via /media_publish, so no AsyncPublisher state machine
// is required.
// Taglio 4.4.
// -----------------------------------------------------------------------------
var (
	_ Provider           = (*InstagramOAuthService)(nil)
	_ OAuthProvider      = (*InstagramOAuthService)(nil)
	_ ResourceDiscoverer = (*InstagramOAuthService)(nil)
	_ ContentValidator   = (*InstagramOAuthService)(nil)
	_ Publisher          = (*InstagramOAuthService)(nil)
)
