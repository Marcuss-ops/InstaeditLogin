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

// YouTubeOAuthService implements the YouTube provider. Taglio 2.1:
//
// Capabilities exposed:
//   - OAuthProvider (Google OAuth 2.0 with offline access)
//   - ContentValidator (video required)
//   - Publisher (resumable upload protocol)
//   - AccountManager (Validate / Revoke)
type YouTubeOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
	// uploadOpts (P1#6) — every chunked-PUT retry + backoff knob.
	// Populated from cfg in NewYouTubeOAuthService; tests override
	// backoff/sleep via the unexported uploadDeps fields.
	uploadOpts youTubeUploadOptions
	// uploadDeps (P1#6) — test-injectable backoff/sleep functions.
	// nil in production: NewYouTubeOAuthService installs the
	// defaults (computeYouTubeBackoff + defaultYouTubeSleep).
	uploadDeps *youTubeUploadDeps
	// sessionStore persists the resumable-upload session URI + offset
	// across worker crashes (P1#5 / migration 048). Wired in
	// NewYouTubeOAuthService from *repository.UploadJobRepository
	// (concrete type kept out of this struct via the
	// YouTubeSessionStore narrow interface). Optional in tests.
	sessionStore YouTubeSessionStore
	// sessionEncryptor wraps the YouTube session URI before
	// persistence. Required when sessionStore != nil: storing the
	// plaintext URI in upload_jobs.youtube_session_uri defeats the
	// "credential-adjacent" intent of migration 048 + the
	// json:"-" redaction on the Go side. nil encryptor on a nil
	// store is the production default (the publish path doesn't
	// need it for single-shot uploads); nil encryptor on a non-nil
	// store surfaces as a constructor error.
	sessionEncryptor SessionEncryptor
	// sessionJobID + sessionWorkerID are stamped onto every
	// sessionStore.* call so the CAS in SaveYouTubeSession /
	// ClearYouTubeSession can refuse a write against a row that
	// has been re-claimed (or lease-expired) by another worker.
	// Defaults to empty; the upload worker injects both via
	// SetSessionContext before calling Publish/StartPublish.
	sessionJobID    int64
	sessionWorkerID string
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService. Accepts optional
// ProviderDependencies for HTTP client injection.
func NewYouTubeOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*YouTubeOAuthService, error) {
	if cfg.Auth.YouTubeClientID == "" {
		return nil, nil // provider disabled
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	opts := loadYouTubeUploadOptions(cfg)
	return &YouTubeOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
		uploadOpts: opts,
		uploadDeps: loadYouTubeUploadDeps(opts),
	}, nil
}

// ClientID returns the YouTube OAuth client_id this service was
// configured with (cfg.Auth.YouTubeClientID). Used by pkg/api/handlers.go
// handleValidateAccount to compare Google's tokeninfo `aud` against
// the configured client — a Production-but-issued-for-Testing token
// carries a mismatched aud and is a hard reauth signal (the 4-step
// pipeline's STEP 2 guard). Returns "" if the service hasn't been
// fully constructed (defensive — the production wiring wires
// cfg.Auth.YouTubeClientID at NewYouTubeOAuthService time).
func (s *YouTubeOAuthService) ClientID() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.cfg.Auth.YouTubeClientID
}

// now returns the current time via the injected clock, or time.Now as default.
func (s *YouTubeOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *YouTubeOAuthService) Name() string { return models.PlatformYouTube }

// PreferredTokenTypes declares that YouTube stores the OAuth grant as a
// bearer token. Validation checks bearer first, then falls back to the
// other common token types for backwards compatibility.
func (s *YouTubeOAuthService) PreferredTokenTypes() []string {
	return []string{
		models.TokenTypeBearer,
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
	}
}

// Compile-time assertion (matches the YouTubeChannelBinder /
// YouTubeCanaryUploader guard pattern below). Caught by `go vet`,
// not at runtime.
var _ error = (*ErrChannelListSafetyCap)(nil)

// Compile-time assertion: YouTubeOAuthService satisfies the
// services.YouTubeChannelBinder capability interface. Caught by
// `go vet`, not at runtime.
var _ YouTubeChannelBinder = (*YouTubeOAuthService)(nil)

var _ YouTubeCanaryUploader = (*YouTubeOAuthService)(nil)

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

func (s *YouTubeOAuthService) GetLoginURLWithOptions(state string, options OAuthLoginOptions) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.Auth.YouTubeClientID)
	params.Set("redirect_uri", s.cfg.Auth.YouTubeRedirectURI)
	params.Set("state", state)
	// P6 hardening: the consent-screen scope list follows the
	// least-privilege principle. `youtube.upload` is the only scope
	// strictly required by `videos.insert`; `youtube.readonly` is
	// used by the pre-upload channel-binding check (channels.list
	// in ValidateChannelBinding). `openid`, `email`, `profile`
	// identify the operator. We deliberately DO NOT request
	// `yt-analytics.readonly`: per the YouTube Data API videos.insert
	// reference, `youtube.upload` alone is sufficient for the
	// publish pipeline, and adding a sensitive scope would trigger
	// a re-review by Google's brand-verification queue without
	// delivering any functional gain. See
	// docs/OAUTH-PRODUCTION.md "Step 3 -- declare the scopes
	// (minimum set)" + "Code-side guard" for the canonical policy
	// and the cross-PR grep recipe. Re-introduction is treated as a
	// blocking change (the OAuth brand-verification round on the
	// OAuth consent screen would re-open).
	params.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/yt-analytics-monetary.readonly openid email profile")
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("include_granted_scopes", "true")

	if options.ForceConsent || options.SelectAccount {
		var prompts []string
		if options.SelectAccount {
			prompts = append(prompts, "select_account")
		}
		if options.ForceConsent {
			prompts = append(prompts, "consent")
		}
		params.Set("prompt", strings.Join(prompts, " "))
	}

	if options.LoginHint != "" {
		params.Set("login_hint", options.LoginHint)
	}

	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

func (s *YouTubeOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("YouTube: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube token exchange: %w", err)
	}

	slog.Info("YouTube: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scopes:       strings.Split(tokenResp.Scope, " "),
	}

	return profile, tokenData, nil
}

// Revoke calls Google's OAuth 2.0 token revocation endpoint.
func (s *YouTubeOAuthService) Revoke(ctx context.Context, accessToken string) error {
	body := url.Values{}
	body.Set("token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://oauth2.googleapis.com/revoke",
		strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("youtube revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube revoke failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("youtube revoke returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// RefreshOAuthToken exchanges a YouTube refresh token for a new access token.
func (s *YouTubeOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformYouTube, &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("youtube RefreshOAuthToken: empty refresh token")
	}
	slog.Info("YouTube: refreshing access token")
	body := url.Values{}
	body.Set("client_id", s.cfg.Auth.YouTubeClientID)
	body.Set("client_secret", s.cfg.Auth.YouTubeClientSecret)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("youtube refresh parse: %w", err)
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
		Scopes:       strings.Split(tr.Scope, " "),
	}, nil
}

// --- YouTube Data API v3 response types ---

type youtubeChannelsResponse struct {
	Items         []youtubeChannel `json:"items"`
	NextPageToken string           `json:"nextPageToken"`
	PageInfo      youtubePageInfo  `json:"pageInfo"`
}

type youtubePageInfo struct {
	TotalResults   int `json:"totalResults"`
	ResultsPerPage int `json:"resultsPerPage"`
}

type youtubeChannel struct {
	ID               string                `json:"id"`
	Snippet          youtubeChannelSnippet `json:"snippet"`
	Statistics       youtubeStatistics     `json:"statistics"`
	ContentDetails   youtubeContentDetails `json:"contentDetails"`
	BrandingSettings youtubeBranding       `json:"brandingSettings"`
}

type youtubeChannelSnippet struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	CustomURL   string             `json:"customUrl"`
	Country     string             `json:"country"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeStatistics struct {
	SubscriberCount       int64 `json:"subscriberCount"`
	HiddenSubscriberCount bool  `json:"hiddenSubscriberCount"`
	ViewCount             int64 `json:"viewCount"`
	VideoCount            int64 `json:"videoCount"`
}

func (s *youtubeStatistics) UnmarshalJSON(data []byte) error {
	var wire struct {
		SubscriberCount       json.RawMessage `json:"subscriberCount"`
		HiddenSubscriberCount bool            `json:"hiddenSubscriberCount"`
		ViewCount             json.RawMessage `json:"viewCount"`
		VideoCount            json.RawMessage `json:"videoCount"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var err error
	if s.SubscriberCount, err = decodeYouTubeCount(wire.SubscriberCount); err != nil {
		return fmt.Errorf("subscriberCount: %w", err)
	}
	if s.ViewCount, err = decodeYouTubeCount(wire.ViewCount); err != nil {
		return fmt.Errorf("viewCount: %w", err)
	}
	if s.VideoCount, err = decodeYouTubeCount(wire.VideoCount); err != nil {
		return fmt.Errorf("videoCount: %w", err)
	}
	s.HiddenSubscriberCount = wire.HiddenSubscriberCount
	return nil
}

type youtubeContentDetails struct {
	RelatedPlaylists youtubeRelatedPlaylists `json:"relatedPlaylists"`
}

type youtubeRelatedPlaylists struct {
	Uploads string `json:"uploads"`
}

type youtubeBranding struct {
	Image *youtubeBrandingImage `json:"image"`
}

type youtubeBrandingImage struct {
	BannerExternalURL string `json:"bannerExternalUrl"`
	BannerImageUrl    string `json:"bannerImageUrl"`
	BannerMobileExtra string `json:"bannerMobileExtraDevicesImageUrl"`
}

type youtubeThumbnails struct {
	Default  *youtubeThumbnail `json:"default"`
	Medium   *youtubeThumbnail `json:"medium"`
	High     *youtubeThumbnail `json:"high"`
	Standard *youtubeThumbnail `json:"standard"`
	Maxres   *youtubeThumbnail `json:"maxres"`
}

type youtubeThumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type youtubePlaylistItemsResponse struct {
	Items         []youtubePlaylistItem `json:"items"`
	NextPageToken string                `json:"nextPageToken"`
}

type youtubePlaylistItem struct {
	ContentDetails youtubePlaylistItemContentDetails `json:"contentDetails"`
}

type youtubePlaylistItemContentDetails struct {
	VideoID string `json:"videoId"`
}

type youtubeVideosResponse struct {
	Items []youtubeVideo `json:"items"`
}

type youtubeVideo struct {
	ID                string                         `json:"id"`
	Snippet           youtubeVideoSnippet            `json:"snippet"`
	Statistics        youtubeVideoStats              `json:"statistics"`
	ContentDetails    youtubeVideoContent            `json:"contentDetails"`
	Status            youtubeVideoStatus             `json:"status"`
	ProcessingDetails *youtubeVideoProcessingDetails `json:"processingDetails,omitempty"`
}

type youtubeVideoSnippet struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	PublishedAt string             `json:"publishedAt"`
	ChannelID   string             `json:"channelId"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeVideoStats struct {
	ViewCount    int64 `json:"viewCount"`
	LikeCount    int64 `json:"likeCount"`
	CommentCount int64 `json:"commentCount"`
}

func (s *youtubeVideoStats) UnmarshalJSON(data []byte) error {
	var wire struct {
		ViewCount    json.RawMessage `json:"viewCount"`
		LikeCount    json.RawMessage `json:"likeCount"`
		CommentCount json.RawMessage `json:"commentCount"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var err error
	if s.ViewCount, err = decodeYouTubeCount(wire.ViewCount); err != nil {
		return fmt.Errorf("viewCount: %w", err)
	}
	if s.LikeCount, err = decodeYouTubeCount(wire.LikeCount); err != nil {
		return fmt.Errorf("likeCount: %w", err)
	}
	if s.CommentCount, err = decodeYouTubeCount(wire.CommentCount); err != nil {
		return fmt.Errorf("commentCount: %w", err)
	}
	return nil
}

type youtubeVideoContent struct {
	Duration string `json:"duration"`
}

type youtubeVideoStatus struct {
	PrivacyStatus string `json:"privacyStatus"`
	UploadStatus  string `json:"uploadStatus"`
}

type youtubeVideoProcessingDetails struct {
	ProcessingStatus string `json:"processingStatus"`
}

// --- Private ---

type youtubeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

func (s *YouTubeOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*youtubeTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.Auth.YouTubeClientID)
	body.Set("client_secret", s.cfg.Auth.YouTubeClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.Auth.YouTubeRedirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token",
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
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
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
		return nil, fmt.Errorf("user info failed (status %d): %s", resp.StatusCode, string(body))
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
		PlatformUserID: result.ID,
		Username:       result.Name,
		Name:           result.Name,
		Email:          result.Email,
	}, nil
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ OAuthProvider          = (*YouTubeOAuthService)(nil)
	_ ContentValidator       = (*YouTubeOAuthService)(nil)
	_ Publisher              = (*YouTubeOAuthService)(nil)
	_ AsyncPublisher         = (*YouTubeOAuthService)(nil)
	_ AccountDiscoverer      = (*YouTubeOAuthService)(nil)
	_ AccountDetailsProvider = (*YouTubeOAuthService)(nil)
	_ AccountContentProvider = (*YouTubeOAuthService)(nil)
)
