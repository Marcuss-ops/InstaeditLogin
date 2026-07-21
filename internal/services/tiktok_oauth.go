package services

import (
	"bytes"
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
	if cfg.TikTokClientID == "" {
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

// tiktokChunkSize is the bytes-per-PUT chunk. 10MB is TikTok's
// documented recommendation (and matches what YouTube's chunked upload
// uses for parity). Tested via httptest.ServeMux in tiktok_oauth_test.go
// to assert the Content-Range header per chunk + final partial chunk.
const tiktokChunkSize = 10 * 1024 * 1024

// effectiveChunkSize resolves the per-call chunk byte size from the
// service-level override (test injection) or the package default
// (production).
func (s *TikTokOAuthService) effectiveChunkSize() int64 {
	if s.chunkSize > 0 {
		return int64(s.chunkSize)
	}
	return int64(tiktokChunkSize)
}

// startPublishPULLFromFile runs the chunked-upload chain synchronously:
//
//  1. fetchVideoBytes — HTTP GET on payload.VideoURL; we already trust
//     the URL (the publisher pre-flight didn't reject it). ContentType
//     propagates from the response headers.
//  2. uploadSessionInit — POST /v2/post/publish/video/init/ with
//     `source_info.source="PULL_FROM_FILE"` + `video_size` + `chunk_size`.
//     Returns (upload_url, publish_id).
//  3. chunkedUpload    — PUT each tiktokChunkSize byte slice to
//     upload_url with Content-Range: bytes X-Y/Z. The final chunk
//     is smaller when len(data) is not chunk-aligned.
//  4. uploadSessionComplete — POST
//     /v2/post/publish/video/upload/complete/ with {publish_id}.
//
// On error from any step, returns immediately with the failure. TikTok
// cleans up the partial upload server-side via the upload_url TTL
// (no client-side cleanup needed).
func (s *TikTokOAuthService) startPublishPULLFromFile(ctx context.Context, accessToken string, payload models.PublishPayload) (publishID string, state string, err error) {
	slog.Info("TikTok: starting async publish (FILE_UPLOAD chunked upload)")

	videoBytes, contentType, err := s.fetchVideoBytes(ctx, payload.VideoURL)
	if err != nil {
		return "", "", fmt.Errorf("tiktok file_upload: fetch video bytes: %w", err)
	}
	if contentType == "" {
		contentType = "video/mp4"
	}

	// TikTok's FILE_UPLOAD mode requires source="FILE_UPLOAD" (NOT
	// "PULL_FROM_FILE", which is only our internal Source discriminator)
	// and a total_chunk_count. Each non-final chunk must be >= 5MB;
	// videos smaller than that must be uploaded as a single chunk whose
	// size equals the whole file.
	total := int64(len(videoBytes))
	chunkSize := s.effectiveChunkSize()
	if total <= 5*1024*1024 {
		chunkSize = total
	}
	totalChunks := (total + chunkSize - 1) / chunkSize

	postInfo := map[string]interface{}{
		"title":           truncateTikTokTitle(payload.Text),
		"privacy_level":   normalizeTikTokPrivacyLevel(payload.PrivacyLevel),
		"disable_comment": modeIsDisabled(payload.CommentMode),
		"disable_duet":    modeIsDisabled(payload.DuetMode),
	}
	initBody := map[string]interface{}{
		"source_info": map[string]interface{}{
			"source":            "FILE_UPLOAD",
			"video_size":        total,
			"chunk_size":        chunkSize,
			"total_chunk_count": totalChunks,
		},
		"post_info": postInfo,
	}
	uploadURL, publishID, err := s.uploadSessionInit(ctx, accessToken, initBody)
	if err != nil {
		return "", "", fmt.Errorf("tiktok file_upload: init: %w", err)
	}

	if err := s.chunkedUpload(ctx, accessToken, uploadURL, videoBytes, contentType, chunkSize); err != nil {
		return "", "", fmt.Errorf("tiktok file_upload: upload chunks: %w", err)
	}

	if err := s.uploadSessionComplete(ctx, accessToken, publishID); err != nil {
		return "", "", fmt.Errorf("tiktok file_upload: complete: %w", err)
	}

	slog.Info("TikTok: FILE_UPLOAD upload finalised",
		"publish_id", publishID,
		"size_bytes", total,
		"chunk_size", chunkSize,
		"total_chunks", totalChunks)
	// TikTok returns the initial state as PROCESSING_UPLOAD; the
	// reconciler goroutine will CheckPublishStatus on subsequent ticks
	// until PUBLISH_COMPLETE or FAILED terminal state.
	return publishID, "PROCESSING_UPLOAD", nil
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
		return "", fmt.Errorf("tiktok status returned status %d: %s", resp.StatusCode, string(body))
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
		return nil, fmt.Errorf("tiktok publish failed: publish_id=%s state=%s", publishID, state)
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

// --- Private ---


// tikTokTitleMaxRunes is TikTok's documented per-post title/caption limit.
const tikTokTitleMaxRunes = 4000

func truncateTikTokTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= tikTokTitleMaxRunes {
		return s
	}
	return string(runes[:tikTokTitleMaxRunes])
}

func normalizeTikTokPrivacyLevel(level string) string {
	// Taglio 4b: ValidateContent already rejected empty/unrecognized
	// values, so this switch always matches. No default fallback.
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE":
		return "PUBLIC_TO_EVERYONE"
	case "MUTUAL_FOLLOW_FRIENDS":
		return "MUTUAL_FOLLOW_FRIENDS"
	case "SELF_ONLY":
		return "SELF_ONLY"
	default:
		return ""
	}
}

// validateTikTokPrivacyLevel returns an error if level is not one of the
// three TikTok-recognized privacy values. Used by ValidateContent.
func validateTikTokPrivacyLevel(level string) error {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PUBLIC_TO_EVERYONE", "MUTUAL_FOLLOW_FRIENDS", "SELF_ONLY":
		return nil
	default:
		return fmt.Errorf("tiktok privacy_level must be one of PUBLIC_TO_EVERYONE, MUTUAL_FOLLOW_FRIENDS, SELF_ONLY (got %q)", level)
	}
}

func modeIsDisabled(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "no_comments", "no_duet", "disabled", "off", "false", "0":
		return true
	default:
		return false
	}
}



// --- PULL_FROM_FILE helpers (Taglio 4.x chunked-upload addendum) ---

// fetchVideoBytes downloads the full video from payload.VideoURL via
// a single HTTP GET. We read ALL the bytes into memory in one shot —
// for the typical TikTok video size (≤ 256MB) this is acceptable; on
// larger inputs the platform would 4xx the init anyway (TikTok's own
// upload ceiling). The ethod is the analog of YouTube's
// headVideo + GET-on-upload-url streaming extracted into a single
// open-and-read pass.
//
// Content-Type propagates from the response headers (defaults to
// "video/mp4" when absent — TikTok's init body accepts a content_type
// field but the chunk PUTs only need it as a header).
func (s *TikTokOAuthService) fetchVideoBytes(ctx context.Context, videoURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", videoURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("video GET request: %w", err)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("video GET failed (url=%s): %w", videoURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("video URL returned status %d: %s", resp.StatusCode, string(body))
	}
	contentType := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read video bytes: %w", err)
	}
	return body, contentType, nil
}

// uploadSessionInit POSTs /v2/post/publish/video/init/ with the
// PULL_FROM_FILE source_info block and returns the (upload_url,
// publish_id) pair TikTok hands back. The Bearer header carries the
// decrypted access token (RefreshOAuthToken has already run via the
// vault before reaching this point). Bad init responses (4xx/5xx)
// surface as an error wrapping the response body so DLQ triage has
// the platform's rejection reason.
func (s *TikTokOAuthService) uploadSessionInit(ctx context.Context, accessToken string, initBody map[string]interface{}) (uploadURL, publishID string, err error) {
	jsonBody, _ := json.Marshal(initBody)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.tiktokapis.com/v2/post/publish/video/init/",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return "", "", fmt.Errorf("init request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("init failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("init returned status %d: %s", resp.StatusCode, string(body))
	}
	var initResult struct {
		Data struct {
			PublishID string `json:"publish_id"`
			UploadURL string `json:"upload_url"`
		} `json:"data"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &initResult); err != nil {
		return "", "", fmt.Errorf("init parse: %w", err)
	}
	if initResult.Data.UploadURL == "" || initResult.Data.PublishID == "" {
		return "", "", fmt.Errorf("init missing publish_id/upload_url (response=%s)", string(body))
	}
	return initResult.Data.UploadURL, initResult.Data.PublishID, nil
}

// chunkedUpload streams data to upload_url as a sequence of
// tiktokChunkSize-byte PUTs with `Content-Range: bytes X-Y/Z` headers.
// The final chunk is naturally smaller when total isn't chunk-aligned.
//
// IMPORTANT: each chunk PUT carries the user's Bearer access token —
// TikTok's upload_url is a per-chunk authenticated endpoint, NOT a
// pre-signed URL. A missing Authorization header on the PUTs would
// make TikTok return 401 on every chunk and we would re-enter the
// outbox's transient-error path needlessly. (Caught by code-review
// pass on the original implementation; the accessToken parameter
// was retrofitted in the review iteration.)
//
// TikTok documentation isn't fully public on the exact PUT response
// success codes (200/201/308 are all plausible per RFC 7233
// resumable-upload conventions); we accept any 2xx OR 308 Resume
// Incomplete marker as success and let the server's
// consistency-window sort out the byte accounting. Any other status
// fails the upload and bubbles up to StartPublish, where the
// per-target state-machine in the worker decides retry vs DLQ.
//
// The function does NOT make a single byte-recovery call on chunk
// failure — TikTok's upload_url TTL is short (typically a few
// minutes); if a chunk fails, the safest course is to abort, let the
// upload_url expire server-side, and let the worker re-dispatch the
// target via its retry column (next_attempt_at / attempt_count,
// migration 018).
func (s *TikTokOAuthService) chunkedUpload(ctx context.Context, accessToken, uploadURL string, data []byte, contentType string, chunkSize int64) error {
	total := int64(len(data))
	var uploaded int64
	chunksSent := 0
	for uploaded < total {
		select {
		case <-ctx.Done():
			return fmt.Errorf("chunk upload cancelled at byte %d: %w", uploaded, ctx.Err())
		default:
		}
		end := uploaded + chunkSize
		if end > total {
			end = total
		}
		chunk := data[uploaded:end]
		contentRange := fmt.Sprintf("bytes %d-%d/%d", uploaded, end-1, total)

		req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(chunk))
		if err != nil {
			return fmt.Errorf("chunk PUT request (range %s): %w", contentRange, err)
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Content-Range", contentRange)
		// CRITICAL: per-chunk Bearer auth. upload_url is NOT a
		// pre-signed signature; it's a server-side endpoint that
		// requires the same Bearer access token used on init.
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.ContentLength = int64(len(chunk))

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("chunk PUT failed at byte %d (range %s): %w", uploaded, contentRange, err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// Acceptable terminal codes: 200 OK, 201 Created, 206
		// Partial Content (intermediate chunk), 308 Resume Incomplete.
		// Anything else fails the upload.
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusPermanentRedirect && resp.StatusCode != 308 {
			return fmt.Errorf("chunk PUT returned status %d (range %s): %s", resp.StatusCode, contentRange, string(respBody))
		}
		chunksSent++
		uploaded = end
	}
	slog.Info("TikTok: chunked upload complete",
		"chunks", chunksSent,
		"total_bytes", total,
		"chunk_size", chunkSize)
	return nil
}

// uploadSessionComplete POSTs to the TikTok chunk-upload-completion
// endpoint with the publish_id so TikTok finalises the chunk-upload
// session and moves the post into the publish state machine.
//
// VERIFY (post-merge): the exact URL for the completion endpoint is
// documented variably across TikTok Content Posting API doc versions:
//   - /v2/post/publish/video/upload/complete/   (most pre-2025 docs)
//   - /v2/post/publish/video/complete/          (newer / 2026 docs)
//
// The path in this implementation is the pre-2025 form; if App
// Review feedback or live testing returns 404 from the completion
// URL, swap to the alternate path here (one-line change). The init
// endpoint and the chunked-PUT protocol are unaffected.
//
// A failure here leaves the chunks on TikTok's side (they'll expire
// server-side via the upload_url TTL); the worker re-dispatches the
// target via its retry column and a fresh init+upload+complete
// cycle picks up.
func (s *TikTokOAuthService) uploadSessionComplete(ctx context.Context, accessToken, publishID string) error {
	completeBody := map[string]string{
		"publish_id": publishID,
	}
	jsonBody, _ := json.Marshal(completeBody)
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.tiktokapis.com/v2/post/publish/video/upload/complete/",
		strings.NewReader(string(jsonBody)))
	if err != nil {
		return fmt.Errorf("complete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("complete failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("complete returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
