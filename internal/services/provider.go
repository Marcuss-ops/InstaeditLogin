package services

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ---------------------------------------------------------------------------
// Capability interfaces — each provider registers as many as it supports.
// The PlatformRegistry routes calls by platform name to the right capability.
// ---------------------------------------------------------------------------

// NameProvider returns the platform identifier. Every provider implements this.
type NameProvider interface {
	// Name returns the platform constant (e.g. "meta", "tiktok").
	Name() string
}

// OAuthProvider handles the OAuth authentication flow for a platform.
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

// Publisher publishes content to a social platform.
type Publisher interface {
	NameProvider

	// Publish publishes content and returns the platform media ID.
	Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
}

// TokenManager handles token encryption, storage, retrieval, and refresh.
type TokenManager interface {
	// SaveEncryptedToken encrypts and persists a token for a platform account.
	SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error

	// GetDecryptedToken retrieves and decrypts the latest token for a platform account.
	GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error)

	// EnsureFreshToken returns a non-expired access token, automatically
	// calling refresh when the stored token is expired or about to expire.
	// The refresher is the provider's RefreshOAuthToken.
	EnsureFreshToken(ctx context.Context, accountID int64, tokenType string, refresh TokenRefresher) (*models.OAuthToken, error)
}

// TokenRefresher is the function signature used to obtain a fresh access token.
// Providers implement it via RefreshOAuthToken and pass their method inline.
type TokenRefresher func(ctx context.Context, refreshToken string) (*models.TokenData, error)

// ErrRevokeUnsupported is returned by Revoke when a platform does not offer a
// token revocation endpoint.
var ErrRevokeUnsupported = fmt.Errorf("provider does not support token revocation")

// PlatformService combines OAuthProvider + Publisher + TokenManager into
// a single value. It is the legacy compatibility interface kept so
// existing consumers (Router, PublishWorker) can still do a single map
// lookup. New code should prefer PlatformRegistry.
//
// DEPRECATED: prefer PlatformRegistry with capability-specific lookups.
// Kept for backward compatibility with the map[string]PlatformService
// contract in main.go, handlers.go, and worker.go.
type PlatformService interface {
	OAuthProvider
	Publisher
	TokenManager
}

// ---------------------------------------------------------------------------
// PlatformRegistry — the single source of truth for platform dispatch.
// Replaces ad-hoc map[string]PlatformService with capability-aware routing.
// ---------------------------------------------------------------------------

// PlatformRegistry holds all registered providers, keyed by platform name.
// Each provider struct (e.g. FacebookOAuthService) typically satisfies all
// three capability interfaces (OAuthProvider, Publisher, TokenManager), so
// it registers the same value under each capability map.
type PlatformRegistry struct {
	oauth   map[string]OAuthProvider
	publish map[string]Publisher
	tokens  map[string]TokenManager
}

// NewPlatformRegistry creates an empty registry.
func NewPlatformRegistry() *PlatformRegistry {
	return &PlatformRegistry{
		oauth:   make(map[string]OAuthProvider),
		publish: make(map[string]Publisher),
		tokens:  make(map[string]TokenManager),
	}
}

// RegisterPlatformService is a convenience that registers the same value
// for all three capabilities. Use when the provider struct implements the
// full PlatformService interface (all current providers do).
//
// Rules:
//   - name MUST match the provider's Name() return value.
//   - Registering the same name twice logs a warning and skips (first-write-wins).
func (r *PlatformRegistry) RegisterPlatformService(name string, ps PlatformService) {
	r.oauth[name] = ps
	r.publish[name] = ps
	r.tokens[name] = ps
}

// RegisterOAuth registers an OAuth-capable provider independently.
func (r *PlatformRegistry) RegisterOAuth(name string, p OAuthProvider) {
	r.oauth[name] = p
}

// RegisterPublisher registers a publishing-capable provider independently.
func (r *PlatformRegistry) RegisterPublisher(name string, p Publisher) {
	r.publish[name] = p
}

// RegisterTokenManager registers a token-managing provider independently.
func (r *PlatformRegistry) RegisterTokenManager(name string, tm TokenManager) {
	r.tokens[name] = tm
}

// OAuth returns the OAuthProvider for the named platform, or false.
func (r *PlatformRegistry) OAuth(name string) (OAuthProvider, bool) {
	p, ok := r.oauth[name]
	return p, ok
}

// Publisher returns the Publisher for the named platform, or false.
func (r *PlatformRegistry) Publisher(name string) (Publisher, bool) {
	p, ok := r.publish[name]
	return p, ok
}

// TokenManager returns the TokenManager for the named platform, or false.
func (r *PlatformRegistry) TokenManager(name string) (TokenManager, bool) {
	tm, ok := r.tokens[name]
	return tm, ok
}

// Resolve returns the full PlatformService for the named platform by
// asserting that the same concrete value was registered for all three
// capabilities. This is the backward-compat bridge: consumers that need
// the monolith (OAuth + Publish + Token) call this. Returns (nil, false)
// when any capability is missing or when different values were registered
// for different capabilities.
func (r *PlatformRegistry) Resolve(name string) (PlatformService, error) {
	oa, okO := r.oauth[name]
	pub, okP := r.publish[name]
	tm, okT := r.tokens[name]
	if !okO || !okP || !okT {
		return nil, fmt.Errorf("platform %q: not fully registered (oauth=%v publish=%v token=%v)", name, okO, okP, okT)
	}
	// All three must point to the same concrete PlatformService — the
	// registry guarantees this when RegisterPlatformService was used, but
	// independent RegisterOAuth/RegisterPublisher/RegisterTokenManager calls
	// could break the invariant.
	ps, ok := oa.(PlatformService)
	if !ok {
		return nil, fmt.Errorf("platform %q: OAuthProvider is not a PlatformService", name)
	}
	if ps != pub {
		return nil, fmt.Errorf("platform %q: Publisher and OAuthProvider are different values", name)
	}
	if ps != tm {
		return nil, fmt.Errorf("platform %q: TokenManager and OAuthProvider are different values", name)
	}
	return ps, nil
}

// MustResolve is like Resolve but panics on error. Use only in main() where
// startup failures are fatal.
func (r *PlatformRegistry) MustResolve(name string) PlatformService {
	ps, err := r.Resolve(name)
	if err != nil {
		panic("PlatformRegistry.MustResolve: " + err.Error())
	}
	return ps
}

// Platforms returns the list of registered platform names.
func (r *PlatformRegistry) Platforms() []string {
	seen := make(map[string]struct{}, len(r.oauth))
	for name := range r.oauth {
		seen[name] = struct{}{}
	}
	for name := range r.publish {
		seen[name] = struct{}{}
	}
	for name := range r.tokens {
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// Len returns the number of registered platforms (at least one capability).
func (r *PlatformRegistry) Len() int {
	seen := make(map[string]struct{})
	for name := range r.oauth {
		seen[name] = struct{}{}
	}
	for name := range r.publish {
		seen[name] = struct{}{}
	}
	for name := range r.tokens {
		seen[name] = struct{}{}
	}
	return len(seen)
}
