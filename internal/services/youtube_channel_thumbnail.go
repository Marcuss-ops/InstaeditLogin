package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func (s *YouTubeOAuthService) SetThumbnail(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error {
	if videoID == "" {
		return fmt.Errorf("youtube set thumbnail: empty video id")
	}
	if size <= 0 {
		return fmt.Errorf("youtube set thumbnail: invalid image size")
	}
	const maxThumbnailBytes = 2 * 1024 * 1024
	if size > maxThumbnailBytes {
		return fmt.Errorf("youtube set thumbnail: image exceeds 2 MB limit")
	}
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return fmt.Errorf("youtube set thumbnail: unsupported content type %q (only image/jpeg and image/png allowed)", mimeType)
	}

	params := url.Values{}
	params.Set("videoId", videoID)
	reqURL := "https://www.googleapis.com/upload/youtube/v3/thumbnails/set?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
	if err != nil {
		return fmt.Errorf("youtube set thumbnail: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", mimeType)
	req.ContentLength = size

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &YouTubeAPIError{StatusCode: 0, Category: "network", Message: fmt.Sprintf("youtube set thumbnail: request: %v", err)}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
		// Drain the body so the underlying connection can be reused.
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	rbody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return &YouTubeAPIError{StatusCode: http.StatusUnauthorized, Category: "auth", Message: "youtube set thumbnail: unauthorized (status 401)"}
	case resp.StatusCode == http.StatusForbidden:
		return &YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "youtube set thumbnail: forbidden (status 403)"}
	case resp.StatusCode == http.StatusNotFound:
		return &YouTubeAPIError{StatusCode: http.StatusNotFound, Category: "not_found", Message: "youtube set thumbnail: video not found (status 404)"}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "youtube set thumbnail: rate limited (status 429)"}
	case resp.StatusCode >= 500:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "server_error", Message: fmt.Sprintf("youtube set thumbnail: server error (status %d)", resp.StatusCode)}
	default:
		return &YouTubeAPIError{StatusCode: resp.StatusCode, Category: "unexpected", Message: fmt.Sprintf("youtube set thumbnail: unexpected status %d: %s", resp.StatusCode, string(rbody))}
	}
}

// GetYouTubeVideo fetches the details for a single YouTube video by id
// and returns the narrow subset of fields the InstaEdit BFF needs to
// validate a video before creating a thumbnail editor session. It
// returns an error when the video does not exist or the upstream call
// fails.
// UpdateVideoPrivacy updates the privacy status (and optionally the
// snippet title and/or description) of an existing YouTube video via
// videos.update. For immediate publication set privacy to "public" or
// "unlisted" and leave publishAt nil. For scheduled publication set
// privacy to "private" and provide a future publishAt timestamp; YouTube
// will make the video public at that time. Non-empty title/description
// are included in the snippet part and sent with part=snippet,status.
// PublishThumbnail uploads a custom thumbnail to YouTube, then
// updates the video privacy status (and, when supplied, the snippet
// title + description) in a single videos.update(part=snippet,status)
// call. Retries transient failures internally (3 retries with
// linear-backoff reset via doWithRetry).
//
// Returns the public YouTube watch URL on success.
func (s *YouTubeOAuthService) PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
	const maxThumbnailBytes = 2 * 1024 * 1024
	if len(thumbnailData) == 0 {
		return "", fmt.Errorf("youtube publish thumbnail: empty thumbnail data")
	}
	if len(thumbnailData) > maxThumbnailBytes {
		return "", fmt.Errorf("youtube publish thumbnail: thumbnail exceeds 2 MB limit")
	}
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return "", fmt.Errorf("youtube publish thumbnail: unsupported content type %q", mimeType)
	}

	// 1. Upload thumbnail with retry.
	setErr := doWithRetry(ctx, 3, time.Second, func() error {
		return s.SetThumbnail(ctx, accessToken, videoID, mimeType, bytes.NewReader(thumbnailData), int64(len(thumbnailData)))
	})
	if setErr != nil {
		return "", fmt.Errorf("youtube publish thumbnail: set thumbnail failed: %w", setErr)
	}

	// 2. Update video metadata + privacy with retry. When opts carries
	//    the P1 extensions (tags / default language / default audio
	//    language) we update them together via the extended-snippet
	//    payload; otherwise we delegate to the byte-identical
	//    UpdateVideoPrivacy path used by every other caller (job
	//    workers, the publish reconciler, …) so the pre-extension
	//    behaviour for callers that only supply title/description is
	//    preserved byte-for-byte.
	updateErr := doWithRetry(ctx, 3, time.Second, func() error {
		if hasExtendedSnippet(opts) {
			return s.updateVideoWithExtendedSnippet(ctx, accessToken, videoID, privacyStatus, publishAt, opts)
		}
		return s.UpdateVideoPrivacy(ctx, accessToken, videoID, privacyStatus, publishAt, opts.Title, opts.Description)
	})
	if updateErr != nil {
		return "", fmt.Errorf("youtube publish thumbnail: update video failed: %w", updateErr)
	}

	return "https://www.youtube.com/watch?v=" + videoID, nil
}

// hasExtendedSnippet reports whether opts carries any of the
// P1 snippet extensions beyond plain title/description. The
// orchestrator uses this gate to decide whether to fold tags +
// default languages into the single videos.update call or to
// delegate to the pre-extension UpdateVideoPrivacy path.
//
// Localizations (Translations) are NOT included here — they are
// applied via separate UpsertLocalizations calls (one per
// language) AFTER the snippet+status update succeeds.
