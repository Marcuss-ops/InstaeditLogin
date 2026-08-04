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

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

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
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("init session failed (status %d)", resp.StatusCode)
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

		// P1 hardening: stamp progress + session URI to upload_jobs
		// after every successful chunk. The helper encrypts the URI
		// via the sessionEncryptor + base64's the ciphertext; a
		// service without attachment falls back to in-memory exactly
		// like pre-P1#5. Logged breadcrumb (Debug) uses the redacted
		// URI shape so an SRE tailing logs can't reconstruct the
		// full Location header from a sequence of related events.
		s.persistSessionProgress(ctx, uploadURL, uploaded+int64(n))

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
		// any) is used inside the chunk loop and also propagated to
		// the durable upload-job retry scheduler after exhaustion.
		return "", retryAfter, true, &RetryAfterError{
			Err:   fmt.Errorf("rate limited (status 429, retry_after=%s)", retryAfter),
			Delay: retryAfter,
		}

	case resp.StatusCode >= 500:
		// 5xx — retryable. Honor Retry-After when present, fall
		// back to the configured exp backoff otherwise.
		return "", retryAfter, true, &RetryAfterError{
			Err:   fmt.Errorf("server error (status %d, retry_after=%s)", resp.StatusCode, retryAfter),
			Delay: retryAfter,
		}

	default:
		// 4xx (excluding 429) — permanent client error. YouTube's
		// docs are clear: bad metadata, body validation errors, etc.
		// won't fix themselves on retry. Bubble up with a typed
		// sentinel so the outer upload-job worker dead-letters the
		// job on the first claim instead of burning its retry budget.
		return "", 0, false, fmt.Errorf("%w: unexpected PUT response (status %d)", ErrPermanentUpload, resp.StatusCode)
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

	if resp.StatusCode == http.StatusNotFound {
		// P1 hardening: a 404 from the status-query probe means the
		// session URI is dead — either expired (>24h since the
		// Location: header was minted), or metadata-incompatible with
		// the resumable session (e.g. channel re-bound under a
		// different oauth_connection_id). Surface as the typed
		// sentinel so the chunk loop's recovery branch can clear +
		// re-initiate, instead of getting swallowed by the generic
		// retry path. Any retry of a 404 just wastes a round-trip
		// (YouTube will keep returning 404 forever for a dead URI),
		// so queryUploadStatusWithRetry MUST NOT swallow this —
		// the upstream caller matches on ErrYouTubeSessionLost
		// explicitly and switches to a fresh initiateResumableSession.
		return 0, ErrYouTubeSessionLost
	}
	if resp.StatusCode != 308 {
		return 0, fmt.Errorf("unexpected status query response: %d", resp.StatusCode)
	}

	// Task 10.10.x polish #1: a successful 308 resume probe is BY
	// DEFINITION a chunk-loss recovery event (otherwise we'd be
	// doing the FIRST chunk PUT, not resuming from a partial
	// state). Increment metrics.resumable_recovery_total{chunk_lost}
	// so the operator dashboard can distinguish "worker crashed
	// mid-upload and the next worker is resuming" from a normal
	// first-time upload (which never reaches this probe).
	//
	// Pre-polish, this line was missing; the production metric went
	// flat after every database migration / cfg-rollout because the
	// only consumer was a manual test helper that masked the
	// real wire-up. The Polish #1 test
	// (internal/services/task_10_10_resumable_recovery_test.go)
	// drives queryUploadStatus via httptest and asserts the
	// counter delta == 1 on a 308 reply. Removing the line below
	// trips that assertion.
	metrics.RecordResumableRecovery(metrics.ResumableRecoveryReasonChunkLost)

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
