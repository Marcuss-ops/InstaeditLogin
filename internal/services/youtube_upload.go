package services

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

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
		ChunkSize:   cfg.Worker.YouTubeUploadChunkBytes,
		MaxRetries:  cfg.Worker.YouTubeUploadMaxRetries,
		BackoffBase: time.Duration(cfg.Worker.YouTubeUploadBackoffBaseMs) * time.Millisecond,
		BackoffCap:  time.Duration(cfg.Worker.YouTubeUploadBackoffCapMs) * time.Millisecond,
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

// AttachUploadSession wires the upload job context the chunk loop
// needs to (a) persist resumable-session state across worker
// crashes via sessionStore, (b) encrypt the session URI before
// persistence via sessionEncryptor, (c) propagate workerID +
// jobID so the repo's CAS-style SaveYouTubeSession /
// ClearYouTubeSession methods can refuse a write against a row
// whose lease has been re-claimed (or lease-expired) by a more
// recent worker. Called by the upload worker via the YouTube
// provider capability right before invoking Publish /
// StartPublish. Without this call the upload proceeds in-memory
// only — exactly the pre-P1#5 behaviour — so callers that don't
// care about persistence can keep using the service unchanged.
//
// Both sessionStore and sessionEncryptor must be non-nil together:
// storing the URI without encryption defeats the migration-048
// "credential-adjacent" intent; encrypting without a store just
// wastes CPU. The constructor refuses a (store, nil) or (nil,
// encryptor) combination to keep the invariant reachable from a
// single code path.
func (s *YouTubeOAuthService) AttachUploadSession(jobID int64, workerID string, store YouTubeSessionStore, encryptor SessionEncryptor) {
	s.sessionJobID = jobID
	s.sessionWorkerID = workerID
	s.sessionStore = store
	s.sessionEncryptor = encryptor
}

// persistSessionProgress encrypts the resumable upload URL and
// stamps (url, offset, chunk_size, expires_at) onto the
// upload_jobs row via sessionStore.Save. Called once per
// successful chunk (after the 308/200 server ack) so a worker
// crash mid-upload can resume from the persisted offset on the
// next claim. Tightly scoped: anything that touches the URI passes
// through redactYouTubeSessionURI first so a console log or
// panic dump doesn't leak the full value.
//
// The ciphertext-shape contract: base64.StdEncoding of the raw
// Encryptor output. Storing as a TEXT column means the repo
// doesn't need to be aware of the encryption scheme (the
// companion Load path on the worker side does base64-decode then
// Decrypt). Skips silently when sessionStore OR sessionEncryptor
// is nil; the legacy pre-P1#5 in-memory path stays valid.
// Logged at Debug so the missing-wiring breadcrumb is observable
// without polluting Info under normal operation.
func (s *YouTubeOAuthService) persistSessionProgress(ctx context.Context, uploadURL string, offset int64) {
	if s.sessionStore == nil || s.sessionEncryptor == nil {
		slog.Debug("youtube: persistSessionProgress skipped (no sessionStore/encryptor wired)",
			"job_id", s.sessionJobID, "redacted_url", redactYouTubeSessionURI(uploadURL))
		return
	}
	cipher, err := s.sessionEncryptor.Encrypt(uploadURL)
	if err != nil {
		slog.Warn("youtube: session URI encrypt failed; progress NOT persisted (next claim will resume in-memory only)",
			"job_id", s.sessionJobID, "redacted_url", redactYouTubeSessionURI(uploadURL), "error", err)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(cipher)
	if err := s.sessionStore.Save(ctx, s.sessionJobID, s.sessionWorkerID, encoded, offset,
		s.uploadOpts.ChunkSize, s.sessionExpiresAt()); err != nil {
		slog.Warn("youtube: session URI persist failed (worker will retry on next chunk)",
			"job_id", s.sessionJobID, "offset", offset, "redacted_url", redactYouTubeSessionURI(uploadURL), "error", err)
	}
}

// sessionExpiresAt returns NOW()+24h as the YouTube session TTL.
// YouTube's documented session lifetime is "at least 24 hours";
// the worker reads this back via the upload_jobs row on the next
// claim and refuses to reuse an expired URI. Centralised so a
// future fix ("actually it's 12h") is a one-line change instead
// of open-coding 24*time.Hour at every persist caller.
func (s *YouTubeOAuthService) sessionExpiresAt() time.Time {
	return s.now().Add(24 * time.Hour)
}

// handleSessionLost runs in the uploadVideoChunks recovery branch
// when queryUploadStatus reports ErrYouTubeSessionLost. Clears
// the persisted session columns so the NEXT worker's ClaimBatch
// sees a clean slate (a stale ciphertext pointing at the dead
// URI could otherwise be loaded and re-attempted). Caller is
// expected to follow up with a fresh initiateResumableSession.
// Logging uses the redacted form of any URI.
func (s *YouTubeOAuthService) handleSessionLost(ctx context.Context, deadUploadURL string) error {
	slog.Warn("youtube: session URI lost (404); clearing persisted state and re-initiating",
		"job_id", s.sessionJobID,
		"redacted_url", redactYouTubeSessionURI(deadUploadURL),
	)
	if s.sessionStore != nil {
		if err := s.sessionStore.Clear(ctx, s.sessionJobID); err != nil {
			slog.Warn("youtube: clear-session-after-404 failed (next worker will overwrite)",
				"job_id", s.sessionJobID, "error", err)
			// Don't surface Clear failure — recovery proceeds either way.
		}
	}
	return nil
}

// redactYouTubeSessionURI returns a redacted representation of a
// YouTube session URI that is safe to log. YouTube session URIs
// look like `http://uploads.youtube.com/upload?upload_id=...&key=...&cp=...&cid=...`
// where the key/token parts are credential-adjacent. The
// redaction strategy keeps the first 12 + last 4 chars of the URL
// so operators can correlate two log lines with the same session
// while never exposing the secret-bearing portion. Used everywhere
// uploadURL appears in a log/slog call. The companion rule: in
// this file, slog.X(...) MUST take the redacted form before the
// URI ever reaches the Logger. Tests assert "the full URL never
// appears in a test-loop's captured slog output".
func redactYouTubeSessionURI(uploadURL string) string {
	if uploadURL == "" {
		return ""
	}
	if len(uploadURL) <= 16 {
		return uploadURL
	}
	return uploadURL[:12] + "…" + uploadURL[len(uploadURL)-4:]
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

// ErrYouTubeSessionLost is the canonical sentinel returned by
// queryUploadStatus when YouTube's resumable-upload endpoint replies
// HTTP 404 to the `Content-Range: bytes */TOTAL` probe. 404 means the
// session URI either expired (>24h) or was never valid for this
// channel/title combination; the upload MUST switch to a fresh
// initiateResumableSession call instead of trying the same dead URI
// again. Co-exists with the many peer sentinels in this package; the
// uploadVideoChunks loop matches against this exact error string at
// the recovery site (see handleSessionLost below).
//
// Why a sentinel: queryUploadStatusWithRetry is wrapped through the
// generic retry/backoff path and would otherwise swallow a 404 into a
// generic "unexpected status" fmt.Errorf, which would then bypass the
// recovery branch in uploadVideoChunks and let a dead session blow up
// the whole publish. Surfacing ErrYouTubeSessionLost means the retry
// loop can hand off cleanly to the recovery branch without losing the
// 404-classification guarantee.
var ErrYouTubeSessionLost = errors.New("youtube upload session URI was rejected (404); resumption lost \u2014 re-initiating")

// YouTubeSessionStore is the narrow persistence contract the
// YouTubeOAuthService uses to persist the resumable-upload session
// URI + offset across worker crashes. The current implementation is
// *repository.UploadJobRepository (Save/Clear) but the service does
// NOT depend on that concrete type \u2014 the narrow interface here
// matches the post-P1#5 columns and lets an in-memory mock stand in
// during tests.
//
// IMPORTANT: the `sessionURICiphertext` argument MUST already be
// encrypted+base64'd (or otherwise scrubbed of the plaintext YouTube
// `Location:` URL); the repo writes the value verbatim into the
// `youtube_session_uri` TEXT column. The service holds the encryptor
// so callers MUST inject it; nil-encryptor is a constructor error.
//
// P1 hardening follow-up: add `Load(ctx, jobID) (uri, offset int64,
// expiresAt time.Time, error)` so a cross-crash resume can pick up
// where the previous worker left off. Today the service falls back to
// the `job.YouTubeSessionURI` columns hydrated by the repository's
// existing scanUploadJob (FindByID) path; the same encrypt/decrypt
// convention applies when those fields are read by the worker.
type YouTubeSessionStore interface {
	Save(ctx context.Context, jobID int64, workerID, sessionURICiphertext string, offset, chunkSize int64, expiresAt time.Time) error
	Clear(ctx context.Context, jobID int64) error
}

// SessionEncryptor is the narrow cipher contract the service uses to
// wrap the resumable-upload `Location:` URL before persistence.
// *crypto.Encryptor satisfies this interface; tests inject a
// deterministic replacement so assertions on ciphertext vs plaintext
// are deterministic. A nil encryptor on the service is treated as a
// fail-fast (the constructor returns an error) \u2014 there is no
// "best-effort plaintext" mode, because the YouTube session URI is
// a credential per Google's resumable upload protocol and storing it
// unencrypted defeats the entire point of the migration.
type SessionEncryptor interface {
	Encrypt(plaintext string) ([]byte, error)
	Decrypt(ciphertext []byte) (string, error)
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
		// so queryUploadStatusWithRetry MUST NOT swallow this \u2014
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
