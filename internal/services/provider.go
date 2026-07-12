// Package services defines the small capability interfaces a social-platform
// provider can implement, plus the CapabilityRouter that holds providers by
// platform name and dispatches per-capability lookups.
//
// Taglio 2.1: five narrow interfaces so each provider implements only what
// its platform actually supports.
package services

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
// Every provider implements this so handlePublishPost can short-circuit
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
//	1. StartPublish       — initiate the publish, return publish_id, return immediately.
//	2. CheckPublishStatus — single status query, no polling. Returns the platform's
//	                        current state string (PROCESSING_UPLOAD / PENDING_PUBLISH /
//	                        IN_REVIEW / PUBLISH_COMPLETE / FAILED).
//	3. ContinuePublish    — for PULL_FROM_FILE chunked upload, no-op for PULL_FROM_URL.
//	4. Reconcile          — combines CheckPublishStatus + transition decision:
//	                          PUBLISH_COMPLETE → success result
//	                          FAILED          → error
//	                          in-flight       → (nil, nil) — try again next tick
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
type capabilities struct {
	raw      any
	oauth    OAuthProvider
	discover AccountDiscoverer
	validate ContentValidator
	publish  Publisher
	async    AsyncPublisher
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
