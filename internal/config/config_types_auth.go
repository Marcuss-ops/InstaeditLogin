package config

// AuthConfig holds OAuth credentials, JWT settings and security tokens.
type AuthConfig struct {
	// Meta OAuth — shared App ID and Secret.
	MetaAppID       string
	MetaAppSecret   string
	MetaRedirectURI string // DEPRECATED

	// Per-platform redirect URIs.
	InstagramRedirectURI string
	FacebookRedirectURI  string
	ThreadsRedirectURI   string

	// TikTok OAuth
	TikTokClientID     string
	TikTokClientSecret string
	TikTokRedirectURI  string

	// X (Twitter) OAuth 2.0 PKCE
	XClientID     string
	XClientSecret string
	XRedirectURI  string

	// YouTube OAuth
	YouTubeClientID     string
	YouTubeClientSecret string
	YouTubeRedirectURI  string

	// YouTubeOAuthClientPool (optional) — the YouTube OAuth Client Pool.
	// Google caps the number of refresh tokens issued for one
	// (Google account, OAuth client) pair at 100. A fleet of 100+
	// channels under a single Google manager therefore spreads its
	// tokens across two OAuth clients instead of exhausting one
	// client. The pool is an additive, optional layer: when no pool
	// client is configured, the legacy single-client path
	// (YouTubeClientID/Secret/RedirectURI) keeps working unchanged.
	// Pool client secrets live in memory only (loaded from env) and
	// must never enter the database, the logs or error messages.
	YouTubeOAuthClientPool YouTubeOAuthClientPoolConfig

	// Google Drive OAuth scope policy. "readonly" is the default import
	// grant; "write" is required by the Google Drive delivery/exporter.
	GoogleDriveClientID     string
	GoogleDriveClientSecret string
	GoogleDriveRedirectURI  string
	GoogleDriveOAuthScope   string

	// LinkedIn OAuth
	LinkedInClientID     string
	LinkedInClientSecret string
	LinkedInRedirectURI  string

	// JWT
	JWTSecret           string
	JWTAccessTTLMinutes int
	JWTRefreshTTLDays   int
	// Deprecated: JWT_TTL_HOURS is the legacy single-knob TTL.
	// If JWT_ACCESS_TTL_MINUTES is unset, the hours value is
	// converted to minutes. Prefer the explicit access/refresh
	// variables for new deployments.
	JWTTTLHours int

	// TrustedProxies is a comma-separated list of IP addresses and/or
	// CIDR ranges that are allowed to supply X-Forwarded-For /
	// X-Real-IP headers. When empty, the API trusts only the direct
	// peer address (RemoteAddr). Example: "10.0.0.0/8,127.0.0.1".
	TrustedProxies string

	// AdminInviteToken gates the public registration endpoint
	// (POST /api/v1/auth/register). The handler requires the request
	// to present the same value via the X-Admin-Token header
	// (constant-time compare). When empty, registration is fully
	// disabled (the handler returns 403 "registration is
	// invite-only"). Generate with `openssl rand -hex 32` and
	// rotate via VPS .env edit + `docker compose restart`. NOT logged, NOT exposed
	// in error messages.
	AdminInviteToken string
}

// YouTubeOAuthClientPoolConfig holds the optional second Google OAuth
// client (pool B) beside the primary YouTube client. The registry in
// internal/services resolves either client by key and selects the
// least-loaded pool for new connections.
type YouTubeOAuthClientPoolConfig struct {
	// ClientA is the first pool client (env YOUTUBE_OAUTH_CLIENT_A_ID /
	// _SECRET / _REDIRECT_URI).
	ClientA YouTubeOAuthPoolClient
	// ClientB is the second pool client (env YOUTUBE_OAUTH_CLIENT_B_ID /
	// _SECRET / _REDIRECT_URI).
	ClientB YouTubeOAuthPoolClient
}

// YouTubeOAuthPoolClient is one Google OAuth client credential set in
// the YouTube OAuth client pool. ClientSecret is a credential: it is
// loaded from env, kept in process memory and never persisted,
// logged or included in error strings.
type YouTubeOAuthPoolClient struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
}
