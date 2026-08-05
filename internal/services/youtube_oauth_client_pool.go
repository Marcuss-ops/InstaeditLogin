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

// ErrYouTubeOAuthClientPoolExhausted is returned by
// SelectForNewConnection when EVERY configured pool client is over the
// critical load threshold (90 active refresh grants). No new
// connection may be issued until one of the pools drains (operator
// must reconnect/rebalance) — Google's hard cap is 100 per
// (account, client), so this margin protects the fleet.
var ErrYouTubeOAuthClientPoolExhausted = errors.New("youtube oauth client pool: all pool clients are over the critical capacity; new connections blocked")

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

// YouTube OAuth pool load thresholds (per the operator's capacity
// spec). Google's hard cap is 100 refresh tokens per (account, client)
// pair; these margins keep InstaEdit well inside it and give the
// operator increasing-warning bands before the hard block.
const (
	// YouTubeOAuthPoolHealthyThreshold: 0–60 active grants = healthy.
	YouTubeOAuthPoolHealthyThreshold int64 = 60
	// YouTubeOAuthPoolWarningThreshold: 61–75 = warning.
	YouTubeOAuthPoolWarningThreshold int64 = 75
	// YouTubeOAuthPoolHighThreshold: 76–85 = high.
	YouTubeOAuthPoolHighThreshold int64 = 85
	// YouTubeOAuthPoolCriticalThreshold: 86–90 = critical. Active
	// grants ABOVE this threshold block new connections on that client.
	YouTubeOAuthPoolCriticalThreshold int64 = 90
)

// YouTubeOAuthPoolHealth classifies a pool client's active-grant load.
type YouTubeOAuthPoolHealth string

const (
	YouTubeOAuthPoolHealthHealthy  YouTubeOAuthPoolHealth = "healthy"
	YouTubeOAuthPoolHealthWarning  YouTubeOAuthPoolHealth = "warning"
	YouTubeOAuthPoolHealthHigh     YouTubeOAuthPoolHealth = "high"
	YouTubeOAuthPoolHealthCritical YouTubeOAuthPoolHealth = "critical"
	// YouTubeOAuthPoolHealthBlocked means the client is over the
	// critical threshold (90) and refuses new connections.
	YouTubeOAuthPoolHealthBlocked YouTubeOAuthPoolHealth = "blocked"
)

// YouTubeOAuthPoolHealthFor maps an active-grant count to its health
// band: 0–60 healthy, 61–75 warning, 76–85 high, 86–90 critical,
// >90 blocked.
func YouTubeOAuthPoolHealthFor(active int64) YouTubeOAuthPoolHealth {
	switch {
	case active > YouTubeOAuthPoolCriticalThreshold:
		return YouTubeOAuthPoolHealthBlocked
	case active > YouTubeOAuthPoolHighThreshold:
		return YouTubeOAuthPoolHealthCritical
	case active > YouTubeOAuthPoolWarningThreshold:
		return YouTubeOAuthPoolHealthHigh
	case active > YouTubeOAuthPoolHealthyThreshold:
		return YouTubeOAuthPoolHealthWarning
	default:
		return YouTubeOAuthPoolHealthHealthy
	}
}

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
// cfg.Auth.YouTubeOAuthClientPool), applying any extra options (e.g.
// WithYouTubeOAuthClientUsageCounter wired from the storage
// repository). Returns (nil, nil) when no pool client is configured,
// preserving the legacy single-client path (cfg.Auth.YouTubeClientID)
// untouched.
func NewYouTubeOAuthClientRegistryFromConfig(cfg *config.Config, opts ...YouTubeOAuthClientRegistryOption) (*YouTubeOAuthClientRegistry, error) {
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
	return NewYouTubeOAuthClientRegistry(clients, opts...)
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
// With a usage counter wired AND a known googleSubjectID, the client
// with the most remaining capacity (RecommendedCapacity − active
// refresh grants) wins; ties break by lower usage, then registration
// order (deterministic, never random — a new channel must not land on
// a different pool across retries). Counter failures fail closed: the
// registry refuses to pick a pool on a storage error rather than
// guessing. Clients over the critical threshold (90 active grants)
// are excluded from selection; if every client is blocked the
// registry returns ErrYouTubeOAuthClientPoolExhausted (new
// connections refused until a pool drains below critical).
//
// An EMPTY googleSubjectID — the production login path, where the
// Google account is unknown until the operator picks it on the
// consent screen — falls back to the first registered client,
// deterministically, exactly as the no-counter selection did. The
// per-(subject, client) usage query would be meaningless without the
// subject, so capacity-aware selection only engages once a caller can
// resolve it (e.g. a future subject-pinned connect-link flow); the
// strict empty-subject rejection lives in selectLeastLoadedPoolClient
// for OAuthTokenCapacityManager.SelectPool, whose contract demands a
// subject.
func (r *YouTubeOAuthClientRegistry) SelectForNewConnection(ctx context.Context, googleSubjectID string) (*YouTubeOAuthClientConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || len(r.clients) == 0 {
		return nil, ErrYouTubeOAuthClientPoolEmpty
	}
	if r.usageCounter == nil || googleSubjectID == "" {
		return &r.clients[0], nil
	}
	selected, _, err := selectLeastLoadedPoolClient(ctx, r.clients, r.usageCounter, googleSubjectID)
	if err != nil {
		return nil, err
	}
	return selected, nil
}

// selectLeastLoadedPoolClient is the shared capacity-aware selection
// heuristic used by YouTubeOAuthClientRegistry.SelectForNewConnection
// and OAuthTokenCapacityManager.SelectPool.
//
// Among the given clients, the one with the most remaining capacity
// (RecommendedCapacity − active refresh grants) wins; ties break by
// lower usage, then registration order (deterministic, never random —
// a new grant must not land on a different pool across retries).
// Clients over the critical threshold (90 active grants) are excluded;
// if every client is blocked, ErrYouTubeOAuthClientPoolExhausted is
// returned (new grants refused until a pool drains below critical).
// Counter failures fail closed — the caller refuses to guess. An empty
// googleSubjectID is rejected (a per-subject usage query would be
// meaningless).
//
// Returns the selected client config and its active-grant count.
func selectLeastLoadedPoolClient(ctx context.Context, clients []YouTubeOAuthClientConfig, counter OAuthClientUsageCounter, googleSubjectID string) (*YouTubeOAuthClientConfig, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if len(clients) == 0 {
		return nil, 0, ErrYouTubeOAuthClientPoolEmpty
	}
	if counter == nil {
		return nil, 0, fmt.Errorf("youtube oauth client pool: usage counter is required for capacity-aware selection")
	}
	if googleSubjectID == "" {
		return nil, 0, fmt.Errorf("youtube oauth client pool: googleSubjectID is required for capacity-aware selection")
	}
	selected := -1 // -1 sentinel: the first available client becomes the baseline
	bestRemaining := int64(0)
	bestUsed := int64(0)
	available := false
	for i := range clients {
		c := &clients[i]
		used, err := counter.CountActiveRefreshTokens(ctx, googleSubjectID, c.Key)
		if err != nil {
			return nil, 0, fmt.Errorf("youtube oauth client pool: count usage for %s: %w", c.Redacted(), err)
		}
		if used > YouTubeOAuthPoolCriticalThreshold {
			// Over the hard block threshold (90): this client must not
			// issue a new grant until it drains below critical. Skip it
			// for selection.
			continue
		}
		available = true
		remaining := int64(c.RecommendedCapacity) - used
		if selected == -1 || remaining > bestRemaining ||
			(remaining == bestRemaining && (used < bestUsed || (used == bestUsed && i < selected))) {
			selected = i
			bestRemaining = remaining
			bestUsed = used
		}
	}
	if !available {
		return nil, 0, ErrYouTubeOAuthClientPoolExhausted
	}
	return &clients[selected], bestUsed, nil
}
