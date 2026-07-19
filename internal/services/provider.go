// Package services defines the small capability interfaces a social-platform
// provider can implement, plus the CapabilityRouter that holds providers by
// platform name and dispatches per-capability lookups.
//
// # Capability interface split (ISP)
//
// Each capability lives in its own file alongside the package so a reader
// can land on the exact contract for what they're querying against:
//
//   - provider_name.go               NameProvider + Provider alias
//   - provider_oauth.go              OAuthProvider
//   - provider_content_validator.go  ContentValidator
//   - provider_publisher.go          Publisher
//   - provider_async_publisher.go    AsyncPublisher
//
// This file keeps the cross-cutting helpers that don't belong to any
// single capability: CapabilityRouter, ProviderDependencies, the rate
// limit error contract, ErrRevokeUnsupported, ParseRetryAfter.
//
// # Pruned dead code (Taglio 5c ISP review)
//
// The following were audited and dropped because they had BOTH zero
// implementers (no OAuth service declared a `var _ X = (*Y)(nil)`
// assertion for them) AND zero external consumers (no caller queried
// the corresponding router accessor):
//
//   - AccountDiscoverer interface + ResourceDiscoverer alias
//   - ResumablePublisher interface (dead today; reserved for chunked-
//     upload platforms like TikTok PULL_FROM_FILE that haven't shipped
//     a provider yet)
//   - DefaultResumableHeartbeat / DefaultPublishLeaseTTL /
//     DefaultMaxPublishAttempts constants (paired with ResumablePublisher,
//     zero usages anywhere)
//
// The router-side plumbing for these was also removed:
//
//   - capabilities.discover / capabilities.resumable fields
//   - CapabilitiesRouter.Discoverer / ResumablePublisher accessors
//   - Register()'s type-assertions for both interfaces
//
// ContentValidator was kept as an interface declaration
// (provider_content_validator.go) because all 7 providers implement
// it and self-call ValidateContent inside Publish. The router's
// Validator(name) accessor was pruned because nothing externally
// queries it today; re-add (5 LOC) when a consumer arrives.
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

// ---------------------------------------------------------------------------
// ProviderDependencies + helpers (variadic arg to NewXxxOAuthService)
// ---------------------------------------------------------------------------

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

// ErrRevokeUnsupported is returned by provider-specific Revoke helpers when
// a platform does not offer a token revocation endpoint.
//
// Real callers (audited in this commit): linkedin_oauth.go:140 and
// tiktok_oauth.go:181. Both implement Revoke as no-op + this sentinel.
// Kept as a package-level sentinel so the worker / handler can map it
// to "non-recoverable, log and move on" without inventing per-platform
// error types.
var ErrRevokeUnsupported = fmt.Errorf("provider does not support token revocation")

// ---------------------------------------------------------------------------
// Rate limit error contract (cross-cutting; not a per-capability interface)
// ---------------------------------------------------------------------------

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
// detects BOTH the legacy *RateLimitError (SPRINT 5.2 contract) AND
// the canonical *ProviderError with Code == rate_limited
// (SPRINT 5.1). Worker code can keep using IsRateLimitError unchanged
// through the 5.1 transition: providers gradually switch from
// returning *RateLimitError to returning *ProviderError, and the
// worker detects both transparently.
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
//     to a relative duration from now()
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
// of the capability interfaces in the per-file provider_*.go siblings;
// the router discovers them by type assertion on Register.
type CapabilityRouter struct {
	providers map[string]*capabilities
}

// capabilities is the per-platform bucket of capability pointers. Each
// field is nil when the registered provider does not satisfy the
// corresponding interface; callers use the (X, ok) pattern to handle
// absence.
//
// Taglio 4.3: added `raw any` to remember the original provider
// instance so Get(name) can recover the concrete value (used by
// platform-specific helpers that aren't on any of the named
// capability interfaces — e.g. per-platform Validate / Revoke
// methods that the router doesn't track as capabilities).
//
// ISP-cull (this commit): the fields tracking AccountDiscoverer and
// ResumablePublisher were removed because the corresponding
// interfaces had no implementer AND no external consumer. The
// ContentValidator field was also removed for the same reason;
// the interface declaration lives in
// provider_content_validator.go so each provider's `var _
// ContentValidator = (*)X)(nil)` still compiles, but the router
// no longer recognises the capability on Register. Re-add the
// field + accessor when an external consumer appears.
//
// Taglio 5d: AccountDiscoverer was re-added because Facebook Pages
// need per-page account discovery at OAuth-connect time. The
// interface is consumed by pkg/api/handlers.go handleCallback to
// create one PlatformAccount per discovered page.
type capabilities struct {
	raw        any
	oauth      OAuthProvider
	publish    Publisher
	async      AsyncPublisher
	discover   AccountDiscoverer
	details    AccountDetailsProvider
	content    AccountContentProvider
	tokenPolicy TokenPolicyProvider
}

// NewCapabilityRouter creates an empty router.
func NewCapabilityRouter() *CapabilityRouter {
	return &CapabilityRouter{
		providers: make(map[string]*capabilities),
	}
}

// DiscoveredAccount is the canonical return type from AccountDiscoverer.
// It generalizes the Facebook-Pages-specific contract so every provider
// (YouTube channels, Instagram business accounts, TikTok accounts, etc.)
// can return 0..N accounts from a single OAuth grant with a uniform shape.
//
//   - Profile carries the identity fields needed to create or re-link a
//     platform_accounts row (PlatformUserID + Username).
//   - Metadata carries platform-specific stable identity data (handle,
//     avatar_url, uploads_playlist_id, country, etc.) that is persisted
//     on the platform_accounts row in the JSONB metadata column.
//   - SupplementalTokens carries additional tokens the provider needs
//     persisted beyond the root OAuth token. Facebook Pages use this for
//     the per-Page Page Access Token. YouTube channels carry none (the
//     root bearer token is shared). Providers that don't need supplemental
//     tokens leave the slice nil or empty — the OAuth callback handler
//     skips them with zero overhead.
type DiscoveredAccount struct {
	Profile            models.PlatformProfile
	Metadata           models.Metadata
	SupplementalTokens []*models.TokenData
}

// AccountDiscoverer is implemented by providers that can enumerate
// multiple platform accounts for a single OAuth grant. Facebook Pages
// is the canonical example: one user grant yields N Pages, each of
// which becomes a distinct PlatformAccount with its own access token.
// The OAuth callback handler uses this capability to create those
// accounts and persist their tokens.
type AccountDiscoverer interface {
	NameProvider
	// DiscoverAccounts returns the platform accounts the user manages
	// given a valid access token and the user's platform-scoped id.
	DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*DiscoveredAccount, error)
}

// Register stores p under name, type-asserting each capability it
// satisfies. Providers that don't implement a capability simply have
// a nil for that slot — callers must check with the (X, ok) pattern.
//
// Re-registering the same name overwrites the previous entry. Callers
// that want to refuse duplicates can check Names() first.
func (r *CapabilityRouter) Register(name string, p any) {
	entry := &capabilities{raw: p}
	if o, ok := p.(OAuthProvider); ok {
		entry.oauth = o
	}
	if pub, ok := p.(Publisher); ok {
		entry.publish = pub
	}
	if ap, ok := p.(AsyncPublisher); ok {
		entry.async = ap
	}
	if d, ok := p.(AccountDiscoverer); ok {
		entry.discover = d
	}
	if dp, ok := p.(AccountDetailsProvider); ok {
		entry.details = dp
	}
	if cp, ok := p.(AccountContentProvider); ok {
		entry.content = cp
	}
	if tp, ok := p.(TokenPolicyProvider); ok {
		entry.tokenPolicy = tp
	}
	r.providers[name] = entry
}

// Discoverer returns the AccountDiscoverer for name, or false. Used by
// pkg/api/handlers.go handleCallback to expand one OAuth grant into
// multiple PlatformAccounts (e.g. Facebook Pages).
func (r *CapabilityRouter) Discoverer(name string) (AccountDiscoverer, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.discover == nil {
		return nil, false
	}
	return e.discover, true
}

// Get returns the raw provider registered under name (e.g. a concrete
// *FacebookOAuthService), or false if not registered. Use this for
// platform-specific helpers (account lifecycle Validate/Revoke,
// Meta-fb_exchange_token, TikTok PULL_FROM_FILE chunked upload, etc.)
// that aren't on any of the capability interfaces. For dispatch
// logic, prefer the typed accessors: OAuth(name), Publisher(name),
// AsyncPublisher(name).
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

// OAuth returns the OAuthProvider for name, or false. Used by:
//   - pkg/api/handlers.go (handleLogin / handleCallback pre-OAuth)
//   - internal/worker/publish_worker.go (token refresh before Publish)
//   - internal/worker/reconcile_worker.go (token refresh before Reconcile)
//   - internal/credentials/vault.go (refresher adapter construction)
func (r *CapabilityRouter) OAuth(name string) (OAuthProvider, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.oauth == nil {
		return nil, false
	}
	return e.oauth, true
}

// Publisher returns the Publisher for name, or false. Used by
// internal/worker/publish_worker.go for the sync Publish() call
// (every tick where a target becomes ready and the platform has
// no AsyncPublisher implementation).
func (r *CapabilityRouter) Publisher(name string) (Publisher, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.publish == nil {
		return nil, false
	}
	return e.publish, true
}

// AsyncPublisher returns the AsyncPublisher for name, or false. The
// reconciler goroutine in the worker uses this to call Reconcile on
// targets whose platform implements the async state machine; the
// publish tick also uses it to short-circuit when Publish() already
// returned a publish_id and the async path should take over.
func (r *CapabilityRouter) AsyncPublisher(name string) (AsyncPublisher, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.async == nil {
		return nil, false
	}
	return e.async, true
}

// TokenPolicy returns the TokenPolicyProvider for name, or false. Used by
// POST /api/v1/accounts/{id}/validate to determine which token types to
// check for the platform.
func (r *CapabilityRouter) TokenPolicy(name string) (TokenPolicyProvider, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.tokenPolicy == nil {
		return nil, false
	}
	return e.tokenPolicy, true
}

// AccountDetails returns the AccountDetailsProvider for name, or false.
// Used by GET /api/v1/accounts/{id} to fetch rich account details.
func (r *CapabilityRouter) AccountDetails(name string) (AccountDetailsProvider, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.details == nil {
		return nil, false
	}
	return e.details, true
}

// AccountContent returns the AccountContentProvider for name, or false.
// Used by GET /api/v1/accounts/{id}/content to list content items.
func (r *CapabilityRouter) AccountContent(name string) (AccountContentProvider, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.content == nil {
		return nil, false
	}
	return e.content, true
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
