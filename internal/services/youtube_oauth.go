package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
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
}

// youTubeUploadOptions captures the P1#6 chunking knobs. Loaded
// from cfg in NewYouTubeOAuthService; also re-readable as
// YouTubeUploadOptions for documentation + future public exposure
// (a future Build(deps, opts...) constructor could pass it in
// directly; today the constructor pulls every field from cfg).
type youTubeUploadOptions struct {
	ChunkSize   int64         // bytes per chunk; must be multiple of 262144 (validated by cfg.validate)
	MaxRetries  int           // per-chunk PUT retry budget (distinct from upload-job-level retries)
	BackoffBase time.Duration // exp-backoff base for the calculated fallback
	BackoffCap  time.Duration // exp-backoff cap for the calculated fallback; Retry-After bypasses this
}

// youTubeUploadDeps lets tests swap the production backoff / sleep
// implementations. Production wiring: NewYouTubeOAuthService
// installs the defaults returned by loadYouTubeUploadDeps(opts).
// Tests (in this package) reach into the unexported fields
// directly and override uploadDeps.backoff / uploadDeps.sleep.
type youTubeUploadDeps struct {
	backoff func(attempt int) time.Duration
	sleep   func(ctx context.Context, d time.Duration) error
}

// loadYouTubeUploadOptions reads the four P1#6 knobs from cfg with
// safe defaults if any field happens to be zero (defensive — the
// boot-time validate() rejects bad shapes, but a test that builds
// cfg manually might skip Validate()).
func loadYouTubeUploadOptions(cfg *config.Config) youTubeUploadOptions {
	o := youTubeUploadOptions{
		ChunkSize:   cfg.YouTubeUploadChunkBytes,
		MaxRetries:  cfg.YouTubeUploadMaxRetries,
		BackoffBase: time.Duration(cfg.YouTubeUploadBackoffBaseMs) * time.Millisecond,
		BackoffCap:  time.Duration(cfg.YouTubeUploadBackoffCapMs) * time.Millisecond,
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = 16 * 1024 * 1024
	}
	if o.MaxRetries <= 0 {
		o.MaxRetries = 5
	}
	if o.BackoffBase <= 0 {
		o.BackoffBase = time.Second
	}
	if o.BackoffCap < o.BackoffBase {
		o.BackoffCap = 5 * time.Minute
	}
	return o
}

// loadYouTubeUploadDeps returns the production defaults used by
// NewYouTubeOAuthService. Each field is an independent function so
// tests can swap one without recomputing the other.
func loadYouTubeUploadDeps(o youTubeUploadOptions) *youTubeUploadDeps {
	return &youTubeUploadDeps{
		backoff: computeYouTubeBackoff(o.BackoffBase, o.BackoffCap),
		sleep:   defaultYouTubeSleep,
	}
}

// computeYouTubeBackoff implements AWS-style decorrelated jitter
// for chunk-level retries: temp = min(cap, base * 3^attempt), sleep =
// base + rand(0..temp-base). Capped at the configured cap. Production
// polish: a future commit can switch this to math/rand/v2 with a
// per-pool source for better concurrency characteristics; today the
// global math/rand source is sufficient for the chunk-loop's
// concurrency (a single worker process is the only caller).
//
// Tests inject a deterministic replacement via the uploadDeps.backoff
// field on the service struct.
func computeYouTubeBackoff(base, cap time.Duration) func(int) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	if cap < base {
		cap = base
	}
	return func(attempt int) time.Duration {
		if attempt < 1 {
			attempt = 1
		}
		prev := base
		for i := 1; i < attempt; i++ {
			prev *= 3
			if prev > cap {
				prev = cap
				break
			}
		}
		if prev < base {
			prev = base
		}
		// Full jitter: rand in [base, prev]. rand.Int63n(n) returns
		// [0, n) so the upper bound is exclusive; widen by 1 to keep
		// prev as a possible outcome when prev > base.
		span := int64(prev) - int64(base)
		if span < 1 {
			return base
		}
		return base + time.Duration(rand.Int63n(span))
	}
}

// defaultYouTubeSleep is the interruptible sleep used between
// chunked-PUT retries. time.NewTimer + select on ctx.Done() is the
// canonical shutdown-safe shape; time.Sleep() would block past
// graceful-shutdown cancellation and break the worker's
// drain-then-stop contract.
func defaultYouTubeSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// parseRetryAfterHeader parses the canonical Retry-After header
// (RFC 7231 §7.1.3 — delta-seconds OR HTTP-date), returning
// time.Duration(0) on any parse error or empty input. Already-
// elapsed delta-seconds clamp to 0 so the worker doesn't wait a
// negative amount of time. Per RFC 7231, an HTTP-date (deprecated
// but seen in the wild) is converted to "until that instant".
func parseRetryAfterHeader(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService. Accepts optional
// ProviderDependencies for HTTP client injection.
func NewYouTubeOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*YouTubeOAuthService, error) {
	if cfg.YouTubeClientID == "" {
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

// ValidateChannelBinding implements services.YouTubeChannelBinder.
// It calls channels.list?part=id&mine=true with a fresh access token
// (the worker has already refreshed via the vault before calling)
// and verifies the returned channel id set includes expectedChannelID.
//
// Behaviour matrix:
//   - 200 OK with single matching channel → nil
//   - 200 OK with multi-channel set INCLUDING expected → nil
//     (a single Grant can manage up to 100 channels; the upload is
//     bound to the one the operator selected)
//   - 200 OK with 0 channels or NO match →
//        fmt.Errorf("...%w...: expected %q, channels=[...]",
//            ErrYouTubeChannelMismatch, expectedChannelID, ...)
//   - 200 OK with 0 channels (grant lost all bindings) → same sentinel
//   - Non-200 / network / decode error → plain wrapped error,
//     DO NOT use the sentinel so the worker treats it as transient.
//
// The method is a single GET; it does NOT re-refresh the access token
// to avoid double-quota usage (the publish worker already refreshed
// in step 5 of publishTarget). The token MUST therefore be a fresh
// bearer token; OAuth-only access tokens (no refresh) are not
// supported on this path — they're an immediate 401 and the worker
// should treat them as reauth-required via the existing token-refresh
// error path.
func (s *YouTubeOAuthService) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	if expectedChannelID == "" {
		return fmt.Errorf("youtube channel binding check: empty expected channel id")
	}

	params := url.Values{}
	params.Set("part", "id")
	params.Set("mine", "true")
	params.Set("maxResults", "50")

	reqURL := "https://www.googleapis.com/youtube/v3/channels?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("youtube channel binding: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube channel binding: channels.list request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return fmt.Errorf("youtube channel binding: channels.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("youtube channel binding: decode channels.list: %w", err)
	}

	if len(result.Items) == 0 {
		// Grant has zero channels: structurally invalid. Flag as
		// mismatch (operators should see reauth_required immediately
		// — there is no acceptable network-retry semantics here).
		return fmt.Errorf("%w: expected %q, grant has 0 channels", ErrYouTubeChannelMismatch, expectedChannelID)
	}

	for _, ch := range result.Items {
		if ch.ID == expectedChannelID {
			return nil // bound to expected channel — proceed with upload
		}
	}

	ids := make([]string, 0, len(result.Items))
	for _, ch := range result.Items {
		ids = append(ids, ch.ID)
	}
	return fmt.Errorf("%w: expected %q, grant-bound channels=%v", ErrYouTubeChannelMismatch, expectedChannelID, ids)
}

// Compile-time assertion: YouTubeOAuthService satisfies the
// services.YouTubeChannelBinder capability interface. Caught by
// `go vet`, not at runtime.
var _ YouTubeChannelBinder = (*YouTubeOAuthService)(nil)

// ErrYouTubeAmbiguousAuthorization is the canonical sentinel returned
// by BindGrantToChannel when channels.list(mine=true) reports >1
// channels for the authenticated Google account AND no
// expected_channel_id was supplied at login time. Co-exists with the
// same-text declaration in pkg/api/handlers.go (the HTTP layer keeps
// a local copy for its 409 Conflict mapping); both layers own their
// own discovery flow.
//
// Cross-references:
//   - pkg/api/routes_test.go::TestHandleCallback_YouTube_MultipleChannels_NoExpected_Conflict
//   - pkg/api/handlers.go::attachDiscoveredAccounts (YouTube branch
//     + 409 mapping)
var ErrYouTubeAmbiguousAuthorization = errors.New("youtube authorization is ambiguous: re-authorize with expected_channel_id")

// BindGrantToChannel consolidates the 1-OAuth-grant-per-1-channel
// policy at the provider level. It is the YouTube analogue of
// "validate before you store": the OAuth callback handler (and any
// future per-channel re-link flow) calls this to ensure the bearer
// token is saved EXACTLY ONCE — for the channel the operator
// verified — and is never cloned across the whole
// channels.list(mine=true) result set.
//
// Behaviour matrix:
//   - expectedChannelID == "" AND len(discovered) == 1 → returns
//     the single *DiscoveredAccount, nil error (canonical happy
//     path for one-Google-account-one-channel operators).
//   - expectedChannelID == "" AND len(discovered) != 1 → returns
//     nil, ErrYouTubeAmbiguousAuthorization wrapped with the
//     observed channel count. Cloning the token across N channels
//     is wrong: YouTube's OAuth grant is bound to whichever Brand
//     Account the operator selected at consent, and silently
//     fanning the token out is exactly the misroute Google warns
//     about for third-party apps that ignore Brand Account
//     selection.
//   - expectedChannelID set AND present in the discovery set →
//     returns the matching *DiscoveredAccount, nil error.
//   - expectedChannelID set AND NOT present → returns nil, an
//     error wrapping ErrYouTubeChannelMismatch (the operator
//     authenticated the wrong Google account, mistyped the id, or
//     imported a Brand Account ID that has since been moved /
//     removed).
//   - transient (5xx / network / decode error, or 0-channels
//     reported by DiscoverAccounts) → returns nil and the error
//     un-sentineled so the caller retries rather than
//     misclassifying a transient as a reauth-required state.
//
// This method does NOT save or clone the token. It is the SINGLE
// source of truth for the YouTube 1:1 policy: any consumer tempted
// to "for each channel save the token" should defer to this method,
// which guarantees at most one *DiscoveredAccount is returned.
func (s *YouTubeOAuthService) BindGrantToChannel(ctx context.Context, accessToken, expectedChannelID string) (*DiscoveredAccount, error) {
	accounts, err := s.DiscoverAccounts(ctx, accessToken, "")
	if err != nil {
		// Preserve the existing 0-channel / network behaviour:
		// DiscoverAccounts already produces a typed error ("the
		// authenticated Google account has no YouTube channel")
		// that callers rely on. Re-wrap so the bind call site is
		// unambiguous in logs but keep the sentinel-free shape so
		// transient errors aren't misclassified as reauth.
		return nil, fmt.Errorf("youtube bind: discover channels: %w", err)
	}

	if expectedChannelID != "" {
		for _, acc := range accounts {
			if acc.Profile.PlatformUserID == expectedChannelID {
				return acc, nil
			}
		}
		return nil, fmt.Errorf("%w: %q is not in channels.list(mine=true) result",
			ErrYouTubeChannelMismatch, expectedChannelID)
	}

	if len(accounts) != 1 {
		return nil, fmt.Errorf("%w: got %d channels, expected 1",
			ErrYouTubeAmbiguousAuthorization, len(accounts))
	}
	return accounts[0], nil
}

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

func (s *YouTubeOAuthService) GetLoginURLWithOptions(state string, options OAuthLoginOptions) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.YouTubeClientID)
	params.Set("redirect_uri", s.cfg.YouTubeRedirectURI)
	params.Set("state", state)
	params.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/yt-analytics.readonly openid email profile")
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

// ValidateContent enforces the YouTube video-required rule
// and a mandatory privacy_level.
// Taglio 4b: privacy_level is now required — one of public, unlisted, private.
func (s *YouTubeOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("youtube requires a video for publishing")
	}
	if payload.PrivacyLevel == "" {
		return fmt.Errorf("youtube requires a privacy_level: one of public, unlisted, private")
	}
	if err := validateYouTubePrivacyLevel(payload.PrivacyLevel); err != nil {
		return err
	}
	return nil
}

// Validate calls the Google userinfo endpoint to verify the access token.
func (s *YouTubeOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return fmt.Errorf("youtube validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube validate failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("youtube validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
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
	body.Set("client_id", s.cfg.YouTubeClientID)
	body.Set("client_secret", s.cfg.YouTubeClientSecret)
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

// P1#6 — chunk size is now configurable via cfg.YouTubeUploadChunkBytes
// (env YOUTUBE_UPLOAD_CHUNK_BYTES, default 16 MB / 16777216, must be a
// multiple of 262144 = 256 KB per Google's resumable upload protocol).

// Publish uploads a video to YouTube using the resumable upload protocol.
// For YouTube this is the async entrypoint: the upload completes synchronously
// and returns a composite publishID (channelID:videoID). The reconciler will
// then poll videos.list processingDetails until the video is fully processed.
func (s *YouTubeOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformYouTube, s.now(), &err)
	publishID, _, err := s.StartPublish(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return nil, err
	}
	_, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		return nil, err
	}
	slog.Info("YouTube: async publish initiated, reconciler will poll processing status",
		"publish_id", publishID, "video_id", videoID)
	return &models.PublishResult{
		PlatformMediaID: publishID,
		PlatformURL:     "https://www.youtube.com/watch?v=" + videoID,
	}, nil
}

// StartPublish performs the resumable upload and returns a composite
// publishID (channelID:videoID) plus the initial "processing" state.
func (s *YouTubeOAuthService) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error) {
	if err := s.ValidateContent(payload); err != nil {
		return "", "", err
	}

	slog.Info("YouTube: starting resumable video upload", "source", payload.VideoURL)

	fileSize, contentType, err := s.headVideo(ctx, payload.VideoURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to inspect source video: %w", err)
	}
	if contentType == "" {
		contentType = "video/mp4"
	}
	slog.Info("YouTube: source video info", "size", fileSize, "content_type", contentType)

	metadata := s.buildUploadMetadata(payload)

	uploadURL, err := s.initiateResumableSession(ctx, accessToken, metadata, fileSize, contentType)
	if err != nil {
		return "", "", fmt.Errorf("failed to initiate resumable session: %w", err)
	}
	slog.Debug("YouTube: resumable session initiated", "upload_url", uploadURL)

	videoID, err := s.uploadVideoChunks(ctx, uploadURL, payload.VideoURL, fileSize)
	if err != nil {
		return "", "", fmt.Errorf("failed to stream video: %w", err)
	}

	slog.Info("YouTube: video uploaded successfully", "video_id", videoID)

	return encodeYouTubePublishID(platformUserID, videoID), "processing", nil
}

// CheckPublishStatus returns the processing status of a YouTube video by
// calling videos.list with part=processingDetails.
func (s *YouTubeOAuthService) CheckPublishStatus(ctx context.Context, accessToken, publishID string) (state string, err error) {
	_, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		return "", err
	}

	video, err := s.fetchVideoStatus(ctx, accessToken, videoID)
	if err != nil {
		return "", err
	}

	if video.ProcessingDetails == nil {
		// No processing details yet; assume still processing.
		return "processing", nil
	}
	return video.ProcessingDetails.ProcessingStatus, nil
}

// ContinuePublish is a no-op for YouTube. The full resumable upload is
// performed inside StartPublish.
func (s *YouTubeOAuthService) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	return nil
}

// Reconcile polls the YouTube video status and drives the async state machine.
// It verifies the video belongs to the expected channel (snippet.channelId)
// and maps processingDetails.processingStatus to terminal or in-flight.
//
//   processing  → (nil, nil)   // still in flight
//   succeeded   → (*PublishResult, nil)
//   failed      → (nil, error)  // terminal failure
//   terminated  → (nil, error)  // terminal failure
func (s *YouTubeOAuthService) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	platformUserID, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		return nil, err
	}

	video, err := s.fetchVideoStatus(ctx, accessToken, videoID)
	if err != nil {
		return nil, err
	}

	// The upload was performed with the account's token, but verify the
	// video landed on the expected channel. A missing channelId is treated
	// as a failure because we cannot confirm ownership.
	if video.Snippet.ChannelID != platformUserID {
		return nil, fmt.Errorf("youtube channel mismatch: expected %s, got %s", platformUserID, video.Snippet.ChannelID)
	}

	processingStatus := ""
	if video.ProcessingDetails != nil {
		processingStatus = video.ProcessingDetails.ProcessingStatus
	}

	switch processingStatus {
	case "", "processing":
		// Still processing or no processing details yet.
		return nil, nil
	case "succeeded":
		return &models.PublishResult{
			PlatformMediaID: videoID,
			PlatformURL:     "https://www.youtube.com/watch?v=" + videoID,
		}, nil
	case "failed":
		return nil, fmt.Errorf("youtube processing failed for video %s", videoID)
	case "terminated":
		return nil, fmt.Errorf("youtube processing terminated for video %s", videoID)
	default:
		// Unknown status; treat as in-flight to avoid premature failure.
		slog.Warn("YouTube: unknown processing status, treating as in-flight",
			"video_id", videoID, "status", processingStatus)
		return nil, nil
	}
}

// fetchVideoStatus calls videos.list with part=snippet,status,processingDetails
// for a single video ID and returns the first (and only) item.
func (s *YouTubeOAuthService) fetchVideoStatus(ctx context.Context, accessToken, videoID string) (*youtubeVideo, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/videos" +
		"?part=snippet,status,processingDetails" +
		"&id=" + url.QueryEscape(videoID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create youtube video status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube video status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("youtube video status returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode youtube video status: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube video %s not found", videoID)
	}

	return &result.Items[0], nil
}

// encodeYouTubePublishID encodes the channel ID and video ID into a single
// opaque publish ID used during the async publishing lifecycle.
//
// The composite is stored temporarily in post_target.platform_post_id while
// the target is in 'publishing' status. On a successful Reconcile, the final
// stored value is overwritten with the plain video ID.
func encodeYouTubePublishID(channelID, videoID string) string {
	return channelID + ":" + videoID
}

// decodeYouTubePublishID splits an encoded publish ID back into channel ID
// and video ID.
func decodeYouTubePublishID(publishID string) (channelID, videoID string, err error) {
	parts := strings.SplitN(publishID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid youtube publish id: %s", publishID)
	}
	return parts[0], parts[1], nil
}

// --- Upload helpers ---

func (s *YouTubeOAuthService) headVideo(ctx context.Context, videoURL string) (size int64, contentType string, err error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", videoURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("HEAD request creation failed: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return s.headViaRange(ctx, videoURL)
	}

	return resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

func (s *YouTubeOAuthService) headViaRange(ctx context.Context, videoURL string) (int64, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", videoURL, nil)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return 0, "", fmt.Errorf("unable to determine video size (status %d)", resp.StatusCode)
	}

	contentRange := resp.Header.Get("Content-Range")
	parts := strings.Split(contentRange, "/")
	if len(parts) != 2 {
		return 0, resp.Header.Get("Content-Type"), fmt.Errorf("unexpected Content-Range: %s", contentRange)
	}

	var total int64
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &total); err != nil {
		return 0, "", fmt.Errorf("failed to parse total size: %w", err)
	}

	return total, resp.Header.Get("Content-Type"), nil
}

func (s *YouTubeOAuthService) initiateResumableSession(ctx context.Context, accessToken string, metadata map[string]interface{}, fileSize int64, contentType string) (string, error) {
	jsonMeta, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	reqURL := "https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status"
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(jsonMeta)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", fileSize))
	req.Header.Set("X-Upload-Content-Type", contentType)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("init request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("init session failed (status %d): %s", resp.StatusCode, string(body))
	}

	uploadURL := resp.Header.Get("Location")
	if uploadURL == "" {
		return "", fmt.Errorf("no Location header in init response")
	}

	return uploadURL, nil
}

// uploadVideoChunks streams the entire source video to YouTube in
// ChunkSize-sized chunks, applying Retry-After-aware exponential
// backoff on transient 5xx/429 PUT failures. P1#6 — replaces the
// pre-P1 hardcoded 256 KB chunks and the bare 3-retry no-backoff loop.
// Per-chunk retry budget is s.uploadOpts.MaxRetries; on exhaustion
// the error bubbles up so the outer upload-job worker can MarkRetry
// or MarkDeadLetter based on the upload_jobs.attempt_count budget.
func (s *YouTubeOAuthService) uploadVideoChunks(ctx context.Context, uploadURL, sourceURL string, fileSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download source video: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return "", fmt.Errorf("source video returned status %d", resp.StatusCode)
	}

	if fileSize <= 0 {
		fileSize = resp.ContentLength
	}
	if fileSize <= 0 {
		resp.Body.Close()
		return "", fmt.Errorf("unable to determine video size (got %d)", fileSize)
	}

	var uploaded int64
	var retries int
	buf := make([]byte, s.uploadOpts.ChunkSize)

	for {
		select {
		case <-ctx.Done():
			resp.Body.Close()
			return "", fmt.Errorf("upload cancelled: %w", ctx.Err())
		default:
		}

		n, readErr := io.ReadFull(resp.Body, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			resp.Body.Close()
			return "", fmt.Errorf("failed to read video chunk: %w", readErr)
		}

		if n == 0 {
			break
		}

		contentRange := fmt.Sprintf("bytes %d-%d/%d", uploaded, uploaded+int64(n)-1, fileSize)

		videoID, retryAfter, retryable, uploadErr := s.putChunk(ctx, uploadURL, buf[:n], contentRange, int64(n))
		if uploadErr != nil {
			if !retryable {
				// 4xx-not-429: permanent client error, fail fast
				// so the outer worker can MarkDeadLetter on attempt 1.
				resp.Body.Close()
				return "", uploadErr
			}
			if retries >= s.uploadOpts.MaxRetries {
				resp.Body.Close()
				return "", fmt.Errorf("upload failed at byte %d after %d retries: %w", uploaded, retries, uploadErr)
			}
			retries++

			// Retry-After ALWAYS wins. Capping a server hint would
			// guarantee we hammer the API mid-quota-window and risk
			// a temporary blacklisting — the cap only applies to
			// the CALCULATED fallback when the server didn't send one.
			var sleepFor time.Duration
			if retryAfter > 0 {
				sleepFor = retryAfter
			} else {
				sleepFor = s.uploadDeps.backoff(retries)
			}

			slog.Warn("YouTube: chunk upload failed, sleeping then retrying",
				"byte", uploaded, "retry", retries, "max_retries", s.uploadOpts.MaxRetries,
				"sleep_for", sleepFor, "error", uploadErr,
			)

			if err := s.uploadDeps.sleep(ctx, sleepFor); err != nil {
				resp.Body.Close()
				return "", fmt.Errorf("upload cancelled during backoff at byte %d: %w", uploaded, err)
			}

			// Recover the byte offset the server actually has via
			// the 308-Range response (with its own small retry budget).
			resumedAt, qErr := s.queryUploadStatusWithRetry(ctx, uploadURL, fileSize, 2)
			if qErr != nil {
				resp.Body.Close()
				return "", fmt.Errorf("upload failed at byte %d (status query failed): %w", uploaded, qErr)
			}
			slog.Info("YouTube: resuming upload from byte", "resumed_at", resumedAt)

			resp.Body.Close()
			req2, _ := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
			req2.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumedAt))
			resp2, err2 := s.httpClient.Do(req2)
			if err2 != nil {
				return "", fmt.Errorf("failed to re-download from byte %d: %w", resumedAt, err2)
			}
			resp = resp2
			uploaded = resumedAt
			continue
		}

		if videoID != "" {
			resp.Body.Close()
			return videoID, nil
		}

		uploaded += int64(n)

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	resp.Body.Close()
	return "", fmt.Errorf("upload completed but no video ID returned")
}

// putChunk performs a single resumable-upload PUT and returns:
//   - videoID string — the upload's permanent id when the response
//     is the terminal 200/201 with the { "id": ... } JSON body.
//   - retryAfter time.Duration — server-supplied Retry-After (parsed
//     from the response header via parseRetryAfterHeader). Zero when
//     the server didn't send one; the caller decides whether to use
//     it or fall back to computed exp backoff.
//   - retryable bool — true for transient failures (5xx, 429, network
//     error) so the uploadVideoChunks loop can sleep + retry; false
//     for terminal failures (200/201 with bad body, 308 [happy path],
//     or 4xx-not-429 [permanent client error]). 4xx-not-429 bubbling
//     up cleanly lets the worker's MarkDeadLetter path classify the
//     row on attempt 1 instead of wasting the entire retry budget
//     on a row YouTube will reject forever.
//   - err error — non-nil on any failure path; nil on 200/201
//     success or 308 "more bytes please".
func (s *YouTubeOAuthService) putChunk(ctx context.Context, uploadURL string, data []byte, contentRange string, expectedLen int64) (videoID string, retryAfter time.Duration, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(data))
	if err != nil {
		return "", 0, false, err
	}
	req.Header.Set("Content-Range", contentRange)
	req.ContentLength = expectedLen

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Network error (DNS, TCP reset, ctx-cancelled before
		// connect): treat as retryable so uploadVideoChunks can
		// resume the byte range from queryUploadStatus.
		return "", 0, true, fmt.Errorf("PUT chunk failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	retryAfter = parseRetryAfterHeader(resp.Header.Get("Retry-After"))

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		var result struct {
			ID string `json:"id"`
		}
		if jerr := json.Unmarshal(body, &result); jerr != nil {
			return "", 0, false, fmt.Errorf("failed to parse upload completion response: %w", jerr)
		}
		return result.ID, 0, false, nil

	case resp.StatusCode == 308:
		// Resume Incomplete — the canonical "more bytes please"
		// response. The Range header on the 308 tells us how far
		// we got, which the caller uses via queryUploadStatus for
		// the next Content-Range. 308 is not an error: it's a
		// normal continuation marker.
		return "", 0, false, nil

	case resp.StatusCode == http.StatusTooManyRequests:
		// 429 — always retryable. The server's Retry-After (if
		// any) is parsed above; when > 0 the caller honors it.
		return "", retryAfter, true, fmt.Errorf("rate limited (status 429, retry_after=%s)", retryAfter)

	case resp.StatusCode >= 500:
		// 5xx — retryable. Honor Retry-After when present, fall
		// back to the configured exp backoff otherwise.
		if retryAfter > 0 {
			return "", retryAfter, true, fmt.Errorf("server error (status %d, retry_after=%s)", resp.StatusCode, retryAfter)
		}
		return "", 0, true, fmt.Errorf("server error (status %d)", resp.StatusCode)

	default:
		// 4xx (excluding 429) — permanent client error. YouTube's
		// docs are clear: bad metadata, body validation errors, etc.
		// won't fix themselves on retry. Bubble up so the outer
		// upload-job worker can MarkDeadLetter on attempt 1 with
		// error_code = 'youtube_error'.
		return "", 0, false, fmt.Errorf("unexpected PUT response (status %d): %s", resp.StatusCode, string(body))
	}
}

// queryUploadStatus issues the canonical status check used on the
// recovery path: PUT with Content-Range: bytes */TOTAL. The 308
// response carries a Range header indicating the next byte offset.
// Non-308 here is unexpected (we expect 308 with a Range after a
// partial upload) — surfaced as a non-retryable error so the caller
// can decide whether to fail or wrap in a higher-level retry.
//
// Single PUT only — its caller
// (uploadVideoChunks::queryUploadStatusWithRetry) owns the small
// retry budget. Splitting the two keeps each function single-purpose.
func (s *YouTubeOAuthService) queryUploadStatus(ctx context.Context, uploadURL string, fileSize int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, http.NoBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
	req.ContentLength = 0

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 308 {
		return 0, fmt.Errorf("unexpected status query response: %d", resp.StatusCode)
	}

	rangeHeader := resp.Header.Get("Range")
	if rangeHeader == "" {
		return 0, nil
	}

	parts := strings.SplitN(rangeHeader, "=", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("malformed Range header: %s", rangeHeader)
	}
	rangeParts := strings.SplitN(parts[1], "-", 2)
	if len(rangeParts) != 2 {
		return 0, fmt.Errorf("malformed Range value: %s", parts[1])
	}

	var lastByte int64
	if _, err := fmt.Sscanf(rangeParts[1], "%d", &lastByte); err != nil {
		return 0, fmt.Errorf("failed to parse Range end byte: %w", err)
	}

	return lastByte + 1, nil
}

// queryUploadStatusWithRetry wraps queryUploadStatus with a small
// independent retry budget (default 2 attempts). P1#6 — the
// status-check PUT itself can hit a 5xx/429 transient; without
// this wrapper we'd abandon the entire upload and force the worker
// to re-claim from byte 0 on the next tick, which is wasteful when
// only the status-query failed. The retry budget is intentionally
// tiny (2) — it covers a single retry, not the full chunk budget,
// because the chunk budget already drove the failure into this
// path in the first place.
func (s *YouTubeOAuthService) queryUploadStatusWithRetry(ctx context.Context, uploadURL string, fileSize int64, maxAttempts int) (int64, error) {
	if maxAttempts <= 0 {
		maxAttempts = 2
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		offset, err := s.queryUploadStatus(ctx, uploadURL, fileSize)
		if err == nil {
			return offset, nil
		}
		lastErr = err
		if attempt < maxAttempts {
			sleepFor := s.uploadDeps.backoff(attempt)
			if sleepErr := s.uploadDeps.sleep(ctx, sleepFor); sleepErr != nil {
				return 0, sleepErr
			}
		}
	}
	return 0, lastErr
}

// buildUploadMetadata constructs the JSON metadata payload for a YouTube
// resumable upload. When PublishAt is set and in the future, the video is
// uploaded as private and YouTube is asked to make it public at that time.
func (s *YouTubeOAuthService) buildUploadMetadata(payload models.PublishPayload) map[string]interface{} {
	status := map[string]string{
		"privacyStatus": normalizeYouTubePrivacyLevel(payload.PrivacyLevel),
	}

	// YouTube only accepts publishAt when the video is private and has
	// never been published before. If a future publish time is provided,
	// force privacy to private and set publishAt.
	if payload.PublishAt != nil && payload.PublishAt.After(s.now()) {
		status["privacyStatus"] = "private"
		status["publishAt"] = payload.PublishAt.UTC().Format(time.RFC3339)
	}

	return map[string]interface{}{
		"snippet": map[string]string{
			"title":       defaultVideoTitle(payload),
			"description": payload.Text,
		},
		"status": status,
	}
}

func defaultVideoTitle(payload models.PublishPayload) string {
	if payload.Title != "" {
		return payload.Title
	}
	if payload.Text != "" {
		if len(payload.Text) > 100 {
			return payload.Text[:97] + "..."
		}
		return payload.Text
	}
	return "Uploaded via InstaEdit"
}

// validateYouTubePrivacyLevel returns an error if level is not one of the
// three YouTube-recognized privacy values. Used by ValidateContent.
// Taglio 4b: no default — empty/unrecognized causes validation_error.
func validateYouTubePrivacyLevel(level string) error {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "public", "unlisted", "private":
		return nil
	default:
		return fmt.Errorf("youtube privacy_level must be one of public, unlisted, private (got %q)", level)
	}
}

// normalizeYouTubePrivacyLevel canonicalizes the privacy value for the
// YouTube API (lowercase). ValidateContent already guarantees the value
// is valid.
func normalizeYouTubePrivacyLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

// DiscoverAccounts returns the YouTube channels owned by the authenticated
// Google account. Uses channels.list with mine=true to retrieve all channels
// linked to the OAuth grant. Each channel becomes a distinct PlatformAccount
// with the real YouTube channel ID (UC...) as PlatformUserID.
func (s *YouTubeOAuthService) DiscoverAccounts(ctx context.Context, accessToken, _ string) ([]*DiscoveredAccount, error) {
	const maxChannels = 500

	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails,status,brandingSettings")
	params.Set("mine", "true")
	params.Set("maxResults", "50")

	var allAccounts []*DiscoveredAccount
	var pageToken string

	for {
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		} else {
			params.Del("pageToken")
		}

		reqURL := "https://www.googleapis.com/youtube/v3/channels?" + params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create youtube channel request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("youtube channel discovery: %w", err)
		}

		var result youtubeChannelsResponse
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return nil, fmt.Errorf("youtube channel discovery returned %d: %s", resp.StatusCode, string(body))
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode youtube channels: %w", err)
		}
		resp.Body.Close()

		for _, ch := range result.Items {
			allAccounts = append(allAccounts, &DiscoveredAccount{
				Profile: models.PlatformProfile{
					PlatformUserID: ch.ID,
					Username:       ch.Snippet.Title,
				},
				Metadata: models.Metadata{
					"description":               ch.Snippet.Description,
					"handle":                    ch.Snippet.CustomURL,
					"avatar_url":                youtubeBestThumbnail(ch.Snippet.Thumbnails),
					"uploads_playlist_id":       ch.ContentDetails.RelatedPlaylists.Uploads,
					"country":                   ch.Snippet.Country,
					"subscriber_count":          ch.Statistics.SubscriberCount,
					"hidden_subscriber_count":   ch.Statistics.HiddenSubscriberCount,
					"video_count":               ch.Statistics.VideoCount,
					"view_count":                ch.Statistics.ViewCount,
				},
			})
		}

		if len(allAccounts) >= maxChannels {
			break
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	if len(allAccounts) == 0 {
		return nil, fmt.Errorf("the authenticated Google account has no YouTube channel")
	}

	return allAccounts, nil
}

// youtubeBestThumbnail selects the highest-resolution thumbnail from a
// YouTube thumbnail set, falling back to default → medium → high.
func youtubeBestThumbnail(thumbs *youtubeThumbnails) string {
	if thumbs == nil {
		return ""
	}
	if thumbs.Maxres != nil && thumbs.Maxres.URL != "" {
		return thumbs.Maxres.URL
	}
	if thumbs.Standard != nil && thumbs.Standard.URL != "" {
		return thumbs.Standard.URL
	}
	if thumbs.High != nil && thumbs.High.URL != "" {
		return thumbs.High.URL
	}
	if thumbs.Medium != nil && thumbs.Medium.URL != "" {
		return thumbs.Medium.URL
	}
	if thumbs.Default != nil && thumbs.Default.URL != "" {
		return thumbs.Default.URL
	}
	return ""
}

// GetAccountDetails fetches the current state of a YouTube channel via
// channels.list with id=<platformUserID>. Returns rich account details
// including statistics, branding, and upload playlist ID.
func (s *YouTubeOAuthService) GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/channels" +
		"?part=snippet,statistics,contentDetails,status,brandingSettings" +
		"&id=" + url.QueryEscape(platformUserID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create youtube channel details request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube channel details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("youtube channel details returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode youtube channel details: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube channel %s not found", platformUserID)
	}

	ch := result.Items[0]
	now := s.now()

	details := &models.AccountDetails{
		ResourceType: "channel",
		ExternalID:   ch.ID,
		DisplayName:  ch.Snippet.Title,
		Description:  ch.Snippet.Description,
		Handle:       ch.Snippet.CustomURL,
		AvatarURL:    youtubeBestThumbnail(ch.Snippet.Thumbnails),
		PublicURL:    "https://www.youtube.com/channel/" + ch.ID,
		FetchedAt:    now,
		Metrics: []models.AccountMetric{
			{
				Key:          "subscribers",
				Label:        "Subscribers",
				Value:        ch.Statistics.SubscriberCount,
				DisplayValue: formatCount(ch.Statistics.SubscriberCount),
			},
			{
				Key:          "views",
				Label:        "Views",
				Value:        ch.Statistics.ViewCount,
				DisplayValue: formatCount(ch.Statistics.ViewCount),
			},
			{
				Key:          "videos",
				Label:        "Videos",
				Value:        ch.Statistics.VideoCount,
				DisplayValue: formatCount(ch.Statistics.VideoCount),
			},
		},
	}

	// Banner URL from branding settings.
	if ch.BrandingSettings.Image != nil {
		details.BannerURL = ch.BrandingSettings.Image.BannerImageUrl
	}

	// Platform-specific properties.
	details.Properties = map[string]any{
		"country":                  ch.Snippet.Country,
		"uploads_playlist_id":      ch.ContentDetails.RelatedPlaylists.Uploads,
		"hidden_subscriber_count":  ch.Statistics.HiddenSubscriberCount,
	}

	return details, nil
}

// ListAccountContent returns recent videos from a YouTube channel by
// reading the channel's uploads playlist and then fetching video
// details. Pagination is supported via the cursor (nextPageToken).
func (s *YouTubeOAuthService) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int) (*models.AccountContentPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Step 1: Get the uploads playlist ID for this channel.
	uploadsPlaylist, err := s.getUploadsPlaylistID(ctx, accessToken, platformUserID)
	if err != nil {
		return nil, fmt.Errorf("get uploads playlist: %w", err)
	}

	// Step 2: List recent items from the uploads playlist.
	videoIDs, nextPageToken, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}

	if len(videoIDs) == 0 {
		return &models.AccountContentPage{Items: []models.AccountContentItem{}}, nil
	}

	// Step 3: Fetch video details (snippet, statistics, contentDetails, status).
	items, err := s.getVideoDetails(ctx, accessToken, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("get video details: %w", err)
	}

	return &models.AccountContentPage{
		Items:      items,
		NextCursor: nextPageToken,
	}, nil
}

func (s *YouTubeOAuthService) getUploadsPlaylistID(ctx context.Context, accessToken, channelID string) (string, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/channels" +
		"?part=contentDetails" +
		"&id=" + url.QueryEscape(channelID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", fmt.Errorf("channels.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Items) == 0 {
		return "", fmt.Errorf("channel %s not found", channelID)
	}

	return result.Items[0].ContentDetails.RelatedPlaylists.Uploads, nil
}

func (s *YouTubeOAuthService) listPlaylistItems(ctx context.Context, accessToken, playlistID, pageToken string, maxResults int) (videoIDs []string, nextPage string, err error) {
	params := url.Values{}
	params.Set("part", "snippet,contentDetails")
	params.Set("playlistId", playlistID)
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}

	reqURL := "https://www.googleapis.com/youtube/v3/playlistItems?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, "", fmt.Errorf("playlistItems.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubePlaylistItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", err
	}

	ids := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item.ContentDetails.VideoID != "" {
			ids = append(ids, item.ContentDetails.VideoID)
		}
	}

	return ids, result.NextPageToken, nil
}

func (s *YouTubeOAuthService) getVideoDetails(ctx context.Context, accessToken string, videoIDs []string) ([]models.AccountContentItem, error) {
	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails,status")
	params.Set("id", strings.Join(videoIDs, ","))

	reqURL := "https://www.googleapis.com/youtube/v3/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("videos.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	items := make([]models.AccountContentItem, 0, len(result.Items))
	for _, v := range result.Items {
		item := models.AccountContentItem{
			ExternalID:   v.ID,
			Title:        v.Snippet.Title,
			Description:  v.Snippet.Description,
			ThumbnailURL: youtubeBestThumbnail(v.Snippet.Thumbnails),
			PublicURL:    "https://www.youtube.com/watch?v=" + v.ID,
			Privacy:      v.Status.PrivacyStatus,
			Status:       v.Status.UploadStatus,
			Metrics: []models.AccountMetric{
				{
					Key:          "views",
					Label:        "Views",
					Value:        v.Statistics.ViewCount,
					DisplayValue: formatCount(v.Statistics.ViewCount),
				},
				{
					Key:          "likes",
					Label:        "Likes",
					Value:        v.Statistics.LikeCount,
					DisplayValue: formatCount(v.Statistics.LikeCount),
				},
				{
					Key:          "comments",
					Label:        "Comments",
					Value:        v.Statistics.CommentCount,
					DisplayValue: formatCount(v.Statistics.CommentCount),
				},
			},
		}

		if v.Snippet.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, v.Snippet.PublishedAt); err == nil {
				item.PublishedAt = &t
			}
		}

		item.Properties = map[string]any{
			"duration": v.ContentDetails.Duration,
		}

		items = append(items, item)
	}

	return items, nil
}

// formatCount returns a human-readable count string (e.g. "125K", "1.2M").
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
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
	ID              string                `json:"id"`
	Snippet         youtubeChannelSnippet `json:"snippet"`
	Statistics      youtubeStatistics     `json:"statistics"`
	ContentDetails  youtubeContentDetails `json:"contentDetails"`
	BrandingSettings youtubeBranding      `json:"brandingSettings"`
}

type youtubeChannelSnippet struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	CustomURL   string            `json:"customUrl"`
	Country     string            `json:"country"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeStatistics struct {
	SubscriberCount      int64 `json:"subscriberCount"`
	HiddenSubscriberCount bool  `json:"hiddenSubscriberCount"`
	ViewCount            int64 `json:"viewCount"`
	VideoCount           int64 `json:"videoCount"`
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
	BannerMobileExtra  string `json:"bannerMobileExtraDevicesImageUrl"`
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
	Items          []youtubePlaylistItem `json:"items"`
	NextPageToken  string                `json:"nextPageToken"`
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
	Title       string            `json:"title"`
	Description string            `json:"description"`
	PublishedAt string            `json:"publishedAt"`
	ChannelID   string            `json:"channelId"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeVideoStats struct {
	ViewCount    int64 `json:"viewCount"`
	LikeCount    int64 `json:"likeCount"`
	CommentCount int64 `json:"commentCount"`
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
	body.Set("client_id", s.cfg.YouTubeClientID)
	body.Set("client_secret", s.cfg.YouTubeClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.YouTubeRedirectURI)

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
	_ OAuthProvider         = (*YouTubeOAuthService)(nil)
	_ ContentValidator      = (*YouTubeOAuthService)(nil)
	_ Publisher             = (*YouTubeOAuthService)(nil)
	_ AsyncPublisher        = (*YouTubeOAuthService)(nil)
	_ AccountDiscoverer     = (*YouTubeOAuthService)(nil)
	_ AccountDetailsProvider = (*YouTubeOAuthService)(nil)
	_ AccountContentProvider = (*YouTubeOAuthService)(nil)
)
