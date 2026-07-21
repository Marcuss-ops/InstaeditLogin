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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
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

// ClientID returns the YouTube OAuth client_id this service was
// configured with (cfg.YouTubeClientID). Used by pkg/api/handlers.go
// handleValidateAccount to compare Google's tokeninfo `aud` against
// the configured client — a Production-but-issued-for-Testing token
// carries a mismatched aud and is a hard reauth signal (the 4-step
// pipeline's STEP 2 guard). Returns "" if the service hasn't been
// fully constructed (defensive — the production wiring wires
// cfg.YouTubeClientID at NewYouTubeOAuthService time).
func (s *YouTubeOAuthService) ClientID() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.cfg.YouTubeClientID
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
// (the worker has already refreshed via the vault before calling) and
// verifies the returned channel id set includes expectedChannelID.
//
// A single Google Account OAuth grant can manage up to ~100 YouTube
// channels (server-side max today); channels.list?maxResults=50
// silently truncates at 50. We therefore follow nextPageToken until
// empty, with a hard upper bound (see maxChannelsPerGrant below) as a
// safety net against (a) a hostile / misconfigured grant reporting
// unbounded channel sets, (b) a future YouTube increase of the
// per-grant cap leaving the operator with N>cap channels visible.
//
// Behaviour matrix:
//   - 200 OK across N pages (1 <= N) with the expected channel id in
//     ANY page → nil. N is bounded by maxChannelsPerGrant (200 today).
//   - 200 OK across all pages, NO match →
//     fmt.Errorf("%w: expected %q, grant-bound channels=%v ...)",
//     ErrYouTubeChannelMismatch, expectedChannelID, <full list>)
//     — a SINGLE call site visibility problem where any channel-list
//     page contains the expected id materially helps the operator
//     diagnose the drift.
//   - 200 OK across all pages, 0 unique channels collected → same
//     sentinel as 'NO match' but with 'grant has 0 channels' diagnostic
//     (the grant lost all bindings — recoverable only via a fresh
//     OAuth dance).
//   - maxChannelsPerGrant safety cap hit BEFORE exhausting nextPageTokens
//     → ErrYouTubeChannelMismatch with 'safety cap reached' diagnostic.
//     Triggered when a manager is bound to >200 channels — today this
//     is impossible (server max ~100 per grant) but the cap guards
//     against future API surface changes.
//   - Non-200 / network / decode error at any page → plain wrapped
//     error (NOT wrapped in ErrYouTubeChannelMismatch) so the worker
//     treats it as transient. The transient contract is unchanged
//     from the pre-pagination single-GET path.
//
// ErrChannelListSafetyCap is the typed error returned by
// ValidateChannelBinding when the loop hit the maxChannelsPerGrant
// (200) safety cap BEFORE nextPageToken exhaustion. The struct fields
// are extracted by tests + cross-package callers via errors.As:
//
//	var cap *ErrChannelListSafetyCap
//	if errors.As(err, &cap) { ... cap.Expected, cap.Cap ... }
//
// Error() returns a string that preserves the original
// ErrYouTubeChannelMismatch prefix so existing log-spelunking on the
// message substring keeps working. Unwrap() returns
// ErrYouTubeChannelMismatch so errors.Is(err, ErrYouTubeChannelMismatch)
// is still green WITHOUT callers needing to know the typed-struct
// shape — the sentinel stays the canonical "channel binding failed"
// signal, and the typed-struct is a refinement that carries the
// "how" (cap-hit) diagnostic.
//
// Distinct from the exhaustion-path mismatch (which still wraps the
// plain ErrYouTubeChannelMismatch sentinel — distinguishable via a
// negative errors.As) and from the BindGrantToChannel mismatch
// (separate production path, OUT OF SCOPE for this refactor).
type ErrChannelListSafetyCap struct {
	// Expected is the channel id the caller asked the loop to find.
	Expected string
	// Cap is the maxChannelsPerGrant value AT THE TIME OF THE HIT.
	// Surfaced as a structured field so tests can assert against
	// it without grepping for the literal "200" in error.Error().
	Cap int
}

// Compile-time assertion (matches the YouTubeChannelBinder /
// YouTubeCanaryUploader guard pattern below). Caught by `go vet`,
// not at runtime.
var _ error = (*ErrChannelListSafetyCap)(nil)

// Error returns the canonical human-readable form. The redundant
// "%v: ..." prefix re-emits ErrYouTubeChannelMismatch's text so the
// resulting message reads identically to the pre-refactor
// fmt.Errorf("%w: ...", ErrYouTubeChannelMismatch, ...). This keeps
// any operator-side log-grep recipe (the "must mention safety cap
// reached" diagnostic the old strings.Contains was enforcing)
// intact while letting go-side consumers switch on the typed
// struct. Pinning the format here means a future message-shape
// change is one-line and the test assertions don't need to update
// in lockstep.
func (e *ErrChannelListSafetyCap) Error() string {
	return fmt.Sprintf("%v: expected %q not found in first %d unique channel ids (safety cap reached)",
		ErrYouTubeChannelMismatch, e.Expected, e.Cap)
}

// Unwrap exposes ErrYouTubeChannelMismatch for both errors.Is and
// errors.As chains. Callers that DON'T care about the typed-struct
// refinement keep working with errors.Is(err, ErrYouTubeChannelMismatch);
// callers that DO care can do errors.As(err, &safetyCap) to recover
// the structured fields.
func (e *ErrChannelListSafetyCap) Unwrap() error {
	return ErrYouTubeChannelMismatch
}

// ErrChannelMismatchMsg formats the canonical operator-facing
// diagnostic for the ValidateChannelBinding EXHAUSTION path (and
// any future non-cap mismatch path). Centralising here means a
// future message-shape change is one-line and tests can pin ONE
// canonical rendering via the helper call rather than reaching
// into the wrapped error string. Currently returned only on the
// exhaustion path (lines below); the 0-channels / safety-cap paths
// use either the typed struct above or the existing inline format,
// and stay that way intentionally (each carries distinct semantics
// the operator needs to tell apart).
func ErrChannelMismatchMsg(expected string, bound []string) string {
	return fmt.Sprintf("expected %q, grant-bound channels=%v", expected, bound)
}

// The method is a paginated GET loop; it does NOT re-refresh the
// access token to avoid double-quota usage (the publish worker
// already refreshed in step 5 of publishTarget). The token MUST
// therefore be a fresh bearer token; OAuth-only access tokens (no
// refresh) are not supported on this path — they're an immediate 401
// and the worker should treat them as reauth-required via the
// existing token-refresh error path.
func (s *YouTubeOAuthService) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	if expectedChannelID == "" {
		return fmt.Errorf("youtube channel binding check: empty expected channel id")
	}

	// Safety cap. Server-side per-grant max is ~100 today; 200
	// leaves headroom for a future API change + a buffer before any
	// runaway loop would hit the underlying quota. Hitting the cap
	// also tells the operator their distribution planning needs to
	// change (see docs/OAUTH-PRODUCTION.md 'channels.list pagination
	// + 40-50 channels per manager').
	const maxChannelsPerGrant = 200

	var (
		pageToken string
		totalIDs  []string
		seen      = make(map[string]struct{}, 64)
	)
	for page := 1; ; page++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("youtube channel binding: cancelled by %w at page %d", err, page)
		}

		params := url.Values{}
		params.Set("part", "id")
		params.Set("mine", "true")
		params.Set("maxResults", "50")
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			"https://www.googleapis.com/youtube/v3/channels?"+params.Encode(), nil)
		if err != nil {
			return fmt.Errorf("youtube channel binding: create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("youtube channel binding: channels.list request: %w", err)
		}

		var result youtubeChannelsResponse
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return fmt.Errorf("youtube channel binding: channels.list returned %d: %s", resp.StatusCode, string(body))
		}
		if jerr := json.NewDecoder(resp.Body).Decode(&result); jerr != nil {
			resp.Body.Close()
			return fmt.Errorf("youtube channel binding: decode channels.list: %w", jerr)
		}
		resp.Body.Close()

		// Accumulate unique IDs in arrival order. Google does NOT
		// guarantee distinct channels across page boundaries, so
		// dedupe here: (a) the final mismatch count matches what
		// the operator will see in the dashboard, and (b) the
		// safety cap below counts UNIQUE channels rather than
		// request rows.
		for _, ch := range result.Items {
			if ch.ID == "" {
				continue
			}
			if _, dup := seen[ch.ID]; dup {
				continue
			}
			seen[ch.ID] = struct{}{}
			totalIDs = append(totalIDs, ch.ID)
			if ch.ID == expectedChannelID {
				return nil // bound to expected channel — proceed with upload
			}
		}

		if len(totalIDs) >= maxChannelsPerGrant {
			// Safety cap reached BEFORE nextPageToken exhausted.
			// Treat as mismatch because we cannot prove the expected
			// is NOT in the truncated set. This is a structural
			// escape valve; operators with >200 channels per grant
			// will be flagged to re-distribute or to raise the cap
			// via docs/OAUTH-PRODUCTION.md.
			//
			// Returns the typed struct (NOT a fmt.Errorf wrap) so
			// errors.As(err, &safetyCap) succeeds AND
			// errors.Is(err, ErrYouTubeChannelMismatch) still works
			// via Unwrap(). The error message is preserved 1:1
			// against the pre-refactor shape (same prefix, same
			// substrings the operator log-grep recipe cared about).
			return &ErrChannelListSafetyCap{
				Expected: expectedChannelID,
				Cap:      maxChannelsPerGrant,
			}
		}

		if result.NextPageToken == "" {
			break // API returned the final page; pagination complete
		}
		pageToken = result.NextPageToken
	}

	if len(totalIDs) == 0 {
		return fmt.Errorf("%w: expected %q, grant has 0 channels",
			ErrYouTubeChannelMismatch, expectedChannelID)
	}
	// Exhaustion path: pages walked to completion, expectedChannelID
	// not found in any. Wraps the canonical ErrYouTubeChannelMismatch
	// sentinel AND formats the operator-facing diagnostic via the
	// ErrChannelMismatchMsg helper so tests pin ONE canonical
	// rendering. Distinguishable from the safety-cap path (which
	// returns the *ErrChannelListSafetyCap typed struct above): an
	// errors.As(err, &safetyCap) check on this error returns false,
	// which ExhaustedMismatch_ReturnsMismatch asserts explicitly.
	return fmt.Errorf("%w: %s", ErrYouTubeChannelMismatch,
		ErrChannelMismatchMsg(expectedChannelID, totalIDs))
}

// Compile-time assertion: YouTubeOAuthService satisfies the
// services.YouTubeChannelBinder capability interface. Caught by
// `go vet`, not at runtime.
var _ YouTubeChannelBinder = (*YouTubeOAuthService)(nil)

// YouTubeCanaryUploader is the YouTube pre-flight canary capability
// interface invoked by publish_worker BEFORE the real publish when
// post.Metadata.canary_upload=true (Task 7/10). The implementation
// uploads a 5-10s/<5MB/privacy=private test video titled
// INSTAEDIT-OAUTH-CANARY-{channel_id}-{timestamp}, then verifies the
// uploaded channel id matches the platform_account.platform_user_id.
//
// Returns (\*CanaryUploadResult, error). nil result + non-nil error
// means the canary itself failed (caller flags PostStatusBlockedAuth
// and platform_account.status='reauth_required'). Non-nil result with
// UploadedChannelID == expectedChannelID means success; the worker
// proceeds to the real publish. Mismatch == blocker.
type YouTubeCanaryUploader interface {
	CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*CanaryUploadResult, error)
}

var _ YouTubeCanaryUploader = (*YouTubeOAuthService)(nil)

// VerifyChannelIdentity (Task 2/10) is the REUSABLE pre-action
// channel-bound guard. It is the public alias for
// YouTubeChannelBinder.ValidateChannelBinding — the canonical
// pre-tx (services.ChannelAuthorizationService.AuthorizeChannel) +
// pre-upload (internal/worker.PublishWorker.publishTarget)
// pre-flight check. Both call sites need exactly the same logic
// (channels.list(mine=true) on the just-refreshed access token,
// compare against the platform_account.platform_user_id) so the
// canonical implementation lives here, behind a typed helper, and
// every consumer delegates. The user's spec asked for a guard named
// verifyChannelIdentity(token, expectedChannelID); the binder
// argument is the narrow YouTube provider interface so tests can
// pass an in-memory fake (no real HTTP round-trip).
//
// Return contract mirrors YouTubeOAuthService.ValidateChannelBinding:
//   - nil → grant is bound to expectedChannelID, proceed.
//   - error wrapping ErrYouTubeChannelMismatch → grant is NOT bound
//     to expectedChannelID. The HTTP layer maps this to 422 +
//     status='reauth_required'; the publish worker maps this to
//     post_target.status='blocked_auth' + platform_account.status=
//     'reauth_required'; neither path crosses the publish boundary.
//   - any other error → transient (network, 5xx, decode). Caller
//     MUST treat as transient (retry on next tick) and MUST NOT
//     flag reauth_required — would lock out the operator for a
//     transient blip.
//
// Pass binder=nil as a no-op (returns nil immediately). Useful in
// tests that don't want to wire a real YouTube provider and for any
// future non-YouTube provider that shouldn't run the YouTube-specific
// channels.list check (the existing provider path already filters
// `account.Platform == models.PlatformYouTube` upstream).
func VerifyChannelIdentity(ctx context.Context, binder YouTubeChannelBinder, accessToken, expectedChannelID string) error {
	if binder == nil {
		return nil
	}
	return binder.ValidateChannelBinding(ctx, accessToken, expectedChannelID)
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
//   - 409 mapping)
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
	params.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly openid email profile")
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

// youtubeTokenInfoResponse mirrors the JSON shape Google returns from
// https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=... .
// Field names match Google's lowercase contract verbatim (Aud→aud,
// Azp→azp, etc.); json.Unmarshal would otherwise need case-insensitive
// matching for every field. Only the operator-visible subset is captured;
// `error`, `error_description` etc. surface in the wrapped error
// message returned by GetTokenInfo on a 400 reply.
type youtubeTokenInfoResponse struct {
	Aud        string `json:"aud"`
	Azp        string `json:"azp"`
	Scope      string `json:"scope"`
	ExpiresIn  int64  `json:"expires_in"`
	AccessType string `json:"access_type"`
	Email      string `json:"email"`
}

// YouTubeTokenInfo is the structured introspection reply returned by
// YouTubeOAuthService.GetTokenInfo. Mirrors the four fields
// scripts/verify-google-oauth-mode.sh prints (aud, azp, scope,
// expires_in) plus an `email` field the script doesn't expose today
// (openid scope returns it; useful for the operator-side audit log).
//
// HasUpload / HasReadonly are derived flags computed at construction
// time so callers can write `if !info.HasUpload { ... }` without
// re-parsing `Scope` themselves. The canonical scope strings are
// the full https://www.googleapis.com/auth/<scope> form (NOT the
// shortened alias) — matches what GetLoginURLWithOptions sets in
// the consent URL and what Google returns from tokeninfo.
type YouTubeTokenInfo struct {
	Aud       string
	Azp       string
	Scope     string
	ExpiresIn time.Duration
	Email     string

	HasUpload   bool
	HasReadonly bool
}

// GetTokenInfo calls Google's oauth2/v3/tokeninfo public introspection
// endpoint with the supplied access token and returns the structured
// introspection reply.
//
// This is the CODE-SIDE equivalent of scripts/verify-google-oauth-mode.sh
// (the bash operator quick-check). Keeping a single canonical
// implementation in Go means the operator script and the handler-level
// validator never drift. Per Google's contract, this endpoint returns:
//
//	200 OK + JSON for any access token in good standing
//	400 Bad Request + {"error":"invalid_token",...} for expired,
//	    revoked, malformed, or otherwise un-introspectable tokens
//
// Error contract:
//   - non-200 (HTTP 400 typically) → wrapped error containing Google's
//     {"error":"invalid_token","error_description":"..."} body. Callers
//     distinguish hard-rejection (Google said the token is bad) from
//     transient (network / decode) by inspecting resp.StatusCode
//     before calling GetTokenInfo, OR by classifying the wrapped
//     error string itself in the handler. The HTTP layer in
//     handleValidateAccount maps a non-200 to 422 +
//     status='reauth_required' — same runbook as an invalid_grant
//     refresh-result.
//   - decode error or network error → plain wrapped error (NOT a
//     sentinel). The handler treats this as transient (next tick
//     retries). Mirrors the existing pre-step-2 channel-binding
//     convention: only ErrYouTubeChannelMismatch-shaped failures
//     flip the platform_account to reauth_required; everything else
//     is operator-deferred.
//
// The endpoint takes the access token AS A QUERY PARAMETER. This is
// documented and supported by Google; their modern docs recommend
// the Authorization header for NEW integrations, but the query-param
// path stays canonical for verification scripts and operator tooling
// (Google's docs link to it explicitly). Confirmed against
// scripts/verify-google-oauth-mode.sh which this method mirrors.
//
// Cross-references:
//   - pkg/api/handlers.go::handleValidateAccount (step 2 of the
//     4-step YouTube OAuth readiness pipeline introduced in
//     conventions/200-channel YouTube OAuth plan)
//   - scripts/verify-google-oauth-mode.sh (operator-shell analogue)
func (s *YouTubeOAuthService) GetTokenInfo(ctx context.Context, accessToken string) (*YouTubeTokenInfo, error) {
	if accessToken == "" {
		return nil, fmt.Errorf("youtube tokeninfo: empty access token")
	}

	reqURL := "https://www.googleapis.com/oauth2/v3/tokeninfo?access_token=" + url.QueryEscape(accessToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube tokeninfo: create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube tokeninfo: request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube tokeninfo returned %d: %s", resp.StatusCode, string(body))
	}

	var r youtubeTokenInfoResponse
	if jerr := json.Unmarshal(body, &r); jerr != nil {
		return nil, fmt.Errorf("youtube tokeninfo: decode: %w", jerr)
	}

	out := &YouTubeTokenInfo{
		Aud:       r.Aud,
		Azp:       r.Azp,
		Scope:     r.Scope,
		ExpiresIn: time.Duration(r.ExpiresIn) * time.Second,
		Email:     r.Email,
	}
	for _, sc := range strings.Fields(r.Scope) {
		switch sc {
		case "https://www.googleapis.com/auth/youtube.upload":
			out.HasUpload = true
		case "https://www.googleapis.com/auth/youtube.readonly":
			out.HasReadonly = true
		}
	}
	return out, nil
}

// canaryUploadLiteral is the SINGLE source-of-truth for the canary
// body. Both the byte slice (canaryUploadBytes) and the size constant
// (canaryUploadSize) derive from this — a future maintainer edits
// THIS line, the two derived values follow without cross-reference
// mistakes. The previous shape (a duplicated literal in two places)
// burst silently: a delete in canaryUploadBytes without the matching
// edit in canaryUploadSize surfaced as a content-range mismatch
// rather than a compile error.
const canaryUploadLiteral = "INSTAEDIT-CANARY-PAYLOAD\n"

// canaryUploadBytes is a small synthetic payload used for the
// optional INSTAEDIT-OAUTH-CANARY test upload. Intentionally small
// (a single PUT chunk) so:
//
//   - The canary doesn't meaningfully consume the daily videos.insert
//     quota (1 call per /validate invocation that requests canary).
//   - Test assertions can hard-code the byte offsets
//     (bytes 0-21 / 22 bytes) without measuring the actual byte length.
//
// YouTube's videos.insert endpoint MAY accept this non-video content
// (returning 200 + video_id) OR reject it with 4xx (invalid argument,
// because the upload protocol expects video/* bytes). Both outcomes
// prove end-to-end binding — the snippet.channelId reconciliation on
// the resulting video (or the videos.list absence on rejection) is
// the source of truth. The canary upload body's content is NOT what
// step-4 measures — channel binding is.
var canaryUploadBytes = []byte(canaryUploadLiteral)

// canaryUploadSize derives from canaryUploadLiteral — guarantees
// compile-time sync with canaryUploadBytes.
const canaryUploadSize = int64(len(canaryUploadLiteral))

// canaryUploadContentType is intentionally NOT video/* — the canary
// upload is a probe, not a real publish. Stamping a non-video MIME
// makes the canary visually distinct in any tooling that filters on
// MIME type, AND signals to Google's API that the body is not a
// real video (Google may 4xx on MIME mismatch; that's still
// acceptable evidence that the OAuth grant can call videos.insert).
const canaryUploadContentType = "application/octet-stream"

// ErrYouTubeCanaryRejected is the canonical sentinel for hard 4xx
// rejections from the canary upload path (videos.insert init OR PUT
// chunk PUT). Distinct from ErrYouTubeChannelMismatch so the handler
// can produce a different audit-log message ("canary upload rejected
// by YouTube" vs "canary landed on the wrong channel"), but the
// runbook is identical (status='reauth_required'). Transient 5xx
// errors are NOT wrapped in this sentinel — they remain plain
// wrapped so the handler treats them as transient (next-sync retry).
//
// IMPORTANT: only 4xx codes SUPPRESSED in isHardRejection4xxStatus
// escalate to this sentinel. Rate-limit 429, Locked 423, every 5xx,
// plus decode / network / ctx-cancelled errors all stay on the
// transient branch — that's the deliberate choice the user's
// 200-channel YouTube OAuth plan asks for (transient blip ≠ grant
// drift ≠ reauth).
var ErrYouTubeCanaryRejected = errors.New("youtube canary upload was rejected by videos.insert (4xx)")

// statusCodeRegexp captures the (status N) triplet embedded in the
// upstream wrapped errors emitted by initiateResumableSession and
// putChunk. The two methods format their errors in known shapes:
//
//   - initiateResumableSession: "init session failed (status N): ..."
//   - putChunk: "unexpected PUT response (status N): ..." /
//     "rate limited (status 429, ...)" /
//     "server error (status N, ...)" or "server error (status N)"
//
// The regex matches just the parenthesized (status N) pair so
// downstream logic stays decoupled from the leading message verb.
// Compile-time build (var not const, regexp.MustCompile panics on
// bad pattern).
var statusCodeRegexp = regexp.MustCompile(`\(status (\d+)\)`)

// isHardRejection4xxStatus inspects the wrapped error returned by
// initiateResumableSession or putChunk (the two upstream callers
// CanaryUpload delegates to) and returns true iff it represents a
// HARD 4xx rejection that should be flagged ErrYouTubeCanaryRejected
// (handler → 422 + reauth) versus a TRANSIENT response that should
// remain plain wrapped (handler → next-sync-retry).
//
// Why regex on err.Error() rather than typed sentinels from the
// upstream methods: initiateResumableSession / putChunk are
// pre-existing call sites used by the publish path (not just the
// canary) and a sentinel refactor would have a much wider blast
// radius. The string-format shape they emit is documented AND
// stable across each method's revisions. The 4xx codes that get
// the reauth treatment are explicitly enumerated; any status
// outside the table falls through to the transient branch by
// default.
//
// Enumerated reauth statuses (4xx-not-429-or-423):
//
//	400 — bad request / malformed metadata
//	401 — YouTube-side token rejection mid-upload (operator must re-consent)
//	403 — forbidden / Brand Account re-bound silently
//	404 — session URI lost or grant revoked by Google
//	408 — rare; request timeout sent by YouTube
//	409 — channel / quota state conflict
//	410 — gone; channel may have been deleted
//	422 — unprocessable; metadata valid but refused
//	451 — legal / jurisdictional unavailability
//
// Transient-by-default (NOT in table):
//
//	429 — rate limit (Retry-After header is honored upstream)
//	423 — Locked; transient alignment-of-resources retry signal
//	5xx — server error; retried on next-sync tick
//	decode / network / ctx-cancelled — pass-through plainly
//
// Long-term: a future refactor should add typed sentinels to
// initiateResumableSession and putChunk so CanaryUpload can switch
// on errors.Is instead of regex. Tracked as a follow-up; the
// regex shape is correct for the 4-step pipeline today.
func isHardRejection4xxStatus(err error) bool {
	if err == nil {
		return false
	}
	m := statusCodeRegexp.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		return false
	}
	switch m[1] {
	case "400", "401", "403", "404", "408", "409", "410", "422", "451":
		return true
	}
	return false
}

// CanaryUploadResult captures the canary's outcome for the handler
// (step 4 of /accounts/{id}/validate). The handler renders this into
// the 200 OK response so the SPA can surface "canary video id"
// alongside the validation summary.
type CanaryUploadResult struct {
	// VideoID is the YouTube-assigned video id (typically 11 chars). The
	// SPA renders it as a clickable link to https://www.youtube.com/watch?v=VIDEOID
	// so the operator can verify the canary exists in their YouTube Studio.
	VideoID string
	// UploadedChannelID is the snippet.channelId YouTube stamped on the
	// resulting video — the channel the upload ACTUALLY landed on. On
	// success ALWAYS equal to the supplied expectedChannelID; the
	// function short-circuits to a wrapped ErrYouTubeChannelMismatch
	// on a bind-mismatch (the consistency check rejects the row before
	// success is returned).
	UploadedChannelID string
}

// CanaryUpload uploads the canary payload as a PRIVATE YouTube video
// (titled INSTAEDIT-OAUTH-CANARY-{channel_id}-{unix-timestamp}), then
// verifies the resulting snippet.channelId matches the expected
// channel. This is the OPTIONAL step 4 of the 4-step
// /accounts/{id}/validate pipeline. The flow is identical to a
// normal publish (initiate resumable session → single-chunk PUT →
// videos.list reconcile for channel binding) but with a fixed-length
// body and an INSTAEDIT-OAUTH-CANARY title so the operator can clean
// them up in bulk from YouTube Studio. Per the user's
// 200-channel YouTube OAuth plan, canary is opt-in per request
// (body field `"canary": true`) so the default validate path stays
// cheap (no quota cost, no noise in YouTube Studio).
//
// Bound to expectedChannelID at TWO checkpoints:
//
//  1. The PUT chunk server confirms the upload completed (terminal
//     200 returning {"id":"<videoID>"}) — the videoID is then used
//     as the query key for step 2.
//  2. After upload, videos.list pulls the actual snippet.channelId
//     YouTube stamped on the video and compares it to
//     `expectedChannelID`. THIS is the source of truth — the handler
//     MUST trust this over channels.list(page1..N) for end-to-end
//     proof. A canary that lands on the wrong channel is a hard
//     reauth-required signal (the OAuth grant is silently re-bound
//     to a different Brand Account, the very failure mode the user
//     spec wants to catch).
//
// Errors:
//   - wrapped ErrYouTubeChannelMismatch → upload succeeded but landed
//     on a DIFFERENT channel. Handler maps to 422 +
//     status='reauth_required' — same runbook as step-3 bind fail.
//   - wrapped ErrYouTubeCanaryRejected → YouTube refused the upload
//     (4xx-not-429: quota exceeded, scope missing, format error).
//     Handler maps to 422 + status='reauth_required' (the grant
//     reached YouTube but was refused — the operator cannot publish
//     this way regardless).
//   - 5xx / decode / network / ctx-cancelled → plain wrapped error.
//     Handler treats as transient (next-sync retry); mirrors the
//     existing pre-step-pre-validate channel-binding convention.
func (s *YouTubeOAuthService) CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*CanaryUploadResult, error) {
	if expectedChannelID == "" {
		return nil, fmt.Errorf("youtube canary: empty expected channel id")
	}
	if accessToken == "" {
		return nil, fmt.Errorf("youtube canary: empty access token")
	}

	title := fmt.Sprintf("INSTAEDIT-OAUTH-CANARY-%s-%d", expectedChannelID, s.now().UTC().Unix())
	metadata := map[string]interface{}{
		"snippet": map[string]interface{}{
			"title":           title,
			"categoryId":      "22", // People & Blogs — neutral category
			"defaultLanguage": "en",
			"description":     "OAuth readiness canary video. Auto-uploaded by InstaEdit to confirm channel binding + upload capability. Safe to delete from YouTube Studio.",
		},
		"status": map[string]interface{}{
			"privacyStatus":           "private",
			"selfDeclaredMadeForKids": false,
		},
	}

	uploadURL, err := s.initiateResumableSession(ctx, accessToken, metadata, canaryUploadSize, canaryUploadContentType)
	if err != nil {
		// initiateResumableSession returns plain wrapped errors today;
		// re-promote HARSH rejections (4xx-not-429/codes) to
		// ErrYouTubeCanaryRejected so the handler routes them. The
		// classifier is regex-based (see isHardRejection4xxStatus)
		// so 429 / Locked / decode / network / 5xx stay transient
		// and don't accidentally escalate to reauth.
		wrapped := fmt.Errorf("youtube canary: initiate session: %w", err)
		if isHardRejection4xxStatus(err) {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryRejected, err)
		}
		return nil, wrapped
	}

	contentRange := fmt.Sprintf("bytes 0-%d/%d", canaryUploadSize-1, canaryUploadSize)
	videoID, _, _, putErr := s.putChunk(ctx, uploadURL, canaryUploadBytes, contentRange, canaryUploadSize)
	if putErr != nil {
		// Same classifier as the initiate path — applies to
		// 200-with-bad-body decode errors, which carry NO (status N)
		// substring and fall through to the transient branch (NOT
		// escalated to ErrYouTubeCanaryRejected). 5xx, 429, 423,
		// and any 4xx-suppressed reauth list per isHardRejection4xxStatus.
		wrapped := fmt.Errorf("youtube canary: upload chunk put: %w", putErr)
		if isHardRejection4xxStatus(putErr) {
			wrapped = fmt.Errorf("%w: %w", ErrYouTubeCanaryRejected, putErr)
		}
		return nil, wrapped
	}
	if videoID == "" {
		return nil, fmt.Errorf("youtube canary: upload returned no video id (unexpected)")
	}

	video, fetchErr := s.fetchVideoStatus(ctx, accessToken, videoID)
	if fetchErr != nil {
		// videos.list on the just-uploaded video returning 4xx/5xx is
		// almost always transient (the video rows are indexed async)
		// — pass through plainly so the handler retries on next tick.
		return nil, fmt.Errorf("youtube canary: post-upload videos.list: %w", fetchErr)
	}
	if video.Snippet.ChannelID == "" {
		return nil, fmt.Errorf("youtube canary: snippet.channelId is empty for video %s (videos.list returned no channel binding)", videoID)
	}
	if video.Snippet.ChannelID != expectedChannelID {
		return nil, fmt.Errorf("%w: canary uploaded to channel %q, expected %q (video_id=%s)",
			ErrYouTubeChannelMismatch, video.Snippet.ChannelID, expectedChannelID, videoID)
	}

	slog.Info("youtube canary: uploaded private canary video and confirmed channel binding",
		"video_id", videoID, "channel_id", expectedChannelID, "title", title)

	return &CanaryUploadResult{
		VideoID:           videoID,
		UploadedChannelID: video.Snippet.ChannelID,
	}, nil
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
					"description":             ch.Snippet.Description,
					"handle":                  ch.Snippet.CustomURL,
					"avatar_url":              youtubeBestThumbnail(ch.Snippet.Thumbnails),
					"uploads_playlist_id":     ch.ContentDetails.RelatedPlaylists.Uploads,
					"country":                 ch.Snippet.Country,
					"subscriber_count":        ch.Statistics.SubscriberCount,
					"hidden_subscriber_count": ch.Statistics.HiddenSubscriberCount,
					"video_count":             ch.Statistics.VideoCount,
					"view_count":              ch.Statistics.ViewCount,
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
		"country":                 ch.Snippet.Country,
		"uploads_playlist_id":     ch.ContentDetails.RelatedPlaylists.Uploads,
		"hidden_subscriber_count": ch.Statistics.HiddenSubscriberCount,
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

// YouTube's Data API encodes statistics counters as JSON strings (for
// example, "viewCount": "123"), while fixtures and some compatible API
// implementations may emit JSON numbers. Accept both wire formats so a
// valid OAuth callback cannot fail while discovering the user's channel.
func decodeYouTubeCount(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		return strconv.ParseInt(value, 10, 64)
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
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
	_ OAuthProvider          = (*YouTubeOAuthService)(nil)
	_ ContentValidator       = (*YouTubeOAuthService)(nil)
	_ Publisher              = (*YouTubeOAuthService)(nil)
	_ AsyncPublisher         = (*YouTubeOAuthService)(nil)
	_ AccountDiscoverer      = (*YouTubeOAuthService)(nil)
	_ AccountDetailsProvider = (*YouTubeOAuthService)(nil)
	_ AccountContentProvider = (*YouTubeOAuthService)(nil)
)
