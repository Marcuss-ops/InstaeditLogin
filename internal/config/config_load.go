package config

import (
	"github.com/joho/godotenv"
)

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		AI: AIConfig{
			NVIDIAAPIKey: getEnv("NVIDIA_API_KEY", ""),
		},
		Velox: VeloxConfig{
			VeloxAPIToken:         getEnv("VELOX_API_TOKEN", ""),
			VeloxControlURL:       getEnv("VELOX_CONTROL_URL", ""),
			VeloxControlJWTSecret: getEnv("VELOX_CONTROL_JWT_SECRET", ""),
			VeloxWebhookSecret:    getEnv("VELOX_WEBHOOK_SECRET", ""),
		},
		Monitoring: MonitoringConfig{
			MetricsBasicAuthUser: getEnv("METRICS_BASIC_AUTH_USER", ""),
			MetricsBasicAuthPass: getEnv("METRICS_BASIC_AUTH_PASS", ""),
			MetricsHost:          getEnv("METRICS_HOST", ""),
			MetricsPort:          getEnvInt("METRICS_PORT", 0),
			// Sentry (Blocco #5.3). SENTRY_DSN empty == SDK never
			// initialised + recovery middleware uses plain recover.
			SentryDSN:         getEnv("SENTRY_DSN", ""),
			SentryEnvironment: getEnv("SENTRY_ENVIRONMENT", ""),
			SentryRelease:     getEnv("SENTRY_RELEASE", ""),
		},
		Auth: AuthConfig{
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
			// YouTube OAuth Client Pool — optional second OAuth client
			// used to spread YouTube refresh tokens across two clients
			// (Google caps refresh tokens per account+client pair at
			// 100). Each pool client is independent: all three fields
			// must be set together, or none.
			YouTubeOAuthClientPool: YouTubeOAuthClientPoolConfig{
				ClientA: YouTubeOAuthPoolClient{
					ClientID:     getEnv("YOUTUBE_OAUTH_CLIENT_A_ID", ""),
					ClientSecret: getEnv("YOUTUBE_OAUTH_CLIENT_A_SECRET", ""),
					RedirectURI:  getEnv("YOUTUBE_OAUTH_CLIENT_A_REDIRECT_URI", ""),
				},
				ClientB: YouTubeOAuthPoolClient{
					ClientID:     getEnv("YOUTUBE_OAUTH_CLIENT_B_ID", ""),
					ClientSecret: getEnv("YOUTUBE_OAUTH_CLIENT_B_SECRET", ""),
					RedirectURI:  getEnv("YOUTUBE_OAUTH_CLIENT_B_REDIRECT_URI", ""),
				},
			},
			GoogleDriveClientID:     getEnv("GOOGLE_DRIVE_CLIENT_ID", ""),
			GoogleDriveClientSecret: getEnv("GOOGLE_DRIVE_CLIENT_SECRET", ""),
			GoogleDriveRedirectURI:  getEnv("GOOGLE_DRIVE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/google-drive/callback"),
			LinkedInClientID:        getEnv("LINKEDIN_CLIENT_ID", ""),
			LinkedInClientSecret:    getEnv("LINKEDIN_CLIENT_SECRET", ""),
			LinkedInRedirectURI:     getEnv("LINKEDIN_REDIRECT_URI", "http://localhost:8080/api/v1/auth/linkedin/callback"),
			JWTSecret:               getEnv("JWT_SECRET", ""),
			JWTAccessTTLMinutes:     getEnvInt("JWT_ACCESS_TTL_MINUTES", 0),
			JWTRefreshTTLDays:       getEnvInt("JWT_REFRESH_TTL_DAYS", 0),
			TrustedProxies:          getEnv("TRUSTED_PROXIES", ""),
			JWTTTLHours:             getEnvInt("JWT_TTL_HOURS", 0),
			// Disable public registration unless an admin invite token
			// is configured. Operators create users manually (via the
			// admin endpoint or by setting ADMIN_INVITE_TOKEN and calling
			// /api/v1/auth/register with X-Admin-Token).
			AdminInviteToken: getEnv("ADMIN_INVITE_TOKEN", ""),
		},
		HTTP: HTTPConfig{
			FrontendURL:        getEnv("FRONTEND_URL", ""),
			EditorURL:          getEnv("EDITOR_URL", ""),
			AllowedCORSOrigins: splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
			CookieDomain:       getEnv("COOKIE_DOMAIN", ""),
			LogLevel:           getEnv("LOG_LEVEL", "info"),
			AppEnv:             getEnv("APP_ENV", "dev"),
		},
		Database: DatabaseConfig{
			DatabaseURL:              getEnv("DATABASE_URL", ""),
			DBHost:                   getEnv("DB_HOST", "localhost"),
			DBPort:                   getEnv("DB_PORT", "5432"),
			DBUser:                   getEnv("DB_USER", "instaedit"),
			DBPassword:               getEnv("DB_PASSWORD", ""),
			DBName:                   getEnv("DB_NAME", "instaedit_login"),
			DBSSLMode:                getEnv("DB_SSLMODE", "disable"),
			ExpectedInstallationUUID: getEnv("EXPECTED_DATABASE_INSTALLATION_UUID", ""),
		},
		Worker: WorkerConfig{
			// P1#6 — YouTube resumable upload tuning. Defaults mirror the
			// valutazione doc spec (16 MB chunks, 5 per-chunk retries, 1 s/5 min
			// backoff). Validation runs unconditionally (so an operator typo
			// surfaces at boot, not first upload).
			YouTubeUploadChunkBytes:           getEnvInt64("YOUTUBE_UPLOAD_CHUNK_BYTES", 16*1024*1024),
			YouTubeUploadMaxRetries:           getEnvInt("YOUTUBE_UPLOAD_MAX_RETRIES", 5),
			YouTubeUploadBackoffBaseMs:        getEnvInt("YOUTUBE_UPLOAD_BACKOFF_BASE_MS", 1000),
			YouTubeUploadBackoffCapMs:         getEnvInt("YOUTUBE_UPLOAD_BACKOFF_CAP_MS", 300000),
			YouTubeDailyQuotaLimit:            getEnvInt("YOUTUBE_DAILY_QUOTA_LIMIT", 300),
			YouTubeGroupVideosMaxAccounts:     getEnvInt("YOUTUBE_GROUP_VIDEOS_MAX_ACCOUNTS", 200),
			YouTubeGroupVideosMaxVideos:       getEnvInt("YOUTUBE_GROUP_VIDEOS_MAX_VIDEOS", 500),
			YouTubeGroupVideosCacheTTLSeconds: getEnvInt("YOUTUBE_GROUP_VIDEOS_CACHE_TTL_SECONDS", 300),
			YouTubeGroupVideosDefaultPageSize: getEnvInt("YOUTUBE_GROUP_VIDEOS_DEFAULT_PAGE_SIZE", 50),
			// Blocco #2 P0 — publish horizon + retention buffer are
			// env-driven. Defaults (30 days / 7 days) match the user-facing
			// spec; surface them via the Worker's struct fields so the HTTP
			// layer + worker pool read the same source of truth.
			PublishHorizonDays:             getEnvInt("PUBLISH_HORIZON_DAYS", 30),
			VideoRetentionBufferDays:       getEnvInt("VIDEO_RETENTION_BUFFER_DAYS", 7),
			PublishWorkerIntervalSeconds:   getEnvInt("PUBLISH_WORKER_INTERVAL_SECONDS", 30),
			ReconcileWorkerIntervalSeconds: getEnvInt("RECONCILE_WORKER_INTERVAL_SECONDS", 5),
			WebhookWorkerIntervalSeconds:   getEnvInt("WEBHOOK_WORKER_INTERVAL_SECONDS", 5),
			SessionCleanupIntervalSeconds:  getEnvInt("SESSION_CLEANUP_INTERVAL_SECONDS", 300),
			AssetCleanupIntervalSeconds:    getEnvInt("ASSET_CLEANUP_INTERVAL_SECONDS", 86400),
			UploadWorkerIntervalSeconds:    getEnvInt("UPLOAD_WORKER_INTERVAL_SECONDS", 30),
			RenderMaxConcurrency:           getEnvInt("RENDER_MAX_CONCURRENCY", 1),
			FFmpegThreads:                  getEnvInt("FFMPEG_THREADS", 1),
			// Token refresh sweep — daily cadence + 4-month inactivity
			// horizon (2 months of margin under Google's ~6-month
			// refresh-token inactivity GC).
			TokenRefreshSweepIntervalSeconds: getEnvInt("TOKEN_REFRESH_SWEEP_INTERVAL_SECONDS", 86400),
			TokenRefreshSweepHorizonDays:     getEnvInt("TOKEN_REFRESH_SWEEP_HORIZON_DAYS", 120),
			// Snapshot refresh sweep — 60s cadence: a page load stamps
			// refresh_pending_at and the worker refreshes the cached
			// snapshot in the background within a minute.
			SnapshotRefreshSweepIntervalSeconds: getEnvInt("SNAPSHOT_REFRESH_SWEEP_INTERVAL_SECONDS", 60),
			// P1 step 2 — worker pool config (see struct comment above).
			UploadIngestConcurrency:           getEnvInt("UPLOAD_INGEST_CONCURRENCY", 3),
			YouTubeUploadConcurrency:          getEnvInt("YOUTUBE_UPLOAD_CONCURRENCY", 4),
			UploadLeaseTTLSeconds:             getEnvInt("UPLOAD_LEASE_TTL_SECONDS", 60),
			UploadHeartbeatIntervalSeconds:    getEnvInt("UPLOAD_HEARTBEAT_INTERVAL_SECONDS", 20),
			UploadReclaimIntervalSeconds:      getEnvInt("UPLOAD_RECLAIM_INTERVAL_SECONDS", 30),
			UploadReclaimOnStart:              getEnvBool("UPLOAD_RECLAIM_ON_START", true),
			UploadEmptyQueueBackoffMinSeconds: getEnvInt("UPLOAD_EMPTY_QUEUE_BACKOFF_MIN_SECONDS", 1),
			UploadEmptyQueueBackoffMaxSeconds: getEnvInt("UPLOAD_EMPTY_QUEUE_BACKOFF_MAX_SECONDS", 30),
		},
		// AppMode lets operators pin the deployment to Google's OAuth-
		// consent-screen publishing status. "production" means refresh
		// tokens are durable (no automatic 7-day expiry); "testing"
		// means Google's Testing-mode 7-day refresh-token TTL applies
		// and any refresh attempt after day 7 surfaces invalid_grant.
		// Default "production" so a missing env var falls into the
		// safer bucket; ops must explicitly opt-in to "testing" when
		// validating against a staging OAuth-client.
		AppMode: getEnv("APP_MODE", "production"),
		Storage: StorageConfig{
			S3Endpoint:                getEnv("S3_ENDPOINT", ""),
			S3Bucket:                  getEnv("S3_BUCKET", ""),
			S3PathStyle:               getEnvBool("S3_PATH_STYLE", false),
			S3AccessKey:               getEnv("S3_ACCESS_KEY", ""),
			S3SecretKey:               getEnv("S3_SECRET_KEY", ""),
			S3Region:                  getEnv("S3_REGION", ""),
			MaxUploadBytes:            getEnvInt64("STORAGE_MAX_UPLOAD_BYTES", 200*1024*1024),
			GoogleDriveAPIKey:         getEnv("GOOGLE_DRIVE_API_KEY", ""),
			GoogleDriveUploadFolderID: getEnv("GOOGLE_DRIVE_UPLOAD_FOLDER_ID", ""),
		},
		EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		// Blocco #2.2: read the multi-key env vars. The actual
		// parsing + validation happens in validate(); Load() only
		// captures the raw strings so validate() can surface
		// high-quality error messages with the original input.
		EncryptionKeysRaw:        getEnv("ENCRYPTION_KEYS", ""),
		ActiveEncryptionKeyIDRaw: getEnv("ACTIVE_ENCRYPTION_KEY_ID", ""),
		// P1 step 2 — worker pool config (see struct comment above).
		StripeSecretKey:     getEnv("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret: getEnv("STRIPE_WEBHOOK_SECRET", ""),
		StripeSuccessURL:    getEnv("STRIPE_SUCCESS_URL", getEnv("FRONTEND_URL", "http://localhost:5173")+"/dashboard/billing?success=1"),
		StripeCancelURL:     getEnv("STRIPE_CANCEL_URL", getEnv("FRONTEND_URL", "http://localhost:5173")+"/dashboard/billing?canceled=1"),
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
	}

	// Resolve JWT TTL defaults and legacy fallback. Access TTL defaults
	// to 15 minutes; refresh TTL defaults to 30 days. The legacy
	// JWT_TTL_HOURS variable is converted to minutes when the explicit
	// access-TTL variable is absent, preserving existing deployments.
	if cfg.Auth.JWTAccessTTLMinutes <= 0 {
		if cfg.Auth.JWTTTLHours > 0 {
			cfg.Auth.JWTAccessTTLMinutes = cfg.Auth.JWTTTLHours * 60
		} else {
			cfg.Auth.JWTAccessTTLMinutes = 15
		}
	}
	if cfg.Auth.JWTRefreshTTLDays <= 0 {
		cfg.Auth.JWTRefreshTTLDays = 30
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
