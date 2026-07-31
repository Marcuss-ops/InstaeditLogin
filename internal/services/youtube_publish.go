package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// P1#6 — chunk size is now configurable via cfg.Worker.YouTubeUploadChunkBytes
// (env YOUTUBE_UPLOAD_CHUNK_BYTES, default 16 MB / 16777216, must be a
// multiple of 262144 = 256 KB per Google's resumable upload protocol).

// ErrYouTubeVideoNotFound is the typed sentinel surfaced from
// videos.update / setThumbnail / UpsertLocalizations when YouTube's
// v3 API returns HTTP 404 with Category="not_found" — meaning the
// referenced video_id no longer exists on the platform (user deleted
// it via YouTube Studio, moderator takedown, etc.). The original
// *YouTubeAPIError stays reachable via errors.As(err, &apiErr) for
// callers that need the StatusCode / Category / Message diagnostic
// fields. Errors.Is(err, ErrYouTubeVideoNotFound) returns true
// regardless of which wrapping layer added the sentinel (the wrap
// in UpdateVideoPrivacy / SetThumbnail uses Go 1.20+ multi-%w so the
// chain is preserved).
//
// Blocco #1 followup — Finding #4 (Phase-1 orphan video): publish
// workers detecting this sentinel route to a fresh publisher.Publish
// path (and clear the stale youtube_target_publications row) instead
// of hard-failing the post_target. See publish_worker.go's
// isOrphanedYouTubeVideo classifier for the worker-side dispatch
// (defense-in-depth fallback to substring match for any code path
// that has not been re-wired to the sentinel yet).
var ErrYouTubeVideoNotFound = errors.New("youtube video not found")

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

	// P1 hardening: a 404 from the chunk loop's status-query probe
	// (ErrYouTubeSessionLost) means the URI went dead — re-initiate
	// once. Cap at 1 extra attempt so a session that loses twice
	// doesn't loop us into a quota spiral; after the cap the chunk
	// loop's underlying error bubbles up to the outer upload-job
	// worker (MarkDeadLetter via attempt_count + max_attempts).
	// The redacted log shape carries no credential information,
	// matching the "MAI loggarli" half of the spec.
	var (
		videoID     string
		uploadURL   string
		publishErr  error
		sessRetried bool
	)
	for attempt := 0; attempt <= 1; attempt++ {
		var iErr error
		uploadURL, iErr = s.initiateResumableSession(ctx, accessToken, metadata, fileSize, contentType)
		if iErr != nil {
			publishErr = fmt.Errorf("failed to initiate resumable session: %w", iErr)
			break
		}
		slog.Debug("YouTube: resumable session initiated",
			"attempt", attempt,
			"redacted_url", redactYouTubeSessionURI(uploadURL),
		)
		videoID, iErr = s.uploadVideoChunks(ctx, uploadURL, payload.VideoURL, fileSize)
		if iErr == nil {
			publishErr = nil
			break
		}
		publishErr = iErr
		if !errors.Is(iErr, ErrYouTubeSessionLost) {
			// Non-404 error (e.g. 5xx already exhausted the retry
			// budget, or 4xx-not-429 permanent). Don't loop — let
			// the outer worker MarkRetry / MarkDeadLetter decide.
			break
		}
		if sessRetried {
			// Cap reached. Don't retry a 2nd time.
			break
		}
		sessRetried = true
		slog.Warn("YouTube: session URI lost (404); clearing persisted state + re-initiating",
			"attempt", attempt,
			"redacted_url", redactYouTubeSessionURI(uploadURL),
			"error", iErr,
		)
		if clearErr := s.handleSessionLost(ctx, uploadURL); clearErr != nil {
			slog.Warn("YouTube: clear-session-on-404 failed (recovery proceeds regardless)",
				"redacted_url", redactYouTubeSessionURI(uploadURL),
				"error", clearErr,
			)
		}
	}
	if publishErr != nil {
		return "", "", fmt.Errorf("failed to stream video: %w", publishErr)
	}

	slog.Info("YouTube: video uploaded successfully", "video_id", videoID)

	return EncodeYouTubePublishID(platformUserID, videoID), "processing", nil
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
//	processing  → (nil, nil)   // still in flight
//	succeeded   → (*PublishResult, nil)
//	failed      → (nil, error)  // terminal failure
//	terminated  → (nil, error)  // terminal failure
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
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("youtube video status returned %d", resp.StatusCode)
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

// EncodeYouTubePublishID encodes the channel ID and video ID into a single
// opaque publish ID used during the async publishing lifecycle.
//
// The composite is stored temporarily in post_target.platform_post_id while
// the target is in 'publishing' status. On a successful Reconcile, the final
// stored value is overwritten with the plain video ID.
//
// P1 (Blocco #1 followup) — exported so PublishWorker.publishTarget's
// Phase-2 reuse block can stamp the SAME composite shape on
// target.PlatformPostID as the async/sync Publish branches already
// stamp via result.PlatformMediaID. Without this export the bypass
// branch used the plain video_id and broke the unified-pipeline view's
// decode shape (ReconcileWorker + dashboard unified pipeline view
// assumed composite). Single rename of the package-private symbol.
func EncodeYouTubePublishID(channelID, videoID string) string {
	return channelID + ":" + videoID
}

// decodeYouTubePublishID splits an encoded publish ID back into channel ID
// and video ID.
//
// Mirror of EncodeYouTubePublishID — kept package-private because no
// external consumer needs it today (ReconcileWorker is in the same
// services package and can use the lowercase form). If a future
// package needs to decode, promote it the same way as the encode.
func decodeYouTubePublishID(publishID string) (channelID, videoID string, err error) {
	parts := strings.SplitN(publishID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid youtube publish id: %s", publishID)
	}
	return parts[0], parts[1], nil
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

// CoercePrivacyForUpdate (Blocco #1 followup — Finding #2) is the single
// source of truth for the privacyStatus × publishAt transform that
// YouTube's v3 videos.update endpoint accepts. Caller contract:
//
//   - publishAt == nil   → return (privacyLevel, nil)
//   - publishAt.IsZero() → return (privacyLevel, nil)  (defensive — same shape as nil)
//   - publishAt <= now   → return (privacyLevel, nil)  (clear publishAt; YouTube can't reschedule in the past)
//   - publishAt > now    → return ("private", publishAt) (YouTube ONLY accepts publishAt with privacyStatus=private)
//
// Why force "private" on the future branch: YouTube's v3 API
// mandates publishAt's effective privacy transition is private→public.
// Sending (privacy=public + publishAt=future) is rejected with HTTP
// 400 by the videos.update endpoint — the helper flips privacy to
// "private" so the subsequent call passes the API guard. The
// internal guard in UpdateVideoPrivacy
// ("publishAt requires privacyStatus=private") mirrors that API rule
// and would error if the coerce didn't run first.
//
// "now" is a parameter so tests inject deterministic clocks without
// monkey-patching time.Now(). Production callers pass s.now() (in
// UpdateVideoPrivacy) or time.Now() (in publish_worker.go bypass).
//
// Idempotent: applying the helper to its own output is a fixed point
// (already-coerced (privacy="private", publishAt=non-nil-in-future)
// → same output). This matters because publish_worker calls it from
// BOTH the bypass AND inside UpdateVideoPrivacy (defense in depth) —
// double-coercion produces identical results.
func CoercePrivacyForUpdate(privacyLevel string, publishAt *time.Time, now time.Time) (string, *time.Time) {
	if publishAt == nil || publishAt.IsZero() || !publishAt.After(now) {
		return privacyLevel, nil
	}
	// Future path: YouTube requires privacyStatus="private" alongside
	// publishAt. The selector block in UpdateVideoPrivacy already
	// validates privacyLevel ∈ {public, unlisted, private} upstream,
	// so the helper does NOT re-validate here.
	return "private", publishAt
}
