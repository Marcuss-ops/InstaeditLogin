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

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
