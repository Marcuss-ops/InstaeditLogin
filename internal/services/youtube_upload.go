package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/sampler"
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
		return sampler.UniformDuration(base, prev, rand.Int63n)
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
// NOT depend on that concrete type — the narrow interface here
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
// fail-fast (the constructor returns an error) — there is no
// "best-effort plaintext" mode, because the YouTube session URI is
// a credential per Google's resumable upload protocol and storing it
// unencrypted defeats the entire point of the migration.
type SessionEncryptor interface {
	Encrypt(plaintext string) ([]byte, error)
	Decrypt(ciphertext []byte) (string, error)
}

// UploadVideoAsPrivate implements services.UploadChannelUploader. It
// performs the Drive→YouTube resumable upload for one target, marking
// the video's status.privacyStatus='private' and returning the assigned
// YouTube video id. NO publish phase — the publish worker drives the
// videos.update call later (privacy=public + publishAt cursor).
//
// Differs from Publish() in that Publish drives the full upload + videos.update
// synchronously (privacy=public + publishAt=...). Here we want the
// upload to land as 'private' immediately so per-channel binding is
// discoverable via YouTube API BEFORE publish_at elapses — and so the
// follow-on Velox thumbnail-editor session can resolve to a real video
// id (the editor's invariant requires a non-public video).
//
// Lifecycle within one call:
//  1. HEAD source URL → size + content-type for chunk math.
//  2. POST metadata (snippet=post.title+caption; status.privacyStatus=private)
//     with X-Upload-Content-Length + X-Upload-Content-Type → Location URL.
//  3. PUT chunks via the existing chunked loop (handles Resume-After,
//     404 session recovery, Retry-After aware backoff).
//  4. Return the video id parsed from the terminal 200 response.
//
// Returns the LAST failure encountered wrapped with context. The upload
// worker decides whether to retry or route to blocked_auth; this method
// has no opinion.
//
// Defensive guards: nil receiver / nil post / empty accessToken / empty
// videoURL → typed error so the worker logs a clear breadcrumb instead of
// a generic nil pointer panic.
func (s *YouTubeOAuthService) UploadVideoAsPrivate(ctx context.Context, accessToken string, post *models.Post, videoURL string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: nil service")
	}
	if post == nil {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: nil post")
	}
	if accessToken == "" {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: empty accessToken")
	}
	if videoURL == "" {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: empty videoURL")
	}

	// 1. HEAD source — authoritative size + content-type for chunk math.
	size, contentType, err := s.headVideo(ctx, videoURL)
	if err != nil {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: head source: %w", err)
	}
	if size <= 0 {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: unknown source size (%d)", size)
	}

	// 2. Build metadata. status.privacyStatus='private' is MANDATORY
	// here — the Velox thumbnail editor requires a non-public video,
	// and the publish phase will flip privacy to the desired
	// post.PrivacyLevel (or cascade fallback) via the separate
	// videos.update call the publish worker drives at publish_at.
	metadata := map[string]interface{}{
		"snippet": map[string]interface{}{
			"title":       post.Title,
			"description": post.Caption,
		},
		"status": map[string]interface{}{
			"privacyStatus": "private",
		},
	}

	uploadURL, err := s.initiateResumableSession(ctx, accessToken, metadata, size, contentType)
	if err != nil {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: initiate resumable session: %w", err)
	}

	// 3. Stream chunks. The existing loop returns the video id parsed
	// from the terminal 200/201 response. The chunk loop's internal
	// per-chunk backoff is independent of the worker lease heartbeat
	// (which the upload-worker shell keeps alive via runWithHeartbeat).
	videoID, err := s.uploadVideoChunks(ctx, uploadURL, videoURL, size)
	if err != nil {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: chunked PUT: %w", err)
	}
	if videoID == "" {
		return "", fmt.Errorf("youtube UploadVideoAsPrivate: completed but no video id returned")
	}
	return videoID, nil
}

// Compile-time assertion: services.YouTubeOAuthService must satisfy
// UploadChannelUploader. Caught by `go vet`, not at runtime.
var _ UploadChannelUploader = (*YouTubeOAuthService)(nil)
