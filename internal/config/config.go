package config

import (
	"fmt"
)

// Minimum-credential thresholds.
const (
	jwtSecretMinBytes        = 32
	aesKeyBytes              = 32
	secretMinChars           = 32
	adminInviteTokenMinChars = 32 // ADMIN_INVITE_TOKEN: prevent trivial brute-force if the operator accidentally sets a short value
)

// DatabaseConfig holds PostgreSQL configuration.
type DatabaseConfig struct {
	// DatabaseURL for production; individual fields (DB_HOST, DB_PORT,
	// DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE) are kept for local
	// tooling. DATABASE_URL takes precedence.
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

	// ExpectedInstallationUUID pins this process to the PostgreSQL
	// installation it was configured for. Production and staging
	// require EXPECTED_DATABASE_INSTALLATION_UUID; local dev may leave
	// it empty while the migration bootstrap creates the identity row.
	ExpectedInstallationUUID string

	// Explicit database/sql pool sizing. The total across API and worker
	// processes must remain below PostgreSQL max_connections.
	DBMaxOpenConns           int
	DBMaxIdleConns           int
	DBConnMaxLifetimeSeconds int
	DBConnMaxIdleTimeSeconds int
}

// DSN returns the PostgreSQL connection string.
func (c *DatabaseConfig) DSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// StorageConfig holds S3-compatible storage and upload-related settings.
type StorageConfig struct {
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

	// GoogleDriveAPIKey is a Google Cloud API key used to list CONTENTS
	// of a public Drive folder when the user has not linked their Drive
	// account. Without it, batch folder imports only work for folders
	// the linked Drive account can access.
	GoogleDriveAPIKey string

	// GoogleDriveUploadFolderID is the optional default Drive folder ID
	// for uploads created via the Google Drive delivery adapter.
	GoogleDriveUploadFolderID string
}

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

	// Google Drive OAuth (read-only import of video clips)
	GoogleDriveClientID     string
	GoogleDriveClientSecret string
	GoogleDriveRedirectURI  string

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
	// rotate via `flyctl secrets import`. NOT logged, NOT exposed
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

// VeloxConfig holds the Velox integration secrets.
type VeloxConfig struct {
	// VeloxAPIToken authenticates artifact HEAD/GET requests back to Velox.
	// This is the REVERSE direction token (Velox → InstaEdit via
	// /internal/v1/* routes). Loaded from VELOX_API_TOKEN.
	VeloxAPIToken string

	// VeloxControlURL is the base URL of the Velox master that the
	// BFF calls when proxying user-facing /api/v1/velox/* requests.
	// The browser never sees this URL — only InstaEdit calls it.
	// Loaded from VELOX_CONTROL_URL. Empty = BFF routes not mounted.
	VeloxControlURL string

	// VeloxControlJWTSecret is the shared HS256 secret for the
	// short-lived JWT InstaEdit signs when calling the Velox master.
	// This MUST be the same value as VeloxEditiingg's
	// INSTAEDIT_CONTROL_JWT_SECRET. It is DISTINCT from
	// VeloxAPIToken (the reverse-direction Bearer token) — the two
	// secrets MUST NOT be reused across directions. Loaded from
	// VELOX_CONTROL_JWT_SECRET. Empty = BFF routes not mounted.
	VeloxControlJWTSecret string

	// VeloxWebhookSecret is the shared HMAC-SHA256 secret used to
	// sign callbacks sent from InstaEdit to Velox. It is distinct
	// from VeloxAPIToken and VeloxControlJWTSecret. Loaded from
	// VELOX_WEBHOOK_SECRET.
	VeloxWebhookSecret string
}

// AIConfig holds AI/ML provider secrets for metadata generation,
// translations, and other AI-assisted features. These keys are
// server-side only — NEVER exposed to the frontend bundle, logs,
// or localStorage. The absence of these keys MUST NOT block manual
// metadata entry or the YouTube publish flow.
type AIConfig struct {
	// NVIDIAAPIKey authenticates calls to the NVIDIA AI API for
	// generating title, description, tags, and translations.
	// Loaded from NVIDIA_API_KEY. Empty = AI metadata generation
	// unavailable (fallback: manual entry in Dark Editor).
	NVIDIAAPIKey string
}

// MonitoringConfig holds observability and metrics configuration.
type MonitoringConfig struct {
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
	SentryDSN string
	// SENTRY_ENVIRONMENT defaults to AppEnv ("dev"/"staging"/"production")
	// when empty; SENTRY_RELEASE is passed straight through to the SDK.
	SentryEnvironment string
	SentryRelease     string

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
}

// HTTPConfig holds HTTP/server surface configuration.
type HTTPConfig struct {
	// FrontendURL is where the OAuth callback should redirect.
	FrontendURL string
	// EditorURL is the base URL of the dark editor SPA. When empty,
	// FrontendURL is used as a fallback.
	EditorURL string
	// AllowedCORSOrigins is the comma-separated list of origins.
	AllowedCORSOrigins []string
	// CookieDomain is the optional `Domain` attribute applied to the
	// csrf_token cookie ONLY (session + refresh cookies stay host-only).
	CookieDomain string
	// LogLevel is one of "debug" or "info" (default "info").
	LogLevel string
	// AppEnv is the deployment environment: dev|staging|production.
	AppEnv string
}

// WorkerConfig holds background-worker tuning parameters.
type WorkerConfig struct {
	// PublishWorkerIntervalSeconds is the cadence of the publish worker.
	PublishWorkerIntervalSeconds int
	// ReconcileWorkerIntervalSeconds is the cadence of the reconcile worker.
	ReconcileWorkerIntervalSeconds int
	// WebhookWorkerIntervalSeconds is the cadence of the webhook worker.
	WebhookWorkerIntervalSeconds int
	// SessionCleanupIntervalSeconds is the cadence of the sessions cleanup worker.
	SessionCleanupIntervalSeconds int
	// AssetCleanupIntervalSeconds is the cadence of the media-asset
	// cleanup worker. Drives the periodic AssetCleanupWorker that
	// DELETEs rows from media_assets whose YouTube publish + post
	// pipeline has fully completed AND aged past
	// VideoRetentionBufferDays. Cadence is intentionally coarse
	// (default 86400s = 24h) because the cleanup predicate is
	// multi-table and benefits from a snapshot read; under typical
	// load a daily sweep keeps the S3 footprint bounded without
	// thrashing Postgres with DELETE/.../USING queries. Operators
	// wanting more aggressive space reclamation can lower to e.g.
	// 3600 (hourly) — set ASSET_CLEANUP_INTERVAL_SECONDS.
	AssetCleanupIntervalSeconds int
	// UploadWorkerIntervalSeconds is the cadence of the upload worker.
	UploadWorkerIntervalSeconds int
	// RenderMaxConcurrency is the global process limit for ffmpeg/ffprobe
	// and future CPU-heavy media renders. It is intentionally independent
	// from upload goroutine counts.
	RenderMaxConcurrency int
	// FFmpegThreads is the explicit per-process thread budget reserved for
	// future ffmpeg commands admitted by the render registry. ffprobe uses
	// the same process budget but does not support ffmpeg's -threads flag.
	FFmpegThreads int
	// UploadIngestConcurrency is the number of ingest goroutines.
	UploadIngestConcurrency int
	// YouTubeUploadConcurrency is the number of YouTube upload goroutines.
	YouTubeUploadConcurrency int
	// UploadLeaseTTLSeconds is the lease TTL for upload jobs.
	UploadLeaseTTLSeconds int
	// UploadHeartbeatIntervalSeconds is the heartbeat interval for upload jobs.
	UploadHeartbeatIntervalSeconds int
	// UploadReclaimIntervalSeconds is the reclaim interval for stale uploads.
	UploadReclaimIntervalSeconds int
	// UploadReclaimOnStart controls whether stale uploads are reclaimed at startup.
	UploadReclaimOnStart bool
	// UploadEmptyQueueBackoffMinSeconds is the initial delay after an
	// empty upload queue claim. Defaults to 1 second.
	UploadEmptyQueueBackoffMinSeconds int
	// UploadEmptyQueueBackoffMaxSeconds caps the empty-queue backoff.
	// Defaults to 30 seconds.
	UploadEmptyQueueBackoffMaxSeconds int
	// YouTubeUploadChunkBytes is the resumable upload chunk size.
	YouTubeUploadChunkBytes int64
	// YouTubeUploadMaxRetries is the per-chunk PUT retry budget.
	YouTubeUploadMaxRetries int
	// YouTubeUploadBackoffBaseMs is the base backoff in milliseconds.
	YouTubeUploadBackoffBaseMs int
	// YouTubeUploadBackoffCapMs is the backoff cap in milliseconds.
	YouTubeUploadBackoffCapMs int
	// YouTubeDailyQuotaLimit is the daily videos.insert quota cap.
	YouTubeDailyQuotaLimit int
	// YouTubeGroupVideosMaxAccounts caps group-video fan-out size.
	YouTubeGroupVideosMaxAccounts int
	// YouTubeGroupVideosMaxVideos caps the aggregate group-video projection.
	YouTubeGroupVideosMaxVideos int
	// YouTubeGroupVideosCacheTTLSeconds controls the short-lived per-account cache.
	YouTubeGroupVideosCacheTTLSeconds int
	// YouTubeGroupVideosDefaultPageSize is the default response page size.
	YouTubeGroupVideosDefaultPageSize int
	// TokenRefreshSweepIntervalSeconds is the cadence of the token
	// refresh sweep worker — renews dormant OAuth grants (last
	// refresh older than TokenRefreshSweepHorizonDays, or provider
	// TTL within 7 days) so Google's ~6-month refresh-token
	// inactivity garbage collection never kills a rarely-publishing
	// channel. Default 86400 (24h): the risk horizon is ~6 months,
	// so even a weekly cadence would keep every grant inside the
	// activity window. Env TOKEN_REFRESH_SWEEP_INTERVAL_SECONDS.
	TokenRefreshSweepIntervalSeconds int
	// TokenRefreshSweepHorizonDays is the inactivity lookahead: a
	// grant whose last_refresh_at (or created_at when never
	// refreshed) is older than this is renewed. Default 120 (~4
	// months — 2 months of margin under Google's 6-month GC). Env
	// TOKEN_REFRESH_SWEEP_HORIZON_DAYS.
	TokenRefreshSweepHorizonDays int
	// SnapshotRefreshSweepIntervalSeconds is the cadence of the
	// snapshot refresh sweep worker — drains accounts whose cached
	// resource snapshot is stale (refresh_pending_at stamped by the
	// read path serving a cached snapshot) and refreshes them in the
	// background with bounded concurrency (4), so opening a channel
	// page NEVER triggers a provider call (strict rule). Default 60s:
	// a page load stamps pending and the worker refreshes within a
	// minute. Env SNAPSHOT_REFRESH_SWEEP_INTERVAL_SECONDS.
	SnapshotRefreshSweepIntervalSeconds int
	// PublishHorizonDays (Blocco #2 P0) caps how far in the future a
	// user/operator can schedule a publish. Used by:
	//   - uploads_handlers.go::handleRescheduleUpload (drag-drop reject
	//     when publish_at > now+horizon),
	//   - drive_batch_v2_handlers.go::handleDriveBatchImportV2 (HARD 422
	//     when the projected worst-case horizon > cap),
	//   - the /api/v1/health response (so the SPA can render the cap).
	// Default 30 = env PUBLISH_HORIZON_DAYS. Operators wanting a longer
	// horizon (e.g. annual content calendars) bump this without a redeploy.
	PublishHorizonDays int
	// VideoRetentionBufferDays (Blocco #2 P0) is the post-publish tail
	// for media_assets.expires_at. Formula:
	//   - with publish_at: max(now+1d, publish_at + buffer)
	//   - without publish_at: now + PublishHorizonDays
	// The 1-day min-floor keeps a slow uploader from racing /complete
	// (returns 410 when asset is already expired). Default 7 = env
	// VIDEO_RETENTION_BUFFER_DAYS. Smaller values free S3 space faster;
	// larger values give the retry worker more slack.
	VideoRetentionBufferDays int
}

// Config holds all configuration for the application.
//
// Taglio 5b: SERVER_PORT + SERVER_HOST removed — the server listens on the
// PORT env var only (Vercel / Railway / Render standard). TWITTER_* env vars
// renamed to X_*; TIKTOK_CLIENT_KEY renamed to TIKTOK_CLIENT_ID.
type Config struct {
	// VeloxAPIToken authenticates artifact HEAD/GET requests back to Velox.
	// This is the REVERSE direction token (Velox → InstaEdit via
	// /internal/v1/* routes). Loaded from VELOX_API_TOKEN.

	// VeloxControlURL is the base URL of the Velox master that the
	// BFF calls when proxying user-facing /api/v1/velox/* requests.
	// The browser never sees this URL — only InstaEdit calls it.
	// Loaded from VELOX_CONTROL_URL. Empty = BFF routes not mounted.

	// VeloxControlJWTSecret is the shared HS256 secret for the
	// short-lived JWT InstaEdit signs when calling the Velox master.
	// This MUST be the same value as VeloxEditiingg's
	// INSTAEDIT_CONTROL_JWT_SECRET. It is DISTINCT from
	// VeloxAPIToken (the reverse-direction Bearer token) — the two
	// secrets MUST NOT be reused across directions. Loaded from
	// VELOX_CONTROL_JWT_SECRET. Empty = BFF routes not mounted.

	// VeloxWebhookSecret is the shared HMAC-SHA256 secret used to
	// sign callbacks sent from InstaEdit to Velox. It is distinct
	// from VeloxAPIToken and VeloxControlJWTSecret. Loaded from
	// VELOX_WEBHOOK_SECRET.
	// FrontendURL is where the OAuth callback should redirect.
	// AllowedCORSOrigins is the comma-separated list of origins.

	// Database (PostgreSQL).
	Database DatabaseConfig

	// Storage (S3-compatible + Google Drive upload folder).
	Storage StorageConfig

	// Auth (OAuth + JWT + security tokens).
	Auth AuthConfig

	// Velox integration secrets.
	Velox VeloxConfig

	// AI/ML provider secrets (NVIDIA, etc.).
	AI AIConfig

	// Monitoring (Sentry + metrics).
	Monitoring MonitoringConfig

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
	// TrustedProxies is a comma-separated list of IP addresses and/or
	// CIDR ranges that are allowed to supply X-Forwarded-For /
	// X-Real-IP headers. When empty, the API trusts only the direct
	// peer address (RemoteAddr). Example: "10.0.0.0/8,127.0.0.1".

	// Metrics basic-auth credentials. In production both must be set;
	// validate() fail-closes the boot if either is empty.
	// MetricsHost/MetricsPort optionally start a separate internal
	// listener for the /metrics endpoint. When MetricsPort is 0, the
	// endpoint is served only on the main HTTP server at
	// /api/v1/metrics. When MetricsPort > 0, an additional listener
	// is started on MetricsHost:MetricsPort (default MetricsHost
	// 127.0.0.1 if empty) so scrapers on a private network can reach
	// metrics without exposing the main API.

	// Deprecated: JWT_TTL_HOURS is the legacy single-knob TTL.
	// If JWT_ACCESS_TTL_MINUTES is unset, the hours value is
	// converted to minutes. Prefer the explicit access/refresh
	// variables for new deployments.

	// Logging

	// AppEnv is the deployment environment.

	// Background worker tuning.
	// Taglio 5.x — independent tick interval for the new
	// ReconcileWorker goroutine. The driver (PublishWorker) ticks
	// at PublishWorkerIntervalSeconds (default 30s); the reconciler
	// ticks faster (default 5s) so an async publish's
	// publishing→published transition is observed promptly without
	// coupling to the driver's cadence. Both run as separate
	// goroutines on independent contexts with parallel shutdown.
	// SPRINT 4.2 — independent tick interval for the WebhookWorker
	// goroutine. Drains the webhook_deliveries table every
	// WEBHOOK_WORKER_INTERVAL_SECONDS (default 5s). Faster than
	// the publish driver so an end-to-end delivery latency is
	// bounded by a 1-2s ceiling under typical load. Same
	// lifecycle shape: independent goroutine, ctx-cancellable,
	// drained in parallel on shutdown.

	// SessionCleanupIntervalSeconds — cadence of the retention
	// policy goroutine (commit: cleanup-policy). Drives the
	// periodic SessionsCleanupWorker that DELETEs rows from the
	// `sessions` table whose revoked_at is older than 30 days OR
	// whose refresh_expires_at is older than 7 days. Default 300s
	// (5 min) is coarse enough to not thrash the DB under traffic
	// spikes but fine-grained enough to keep the sessions table
	// bounded under normal load.

	// UploadWorkerIntervalSeconds — cadence of the background upload
	// worker that drains upload_jobs (public or authenticated Google
	// Drive imports). Default 30s.

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

	// AdminInviteToken gates the public registration endpoint
	// (POST /api/v1/auth/register). The handler requires the request
	// to present the same value via the X-Admin-Token header
	// (constant-time compare). When empty, registration is fully
	// disabled (the handler returns 403 "registration is
	// invite-only"). Generate with `openssl rand -hex 32` and
	// rotate via `flyctl secrets import`. NOT logged, NOT exposed
	// in error messages.

	// AppMode lets operators pin the deployment to Google's OAuth-
	// consent-screen publishing status. "production" means refresh
	// tokens are durable (no automatic 7-day expiry). "testing"
	// means Google's Testing-mode 7-day refresh-token TTL applies
	// and any refresh attempt after day 7 returns invalid_grant.
	// Default "production" so a missing env var falls into the safer
	// bucket; ops must explicitly opt-in to "testing" when validating
	// against a staging OAuth-client. Loaded from env APP_MODE.
	AppMode string
	// HTTP/server surface configuration.
	HTTP HTTPConfig
	// Background worker tuning.
	Worker WorkerConfig
}
