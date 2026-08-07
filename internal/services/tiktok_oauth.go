package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TikTokOAuthService implements the TikTok provider. Taglio 2.1:
//
// Capabilities exposed (Taglio 4.2):
//   - OAuthProvider (login flow)
//   - ContentValidator (video required; caption ≤ 4000 runes)
//   - Publisher (Publisher.Publish = thin wrapper that calls StartPublish
//     and returns immediately with the publish_id, for backward compat
//     with the existing Publisher contract used by the worker's tick)
//   - AsyncPublisher (the 4-step state machine: StartPublish /
//     CheckPublishStatus / ContinuePublish / Reconcile) — this is the
//     new surface that the reconciler goroutine drives instead of
//     calling a synchronous polling loop inside the request path.
//   - AccountManager (Validate / Revoke — non-interface helpers)
type TikTokOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time

	// chunkSize overrides tiktokChunkSize for unit tests so they can
	// verify Content-Range arithmetic against a few-hundred-byte
	// video instead of materialising 10MB+ payloads. Zero means
	// fall back to the package-level default (10MB). Production
	// initialisation leaves this zero — see NewTikTokOAuthService.
	chunkSize int
}

// NewTikTokOAuthService creates a new TikTokOAuthService. Accepts optional
// ProviderDependencies for HTTP client injection (tests inject httptest
// server clients through deps).
func NewTikTokOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*TikTokOAuthService, error) {
	if cfg.Auth.TikTokClientID == "" {
		return nil, nil // provider disabled
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	return &TikTokOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
	}, nil
}

// now returns the current time via the injected clock, or time.Now as default.
func (s *TikTokOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// Name returns the platform identifier.
func (s *TikTokOAuthService) Name() string { return models.PlatformTikTok }

// maskClientKey restituisce una versione mascherata della client key per i log.
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

// truncateForLog restituisce una versione troncata di una stringa per i log.
func truncateForLog(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 200
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ValidateContent enforces TikTok's hard requirements: a video,
// a privacy_level (mandatory — no default), and caption ≤ 4000 runes.
// Taglio 4b: privacy_level is now required — empty/unrecognized values
// return a validation_error instead of silently defaulting to PUBLIC_TO_EVERYONE.
func (s *TikTokOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("tiktok requires a video for publishing")
	}
	if payload.PrivacyLevel == "" {
		return fmt.Errorf("tiktok requires a privacy_level: one of PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, SELF_ONLY")
	}
	if err := validateTikTokPrivacyLevel(payload.PrivacyLevel); err != nil {
		return err
	}
	if n := len([]rune(payload.Text)); n > tikTokTitleMaxRunes {
		return fmt.Errorf("tiktok caption exceeds %d-rune limit (got %d)", tikTokTitleMaxRunes, n)
	}
	return nil
}

// Publish (Taglio 4.2) is a thin wrapper that calls StartPublish and
// returns the publish_id. Kept on the Publisher interface for backward
// compat with the worker's existing tick() call site — the worker's
// publishTarget() calls publisher.Publish(ctx, token, account.PlatformUserID,
// payload) and expects a *models.PublishResult. The reconciler goroutine
// (new in Taglio 4.2) drives the async state machine via the AsyncPublisher
// capability (CheckPublishStatus / Reconcile) instead of this method.
func (s *TikTokOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformTikTok, s.now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}
	publishID, state, err := s.StartPublish(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return nil, err
	}
	slog.Info("TikTok: publish initialized (worker will store publish_id + state, reconciler will poll)", "publish_id", publishID, "state", state)
	return &models.PublishResult{PlatformMediaID: publishID}, nil
}

// StartPublish (Taglio 4.2 + PULL_FROM_FILE in Taglio 4.x chunked-upload
// addendum) is the first step of the async state machine. It dispatches
// between the two TikTok publish paths:
//
//   - PublishSourcePULLFromURL (default; empty Source): one HTTP call to
//     /v2/post/publish/video/init/ with `source_info.source="PULL_FROM_URL"`.
//     The platform fetches the video directly from the URL we hand in.
//     Returns immediately with publish_id + initial state. No polling — the
//     reconciler goroutine calls CheckPublishStatus on the next tick.
//   - PublishSourcePULLFromFile: chunked-upload flow. Calls init with
//     `source_info.source="PULL_FROM_FILE"` (returns publish_id + upload_url),
//     streams the video bytes (downloaded from VideoURL via HTTP GET) as
//     chunked PUT requests to upload_url with Content-Range, then POSTs to
//     /v2/post/publish/video/upload/complete/ to finalize. The four steps
//     run synchronously inside StartPublish; the reconciler still owns the
//     publishing→published transition via the existing CheckPublishStatus +
//     Reconcile path.
//
// Both paths return the same publish_id + state contract so the worker +
// reconciler don't need to know which path was used.
func (s *TikTokOAuthService) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error) {
	if err := s.ValidateContent(payload); err != nil {
		return "", "", err
	}

	// Source discrimination. Anything other than an explicit
	// PublishSourcePULLFromFile value falls through to the legacy
	// PULL_FROM_URL path — backward-compatible with existing callers
	// that don't set the new field.
	if strings.EqualFold(strings.TrimSpace(payload.Source), models.PublishSourcePULLFromFile) {
		return s.startPublishPULLFromFile(ctx, accessToken, payload)
	}
	return s.startPublishPULLFromURL(ctx, accessToken, payload)
}

// startPublishPULLFromURL is the legacy path: one POST to init, hand
// TikTok the video_url, return. Kept as a private method so the public
// StartPublish can route between the two code paths cleanly.
func (s *TikTokOAuthService) startPublishPULLFromURL(ctx context.Context, accessToken string, payload models.PublishPayload) (publishID string, state string, err error) {
	slog.Info("TikTok: starting async publish (PULL_FROM_URL init)")

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
		return "", "", fmt.Errorf("tiktok init request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("tiktok init failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("tiktok init returned status %d: %s", resp.StatusCode, string(body))
	}

	var initResult struct {
		Data struct {
			PublishID string `json:"publish_id"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &initResult); err != nil {
		return "", "", fmt.Errorf("tiktok init parse: %w", err)
	}

	publishID = initResult.Data.PublishID
	state = initResult.Data.Status
	slog.Info("TikTok: async publish initialized", "publish_id", publishID, "state", state)
	return publishID, state, nil
}

// CheckPublishStatus (Taglio 4.2) does a SINGLE GET to the TikTok status
// endpoint. Returns the platform's current state string. Does NOT poll.
// The reconciler goroutine calls this on every tick to advance the
// post_target through the async state machine.
//
// Expected state values (from TikTok API docs):
//   - PROCESSING_UPLOAD — TikTok is fetching the video from the URL
//   - PENDING_PUBLISH   — video received, waiting for processing
//   - IN_REVIEW         — TikTok is reviewing the video
//   - PUBLISH_COMPLETE  — video is live
//   - FAILED            — publish failed
func (s *TikTokOAuthService) CheckPublishStatus(ctx context.Context, accessToken, publishID string) (state string, err error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://open.tiktokapis.com/v2/post/publish/status/fetch/", nil)
	if err != nil {
		return "", fmt.Errorf("tiktok status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	q := req.URL.Query()
	q.Set("publish_id", publishID)
	req.URL.RawQuery = q.Encode()

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("tiktok status fetch failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", NewProviderError(models.PlatformTikTok, resp.StatusCode, string(body), resp.Header, nil)
	}

	var statusResult struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &statusResult); err != nil {
		return "", fmt.Errorf("tiktok status parse: %w", err)
	}
	return statusResult.Data.Status, nil
}

// ContinuePublish (Taglio 4.2 + PULL_FROM_FILE addendum) is a no-op
// for both PULL_FROM_URL and PULL_FROM_FILE flows today. The
// PULL_FROM_FILE chain (init → chunked PUT → complete) runs
// synchronously inside StartPublish with the reconciler owning the
// publishing→published transition via CheckPublishStatus + Reconcile —
// no per-tick upload continuation needed. Kept here because the
// AsyncPublisher interface contract requires the slot, and a future
// async platform (e.g. one that requires per-tick upload progress)
// can implement ContinuePublish as a non-no-op without breaking the
// TikTok path.
func (s *TikTokOAuthService) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	// PULL_FROM_URL: TikTok already has the video from StartPublish.
	// PULL_FROM_FILE: StartPublish streamed all chunks + completed
	// the session synchronously. No continuation needed for either.
	return nil
}

// Reconcile (Taglio 4.2) is the terminal-state detector the reconciler
// goroutine calls. It combines CheckPublishStatus with transition logic:
//
//	PUBLISH_COMPLETE → returns *PublishResult (success, terminal)
//	FAILED          → returns error (terminal)
//	in-flight       → returns (nil, nil) — caller should retry next tick
//
// The reconciler in the worker uses this contract: nil result + nil err
// means "leave the target alone, check again next tick". A non-nil result
// means "transition to published". A non-nil err means "transition to failed".
func (s *TikTokOAuthService) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	state, err := s.CheckPublishStatus(ctx, accessToken, publishID)
	if err != nil {
		return nil, err
	}
	switch state {
	case "PUBLISH_COMPLETE":
		return &models.PublishResult{PlatformMediaID: publishID}, nil
	case "FAILED":
		return nil, NewTerminalPublishError(state, fmt.Errorf("tiktok publish failed: publish_id=%s state=%s", publishID, state))
	default:
		// PROCESSING_UPLOAD, PENDING_PUBLISH, IN_REVIEW — still in flight.
		// Caller (reconciler goroutine) leaves the target as-is and
		// checks again on the next tick.
		return nil, nil
	}
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// TikTok implements both Publisher (sync legacy path / direct publish)
// AND AsyncPublisher (Taglio 4.2 four-step state machine). The router
// uses AsyncPublisher when present, falling back to Publisher only on
// platforms that don't satisfy the async state machine.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ OAuthProvider    = (*TikTokOAuthService)(nil)
	_ ContentValidator = (*TikTokOAuthService)(nil)
	_ Publisher        = (*TikTokOAuthService)(nil)
	_ AsyncPublisher   = (*TikTokOAuthService)(nil)
)
