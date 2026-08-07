package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Minimum-credential thresholds used by configuration validation.
const (
	jwtSecretMinBytes        = 32
	aesKeyBytes              = 32
	secretMinChars           = 32
	adminInviteTokenMinChars = 32
)

// metricsConfigured returns true only when both metrics basic-auth
// credentials are non-empty. It is used for both runtime fail-closed
// decisions and boot-time validation in production.
func (c *Config) metricsConfigured() bool {
	return c.Monitoring.MetricsBasicAuthUser != "" && c.Monitoring.MetricsBasicAuthPass != ""
}

func validateDBPoolProfile(name string, p DBPoolProfile) error {
	if p.MaxOpenConns < 1 {
		return fmt.Errorf("%s_MAX_OPEN_CONNS must be positive (got %d)", name, p.MaxOpenConns)
	}
	if p.MaxIdleConns < 0 || p.MaxIdleConns > p.MaxOpenConns {
		return fmt.Errorf("%s_MAX_IDLE_CONNS must be between 0 and %s_MAX_OPEN_CONNS (%d) (got %d)", name, name, p.MaxOpenConns, p.MaxIdleConns)
	}
	if p.ConnMaxLifetimeSeconds < 1 {
		return fmt.Errorf("%s_CONN_MAX_LIFETIME_SECONDS must be positive (got %d)", name, p.ConnMaxLifetimeSeconds)
	}
	if p.ConnMaxIdleTimeSeconds < 1 {
		return fmt.Errorf("%s_CONN_MAX_IDLE_TIME_SECONDS must be positive (got %d)", name, p.ConnMaxIdleTimeSeconds)
	}
	return nil
}

func (c *Config) validate() error {
	// Metrics are fail-closed in production: missing or incomplete
	// basic-auth credentials prevent the process from booting. This
	// keeps /api/v1/metrics from ever being served publicly in prod.
	if c.HTTP.AppEnv == "production" && !c.metricsConfigured() {
		return fmt.Errorf("METRICS_BASIC_AUTH_USER and METRICS_BASIC_AUTH_PASS are required in production")
	}

	switch c.HTTP.AppEnv {
	case "dev", "staging", "production":
	default:
		return fmt.Errorf("APP_ENV must be one of dev|staging|production (got %q)", c.HTTP.AppEnv)
	}

	// APP_MODE is the explicit mirror of Google's OAuth consent-screen
	// publishing status used by the refresh-token policy and test seams.
	// Keep "dev" valid for existing local environments, but reject
	// misspellings so a deployment cannot silently select an unintended
	// refresh-token policy. Only "testing" models Google's seven-day
	// Testing-mode refresh-token expiry; "production" is the durable mode.
	switch mode := strings.ToLower(strings.TrimSpace(c.AppMode)); mode {
	case "":
		// Config values assembled directly by older tests/callers may omit
		// AppMode. Preserve the Load() default rather than rejecting an
		// otherwise valid configuration; an explicitly unknown value still
		// fails closed below.
		c.AppMode = "production"
	case "dev", "testing", "production":
		c.AppMode = mode
	default:
		return fmt.Errorf("APP_MODE must be one of dev|testing|production (got %q)", c.AppMode)
	}

	// Database: DATABASE_URL takes precedence; individual params fallback.
	// Pool limits are explicit and fail closed to avoid silently creating
	// too many connections. Zero idle connections is valid and means
	// callers intentionally disable the idle connection cache; Load()
	// supplies the normal default of five when the environment is unset.
	if c.Database.DBMaxOpenConns == 0 {
		c.Database.DBMaxOpenConns = 25
	}
	if c.Database.DBConnMaxLifetimeSeconds == 0 {
		c.Database.DBConnMaxLifetimeSeconds = 1800
	}
	if c.Database.DBConnMaxIdleTimeSeconds == 0 {
		c.Database.DBConnMaxIdleTimeSeconds = 300
	}
	if c.Database.DBMaxOpenConns < 1 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be positive (got %d)", c.Database.DBMaxOpenConns)
	}
	if c.Database.DBMaxIdleConns < 0 || c.Database.DBMaxIdleConns > c.Database.DBMaxOpenConns {
		return fmt.Errorf("DB_MAX_IDLE_CONNS must be between 0 and DB_MAX_OPEN_CONNS (%d) (got %d)", c.Database.DBMaxOpenConns, c.Database.DBMaxIdleConns)
	}
	if c.Database.DBConnMaxLifetimeSeconds < 1 {
		return fmt.Errorf("DB_CONN_MAX_LIFETIME_SECONDS must be positive (got %d)", c.Database.DBConnMaxLifetimeSeconds)
	}
	if c.Database.DBConnMaxIdleTimeSeconds < 1 {
		return fmt.Errorf("DB_CONN_MAX_IDLE_TIME_SECONDS must be positive (got %d)", c.Database.DBConnMaxIdleTimeSeconds)
	}
	if c.Database.DBPoolRole != "" {
		switch c.Database.DBPoolRole {
		case DBPoolRoleAPI, DBPoolRoleWorker, DBPoolRoleMaintenance:
		default:
			return fmt.Errorf("DB_POOL_ROLE must be one of api|worker|maintenance (got %q)", c.Database.DBPoolRole)
		}
	}
	for name, profile := range map[string]DBPoolProfile{
		"DB_API": c.Database.DBAPI, "DB_WORKER": c.Database.DBWorker,
		"DB_MAINTENANCE": c.Database.DBMaintenance,
	} {
		if profile.isZero() {
			continue
		}
		if err := validateDBPoolProfile(name, profile); err != nil {
			return err
		}
	}
	if c.Database.DatabaseURL == "" {
		if c.Database.DBPassword == "" {
			return fmt.Errorf("DB_PASSWORD is required (or set DATABASE_URL)")
		}
	}
	// Persistent deployments must pin the process to one known database
	// installation. The migration creates the singleton identity row;
	// runtime verification is performed after migrations (or immediately
	// by API/worker processes). Local dev remains opt-in for convenience.
	if c.HTTP.AppEnv == "production" || c.HTTP.AppEnv == "staging" {
		if strings.TrimSpace(c.Database.ExpectedInstallationUUID) == "" {
			return fmt.Errorf("EXPECTED_DATABASE_INSTALLATION_UUID is required in %s", c.HTTP.AppEnv)
		}
		if _, err := uuid.Parse(strings.TrimSpace(c.Database.ExpectedInstallationUUID)); err != nil {
			return fmt.Errorf("EXPECTED_DATABASE_INSTALLATION_UUID must be a valid UUID")
		}
	}

	// S3-compatible storage (mandatory).
	if c.Storage.S3Endpoint == "" {
		return fmt.Errorf("S3_ENDPOINT is required")
	}
	if c.Storage.S3Bucket == "" {
		return fmt.Errorf("S3_BUCKET is required")
	}
	if c.Storage.S3AccessKey == "" {
		return fmt.Errorf("S3_ACCESS_KEY is required")
	}
	if c.Storage.S3SecretKey == "" {
		return fmt.Errorf("S3_SECRET_KEY is required")
	}

	// Meta OAuth (optional).
	if c.Auth.MetaAppID == "" && c.Auth.MetaAppSecret == "" {
		// platform disabled
	} else if c.Auth.MetaAppID == "" {
		return fmt.Errorf("META_APP_ID is required when META_APP_SECRET is set (or unset both)")
	} else if c.Auth.MetaAppSecret == "" {
		return fmt.Errorf("META_APP_SECRET is required when META_APP_ID is set (or unset both)")
	} else if len(c.Auth.MetaAppSecret) < secretMinChars {
		return fmt.Errorf("META_APP_SECRET must be at least %d characters (got %d)", secretMinChars, len(c.Auth.MetaAppSecret))
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
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required (must be at least %d bytes)", jwtSecretMinBytes)
	}
	if len(c.Auth.JWTSecret) < jwtSecretMinBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes for HS256 (got %d)", jwtSecretMinBytes, len(c.Auth.JWTSecret))
	}

	// Sentry (Blocco #5.3 — optional). When SET, validate the DSN
	// shape so a typo at boot surfaces as a process-exit rather than
	// a silent no-op at the first panic render. When UNSET, no
	// validation; the absence is the signal the operator gave us to
	// disable the observability surface.
	if c.Monitoring.SentryDSN != "" {
		if err := validateSentryDSN(c.Monitoring.SentryDSN, c.HTTP.AppEnv); err != nil {
			return fmt.Errorf("SENTRY_DSN: %w", err)
		}
		// Defaults: if the operator set SENTRY_DSN but didn't supply
		// an environment label, derive it from AppEnv so the SDK
		// dashboard tags events correctly. Empty Release is fine —
		// the SDK emits events with no release tag (still useful).
		if c.Monitoring.SentryEnvironment == "" {
			c.Monitoring.SentryEnvironment = c.HTTP.AppEnv
		}
	}

	// Optional OAuth platforms.
	if err := c.validateOptionalPlatform("TIKTOK", c.Auth.TikTokClientID, c.Auth.TikTokClientSecret); err != nil {
		return err
	}

	// Admin invite token: empty disables registration (per WithAdminInviteToken
	// contract); non-empty must be long enough to make online brute-force
	// impractical. Mirrors the JWT secret's 32-byte threshold so a
	// generated `openssl rand -hex 32` (64 hex chars) sails through and
	// a 4-char typo is rejected at boot rather than exploited at runtime.
	if c.Auth.AdminInviteToken != "" && len(c.Auth.AdminInviteToken) < adminInviteTokenMinChars {
		return fmt.Errorf("ADMIN_INVITE_TOKEN must be at least %d characters when set (got %d); generate with `openssl rand -hex 32` (64 hex chars) or leave it unset to disable registration entirely", adminInviteTokenMinChars, len(c.Auth.AdminInviteToken))
	}
	if err := c.validateOptionalPlatform("X", c.Auth.XClientID, c.Auth.XClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("YOUTUBE", c.Auth.YouTubeClientID, c.Auth.YouTubeClientSecret); err != nil {
		return err
	}
	if err := c.validateYouTubeOAuthClientPool(); err != nil {
		return err
	}
	if c.Auth.YouTubeClientID != "" {
		if err := validateYouTubeRedirectURI(c.Auth.YouTubeRedirectURI, c.HTTP.AppEnv); err != nil {
			return err
		}
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
	if c.Auth.YouTubeClientID != "" {
		if c.Worker.YouTubeGroupVideosMaxAccounts <= 0 {
			return fmt.Errorf("YOUTUBE_GROUP_VIDEOS_MAX_ACCOUNTS must be positive (got %d)", c.Worker.YouTubeGroupVideosMaxAccounts)
		}
		if c.Worker.YouTubeGroupVideosMaxVideos <= 0 {
			return fmt.Errorf("YOUTUBE_GROUP_VIDEOS_MAX_VIDEOS must be positive (got %d)", c.Worker.YouTubeGroupVideosMaxVideos)
		}
		if c.Worker.YouTubeGroupVideosDefaultPageSize <= 0 {
			return fmt.Errorf("YOUTUBE_GROUP_VIDEOS_DEFAULT_PAGE_SIZE must be positive (got %d)", c.Worker.YouTubeGroupVideosDefaultPageSize)
		}
		if c.Worker.YouTubeGroupVideosDefaultPageSize > c.Worker.YouTubeGroupVideosMaxVideos {
			return fmt.Errorf("YOUTUBE_GROUP_VIDEOS_DEFAULT_PAGE_SIZE (%d) must be <= YOUTUBE_GROUP_VIDEOS_MAX_VIDEOS (%d)", c.Worker.YouTubeGroupVideosDefaultPageSize, c.Worker.YouTubeGroupVideosMaxVideos)
		}
		if c.Worker.YouTubeGroupVideosCacheTTLSeconds < 0 {
			return fmt.Errorf("YOUTUBE_GROUP_VIDEOS_CACHE_TTL_SECONDS must not be negative (got %d)", c.Worker.YouTubeGroupVideosCacheTTLSeconds)
		}
		if c.Worker.YouTubeUploadChunkBytes <= 0 || c.Worker.YouTubeUploadChunkBytes%262144 != 0 {
			return fmt.Errorf("YOUTUBE_UPLOAD_CHUNK_BYTES must be a positive multiple of 256 KB (262144 bytes); got %d (default 16777216 = 16 MB)", c.Worker.YouTubeUploadChunkBytes)
		}
		// Blocco #2 P0 — publish horizon + retention buffer must be
		// positive; 0 would let the worker compute expires_at in
		// the past and /complete would 410 every upload. Operators
		// wanting to disable the gating should bump to a large value
		// (e.g. 365) instead of 0 — the spam-prevention here is
		// explicit (no implicit "disable by zero").
		if c.Worker.PublishHorizonDays <= 0 {
			return fmt.Errorf("PUBLISH_HORIZON_DAYS must be a positive integer (got %d); set to 365 to effectively disable the cap, not 0", c.Worker.PublishHorizonDays)
		}
		if c.Worker.VideoRetentionBufferDays <= 0 {
			return fmt.Errorf("VIDEO_RETENTION_BUFFER_DAYS must be a positive integer (got %d); set to a large value (e.g. 90) to extend the tail, not 0", c.Worker.VideoRetentionBufferDays)
		}
		// Asset cleanup interval must be positive; 0 would spin the
		// ticker hot-loop and DDoS Postgres. Operators wanting to
		// effectively disable the schedule should set a very large
		// value (e.g. 86400*365) and not set 0 — the explicit-default
		// shape mirrors PublishHorizonDays + VideoRetentionBufferDays.
		if c.Worker.AssetCleanupIntervalSeconds <= 0 {
			return fmt.Errorf("ASSET_CLEANUP_INTERVAL_SECONDS must be a positive integer (got %d); set to a large value (e.g. 86400) to effectively disable frequent sweeps, not 0", c.Worker.AssetCleanupIntervalSeconds)
		}
		if c.Worker.YouTubeUploadMaxRetries < 1 {
			return fmt.Errorf("YOUTUBE_UPLOAD_MAX_RETRIES must be at least 1 (got %d)", c.Worker.YouTubeUploadMaxRetries)
		}
		if c.Worker.YouTubeUploadBackoffBaseMs <= 0 {
			return fmt.Errorf("YOUTUBE_UPLOAD_BACKOFF_BASE_MS must be positive (got %d)", c.Worker.YouTubeUploadBackoffBaseMs)
		}
		if c.Worker.YouTubeUploadBackoffCapMs < c.Worker.YouTubeUploadBackoffBaseMs {
			return fmt.Errorf("YOUTUBE_UPLOAD_BACKOFF_CAP_MS (%d) must be >= YOUTUBE_UPLOAD_BACKOFF_BASE_MS (%d)", c.Worker.YouTubeUploadBackoffCapMs, c.Worker.YouTubeUploadBackoffBaseMs)
		}
	}
	// Older callers build Config values directly and leave newly-added
	// worker knobs at zero. Normalize that legacy shape to the same
	// conservative defaults used by Load(); reject only explicit
	// negative values, which are invalid operator input.
	if c.Worker.RenderMaxConcurrency == 0 {
		c.Worker.RenderMaxConcurrency = 1
	} else if c.Worker.RenderMaxConcurrency < 0 {
		return fmt.Errorf("RENDER_MAX_CONCURRENCY must not be negative (got %d)", c.Worker.RenderMaxConcurrency)
	}
	if c.Worker.FFmpegThreads == 0 {
		c.Worker.FFmpegThreads = 1
	} else if c.Worker.FFmpegThreads < 0 {
		return fmt.Errorf("FFMPEG_THREADS must not be negative (got %d)", c.Worker.FFmpegThreads)
	}

	// Token refresh sweep knobs — active only when a Google provider
	// (YouTube or Drive) is wired; a Google-less deployment has
	// nothing to renew. Positive required: a 0 interval would spin
	// the ticker hot-loop (and DDoS the token endpoint), a 0
	// horizon would renew every active grant on every tick.
	// Operators wanting to effectively disable the sweep set a very
	// large value (e.g. 86400*365) — the explicit-default shape
	// mirrors AssetCleanupIntervalSeconds.
	if c.Auth.YouTubeClientID != "" || c.Auth.GoogleDriveClientID != "" {
		if c.Worker.TokenRefreshSweepIntervalSeconds <= 0 {
			return fmt.Errorf("TOKEN_REFRESH_SWEEP_INTERVAL_SECONDS must be a positive integer (got %d); set to a large value (e.g. 86400) to effectively disable the sweep, not 0", c.Worker.TokenRefreshSweepIntervalSeconds)
		}
		if c.Worker.TokenRefreshSweepHorizonDays <= 0 {
			return fmt.Errorf("TOKEN_REFRESH_SWEEP_HORIZON_DAYS must be a positive integer (got %d); set to a large value (e.g. 365) to effectively disable proactive renewal, not 0", c.Worker.TokenRefreshSweepHorizonDays)
		}
	}
	// Snapshot refresh sweep cadence. Fail-safe default (not an error):
	// Configs assembled directly by tests/older callers may omit the
	// field, and a 0 here must not fail an otherwise valid boot — the
	// worker constructor applies the same 60s fallback, so the default
	// converges on the same cadence as config.Load(). Operators wanting
	// to effectively disable the background refresh set a very large
	// value (e.g. 86400*365) — the explicit-default shape mirrors the
	// token sweep above.
	if c.Worker.SnapshotRefreshSweepIntervalSeconds <= 0 {
		c.Worker.SnapshotRefreshSweepIntervalSeconds = 60
	}
	if err := c.validateOptionalPlatform("GOOGLE_DRIVE", c.Auth.GoogleDriveClientID, c.Auth.GoogleDriveClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("LINKEDIN", c.Auth.LinkedInClientID, c.Auth.LinkedInClientSecret); err != nil {
		return err
	}

	if err := c.validateVelox(); err != nil {
		return err
	}

	return nil
}

// validateYouTubeOAuthClientPool enforces the YouTube OAuth Client Pool
// contract: each pool client (A/B) is either fully configured (ID +
// secret + redirect URI, secret ≥ 32 chars) or completely absent.
// Half-configured pool clients fail at boot rather than surfacing at
// the first OAuth redirect, mirroring validateOptionalPlatform. The
// secret value is never echoed in errors — only its presence/length.
// validateYouTubeRedirectURI enforces the production callback contract.
// Local development and staging intentionally retain their existing HTTP
// localhost/test-host compatibility; production must use the exact public
// InstaEdit callback endpoint registered in Google Cloud.
func validateYouTubeRedirectURI(rawURI, appEnv string) error {
	const callbackPath = "/api/v1/auth/youtube/callback"
	const canonicalHost = "api.instaedit.org"

	if appEnv != "production" {
		return nil
	}
	uri, err := url.Parse(strings.TrimSpace(rawURI))
	if err != nil {
		return fmt.Errorf("YOUTUBE_REDIRECT_URI must be a valid URL in production: %w", err)
	}
	if uri.Scheme != "https" {
		return fmt.Errorf("YOUTUBE_REDIRECT_URI must use HTTPS in production")
	}
	if uri.User != nil || uri.Port() != "" || uri.RawQuery != "" || uri.Fragment != "" {
		return fmt.Errorf("YOUTUBE_REDIRECT_URI must not include credentials, a port, query parameters, or a fragment in production")
	}
	if strings.EqualFold(uri.Hostname(), "localhost") ||
		strings.EqualFold(uri.Hostname(), "127.0.0.1") ||
		strings.EqualFold(uri.Hostname(), "::1") {
		return fmt.Errorf("YOUTUBE_REDIRECT_URI must not use localhost or loopback in production")
	}
	host := strings.ToLower(uri.Hostname())
	if host != canonicalHost {
		return fmt.Errorf("YOUTUBE_REDIRECT_URI host must be %s in production", canonicalHost)
	}
	if uri.Path != callbackPath {
		return fmt.Errorf("YOUTUBE_REDIRECT_URI path must be %s in production", callbackPath)
	}
	return nil
}

func (c *Config) validateYouTubeOAuthClientPool() error {
	clients := []struct {
		name   string
		client YouTubeOAuthPoolClient
	}{
		{"YOUTUBE_OAUTH_CLIENT_A", c.Auth.YouTubeOAuthClientPool.ClientA},
		{"YOUTUBE_OAUTH_CLIENT_B", c.Auth.YouTubeOAuthClientPool.ClientB},
	}
	for _, cl := range clients {
		configured := cl.client.ClientID != "" || cl.client.ClientSecret != "" || cl.client.RedirectURI != ""
		if !configured {
			continue
		}
		if cl.client.ClientID == "" {
			return fmt.Errorf("%s_ID is required when any %s_* field is set (set all three or unset all three)", cl.name, cl.name)
		}
		if cl.client.ClientSecret == "" {
			return fmt.Errorf("%s_SECRET is required when any %s_* field is set (set all three or unset all three)", cl.name, cl.name)
		}
		if cl.client.RedirectURI == "" {
			return fmt.Errorf("%s_REDIRECT_URI is required when any %s_* field is set (set all three or unset all three)", cl.name, cl.name)
		}
		if len(cl.client.ClientSecret) < secretMinChars {
			return fmt.Errorf("%s_SECRET must be at least %d characters (got %d)", cl.name, secretMinChars, len(cl.client.ClientSecret))
		}
	}
	return nil
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
