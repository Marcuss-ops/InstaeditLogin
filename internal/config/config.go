package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Minimum-credential thresholds.
const (
	jwtSecretMinBytes        = 32
	aesKeyBytes              = 32
	secretMinChars           = 32
	adminInviteTokenMinChars = 32 // ADMIN_INVITE_TOKEN: prevent trivial brute-force if the operator accidentally sets a short value
)

// Config holds all configuration for the application.
//
// Taglio 5b: SERVER_PORT + SERVER_HOST removed — the server listens on the
// PORT env var only (Vercel / Railway / Render standard). TWITTER_* env vars
// renamed to X_*; TIKTOK_CLIENT_KEY renamed to TIKTOK_CLIENT_ID.
type Config struct {
	// VeloxAPIToken authenticates artifact HEAD/GET requests back to Velox.
	VeloxAPIToken string
	// FrontendURL is where the OAuth callback should redirect.
	FrontendURL string
	// AllowedCORSOrigins is the comma-separated list of origins.
	AllowedCORSOrigins []string

	// Database (PostgreSQL). DATABASE_URL for production; individual fields
	// (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE) are kept
	// for local tooling. DATABASE_URL takes precedence.
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

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

	// P1#6 — YouTube resumable-upload tuning. The resumable upload
	// protocol streams the binary in N chunks; Google requires each
	// chunk be a multiple of 256 KB (262144 bytes) and recommends
	// larger chunks for fewer round-trips (valutazione doc spec:
	// 16 MB default). Backoff uses full-jitter exponential growth
	// capped at 5 min, with Retry-After from the server ALWAYS honored
	// (the cap applies only to the calculated fallback when the
	// server didn't send a hint — see youtube_oauth.go for the
	// rationale: capping a server hint to a smaller value guarantees
	// we'd hammer the API mid-quota-window and risk blacklisting).
	//
	// These are independent of the upload-job-level retries in
	// internal/worker/upload_worker.go::computeUploadBackoff; chunk
	// retries recover a transient network blip during the PUT, while
	// job-level retries recover a budget-exhausted publish that the
	// inner chunk loop couldn't escape.
	YouTubeUploadChunkBytes    int64 // YOUTUBE_UPLOAD_CHUNK_BYTES; default 16777216 (16 MB), MUST be multiple of 262144 (256 KB)
	YouTubeUploadMaxRetries    int   // YOUTUBE_UPLOAD_MAX_RETRIES; default 5 (per-chunk PUT budget, distinct from upload-job retries)
	YouTubeUploadBackoffBaseMs int   // YOUTUBE_UPLOAD_BACKOFF_BASE_MS; default 1000 (1 s)
	YouTubeUploadBackoffCapMs  int   // YOUTUBE_UPLOAD_BACKOFF_CAP_MS; default 300000 (5 min); applies to CALCULATED backoff only, NOT server Retry-After
	YouTubeDailyQuotaLimit     int   // YOUTUBE_DAILY_QUOTA_LIMIT; default 300; daily pre-call videos.insert gate. 1 videos.insert = 1 bucket unit under the 2026 quota model (default 100/day from Google, 300/day for the 200-channel rollout with 50% buffer). When calls >= limit, publish_worker stamps retry_wait + metadata.retry_after_seconds until next UTC midnight.

	// Google Drive OAuth (read-only import of video clips)
	GoogleDriveClientID       string
	GoogleDriveClientSecret   string
	GoogleDriveRedirectURI    string
	GoogleDriveUploadFolderID string

	// LinkedIn OAuth
	LinkedInClientID     string
	LinkedInClientSecret string
	LinkedInRedirectURI  string

	// Encryption (Blocco #2.2 — multi-key support).
	//
	// Two parallel surfaces:
	//
	//   1. ENCRYPTION_KEY + (implied) ACTIVE_ENCRYPTION_KEY_ID=1
	//      — the legacy single-key path. Pre-Blocco #2.2 deployments
	//      set only ENCRYPTION_KEY. Validate() promotes that single
	//      key into EncryptionKeys[1] and ActiveEncryptionKeyID=1,
	//      so Wire() and every consumer sees the same struct shape
	//      regardless of which env-var surface the operator uses.
	//
	//   2. ENCRYPTION_KEYS (CSV: id:base64key,id:base64key,...) +
	//      ACTIVE_ENCRYPTION_KEY_ID (uint32) — the multi-key path.
	//      Validate() parses the CSV, validates every key
	//      (base64 + 32 bytes), and confirms the active id is
	//      present in the map.
	//
	// Mixing both surfaces is a misconfiguration: validate() rejects
	// "both ENCRYPTION_KEY and ENCRYPTION_KEYS set" with a
	// descriptive error so operators can act on it.
	EncryptionKey string // LEGACY: single-key fallback
	// EncryptionKeys is the post-validation map of all key ids
	// known to this process. Always populated: either from
	// ENCRYPTION_KEYS (multi-key) or from the legacy fallback.
	EncryptionKeys map[uint32]string
	// ActiveEncryptionKeyID is the key id used for new Encrypt
	// calls. Always populated: either from ACTIVE_ENCRYPTION_KEY_ID
	// (multi-key) or 1 (legacy fallback).
	ActiveEncryptionKeyID uint32
	// EncryptionKeysRaw is the unparsed ENCRYPTION_KEYS string,
	// preserved here only for the validate() error message when
	// the CSV is malformed. Not used outside validation.
	EncryptionKeysRaw string
	// ActiveEncryptionKeyIDRaw is the unparsed ACTIVE_ENCRYPTION_KEY_ID
	// string, same purpose as EncryptionKeysRaw.
	ActiveEncryptionKeyIDRaw string

	// JWT
	JWTSecret           string
	JWTAccessTTLMinutes int
	JWTRefreshTTLDays   int
	// TrustedProxies is a comma-separated list of IP addresses and/or
	// CIDR ranges that are allowed to supply X-Forwarded-For /
	// X-Real-IP headers. When empty, the API trusts only the direct
	// peer address (RemoteAddr). Example: "10.0.0.0/8,127.0.0.1".
	TrustedProxies string

	// Metrics basic-auth credentials. In production both must be set;
	// validate() fail-closes the boot if either is empty.
	MetricsBasicAuthUser string
	MetricsBasicAuthPass string
	// MetricsHost/MetricsPort optionally start a separate internal
	// listener for the /metrics endpoint. When MetricsPort is 0, the
	// endpoint is served only on the main HTTP server at
	// /api/v1/metrics. When MetricsPort > 0, an additional listener
	// is started on MetricsHost:MetricsPort (default MetricsHost
	// 127.0.0.1 if empty) so scrapers on a private network can reach
	// metrics without exposing the main API.
	MetricsHost string
	MetricsPort int

	// Deprecated: JWT_TTL_HOURS is the legacy single-knob TTL.
	// If JWT_ACCESS_TTL_MINUTES is unset, the hours value is
	// converted to minutes. Prefer the explicit access/refresh
	// variables for new deployments.
	JWTTTLHours int

	// Logging
	LogLevel string

	// AppEnv is the deployment environment.
	AppEnv string

	// Background worker tuning.
	PublishWorkerIntervalSeconds int
	// Taglio 5.x — independent tick interval for the new
	// ReconcileWorker goroutine. The driver (PublishWorker) ticks
	// at PublishWorkerIntervalSeconds (default 30s); the reconciler
	// ticks faster (default 5s) so an async publish's
	// publishing→published transition is observed promptly without
	// coupling to the driver's cadence. Both run as separate
	// goroutines on independent contexts with parallel shutdown.
	ReconcileWorkerIntervalSeconds int
	// SPRINT 4.2 — independent tick interval for the WebhookWorker
	// goroutine. Drains the webhook_deliveries table every
	// WEBHOOK_WORKER_INTERVAL_SECONDS (default 5s). Faster than
	// the publish driver so an end-to-end delivery latency is
	// bounded by a 1-2s ceiling under typical load. Same
	// lifecycle shape: independent goroutine, ctx-cancellable,
	// drained in parallel on shutdown.
	WebhookWorkerIntervalSeconds int

	// SessionsCleanupIntervalSeconds — cadence of the retention
	// policy goroutine (commit: cleanup-policy). Drives the
	// periodic SessionsCleanupWorker that DELETEs rows from the
	// `sessions` table whose revoked_at is older than 30 days OR
	// whose refresh_expires_at is older than 7 days. Default 300s
	// (5 min) is coarse enough to not thrash the DB under traffic
	// spikes but fine-grained enough to keep the sessions table
	// bounded under normal load.
	SessionsCleanupIntervalSeconds int

	// UploadWorkerIntervalSeconds — cadence of the background upload
	// worker that drains upload_jobs (public or authenticated Google
	// Drive imports). Default 30s.
	UploadWorkerIntervalSeconds int

	// P1 step 2 — ingest pool / upload pool split. The upload_worker
	// package now spawns two parallel pools against the upload_jobs
	// queue, each with its own concurrency cap (the valutazione doc
	// recommends 2–3 ingest + 3–4 YouTube-upload on dev boxen,
	// scaling only after RAM/disk/bandwidth measurements).
	//
	// The ingest pool claims status IN ('pending','retry_wait') and
	// streams Drive→S3; the upload pool claims status =
	// 'ready_to_publish' and runs videos.insert. Both pools use the
	// same lease + heartbeat machinery, with distinct workerID
	// prefixes so a Mark* CAS can never collide.
	UploadIngestConcurrency        int  // UPLOAD_INGEST_CONCURRENCY; default 3
	YouTubeUploadConcurrency       int  // YOUTUBE_UPLOAD_CONCURRENCY; default 4
	UploadLeaseTTLSeconds          int  // UPLOAD_LEASE_TTL_SECONDS; default 60
	UploadHeartbeatIntervalSeconds int  // UPLOAD_HEARTBEAT_INTERVAL_SECONDS; default 20
	UploadReclaimIntervalSeconds   int  // UPLOAD_RECLAIM_INTERVAL_SECONDS; default 30
	UploadReclaimOnStart           bool // UPLOAD_RECLAIM_ON_START; default true

	// GoogleDriveAPIKey is a Google Cloud API key used to list CONTENTS
	// of a public Drive folder when the user has not linked their Drive
	// account. Without it, batch folder imports only work for folders
	// the linked Drive account can access (typically: nothing, since the
	// linked account isn't the folder's owner).
	//
	// Create one at https://console.cloud.google.com → APIs & Services →
	// Credentials → API key, scoped to the Google Drive API. Empty
	// default means the batch folder endpoint can only target folders
	// the user's Drive OAuth grant can see.
	GoogleDriveAPIKey string

	// S3-compatible storage (mandatory).
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string
	// S3PathStyle selects path-style addressing ({host}/{bucket}/{key})
	// instead of the default virtual-hosted ({bucket}.{host}/{key}).
	// Required when S3_ENDPOINT is a single fixed origin (e.g. a
	// Cloudflare quick tunnel) that cannot serve per-bucket subdomains.
	S3PathStyle bool

	// MaxUploadBytes caps the size of any single file upload.
	MaxUploadBytes int64

	// Stripe billing (optional — billing endpoints are 501 when not configured).
	StripeSecretKey     string
	StripeWebhookSecret string
	StripeSuccessURL    string
	StripeCancelURL     string

	// Sentry (optional, Blocco #5.3).
	//
	// SENTRY_DSN is the SDK DSN string (`https://key@sentry.io/projid`).
	// Empty (the default) disables the entire observability surface:
	//   - sentry.Init is NOT called at startup.
	//   - the panic-catching middleware falls back to a plain
	//     `recover(http.Handler)` that writes 500 with NO outbound
	//     network traffic.
	// - Non-empty: sentry.Init runs at startup; the panic-catching
	//   middleware wraps with sentryhttp.New so CaptureException is
	//   called for every recovered panic and the SDK buffers out-of-band.
	//
	// SENTRY_ENVIRONMENT defaults to AppEnv ("dev"/"staging"/"production")
	// when empty; SENTRY_RELEASE is passed straight through to the SDK
	// (the operator typically wires this to the deploy SHA via the CI
	// pipeline). Both are passed via env so the production deploy can
	// set them without re-baking the binary.
	SentryDSN         string
	SentryEnvironment string
	SentryRelease     string

	// CookieDomain is the optional `Domain` attribute applied to the
	// csrf_token cookie ONLY (session + refresh cookies stay host-only).
	// Defaults to empty so dev (localhost:5173 + localhost:8080) keeps
	// working unchanged. Production sets it to e.g. ".instaedit.org"
	// so the SPA on app.instaedit.org can read the csrf_token via
	// document.cookie over a cross-origin backend on api.instaedit.org.
	// Use a leading dot to make the cookie available to every
	// subdomain; the value is passed straight through to Go's
	// http.Cookie Domain field (which the browser interprets per
	// RFC 6265). Validation is intentionally NOT applied — the
	// HTTPS / SameSite=None / leading-dot trade-off is the
	// operator's call.
	CookieDomain string

	// AdminInviteToken gates the public registration endpoint
	// (POST /api/v1/auth/register). The handler requires the request
	// to present the same value via the X-Admin-Token header
	// (constant-time compare). When empty, registration is fully
	// disabled (the handler returns 403 "registration is
	// invite-only"). Generate with `openssl rand -hex 32` and
	// rotate via `flyctl secrets import`. NOT logged, NOT exposed
	// in error messages.
	AdminInviteToken string

	// AppMode lets operators pin the deployment to Google's OAuth-
	// consent-screen publishing status. "production" means refresh
	// tokens are durable (no automatic 7-day expiry). "testing"
	// means Google's Testing-mode 7-day refresh-token TTL applies
	// and any refresh attempt after day 7 returns invalid_grant.
	// Default "production" so a missing env var falls into the safer
	// bucket; ops must explicitly opt-in to "testing" when validating
	// against a staging OAuth-client. Loaded from env APP_MODE.
	AppMode string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		FrontendURL:          getEnv("FRONTEND_URL", ""),
		AllowedCORSOrigins:   splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
		DatabaseURL:          getEnv("DATABASE_URL", ""),
		DBHost:               getEnv("DB_HOST", "localhost"),
		DBPort:               getEnv("DB_PORT", "5432"),
		DBUser:               getEnv("DB_USER", "instaedit"),
		DBPassword:           getEnv("DB_PASSWORD", ""),
		DBName:               getEnv("DB_NAME", "instaedit_login"),
		DBSSLMode:            getEnv("DB_SSLMODE", "disable"),
		MetaAppID:            getEnv("META_APP_ID", ""),
		MetaAppSecret:        getEnv("META_APP_SECRET", ""),
		MetaRedirectURI:      getEnv("META_REDIRECT_URI", ""),
		InstagramRedirectURI: getEnv("INSTAGRAM_REDIRECT_URI", "http://localhost:8080/api/v1/auth/instagram/callback"),
		FacebookRedirectURI:  getEnv("FACEBOOK_REDIRECT_URI", "http://localhost:8080/api/v1/auth/facebook/callback"),
		ThreadsRedirectURI:   getEnv("THREADS_REDIRECT_URI", "http://localhost:8080/api/v1/auth/threads/callback"),
		TikTokClientID:       getEnv("TIKTOK_CLIENT_ID", ""),
		TikTokClientSecret:   getEnv("TIKTOK_CLIENT_SECRET", ""),
		TikTokRedirectURI:    getEnv("TIKTOK_REDIRECT_URI", "http://localhost:8080/api/v1/auth/tiktok/callback"),
		XClientID:            getEnv("X_CLIENT_ID", ""),
		XClientSecret:        getEnv("X_CLIENT_SECRET", ""),
		XRedirectURI:         getEnv("X_REDIRECT_URI", "http://localhost:8080/api/v1/auth/twitter/callback"),
		YouTubeClientID:      getEnv("YOUTUBE_CLIENT_ID", ""),
		YouTubeClientSecret:  getEnv("YOUTUBE_CLIENT_SECRET", ""),
		YouTubeRedirectURI:   getEnv("YOUTUBE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/youtube/callback"),
		// P1#6 — YouTube resumable upload tuning. Defaults mirror the
		// valutazione doc spec (16 MB chunks, 5 per-chunk retries, 1 s/5 min
		// backoff). Validation runs unconditionally (so an operator typo
		// surfaces at boot, not first upload).
		YouTubeUploadChunkBytes: getEnvInt64("YOUTUBE_UPLOAD_CHUNK_BYTES", 16*1024*1024),
		// AppMode lets operators pin the deployment to Google's OAuth-
		// consent-screen publishing status. "production" means refresh
		// tokens are durable (no automatic 7-day expiry); "testing"
		// means Google's Testing-mode 7-day refresh-token TTL applies
		// and any refresh attempt after day 7 surfaces invalid_grant.
		// Default "production" so a missing env var falls into the
		// safer bucket; ops must explicitly opt-in to "testing" when
		// validating against a staging OAuth-client.
		AppMode:                    getEnv("APP_MODE", "production"),
		YouTubeUploadMaxRetries:    getEnvInt("YOUTUBE_UPLOAD_MAX_RETRIES", 5),
		YouTubeUploadBackoffBaseMs: getEnvInt("YOUTUBE_UPLOAD_BACKOFF_BASE_MS", 1000),
		YouTubeUploadBackoffCapMs:  getEnvInt("YOUTUBE_UPLOAD_BACKOFF_CAP_MS", 300000),
		YouTubeDailyQuotaLimit:     getEnvInt("YOUTUBE_DAILY_QUOTA_LIMIT", 300),
		GoogleDriveClientID:        getEnv("GOOGLE_DRIVE_CLIENT_ID", ""),
		GoogleDriveClientSecret:    getEnv("GOOGLE_DRIVE_CLIENT_SECRET", ""),
		GoogleDriveRedirectURI:     getEnv("GOOGLE_DRIVE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/google-drive/callback"),
		GoogleDriveUploadFolderID:  getEnv("GOOGLE_DRIVE_UPLOAD_FOLDER_ID", ""),
		VeloxAPIToken:              getEnv("VELOX_API_TOKEN", ""),
		LinkedInClientID:           getEnv("LINKEDIN_CLIENT_ID", ""),
		LinkedInClientSecret:       getEnv("LINKEDIN_CLIENT_SECRET", ""),
		LinkedInRedirectURI:        getEnv("LINKEDIN_REDIRECT_URI", "http://localhost:8080/api/v1/auth/linkedin/callback"),
		EncryptionKey:              getEnv("ENCRYPTION_KEY", ""),
		// Blocco #2.2: read the multi-key env vars. The actual
		// parsing + validation happens in validate(); Load() only
		// captures the raw strings so validate() can surface
		// high-quality error messages with the original input.
		EncryptionKeysRaw:              getEnv("ENCRYPTION_KEYS", ""),
		ActiveEncryptionKeyIDRaw:       getEnv("ACTIVE_ENCRYPTION_KEY_ID", ""),
		JWTSecret:                      getEnv("JWT_SECRET", ""),
		JWTAccessTTLMinutes:            getEnvInt("JWT_ACCESS_TTL_MINUTES", 0),
		JWTRefreshTTLDays:              getEnvInt("JWT_REFRESH_TTL_DAYS", 0),
		TrustedProxies:                 getEnv("TRUSTED_PROXIES", ""),
		MetricsBasicAuthUser:           getEnv("METRICS_BASIC_AUTH_USER", ""),
		MetricsBasicAuthPass:           getEnv("METRICS_BASIC_AUTH_PASS", ""),
		MetricsHost:                    getEnv("METRICS_HOST", ""),
		MetricsPort:                    getEnvInt("METRICS_PORT", 0),
		JWTTTLHours:                    getEnvInt("JWT_TTL_HOURS", 0),
		LogLevel:                       getEnv("LOG_LEVEL", "info"),
		AppEnv:                         getEnv("APP_ENV", "dev"),
		PublishWorkerIntervalSeconds:   getEnvInt("PUBLISH_WORKER_INTERVAL_SECONDS", 30),
		ReconcileWorkerIntervalSeconds: getEnvInt("RECONCILE_WORKER_INTERVAL_SECONDS", 5),
		WebhookWorkerIntervalSeconds:   getEnvInt("WEBHOOK_WORKER_INTERVAL_SECONDS", 5),
		SessionsCleanupIntervalSeconds: getEnvInt("SESSION_CLEANUP_INTERVAL_SECONDS", 300),
		UploadWorkerIntervalSeconds:    getEnvInt("UPLOAD_WORKER_INTERVAL_SECONDS", 30),
		// P1 step 2 — worker pool config (see struct comment above).
		UploadIngestConcurrency:        getEnvInt("UPLOAD_INGEST_CONCURRENCY", 3),
		YouTubeUploadConcurrency:       getEnvInt("YOUTUBE_UPLOAD_CONCURRENCY", 4),
		UploadLeaseTTLSeconds:          getEnvInt("UPLOAD_LEASE_TTL_SECONDS", 60),
		UploadHeartbeatIntervalSeconds: getEnvInt("UPLOAD_HEARTBEAT_INTERVAL_SECONDS", 20),
		UploadReclaimIntervalSeconds:   getEnvInt("UPLOAD_RECLAIM_INTERVAL_SECONDS", 30),
		UploadReclaimOnStart:           getEnvBool("UPLOAD_RECLAIM_ON_START", true),
		GoogleDriveAPIKey:              getEnv("GOOGLE_DRIVE_API_KEY", ""),
		S3Endpoint:                     getEnv("S3_ENDPOINT", ""),
		S3Bucket:                       getEnv("S3_BUCKET", ""),
		S3PathStyle:                    getEnvBool("S3_PATH_STYLE", false),
		S3AccessKey:                    getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:                    getEnv("S3_SECRET_KEY", ""),
		S3Region:                       getEnv("S3_REGION", ""),
		MaxUploadBytes:                 getEnvInt64("STORAGE_MAX_UPLOAD_BYTES", 200*1024*1024),
		StripeSecretKey:                getEnv("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret:            getEnv("STRIPE_WEBHOOK_SECRET", ""),
		StripeSuccessURL:               getEnv("STRIPE_SUCCESS_URL", getEnv("FRONTEND_URL", "http://localhost:5173")+"/dashboard/billing?success=1"),
		StripeCancelURL:                getEnv("STRIPE_CANCEL_URL", getEnv("FRONTEND_URL", "http://localhost:5173")+"/dashboard/billing?canceled=1"),
		// Sentry (Blocco #5.3). SENTRY_DSN empty == SDK never
		// initialised + recovery middleware uses plain recover.
		SentryDSN:         getEnv("SENTRY_DSN", ""),
		SentryEnvironment: getEnv("SENTRY_ENVIRONMENT", ""),
		SentryRelease:     getEnv("SENTRY_RELEASE", ""),
		// COOKIE_DOMAIN: optional cross-subdomain scope for the
		// csrf_token cookie ONLY (session + refresh stay host-only).
		// Defaults to empty so dev (localhost:5173 + localhost:8080)
		// keeps working unchanged. Pass ".instaedit.org" in
		// production so the SPA on app.instaedit.org can read the
		// csrf_token via document.cookie against the API on
		// api.instaedit.org. NOT validated — the operator owns the
		// Domain shape (leading dot for cross-subdomain, exact host
		// to pin, etc.) and Go's http.Cookie Domain field will
		// pass it straight through to the browser unchanged.
		CookieDomain: getEnv("COOKIE_DOMAIN", ""),
		// Disable public registration unless an admin invite token
		// is configured. Operators create users manually (via the
		// admin endpoint or by setting ADMIN_INVITE_TOKEN and calling
		// /api/v1/auth/register with X-Admin-Token).
		AdminInviteToken: getEnv("ADMIN_INVITE_TOKEN", ""),
	}

	// Resolve JWT TTL defaults and legacy fallback. Access TTL defaults
	// to 15 minutes; refresh TTL defaults to 30 days. The legacy
	// JWT_TTL_HOURS variable is converted to minutes when the explicit
	// access-TTL variable is absent, preserving existing deployments.
	if cfg.JWTAccessTTLMinutes <= 0 {
		if cfg.JWTTTLHours > 0 {
			cfg.JWTAccessTTLMinutes = cfg.JWTTTLHours * 60
		} else {
			cfg.JWTAccessTTLMinutes = 15
		}
	}
	if cfg.JWTRefreshTTLDays <= 0 {
		cfg.JWTRefreshTTLDays = 30
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// metricsConfigured returns true only when both metrics basic-auth
// credentials are non-empty. It is used for both runtime fail-closed
// decisions and boot-time validation in production.
func (c *Config) metricsConfigured() bool {
	return c.MetricsBasicAuthUser != "" && c.MetricsBasicAuthPass != ""
}

func (c *Config) validate() error {
	// Metrics are fail-closed in production: missing or incomplete
	// basic-auth credentials prevent the process from booting. This
	// keeps /api/v1/metrics from ever being served publicly in prod.
	if c.AppEnv == "production" && !c.metricsConfigured() {
		return fmt.Errorf("METRICS_BASIC_AUTH_USER and METRICS_BASIC_AUTH_PASS are required in production")
	}

	switch c.AppEnv {
	case "dev", "staging", "production":
	default:
		return fmt.Errorf("APP_ENV must be one of dev|staging|production (got %q)", c.AppEnv)
	}

	// Database: DATABASE_URL takes precedence; individual params fallback.
	if c.DatabaseURL == "" {
		if c.DBPassword == "" {
			return fmt.Errorf("DB_PASSWORD is required (or set DATABASE_URL)")
		}
	}

	// S3-compatible storage (mandatory).
	if c.S3Endpoint == "" {
		return fmt.Errorf("S3_ENDPOINT is required")
	}
	if c.S3Bucket == "" {
		return fmt.Errorf("S3_BUCKET is required")
	}
	if c.S3AccessKey == "" {
		return fmt.Errorf("S3_ACCESS_KEY is required")
	}
	if c.S3SecretKey == "" {
		return fmt.Errorf("S3_SECRET_KEY is required")
	}

	// Meta OAuth (optional).
	if c.MetaAppID == "" && c.MetaAppSecret == "" {
		// platform disabled
	} else if c.MetaAppID == "" {
		return fmt.Errorf("META_APP_ID is required when META_APP_SECRET is set (or unset both)")
	} else if c.MetaAppSecret == "" {
		return fmt.Errorf("META_APP_SECRET is required when META_APP_ID is set (or unset both)")
	} else if len(c.MetaAppSecret) < secretMinChars {
		return fmt.Errorf("META_APP_SECRET must be at least %d characters (got %d)", secretMinChars, len(c.MetaAppSecret))
	}

	// Encryption key (Blocco #2.2 — multi-key).
	//
	// Three valid configurations:
	//   - Only ENCRYPTION_KEY set → legacy single-key path
	//     (EncryptionKeys[1] = ENCRYPTION_KEY, ActiveEncryptionKeyID = 1).
	//   - ENCRYPTION_KEYS + ACTIVE_ENCRYPTION_KEY_ID set → multi-key path.
	//   - Both set → rejected as ambiguous.
	//   - Neither set → rejected as missing.
	if err := c.resolveEncryptionConfig(); err != nil {
		return err
	}

	// JWT signing key.
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required (must be at least %d bytes)", jwtSecretMinBytes)
	}
	if len(c.JWTSecret) < jwtSecretMinBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes for HS256 (got %d)", jwtSecretMinBytes, len(c.JWTSecret))
	}

	// Sentry (Blocco #5.3 — optional). When SET, validate the DSN
	// shape so a typo at boot surfaces as a process-exit rather than
	// a silent no-op at the first panic render. When UNSET, no
	// validation; the absence is the signal the operator gave us to
	// disable the observability surface.
	if c.SentryDSN != "" {
		if err := validateSentryDSN(c.SentryDSN, c.AppEnv); err != nil {
			return fmt.Errorf("SENTRY_DSN: %w", err)
		}
		// Defaults: if the operator set SENTRY_DSN but didn't supply
		// an environment label, derive it from AppEnv so the SDK
		// dashboard tags events correctly. Empty Release is fine —
		// the SDK emits events with no release tag (still useful).
		if c.SentryEnvironment == "" {
			c.SentryEnvironment = c.AppEnv
		}
	}

	// Optional OAuth platforms.
	if err := c.validateOptionalPlatform("TIKTOK", c.TikTokClientID, c.TikTokClientSecret); err != nil {
		return err
	}

	// Admin invite token: empty disables registration (per WithAdminInviteToken
	// contract); non-empty must be long enough to make online brute-force
	// impractical. Mirrors the JWT secret's 32-byte threshold so a
	// generated `openssl rand -hex 32` (64 hex chars) sails through and
	// a 4-char typo is rejected at boot rather than exploited at runtime.
	if c.AdminInviteToken != "" && len(c.AdminInviteToken) < adminInviteTokenMinChars {
		return fmt.Errorf("ADMIN_INVITE_TOKEN must be at least %d characters when set (got %d); generate with `openssl rand -hex 32` (64 hex chars) or leave it unset to disable registration entirely", adminInviteTokenMinChars, len(c.AdminInviteToken))
	}
	if err := c.validateOptionalPlatform("X", c.XClientID, c.XClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("YOUTUBE", c.YouTubeClientID, c.YouTubeClientSecret); err != nil {
		return err
	}

	// P1#6 — YouTube resumable-upload tuning. Gated behind YouTube being
	// enabled (the same pattern as validateOptionalPlatform above): when
	// YouTube is fully disabled the upload knobs are inert and the
	// zero-defaults applied by getEnvInt64/getEnvInt must not block the
	// boot of a config built by tests or a YouTube-less deployment.
	// Per Google's resumable upload protocol, each chunk must be a
	// multiple of 256 KB (262144 bytes); values below the minimum or
	// non-multiples are silently rejected by the API with a generic 400,
	// which is hard to triage. The backoff env vars share one cross-check:
	// cap >= base; otherwise the calculated fallback would be capped
	// immediately and the chunk-loop would poll as fast as the worker can
	// count.
	if c.YouTubeClientID != "" {
		if c.YouTubeUploadChunkBytes <= 0 || c.YouTubeUploadChunkBytes%262144 != 0 {
			return fmt.Errorf("YOUTUBE_UPLOAD_CHUNK_BYTES must be a positive multiple of 256 KB (262144 bytes); got %d (default 16777216 = 16 MB)", c.YouTubeUploadChunkBytes)
		}
		if c.YouTubeUploadMaxRetries < 1 {
			return fmt.Errorf("YOUTUBE_UPLOAD_MAX_RETRIES must be at least 1 (got %d)", c.YouTubeUploadMaxRetries)
		}
		if c.YouTubeUploadBackoffBaseMs <= 0 {
			return fmt.Errorf("YOUTUBE_UPLOAD_BACKOFF_BASE_MS must be positive (got %d)", c.YouTubeUploadBackoffBaseMs)
		}
		if c.YouTubeUploadBackoffCapMs < c.YouTubeUploadBackoffBaseMs {
			return fmt.Errorf("YOUTUBE_UPLOAD_BACKOFF_CAP_MS (%d) must be >= YOUTUBE_UPLOAD_BACKOFF_BASE_MS (%d)", c.YouTubeUploadBackoffCapMs, c.YouTubeUploadBackoffBaseMs)
		}
	}
	if err := c.validateOptionalPlatform("GOOGLE_DRIVE", c.GoogleDriveClientID, c.GoogleDriveClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("LINKEDIN", c.LinkedInClientID, c.LinkedInClientSecret); err != nil {
		return err
	}

	return nil
}

func (c *Config) validateOptionalPlatform(name, id, secret string) error {
	if id == "" && secret == "" {
		return nil
	}
	if secret == "" {
		return fmt.Errorf("%s_CLIENT_SECRET is required when %s_CLIENT_ID is set (or unset both to disable the platform)", name, name)
	}
	if len(secret) < secretMinChars {
		return fmt.Errorf("%s_CLIENT_SECRET must be at least %d characters (got %d)", name, secretMinChars, len(secret))
	}
	return nil
}

// resolveEncryptionConfig normalises the two encryption surfaces
// (legacy single ENCRYPTION_KEY vs multi-key ENCRYPTION_KEYS +
// ACTIVE_ENCRYPTION_KEY_ID) into the unified EncryptionKeys map +
// ActiveEncryptionKeyID uint32 fields. It also rejects the
// ambiguous "both set" case and the "neither set" case.
//
// The matrix of valid inputs:
//
//	ENCRYPTION_KEY only             → EncryptionKeys={1: key}, ActiveID=1
//	ENCRYPTION_KEYS + ACTIVE_*      → parsed CSV, validated, verified
//	ENCRYPTION_KEY  + ENCRYPTION_KEYS → rejected (ambiguous)
//	(neither)                       → rejected (required)
//
// The CSV format is `id:base64key,id:base64key,...`. Each entry
// must be valid base64 and decode to exactly 32 bytes (AES-256).
// Duplicate ids in the CSV are rejected (would silently overwrite
// in a map literal, defeating the rotation audit log).
//
// On success, the post-validated fields are c.EncryptionKeys and
// c.ActiveEncryptionKeyID. The legacy c.EncryptionKey field is
// preserved (NOT cleared) so the rest of the codebase that still
// reads it for telemetry/diagnostics continues to work.
func (c *Config) resolveEncryptionConfig() error {
	hasLegacy := c.EncryptionKey != ""
	hasMulti := c.EncryptionKeysRaw != ""

	switch {
	case hasLegacy && hasMulti:
		return fmt.Errorf("ambiguous encryption config: set EITHER ENCRYPTION_KEY OR ENCRYPTION_KEYS+ACTIVE_ENCRYPTION_KEY_ID, not both")
	case !hasLegacy && !hasMulti:
		return fmt.Errorf("ENCRYPTION_KEY is required (or set ENCRYPTION_KEYS+ACTIVE_ENCRYPTION_KEY_ID for multi-key mode)")
	case hasLegacy:
		// Legacy single-key path: promote the single key into the
		// unified map under id=1. Re-validate the same shape the
		// pre-Blocco #2.2 validate() did (base64 + 32 bytes) so
		// operators get the same error messages.
		if err := validateSingleBase64Key(c.EncryptionKey); err != nil {
			return fmt.Errorf("ENCRYPTION_KEY: %w", err)
		}
		c.EncryptionKeys = map[uint32]string{1: c.EncryptionKey}
		c.ActiveEncryptionKeyID = 1
		return nil
	default: // hasMulti
		keys, err := parseEncryptionKeysCSV(c.EncryptionKeysRaw)
		if err != nil {
			return fmt.Errorf("ENCRYPTION_KEYS: %w", err)
		}
		active, err := parseActiveKeyID(c.ActiveEncryptionKeyIDRaw)
		if err != nil {
			return fmt.Errorf("ACTIVE_ENCRYPTION_KEY_ID: %w", err)
		}
		if _, ok := keys[active]; !ok {
			return fmt.Errorf("ACTIVE_ENCRYPTION_KEY_ID=%d not in ENCRYPTION_KEYS (have %v)", active, SortedKeyIDs(keys))
		}
		c.EncryptionKeys = keys
		c.ActiveEncryptionKeyID = active
		return nil
	}
}

// validateSingleBase64Key checks a single key string is valid
// base64 and decodes to exactly 32 bytes (AES-256). Extracted so
// the legacy path and the multi-key path share one source of
// truth for the length check (so a future "AES-128" support
// change touches one function, not two).
//
// Error-message contract (Blocco #2.2 follow-up): the test suite
// (TestValidate_EncryptionKeyLength) pins the operator-facing
// shape: the error MUST contain both the actual length ("got N")
// AND the expected shape ("exactly 32 bytes"). Changing either
// substring is a contract change that requires updating the
// tests in the same commit.
func validateSingleBase64Key(s string) error {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("not valid base64: %w", err)
	}
	if len(raw) != aesKeyBytes {
		return fmt.Errorf("must be exactly 32 bytes (got %d)", len(raw))
	}
	return nil
}

// parseEncryptionKeysCSV parses the ENCRYPTION_KEYS env var
// ("id:base64key,id:base64key,...") into a map[uint32]string with
// every key validated (base64 + 32 bytes). Duplicate ids are
// rejected so an operator typo (e.g. "1:key1,1:key2") doesn't
// silently overwrite the first entry — that would defeat the
// rotation audit log.
func parseEncryptionKeysCSV(s string) (map[uint32]string, error) {
	out := make(map[uint32]string)
	for i, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("entry %d is empty (trailing comma or extra whitespace?)", i+1)
		}
		colon := strings.IndexByte(entry, ':')
		if colon <= 0 || colon == len(entry)-1 {
			return nil, fmt.Errorf("entry %d (%q) must be in the form 'id:base64key'", i+1, entry)
		}
		idStr := strings.TrimSpace(entry[:colon])
		keyStr := strings.TrimSpace(entry[colon+1:])
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("entry %d: key id %q is not a uint32: %w", i+1, idStr, err)
		}
		if err := validateSingleBase64Key(keyStr); err != nil {
			return nil, fmt.Errorf("entry %d (id=%d): %w", i+1, uint32(id), err)
		}
		if _, dup := out[uint32(id)]; dup {
			return nil, fmt.Errorf("entry %d: duplicate key id %d in ENCRYPTION_KEYS", i+1, id)
		}
		out[uint32(id)] = keyStr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ENCRYPTION_KEYS is empty (set at least one id:base64key entry)")
	}
	return out, nil
}

// parseActiveKeyID parses the ACTIVE_ENCRYPTION_KEY_ID env var.
// Empty is rejected (the operator must pick a key — silently
// defaulting to 1 would re-introduce a class of "I forgot to set
// the active id" bugs).
func parseActiveKeyID(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("ACTIVE_ENCRYPTION_KEY_ID is required when ENCRYPTION_KEYS is set")
	}
	id, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a uint32: %w", s, err)
	}
	return uint32(id), nil
}

// SortedKeyIDs returns the key ids in ascending order, exported so
// internal/bootstrap can log them at startup as a diagnostic
// breadcrumb (operators can confirm "the active id is 2, and the
// key map has ids 1 and 2" by reading the boot log).
func SortedKeyIDs(m map[uint32]string) []uint32 {
	out := make([]uint32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// DSN returns the PostgreSQL connection string.
func (c *Config) DSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off", "":
			return false
		}
	}
	return fallback
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

// validateSentryDSN parses the DSN as a URL and asserts the canonical
// Sentry shape (scheme, key@host, project path). Empty input is
// rejected upstream by the caller's guard so we can assume non-empty
// here. Format errors return with both the underlying url.Parse error
// AND the original DSN so the operator can copy/paste the failing
// value into their tooling.
//
// Scheme allowance: https is always accepted. http is accepted ONLY
// when appEnv is "dev" or "staging" — production deployments reject
// unencrypted DSN at boot so an operator typo doesn't accidentally
// ship PII-tinted stack traces to a cleartext endpoint. The
// self-hosted Sentry dev path (`make dev` → docker-compose Sentry on
// http://localhost:9000/1) is unblocked by this gating.
func validateSentryDSN(dsn, appEnv string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w (dsn=%q)", err, dsn)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("scheme must be http or https (got %q, dsn=%q)", u.Scheme, dsn)
	}
	if u.Scheme == "http" && appEnv == "production" {
		return fmt.Errorf("scheme=http is not allowed in production (use https, dsn=%q)", dsn)
	}
	if u.User == nil {
		return fmt.Errorf("missing public key (expected https://<key>@host/<project>, got %q)", dsn)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host (dsn=%q)", dsn)
	}
	if u.Path == "" || u.Path == "/" {
		return fmt.Errorf("missing project id (expected https://<key>@host/<project>, got %q)", dsn)
	}
	return nil
}
