package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// YouTubeOAuthClientConfig is one Google OAuth client in the YouTube
// OAuth Client Pool. Google caps the number of refresh tokens issued
// for one (Google account, OAuth client) pair at 100, so a fleet of
// 100+ channels under a single Google manager spreads its tokens
// across two clients (youtube_pool_a / youtube_pool_b).
//
// ClientSecret is a credential. It lives in process memory only and
// must NEVER enter the database, the logs, or an error string. The
// registry enforces this at the API boundary: Resolve/Select errors
// and Redacted() only ever expose Key, ClientID and RedirectURI.
type YouTubeOAuthClientConfig struct {
	// Key is the stable pool identifier persisted on the OAuth
	// connection (e.g. "youtube_pool_a").
	Key string
	// ClientID is the Google OAuth client_id.
	ClientID string
	// ClientSecret is the Google OAuth client_secret (never logged).
	ClientSecret string
	// RedirectURI is the OAuth redirect_uri registered on the console.
	RedirectURI string
	// RecommendedCapacity is the soft ceiling of active refresh
	// grants for this client. Defaults to 50 (half of Google's 100
	// per-account+client cap) so the pool never rides the limit.
	RecommendedCapacity int
}

// Redacted returns a log-safe summary of the client config. It is the
// only stringer-style projection the registry exposes and deliberately
// omits ClientSecret: callers may log it freely.
func (c *YouTubeOAuthClientConfig) Redacted() string {
	if c == nil {
		return "youtube_oauth_client_pool(nil)"
	}
	return fmt.Sprintf("%s(client_id=%q, redirect_uri=%q)", c.Key, c.ClientID, c.RedirectURI)
}

// ErrYouTubeOAuthClientPoolEmpty is returned when the registry has no
// configured clients (Resolve/SelectForNewConnection on an empty pool).
var ErrYouTubeOAuthClientPoolEmpty = errors.New("youtube oauth client pool: no clients configured")

// ErrYouTubeOAuthClientUnknown is returned by Resolve for a key that is
// not part of the registry. The error never contains credential
// material — only the offending key.
var ErrYouTubeOAuthClientUnknown = errors.New("youtube oauth client pool: unknown client key")

// OAuthClientUsageCounter is the narrow storage capability the
// registry needs to select the least-loaded pool for a Google subject.
// The production repository implements it once the oauth_client_key
// column exists (migration 099); until that wiring lands,
// SelectForNewConnection falls back to a deterministic first-client
// selection and tests inject a fake counter.
type OAuthClientUsageCounter interface {
	// CountActiveRefreshTokens returns the number of active refresh
	// grants for one Google subject on one pool client.
	CountActiveRefreshTokens(ctx context.Context, providerSubjectID, oauthClientKey string) (int64, error)
}

// defaultYouTubePoolCapacity is the RecommendedCapacity applied when a
// client config does not set one. Half of Google's 100 refresh-token
// cap per (account, client) pair leaves room for reconnects,
// migrations and recovery.
const defaultYouTubePoolCapacity = 50

// YouTubeOAuthClientRegistry resolves and selects Google OAuth clients
// in the YouTube OAuth Client Pool.
//
//	selection order for a new connection:
//	  usageCounter set → least-loaded client (max remaining capacity,
//	                     tie-break: lower usage, then registration order)
//	  usageCounter nil  → first registered client (deterministic; the
//	                     pre-wiring fallback)
//
// The registry never logs or returns client secrets. All error strings
// are constructed from Key/ClientID/RedirectURI only.
type YouTubeOAuthClientRegistry struct {
	clients      []YouTubeOAuthClientConfig // registration order = deterministic tie-break
	byKey        map[string]int
	usageCounter OAuthClientUsageCounter
}

// YouTubeOAuthClientRegistryOption configures a
// YouTubeOAuthClientRegistry at construction time.
type YouTubeOAuthClientRegistryOption func(*YouTubeOAuthClientRegistry)

// WithYouTubeOAuthClientUsageCounter wires the storage counter used by
// SelectForNewConnection to pick the least-loaded pool.
func WithYouTubeOAuthClientUsageCounter(counter OAuthClientUsageCounter) YouTubeOAuthClientRegistryOption {
	return func(r *YouTubeOAuthClientRegistry) { r.usageCounter = counter }
}

// NewYouTubeOAuthClientRegistry builds a pool registry from the given
// client configs. Entries with all credential fields empty are skipped
// (caller convenience); an entry with SOME fields set is rejected as
// half-configured — a client registered without a secret or redirect
// URI would only surface as invalid_client at refresh time, so the
// registry fails at construction instead. RecommendedCapacity defaults
// to 50 when unset. Returns ErrYouTubeOAuthClientPoolEmpty when no
// usable client remains. Error messages never contain credential
// material.
func NewYouTubeOAuthClientRegistry(clients []YouTubeOAuthClientConfig, opts ...YouTubeOAuthClientRegistryOption) (*YouTubeOAuthClientRegistry, error) {
	r := &YouTubeOAuthClientRegistry{byKey: map[string]int{}}
	for _, opt := range opts {
		opt(r)
	}
	seen := map[string]bool{}
	for _, c := range clients {
		if c.ClientID == "" && c.ClientSecret == "" && c.RedirectURI == "" {
			continue
		}
		if c.Key == "" {
			return nil, fmt.Errorf("youtube oauth client pool: client with empty key cannot be registered")
		}
		if seen[c.Key] {
			return nil, fmt.Errorf("youtube oauth client pool: duplicate client key %q", c.Key)
		}
		if c.ClientID == "" || c.ClientSecret == "" || c.RedirectURI == "" {
			return nil, fmt.Errorf("youtube oauth client pool: client %q is half-configured (client_id=%t, client_secret=%t, redirect_uri=%t); register all three fields or none",
				c.Key, c.ClientID != "", c.ClientSecret != "", c.RedirectURI != "")
		}
		seen[c.Key] = true
		if c.RecommendedCapacity <= 0 {
			c.RecommendedCapacity = defaultYouTubePoolCapacity
		}
		r.byKey[c.Key] = len(r.clients)
		r.clients = append(r.clients, c)
	}
	if len(r.clients) == 0 {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	return r, nil
}

// NewYouTubeOAuthClientRegistryFromConfig builds the pool registry from
// the optional YOUTUBE_OAUTH_CLIENT_A/B_* env vars (via
// cfg.Auth.YouTubeOAuthClientPool). Returns (nil, nil) when no pool
// client is configured, preserving the legacy single-client path
// (cfg.Auth.YouTubeClientID) untouched.
func NewYouTubeOAuthClientRegistryFromConfig(cfg *config.Config) (*YouTubeOAuthClientRegistry, error) {
	if cfg == nil {
		return nil, fmt.Errorf("youtube oauth client pool: nil config")
	}
	var clients []YouTubeOAuthClientConfig
	pool := cfg.Auth.YouTubeOAuthClientPool
	if pool.ClientA.ClientID != "" || pool.ClientA.ClientSecret != "" {
		clients = append(clients, YouTubeOAuthClientConfig{
			Key:          "youtube_pool_a",
			ClientID:     pool.ClientA.ClientID,
			ClientSecret: pool.ClientA.ClientSecret,
			RedirectURI:  pool.ClientA.RedirectURI,
		})
	}
	if pool.ClientB.ClientID != "" || pool.ClientB.ClientSecret != "" {
		clients = append(clients, YouTubeOAuthClientConfig{
			Key:          "youtube_pool_b",
			ClientID:     pool.ClientB.ClientID,
			ClientSecret: pool.ClientB.ClientSecret,
			RedirectURI:  pool.ClientB.RedirectURI,
		})
	}
	if len(clients) == 0 {
		return nil, nil
	}
	return NewYouTubeOAuthClientRegistry(clients)
}

// Len returns the number of configured pool clients.
func (r *YouTubeOAuthClientRegistry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.clients)
}

// Keys returns the configured pool keys in registration order
// (youtube_pool_a, youtube_pool_b, …).
func (r *YouTubeOAuthClientRegistry) Keys() []string {
	if r == nil {
		return nil
	}
	keys := make([]string, 0, len(r.clients))
	for _, c := range r.clients {
		keys = append(keys, c.Key)
	}
	return keys
}

// Resolve returns the pool client for the given key. The returned
// config must be treated as read-only; the registry owns its storage.
// Unknown keys return ErrYouTubeOAuthClientUnknown (wrapped with the
// key for diagnostics — never with credential material).
func (r *YouTubeOAuthClientRegistry) Resolve(key string) (*YouTubeOAuthClientConfig, error) {
	if r == nil {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	idx, ok := r.byKey[key]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrYouTubeOAuthClientUnknown, key)
	}
	return &r.clients[idx], nil
}

// SelectForNewConnection returns the pool client that should issue the
// next OAuth grant for the given Google subject.
//
// With a usage counter wired, the client with the most remaining
// capacity (RecommendedCapacity − active refresh grants) wins; ties
// break by lower usage, then registration order (deterministic, never
// random — a new channel must not land on a different pool across
// retries). Counter failures fail closed: the registry refuses to
// pick a pool on a storage error rather than guessing. An empty
// googleSubjectID is rejected when a counter is wired (the usage
// query would be meaningless); the no-counter fallback does not need
// the subject.
//
// Without a counter (pre-migration wiring), the first registered
// client is returned deterministically.
func (r *YouTubeOAuthClientRegistry) SelectForNewConnection(ctx context.Context, googleSubjectID string) (*YouTubeOAuthClientConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || len(r.clients) == 0 {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	if r.usageCounter == nil {
		return &r.clients[0], nil
	}
	if googleSubjectID == "" {
		return nil, fmt.Errorf("youtube oauth client pool: googleSubjectID is required for capacity-aware selection")
	}
	selected := 0
	bestRemaining := int64(-1)
	bestUsed := int64(0)
	for i := range r.clients {
		c := &r.clients[i]
		used, err := r.usageCounter.CountActiveRefreshTokens(ctx, googleSubjectID, c.Key)
		if err != nil {
			return nil, fmt.Errorf("youtube oauth client pool: count usage for %s: %w", c.Redacted(), err)
		}
		remaining := int64(c.RecommendedCapacity) - used
		if remaining > bestRemaining ||
			(remaining == bestRemaining && (used < bestUsed || (used == bestUsed && i < selected))) {
			selected = i
			bestRemaining = remaining
			bestUsed = used
		}
	}
	return &r.clients[selected], nil
}
