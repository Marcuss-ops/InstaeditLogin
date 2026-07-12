// Package services defines the small capability interfaces a social-platform
// provider can implement, plus the CapabilityRouter that holds providers by
// platform name and dispatches per-capability lookups.
//
// Taglio 2.1: five narrow interfaces so each provider implements only what
// its platform actually supports.
package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ProviderDependencies holds injectable dependencies for provider constructors.
// Passed as variadic arg to NewXxxOAuthService(cfg, deps...); when empty,
// sensible production defaults are used (NewHTTPClient, time.Now).
//
// Taglio 5c testability: tests inject httptest.Server-backed HTTP clients
// and deterministic clocks via this struct.
type ProviderDependencies struct {
	// HTTPClient overrides the default NewHTTPClient() used for outbound
	// OAuth and publishing API calls. Tests inject an *http.Client whose
	// Transport rewrites URLs to an httptest.Server.
	HTTPClient *http.Client

	// Clock replaces time.Now for deterministic timestamp generation.
	// Tests inject a fixed clock; production defaults to time.Now.
	Clock func() time.Time
}

// resolveHTTPClient returns deps.HTTPClient if set, or a new default.
func (d ProviderDependencies) resolveHTTPClient() *http.Client {
	if d.HTTPClient != nil {
		return d.HTTPClient
	}
	return NewHTTPClient()
}

// resolveClock returns deps.Clock if set, or time.Now as default.
func (d ProviderDependencies) resolveClock() func() time.Time {
	if d.Clock != nil {
		return d.Clock
	}
	return time.Now
}

// ---------------------------------------------------------------------------
// Capability interfaces — each provider registers as many as it supports.
// The CapabilityRouter routes calls by platform name to the right capability.
// ---------------------------------------------------------------------------

// NameProvider returns the platform identifier. Every provider implements this.
type NameProvider interface {
	// Name returns the platform constant (e.g. "instagram", "tiktok", "youtube").
	Name() string
}

// Provider is a type alias for NameProvider. The canonical short name
// per the Zernio-like Platform Registry contract: every registered
// capability row is keyed by its Provider.Name() string.
//
// Taglio 4.3: NameProvider kept as the existing symbol so legacy call
// sites compile unchanged; Provider is the preferred name for new
// code. They are interchangeable at compile time.
type Provider = NameProvider

// OAuthProvider handles the OAuth login flow: build login URL, exchange the
// authorization code for a token, fetch the user profile, refresh the token
// when it expires. Every provider that supports user login implements this.
type OAuthProvider interface {
	NameProvider

	// GetLoginURL builds the OAuth authorization URL for user redirection.
	GetLoginURL(state string) string

	// HandleCallback processes the full OAuth callback flow:
	// 1. Exchange code for token
	// 2. Fetch user profile
	// Returns the profile and token data.
	HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)

	// RefreshOAuthToken obtains a fresh access token from the platform.
	// For YouTube/Twitter/TikTok the argument is a refresh token; for Meta,
	// it is the current long-lived access token (re-exchange via fb_exchange_token).
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
}

// AccountDiscoverer discovers sub-accounts on the platform (e.g. Meta: the
// Facebook Pages a user manages, or the Instagram Business Account linked to
// a Facebook user). NOT implemented by all providers — only Meta-family.
type AccountDiscoverer interface {
	// DiscoverAccounts returns the list of platform accounts the user has
	// access to publish to. Returns an empty slice (no error) if the user
	// has no sub-accounts on this platform.
	DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*models.PlatformAccount, error)
}

// ResourceDiscoverer is a type alias for AccountDiscoverer. The
// canonical Zernio-like name for "provider that can list sub-resources
// connected to the same access token" (Facebook Pages, LinkedIn
// Organizations, Instagram Business Accounts, etc.).
//
// Taglio 4.3: AccountDiscoverer kept for backward compatibility;
// ResourceDiscoverer is the preferred name for new code per the
// Platform Registry spec. They are interchangeable at compile time.
type ResourceDiscoverer = AccountDiscoverer

// ContentValidator validates that a publish payload is acceptable for the
// platform (e.g. YouTube requires a video, LinkedIn requires text).
// Every provider implements this so the worker can short-circuit
// before the per-platform Publish call.
type ContentValidator interface {
	// ValidateContent returns nil if the payload can be published, or a
	// descriptive error if a field is missing or out of range.
	ValidateContent(payload models.PublishPayload) error
}

// Publisher publishes content to the platform. Every provider that
// supports publishing implements this.
type Publisher interface {
	NameProvider

	// Publish publishes content and returns the platform media ID.
	Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
}

// AsyncPublisher models the four-step state machine for platforms whose
// publish is asynchronous (TikTok today; the interface is here so other
// async platforms can opt in without changing the worker).
//
// The flow is:
//
//  1. StartPublish       — initiate the publish, return publish_id, return immediately.
//  2. CheckPublishStatus — single status query, no polling. Returns the platform's
//     current state string (PROCESSING_UPLOAD / PENDING_PUBLISH /
//     IN_REVIEW / PUBLISH_COMPLETE / FAILED).
//  3. ContinuePublish    — for PULL_FROM_FILE chunked upload, no-op for PULL_FROM_URL.
//  4. Reconcile          — combines CheckPublishStatus + transition decision:
//     PUBLISH_COMPLETE → success result
//     FAILED          → error
//     in-flight       → (nil, nil) — try again next tick
//
// Taglio 4.2: replaces the old synchronous polling loop inside the worker's
// tick with a separate reconciler goroutine. Publish() returns immediately
// with the publish_id; the reconciler calls Reconcile on every tick to
// advance the async state machine.
type AsyncPublisher interface {
	NameProvider
	// StartPublish initiates the async publish and returns the platform's
	// publish_id (stored on post_target.platform_post_id) plus the
	// platform's current state (stored on post_target.provider_state).
	// Returns immediately — no polling, no waiting for the publish to
	// complete. The reconciler will check status later.
	StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error)
	// CheckPublishStatus does a SINGLE GET to the platform's status
	// endpoint. Returns the current state string. Does NOT poll.
	// Errors on network failure, 4xx/5xx, or unexpected response shape.
	CheckPublishStatus(ctx context.Context, accessToken, publishID string) (state string, err error)
	// ContinuePublish is a placeholder for PULL_FROM_FILE chunked upload
	// flows. For PULL_FROM_URL (TikTok's current default) it's a no-op
	// that returns nil — the platform fetches the video directly from
	// the URL. Provided for forward-compat with platforms that need
	// explicit chunked upload.
	ContinuePublish(ctx context.Context, accessToken, publishID string) error
	// Reconcile queries the platform and decides the transition:
	//   PUBLISH_COMPLETE → returns *PublishResult (success, terminal)
	//   FAILED          → returns error (terminal)
	//   in-flight       → returns (nil, nil) — caller should retry later
	// The reconciler goroutine in the worker calls this on every tick.
	Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error)
}

// ErrRevokeUnsupported is returned by provider-specific Revoke helpers when
// a platform does not offer a token revocation endpoint. Kept here so
// provider methods that aren't on a capability interface (Validate /
// Revoke) can still signal "not supported" without inventing a per-provider
// error type.
var ErrRevokeUnsupported = fmt.Errorf("provider does not support token revocation")

// -----------------------------------------------------------------------
// SPRINT 5.2 (P1#10) — worker hardening for long uploads.
// -----------------------------------------------------------------------

// RateLimitError is the typed error a provider returns when the platform
// responds with 429 Too Many Requests (or equivalent). The PublishWorker
// uses errors.As to detect it; on detection the worker stamps
// next_retry_at + rate_limit_reset_at to NOW() + RetryAfter and clears
// the lease, but does NOT increment attempt_count. Rate-limiting is
// not a fault — the platform told us when to come back, so retrying
// sooner is the right behavior.
//
// RetryAfter is parsed from the platform's Retry-After header (RFC
// 7231) OR a X-RateLimit-Reset timestamp (epoch seconds) — see
// ParseRetryAfter. A zero RetryAfter is a programming error in the
// provider; the worker falls back to the decorrelated-jitter backoff
// in that case.
//
// Providers wrap their rate-limit detection like:
//
//	return nil, &services.RateLimitError{RetryAfter: 90 * time.Second}
//
// and the worker's `var rle *services.RateLimitError; if errors.As(err, &rle)`
// branch handles it.
type RateLimitError struct {
	RetryAfter time.Duration
}

// Error implements the error interface. Includes the RetryAfter so
// the worker's log line on rate-limit-hit is self-describing.
func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limited: retry after %s", e.RetryAfter)
}

// IsRateLimitError is a convenience wrapper for `errors.As` that
// detects BOTH the legacy *RateLimitError (SPRINT 5.2 contract)
// AND the canonical *ProviderError with Code == rate_limited
// (SPRINT 5.1). Worker code can keep using IsRateLimitError
// unchanged through the 5.1 transition: providers gradually switch
// from returning *RateLimitError to returning *ProviderError, and
// the worker detects both transparently.
func IsRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	var rle *RateLimitError
	if errors.As(err, &rle) {
		return true
	}
	var pe *ProviderError
	if errors.As(err, &pe) && pe.Code == ErrorCodeRateLimited {
		return true
	}
	return false
}

// ResumablePublisher is the optional capability for platforms whose
// publish is a long-running chunked upload (TikTok's PULL_FROM_FILE
// flow today; future chunked-upload platforms opt in by implementing
// the additional methods). The PublishWorker probes for this
// capability via the CapabilityRouter and, when present, calls
// Heartbeat() on every heartbeat tick and Resume() on a row whose
// upload_offset > 0 (the crashed-mid-upload case).
//
// Sync publishers (Instagram, Facebook, etc.) do NOT implement this
// interface — their Publish() returns the platform's media id in
// one shot and there's no upload progress to heartbeat or resume.
// The CapabilityRouter's ResumablePublisher(name) returns (nil, false)
// for those platforms and the worker falls through to the regular
// Publish() / StartPublish() path.
type ResumablePublisher interface {
	AsyncPublisher
	// Resume picks up a chunked upload that was interrupted (the
	// previous worker crashed mid-upload, the lease expired, and
	// the reconciler reclaimed the row). The worker passes the
	// persisted provider_state (e.g. the platform's upload_url +
	// upload_session_token) and the persisted offset (bytes
	// uploaded so far). The platform's resume API continues from
	// offsetBytes and returns the new state + offset for the next
	// heartbeat.
	//
	// Returns:
	//   - newPublishID: the platform's id (may equal the original
	//     publishID, or a fresh one if the platform rotated it on
	//     resume).
	//   - newState: the platform's current state string.
	//   - newOffset: the platform's current upload offset in bytes.
	//   - err: a transient / terminal error. RateLimitError is
	//     honored by the worker (Retry-After retry path).
	Resume(ctx context.Context, accessToken, publishID, providerState string, offsetBytes int64) (newPublishID, newState string, newOffset int64, err error)
	// Heartbeat probes the platform for current upload progress
	// while a publish is in flight. The worker calls this every
	// heartbeat tick (default 30s) to:
	//   1. Update post_targets.upload_offset + provider_state on
	//      the row so a crash-and-recover can resume from the
	//      latest known offset.
	//   2. Detect early failures (platform returns FAILED) so the
	//      worker can short-circuit the heartbeat loop and mark
	//      the row terminal without waiting for the publish call
	//      to return.
	//
	// Returns:
	//   - newState: the platform's current state string.
	//   - offsetBytes: the platform's current upload offset in
	//     bytes (0 if not applicable / not yet known).
	//   - err: a transient error (network blip, 5xx) — the
	//     heartbeat goroutine logs and continues; the next tick
	//     retries. A terminal error is returned wrapped with
	//     ErrTerminal sentinel; the worker stops the heartbeat
	//     and marks the row terminal.
	Heartbeat(ctx context.Context, accessToken, publishID, currentState string) (newState string, offsetBytes int64, err error)
}

// DefaultResumableHeartbeat is the default interval between Heartbeat
// calls in the worker's heartbeat goroutine. The user spec said "~30s"
// which is the canonical upload-heartbeat cadence for video platforms
// (TikTok, YouTube resumable uploads, Vimeo, etc.). Operators can
// override via env in the future (PublishWorkerHeartbeatIntervalSeconds).
const DefaultResumableHeartbeat = 30 * time.Second

// DefaultPublishLeaseTTL is the default lease TTL for in-flight
// publishes. Set to 3x the heartbeat interval (90s) so a single
// missed heartbeat still leaves 60s of grace before the lease expires
// and the reconciler can take over. Mirrors the outbox dispatcher's
// ratio (LeaseTTL = 3 * HeartbeatInterval).
const DefaultPublishLeaseTTL = 90 * time.Second

// DefaultMaxPublishAttempts is the cap for retrying transient publish
// failures. After 5 failed attempts (the outbox dispatcher's default),
// the row is sent to DLQ. Operators can override via env in the
// future (PublishWorkerMaxAttempts).
const DefaultMaxPublishAttempts = 5

// ParseRetryAfter parses the value of an HTTP Retry-After response
// header (RFC 7231 §7.1.3) OR an X-RateLimit-Reset unix-epoch-seconds
// value (the de-facto convention used by Twitter, GitHub, Stripe, etc.)
// into a relative time.Duration. Returns 0 on parse failure — the
// worker falls back to the decorrelated-jitter backoff in that case.
//
// Supported inputs:
//   - "" or whitespace-only  → 0 (caller should use the default backoff)
//   - "120"                  → 120s (delta-seconds per RFC 7231)
//   - "120s"                 → 120s
//   - "2m" / "2m30s"         → parsed via time.ParseDuration
//   - "Mon, 02 Jan 2026 ..." → parsed via time.Parse(RFC1123), converted
//                              to a relative duration from now()
//
// X-RateLimit-Reset is auto-detected: an integer that fits in 11
// digits (year-2286 in unix seconds) is treated as an absolute
// epoch, otherwise as a delta-seconds.
//
// This is a best-effort helper — the platform is the authoritative
// source for "come back in N seconds". A parse failure here is
// logged + retried with the default backoff; the worker does NOT
// treat parse failure as a fatal error.
func ParseRetryAfter(raw string, now time.Time) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	// Absolute HTTP-date (RFC 7231 form 1).
	if t, err := time.Parse(time.RFC1123, raw); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0 // already expired — retry now
		}
		return d
	}
	// Go duration string ("120s", "2m30s").
	if d, err := time.ParseDuration(raw); err == nil {
		if d < 0 {
			return 0
		}
		return d
	}
	// Integer — either delta-seconds (small) or unix epoch (large).
	// Heuristic: <= 1e7 is a delta (the max delta-seconds is ~116 days
	// per RFC 7231; in practice platforms use values up to a few hours).
	// Larger values are epoch seconds.
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n <= 0 {
			return 0
		}
		if n <= 1e7 {
			return time.Duration(n) * time.Second
		}
		// Epoch seconds.
		resetAt := time.Unix(n, 0)
		d := resetAt.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	// Unparseable — caller uses the default backoff.
	return 0
}

// ---------------------------------------------------------------------------
// CapabilityRouter — the single source of truth for platform dispatch.
// ---------------------------------------------------------------------------

// CapabilityRouter holds all registered providers, keyed by platform name.
// Each provider struct (e.g. FacebookOAuthService) typically satisfies 3-4
// of the five capability interfaces; the router discovers them by type
// assertion on Register.
type CapabilityRouter struct {
	providers map[string]*capabilities
}

// capabilities is the per-platform bucket. Each field is nil if the
// registered provider does not satisfy that interface — callers must
// use the (X, ok) pattern to handle the absence.
//
// Taglio 4.3: added `raw any` to remember the original provider
// instance so Get(name) can recover the concrete value (used by
// platform-specific helpers like Validate / Revoke that are NOT on
// any of the named capability interfaces).
//
// SPRINT 5.2: added `resumable ResumablePublisher` for chunked-upload
// platforms (TikTok PULL_FROM_FILE today; future opt-ins). The
// presence of this field on a registered platform means the worker
// will heartbeat and resume rather than treat the publish as
// one-shot. Use CapabilityRouter.ResumablePublisher(name) to
// probe (returns (nil, false) for sync platforms).
type capabilities struct {
	raw       any
	oauth     OAuthProvider
	discover  AccountDiscoverer
	validate  ContentValidator
	publish   Publisher
	async     AsyncPublisher
	resumable ResumablePublisher
}

// NewCapabilityRouter creates an empty router.
func NewCapabilityRouter() *CapabilityRouter {
	return &CapabilityRouter{
		providers: make(map[string]*capabilities),
	}
}

// Register stores p under name, type-asserting each capability it satisfies.
// Providers that don't implement a capability simply have a nil for that
// slot — callers must check with the (X, ok) pattern.
//
// Re-registering the same name overwrites the previous entry. Callers
// that want to refuse duplicates can check Names() first.
//
// SPRINT 5.2: also probes for ResumablePublisher (chunked-upload
// capability). Providers that don't implement it have a nil; the
// ResumablePublisher(name) accessor returns (nil, false) and the
// worker falls through to the regular publish path.
func (r *CapabilityRouter) Register(name string, p any) {
	entry := &capabilities{raw: p}
	if o, ok := p.(OAuthProvider); ok {
		entry.oauth = o
	}
	if d, ok := p.(AccountDiscoverer); ok {
		entry.discover = d
	}
	if v, ok := p.(ContentValidator); ok {
		entry.validate = v
	}
	if pub, ok := p.(Publisher); ok {
		entry.publish = pub
	}
	if ap, ok := p.(AsyncPublisher); ok {
		entry.async = ap
	}
	if rp, ok := p.(ResumablePublisher); ok {
		entry.resumable = rp
	}
	r.providers[name] = entry
}

// Get returns the raw provider registered under name (e.g. a concrete
// *FacebookOAuthService), or false if not registered. Use this for
// platform-specific helpers (account lifecycle Validate/Revoke,
// Meta-fb_exchange_token, TikTok PULL_FROM_FILE chunked upload, etc.)
// that aren't on any of the capability interfaces. For dispatch
// logic, prefer the typed accessors: OAuth(name), Publisher(name),
// AsyncPublisher(name), ResourceDiscoverer(name), Validator(name).
//
// Taglio 4.3: the raw instance is stashed at Register time so callers
// don't need to do their own type assertion in the hot path.
func (r *CapabilityRouter) Get(name string) (any, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil {
		return nil, false
	}
	return e.raw, true
}

// OAuth returns the OAuthProvider for name, or false.
func (r *CapabilityRouter) OAuth(name string) (OAuthProvider, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.oauth == nil {
		return nil, false
	}
	return e.oauth, true
}

// Discoverer returns the AccountDiscoverer for name, or false.
func (r *CapabilityRouter) Discoverer(name string) (AccountDiscoverer, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.discover == nil {
		return nil, false
	}
	return e.discover, true
}

// Validator returns the ContentValidator for name, or false.
func (r *CapabilityRouter) Validator(name string) (ContentValidator, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.validate == nil {
		return nil, false
	}
	return e.validate, true
}

// Publisher returns the Publisher for name, or false.
func (r *CapabilityRouter) Publisher(name string) (Publisher, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.publish == nil {
		return nil, false
	}
	return e.publish, true
}

// AsyncPublisher returns the AsyncPublisher for name, or false. The
// reconciler goroutine in the worker uses this to call Reconcile on
// targets whose platform implements the async state machine.
func (r *CapabilityRouter) AsyncPublisher(name string) (AsyncPublisher, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.async == nil {
		return nil, false
	}
	return e.async, true
}

// ResumablePublisher (SPRINT 5.2) returns the ResumablePublisher
// for name, or false. The PublishWorker's heartbeat goroutine uses
// this to probe a platform for upload progress on every tick;
// the claim path uses it to call Resume() on a row whose
// upload_offset > 0 (crashed-mid-upload case).
//
// Returns (nil, false) for sync platforms (the common case) — the
// worker falls through to the regular Publish() / StartPublish()
// path and never spawns a heartbeat goroutine.
func (r *CapabilityRouter) ResumablePublisher(name string) (ResumablePublisher, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.resumable == nil {
		return nil, false
	}
	return e.resumable, true
}

// Names returns the list of registered platform names. The order is
// non-deterministic (Go map iteration).
func (r *CapabilityRouter) Names() []string {
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	return out
}

// Len returns the number of registered providers.
func (r *CapabilityRouter) Len() int {
	return len(r.providers)
}
