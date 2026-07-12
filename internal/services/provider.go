// Package services defines the small capability interfaces a social-platform
// provider can implement, plus the CapabilityRouter that holds providers by
// platform name and dispatches per-capability lookups.
//
// Taglio 2.1 replaces the old composite PlatformService (OAuthProvider +
// Publisher + TokenManager) with five narrow interfaces so each provider
// implements only what its platform actually supports. The token-encryption
// logic was lifted out of the per-provider concern entirely and now lives
// in a shared TokenService (internal/services/token_service.go) — every
// provider shares the same encrypted token schema, so the logic was
// infrastructure, not capability.
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
	// Name returns the platform constant (e.g. "meta", "tiktok", "youtube").
	Name() string
}

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

// ContentValidator validates that a publish payload is acceptable for the
// platform (e.g. YouTube requires video_url, LinkedIn requires text).
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

// PublishReconciler reconciles a publish after the fact (e.g. polls for
// status when the initial response is async). NOT implemented by all
// providers — only TikTok today, but the interface is here so other
// async platforms can opt in without changing the worker.
type PublishReconciler interface {
	// ReconcilePublish polls the platform for the final state of a previously
	// initiated publish. publishID is the value returned from the initial
	// Publish call (or stored on the post_target).
	ReconcilePublish(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error)
}

// ErrRevokeUnsupported is returned by provider-specific Revoke helpers when
// a platform does not offer a token revocation endpoint. Kept here so
// provider methods that aren't on a capability interface (Validate /
// Revoke) can still signal "not supported" without inventing a per-provider
// error type.
var ErrRevokeUnsupported = fmt.Errorf("provider does not support token revocation")

// ---------------------------------------------------------------------------
// CapabilityRouter — the single source of truth for platform dispatch.
// Replaces the old map[string]PlatformService with capability-aware routing.
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
type capabilities struct {
	oauth    OAuthProvider
	discover AccountDiscoverer
	validate ContentValidator
	publish  Publisher
	recon    PublishReconciler
}

// NewCapabilityRouter creates an empty router.
func NewCapabilityRouter() *CapabilityRouter {
	return &CapabilityRouter{
		providers: make(map[string]*capabilities),
	}
}

// Register stores p under name, type-asserting each capability it satisfies.
// Providers that don't implement a capability simply have a nil for that
// slot — callers must check with the (X, ok) pattern. This avoids the
// PlatformService-of-everything problem where every provider is forced to
// satisfy OAuth + Publish + Token just to be a member of the registry.
//
// Re-registering the same name overwrites the previous entry. Callers
// that want to refuse duplicates can check Names() first.
func (r *CapabilityRouter) Register(name string, p any) {
	entry := &capabilities{}
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
	if rec, ok := p.(PublishReconciler); ok {
		entry.recon = rec
	}
	r.providers[name] = entry
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

// Reconciler returns the PublishReconciler for name, or false.
func (r *CapabilityRouter) Reconciler(name string) (PublishReconciler, bool) {
	e, ok := r.providers[name]
	if !ok || e == nil || e.recon == nil {
		return nil, false
	}
	return e.recon, true
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
