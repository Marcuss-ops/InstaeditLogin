// Package bootstrap owns the shared startup wiring for every InstaEditLogin
// binary (cmd/api, cmd/worker, cmd/migrate, cmd/server).
//
// Blocco #2.1 split cmd/server/main.go into:
//   - cmd/api     — HTTP only
//   - cmd/worker  — 7 background goroutines (publish, reconcile, outbox,
//     webhook, metrics, sessions_cleanup, upload)
//   - cmd/migrate — Connect + Migrate + exit (one-shot pre-deploy job)
//   - cmd/server  — wrapper: dev/local-compat single-bundle that runs
//     migrate + api + (optionally) workers in one process.
//
// Migrate is NOT part of Wire() on purpose: the production deploy topology
// runs cmd/migrate as a one-shot pre-deploy job, so api/worker MUST NOT
// re-run Migrate() — they'd race against an in-flight migration job. The
// dev wrapper cmd/server does call Migrate() (via internal/database.Migrate)
// because it assumes "this is the only process touching the DB just now".
package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/providers"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxclient"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// App is the wired runtime holding every dependency that the api and
// worker binaries share. cmd/api reads App.HTTPHandler (and App.Cfg for
// PORT); cmd/worker reads App.DB / App.Vault / App.CapRouter /
// App.WebhookRepo to construct + supervise the 7 goroutines; cmd/server
// (the wrapper) reads both halves.
type App struct {
	Cfg         *config.Config
	DB          *sql.DB
	Vault       credentials.VaultAPI
	CapRouter   *services.CapabilityRouter
	WebhookRepo *repository.WebhookRepository
	HTTPHandler http.Handler
	Logger      *slog.Logger

	// WorkerRegistry (Blocco #5.3 refactor) supervises the lifecycle
	// of every background goroutine. It is constructed in Wire() and
	// consumed by RunWorkers() + the worker health listener.
	WorkerRegistry *worker.Registry

	// SentryHub (Blocco #5.3). Nil when SENTRY_DSN is empty
	// (operator-disables-by-omission contract). When non-nil, the
	// panic-catching middleware uses sentryhttp.New() against
	// this hub so CaptureException flows correct on every panic.
	SentryHub *sentry.Hub

	// WorkerID (commit DI refactor) is the per-process identity
	// generated locally via metrics.NewWorkerID and threaded into
	// each worker's constructor — no global singleton, no
	// sync.Once. Stored on App so external callers (and the
	// RunWorkers goroutine-launch closures) can pass it on.
	WorkerID string

	// MemoryLimiter (commit DI refactor) is constructed once in
	// Wire() and shared between RateLimitService (request path)
	// and the workers (background path). Single instance per
	// process; explicit receiver avoids a sync.Once-protected
	// lazy global. The reaper goroutine dies with the process, so
	// no Shutdown() wiring is strictly required — the field is
	// exposed for future graceful-drain work.
	MemoryLimiter *services.MemoryLimiter

	// StorageProvider is the S3-compatible storage backend. Shared
	// between the API (presign / complete / drive import) and the
	// upload worker (background Drive → S3 streaming).
	StorageProvider services.StorageProvider

	// Encryptor (Task 8/10) exposes *crypto.Encryptor to RunWorkers
	// so the DeliveryRegistry can wire services.SessionEncryptor
	// for the Drive destination's session-URI ciphertext. Same
	// instance constructed at the top of Wire(); we expose it as
	// a field rather than a setter so RunWorkers reads a
	// single canonical reference.
	Encryptor *crypto.Encryptor

	// SessionsSvc is the wired *SessionsService, populated by
	// Wire(). cmd/worker reads it to drive the retention-policy
	// goroutine (SessionsCleanupWorker); cmd/api reads it through
	// the router (which already gets a copy via WithSessionsService
	// in the Wire's opts block). Exposing it as a field avoids
	// re-constructing the service in RunWorkers — the same instance
	// is shared across the api and worker processes.
	SessionsSvc *services.SessionsService

	// OneTimeCodes is the PostgreSQL-backed OAuth-callback bridge store
	// (Taglio 1.2). cmd/api consumes it via the router's
	// WithOneTimeCodeStore option (redirect/exchange handlers).
	// cmd/worker's RunWorkers calls OneTimeCodes.Stop() during
	// graceful shutdown so the background sweep goroutine exits
	// cleanly. Without this wiring, SIGTERM would let the sweeper
	// become a zombie until the process is killed.
	OneTimeCodes      api.OneTimeCodeStore
	VeloxDownloadJobs chan worker.VeloxDownloadJob
}

// Wire connects to the database, builds every shared dependency, and
// returns a fully-wired *App. It does NOT run migrations and does NOT
// start any goroutine — callers choose what to run. Returns an error
// on config / database / encryption-key / provider-registry failures
// (these are fail-fast at startup, never silent).
//
// Taglio 3.1: S3 storage is mandatory. Wire panics — via the returned
// error — when S3 config is missing (the caller decides how to handle
// it; the wrapper cmd/server treats Wire errors as fatal-exit).
func Wire(ctx context.Context) (*App, error) {
	_ = ctx // reserved for future context-aware config loading

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		return nil, fmt.Errorf("S3 storage is required: set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Environment", "app_env", cfg.AppEnv)

	db, err := database.Connect(cfg)
	if err != nil {
		return nil, fmt.Errorf("connect db: %w", err)
	}

	// Per-process worker id (commit DI refactor). Generated locally
	// rather than via metrics.InitWorkerID() so the value lives only
	// on App.WorkerID — each consumer (workers, log context lines)
	// receives it as an explicit value, not a global read.
	workerID := metrics.NewWorkerID()
	slog.Info("worker_id initialised", "worker_id", workerID)

	// Per-process rate-limit MemoryLimiter (commit DI refactor).
	// Constructed once, shared between RateLimitService and the
	// workers — single instance, no sync.Once-protected lazy global.
	memoryLimiter := services.NewMemoryLimiter()

	// Blocco #2.2 — multi-key support. Wire() consumes the
	// post-validated EncryptionKeys map + ActiveEncryptionKeyID
	// regardless of which env-var surface the operator used:
	//   - ENCRYPTION_KEY (legacy single-key) → resolveEncryptionConfig
	//     promotes it into EncryptionKeys[1] with active=1
	//   - ENCRYPTION_KEYS + ACTIVE_ENCRYPTION_KEY_ID (multi-key) →
	//     the parsed CSV + the operator-chosen active id
	// This is the only call site in the codebase that constructs
	// the Encryptor from the Config — every other consumer reads
	// the already-validated *crypto.Encryptor through the App
	// struct or a narrower interface.
	enc, err := crypto.NewEncryptor(cfg.ActiveEncryptionKeyID, cfg.EncryptionKeys)
	if err != nil {
		return nil, fmt.Errorf("init encryptor: %w", err)
	}
	slog.Info("encryption configured",
		"active_key_id", cfg.ActiveEncryptionKeyID,
		"key_count", len(cfg.EncryptionKeys),
		"key_ids", config.SortedKeyIDs(cfg.EncryptionKeys))

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	teamRepo := repository.NewTeamRepository(db)
	workspaceRepo := repository.NewWorkspaceRepository(db)
	apiKeyRepo := repository.NewApiKeyRepository(db)
	apiKeyAuth := auth.NewApiKeyAuthenticator(apiKeyRepo)
	idempotencyRepo := repository.NewIdempotencyRepository(db)
	postRepo := repository.NewPostRepository(db)
	mediaRepo := repository.NewMediaAssetRepository(db)
	uploadJobRepo := repository.NewUploadJobRepository(db)
	importBatchRepo := repository.NewImportBatchRepository(db)
	connectionStateRepo := repository.NewConnectionStateRepository(db)
	auditLogRepo := repository.NewAuditLogRepository(db)
	externalDestinationRepo := repository.NewExternalDestinationRepository(db)
	externalDeliveryRepo := repository.NewExternalDeliveryRepository(db)
	connectLinkNonceRepo := repository.NewConnectLinkNonceRepository(db)

	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

	registry, err := providers.BuildRegistry(cfg)
	if err != nil {
		return nil, fmt.Errorf("build provider registry: %w", err)
	}
	capRouter := registry

	// channelAuthorizer (Task 1/10) — atomic OAuth finalize gate.
	// Pulls the YouTubeChannelBinder off the capability router so
	// AuthorizeChannel can run the channels.list(mine=true)
	// pre-tx guard. YouTube MUST satisfy YouTubeChannelBinder in
	// production — if the assertion fails, fail Wire() fast rather
	// than silently no-op'ing the most important safety net from
	// Task 1/10 (a misconfigured refactor would otherwise let a
	// publish target the wrong channel and only surface the bug
	// at the first upload time).
	var ytBinder services.YouTubeChannelBinder
	if ytp, ok := capRouter.Get(models.PlatformYouTube); ok {
		b, typeOK := ytp.(services.YouTubeChannelBinder)
		if !typeOK {
			return nil, fmt.Errorf("youtube provider registered but does not implement YouTubeChannelBinder; channels.list(mine=true) guard would be a silent no-op (Task 1/10 invariant violated)")
		}
		ytBinder = b
	}
	channelAuthorizer := services.NewChannelAuthorizationService(db, enc, tokenRepo, ytBinder)

	authMgr := auth.NewManager(
		cfg.JWTSecret,
		time.Duration(cfg.JWTAccessTTLMinutes)*time.Minute,
		time.Duration(cfg.JWTRefreshTTLDays)*24*time.Hour,
	).WithEnv(cfg.AppEnv)
	oneTimeCodes := api.NewOneTimeCodePostgresStore(db, 60*time.Second)
	veloxDownloadJobs := make(chan worker.VeloxDownloadJob, 64)
	// oneTimeCodes sweeper is gracefully stopped by RunWorkers. cmd/api
	// (HTTP-only binary) does not run RunWorkers, so the sweeper is
	// collected at process termination there. Exposing the
	// store on App avoids re-constructing it in RunWorkers —
	// the same instance is shared across api + worker processes
	// when cmd/server bundles both.

	corsOrigins := cfg.AllowedCORSOrigins
	if len(corsOrigins) == 0 && cfg.FrontendURL != "" {
		corsOrigins = []string{cfg.FrontendURL}
	}

	// Parse the trusted proxy list once at startup so IP extraction
	// and the rate limiter agree on which peers may supply
	// X-Forwarded-For / X-Real-IP headers.
	trustedProxies, err := api.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("parse TRUSTED_PROXIES: %w", err)
	}

	storageProvider, err := services.NewS3Provider(
		cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region,
		cfg.S3AccessKey, cfg.S3SecretKey, cfg.S3PathStyle, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("construct S3 provider: %w", err)
	}
	slog.Info("storage provider: S3-compatible configured",
		"endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket, "region", cfg.S3Region)

	userWorkspaceHelper := api.RepoUserWorkspaceHelper(workspaceRepo, teamRepo)
	authEmailBackend := services.NewAuthService(userRepo, workspaceRepo, teamRepo)
	authEmailSvc := api.NewAuthEmailServiceAdapter(authEmailBackend)

	sessionRepo := repository.NewSessionRepository(db)
	sessionsSvc := services.NewSessionsService(sessionRepo, authMgr)

	rateLimitRepo := repository.NewRateLimitRepository(db)
	rateLimitSvc := services.NewRateLimitServiceWithMemory(rateLimitRepo, memoryLimiter)

	webhookRepo := repository.NewWebhookRepository(db)

	opts := []api.RouterOption{
		api.WithCredentialVault(vault),
		// Task 1/10 atomic OAuth finalize gate. Wired
		// unconditionally before the storage provider so the
		// field on Router is non-nil by the time Setup() runs.
		api.WithChannelAuthorizer(channelAuthorizer),
		api.WithStorageProvider(storageProvider),
		api.WithMaxUploadBytes(cfg.MaxUploadBytes),
		api.WithApiKeyStore(apiKeyRepo),
		api.WithApiKeyAuthenticator(apiKeyAuth),
		api.WithIdempotencyStore(idempotencyRepo),
		api.WithUserWorkspaceHelper(userWorkspaceHelper),
		api.WithTeamStore(teamRepo),
		api.WithAuthEmailService(authEmailSvc),
		api.WithSessionsService(sessionsSvc),
		api.WithWorkspaceStore(workspaceRepo),
		api.WithPostStore(postRepo),
		api.WithMediaStore(mediaRepo),
		api.WithUploadJobStore(uploadJobRepo),
		// P2 — ops dashboard store. AdminRepository powers every
		// /admin/* endpoint (channels / queue / health + their
		// .csv variants). When nil the route table short-circuits
		// the admin registration block (handlers.go Setup()).
		api.WithAdminStore(repository.NewAdminRepository(db)),
		// P1#7 — producer-side handler (POST
		// /api/v1/media/import/drive/folder/async) and poll endpoint
		// (GET .../async/{id}) share this ImportBatchStore. The
		// crawler worker uses the SAME *repository.ImportBatchRepository
		// but through a narrower CrawlerBatchStore interface
		// declared in internal/worker/drive_batch_crawler.go.
		api.WithImportBatchStore(importBatchRepo),
		// P1#7 — exporter for the crawler goroutine spawned in
		// RunWorkers. Same instance as ImportBatchStore above; the
		// split into two interfaces lets each consumer request only
		// the methods it actually calls.
		api.WithConnectionStateStore(&connectionStateStoreWrapper{connectionStateRepo}),
		api.WithAuditLogStore(&auditLogStoreWrapper{auditLogRepo}),
		api.WithExternalDestinationStore(externalDestinationRepo),
		api.WithExternalDeliveryStore(externalDeliveryRepo),
		api.WithConnectLinkNonceStore(connectLinkNonceRepo),
		api.WithVeloxAPIToken(os.Getenv("VELOX_API_TOKEN")),
		api.WithVeloxDownloadJobChannel(veloxDownloadJobs),
		// P2 Velox BFF — wire the typed client that signs a short-lived
		// JWT (VELOX_CONTROL_JWT_SECRET) and calls the Velox master
		// (VELOX_CONTROL_URL). When either env is empty, veloxclient.New
		// returns nil and the VeloxBFFModule does not mount its routes
		// (nil-guard pattern matching the other feature flags). The auth
		// + CSRF middlewares mirror the destinations route wiring so the
		// /api/v1/velox/* chain is: auth → CSRF → handler.
		func() api.RouterOption {
			vc := veloxclient.New(cfg.VeloxControlURL, cfg.VeloxControlJWTSecret)
			if vc == nil {
				slog.Info("velox BFF client not configured (VELOX_CONTROL_URL or VELOX_CONTROL_JWT_SECRET empty) — /api/v1/velox/* routes not mounted")
				return func(*api.Router) {} // no-op option
			}
			slog.Info("velox BFF client configured",
				"control_url", cfg.VeloxControlURL)
			return api.WithVeloxBFFClient(vc)
		}(),
		api.WithVeloxBFFAuthMiddleware(authMgr.Middleware),
		api.WithVeloxBFFCSRFMiddleware(func(next http.Handler) http.Handler {
			return auth.NewCSRF(auth.CSRFConfig{
				Secure:       true,
				Path:         "/",
				CookieDomain: cfg.CookieDomain,
				SameSite:     http.SameSiteNoneMode,
			}, next)
		}),
		api.WithCookieSecure(true),
		// csrf_token cookie Domain (Blocco #2.4): threaded from
		// cfg.CookieDomain (COOKIE_DOMAIN env var). Empty stays
		// host-only, which is correct for dev (localhost crosses
		// different ports and a parent-domain match wouldn't help).
		// Production sets e.g. ".instaedit.org" so the SPA on
		// app.instaedit.org can read the csrf_token via
		// document.cookie against the API on api.instaedit.org.
		// Session + refresh cookies deliberately remain host-only:
		// they are HttpOnly on the API origin, JS cannot read them
		// anyway, and giving them a Domain would only widen the
		// CSRF attack surface for zero security upside.
		api.WithCookieDomain(cfg.CookieDomain),
		api.WithRateLimitService(rateLimitSvc),
		api.WithWebhookStore(webhookRepo),
		// ADMIN_INVITE_TOKEN gates public registration. If the env
		// is unset, registration is disabled (handler returns 403).
		api.WithAdminInviteToken(cfg.AdminInviteToken),
		api.WithSnapshotStore(repository.NewSnapshotRepository(db)),
		api.WithMetricHistoryStore(repository.NewAccountMetricsRepository(db)),
		// P1#7 — export the importBatchRepo on App so the
		// command-line crawler (cmd/worker) can wire it directly.
	}
	// Blocco #5.3 — Sentry init (lazy). The user contract is
	// "SENTRY_DSN empty == no init; non-empty == CaptureException
	// pipeline". We honour that by short-circuiting sentry.Init
	// entirely when the DSN is empty (no outbound DNS lookup, no
	// background transport goroutine, no per-event CPU cost). When
	// the DSN is set, sentry.Init runs once; sentry-go guards
	// against repeat Init in a single process so this is idempotent
	// across Wire() calls within the same binary.
	var hub *sentry.Hub
	if cfg.SentryDSN != "" {
		clientOpts := sentry.ClientOptions{
			Dsn:         cfg.SentryDSN,
			Environment: cfg.SentryEnvironment,
			Release:     cfg.SentryRelease,
			// ServerName is intentionally LET-default (the
			// SDK reads it from the OS). Overriding with
			// cfg.AppEnv would double-up the env label.
		}
		if err := sentry.Init(clientOpts); err != nil {
			// Sentry init failure is SOFT: log + continue without
			// the observability surface rather than refusing to
			// boot. Operators can fix the DSN + redeploy; the
			// recovery middleware drops to plain recover for the
			// remainder of this process's lifetime.
			slog.Warn("sentry init failed; recovery middleware will run without Sentry capture",
				"error", err)
		} else {
			hub = sentry.CurrentHub()
			slog.Info("sentry configured",
				"environment", cfg.SentryEnvironment,
				"release", cfg.SentryRelease)
		}
	} else {
		slog.Info("sentry disabled (SENTRY_DSN empty)")
	} // Inject the Sentry hub into the router options so the recovery
	// middleware can read it via the Router field (not via the App
	// field — pkg/api stays decoupled from internal/bootstrap).
	opts = append(opts, api.WithSentryHub(hub))

	// Trusted proxies are applied AFTER all options so both
	// clientIP() and the rate limiter see the same parsed list.
	opts = append(opts, api.WithTrustedProxies(trustedProxies))

	// Metrics basic-auth credentials are wired explicitly so the
	// /api/v1/metrics handler does not read env vars at request
	// time. Incomplete credentials trigger fail-closed 503 in the
	// handler; production boot already rejects them in
	// cfg.validate().
	opts = append(opts, api.WithMetricsAuth(cfg.MetricsBasicAuthUser, cfg.MetricsBasicAuthPass))

	// Blocco #5.3 — wire the DB into /ready's contract. API readiness
	// now only checks DB ping + schema migrations; worker readiness is
	// exposed separately by the worker process via the WorkerRegistry.
	opts = append(opts, api.WithDB(db))

	router, err := api.NewRouter(capRouter, userRepo, authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(oneTimeCodes)}, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("build router: %w", err)
	}

	slog.Info("Router configured",
		"jwt_access_ttl_minutes", cfg.JWTAccessTTLMinutes,
		"jwt_refresh_ttl_days", cfg.JWTRefreshTTLDays,
		"frontend_url", cfg.FrontendURL,
		"cors_origins", corsOrigins,
		"platforms", capRouter.Names(),
		"api_keys_enabled", apiKeyRepo != nil,
		"sentry_enabled", hub != nil,
		"ready_endpoint", "/ready")

	return &App{
		Cfg:               cfg,
		DB:                db,
		Vault:             vault,
		CapRouter:         capRouter,
		WebhookRepo:       webhookRepo,
		HTTPHandler:       router.Setup(),
		Logger:            logger,
		WorkerRegistry:    worker.NewRegistry(),
		SentryHub:         hub,
		WorkerID:          workerID,
		MemoryLimiter:     memoryLimiter,
		StorageProvider:   storageProvider,
		SessionsSvc:       sessionsSvc,
		OneTimeCodes:      oneTimeCodes,
		Encryptor:         enc,
		VeloxDownloadJobs: veloxDownloadJobs,
	}, nil
}

// RunWorkers starts the 9 background goroutines (publish, reconcile,
// outbox, webhook, metrics, sessions_cleanup, velox_downloader,
// upload, drive_batch_crawler) under a shared WorkerRegistry. The
// registry handles startup, heartbeat tracking, supervision, logging,
// and shutdown. A critical worker that exits with a non-context error
// aborts the whole process by returning the error from RunWorkers.
//
// On cancellation it cancels every goroutine concurrently and waits
// up to 15s total for their Run loops to drain gracefully.
func (a *App) RunWorkers(ctx context.Context) error {
	if a.WorkerRegistry == nil {
		return fmt.Errorf("RunWorkers called with nil App.WorkerRegistry")
	}

	// 1. Publish worker driver — queued → publishing transition
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "publish",
		Critical: true,
		Run: func(ctx context.Context) error {
			pw := worker.NewPublishWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				a.WorkerID,
				a.MemoryLimiter,
				time.Duration(a.Cfg.PublishWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			deliveryRegistry := services.NewDeliveryRegistry()
			if ytPub, ok := a.CapRouter.Publisher(models.PlatformYouTube); ok {
				_ = deliveryRegistry.Register(services.NewYouTubeDeliveryAdapter(ytPub))
			}
			if a.Cfg.GoogleDriveClientID != "" && a.Cfg.GoogleDriveClientSecret != "" {
				driveSessionRepo := repository.NewDeliverySessionRepository(a.DB)
				var googleDriveOAuth *services.GoogleDriveOAuthService
				if gd, ok := a.CapRouter.Get(models.PlatformGoogleDrive); ok {
					if gdOAuth, typeOK := gd.(*services.GoogleDriveOAuthService); typeOK {
						googleDriveOAuth = gdOAuth
					}
				}
				if googleDriveOAuth != nil {
					driveVault, vaultOK := a.Vault.(services.DriveTokenVault)
					if !vaultOK {
						return fmt.Errorf("publish worker: credential vault lacks Drive refresh-token capability")
					}
					driveTokenProvider := services.NewDriveVaultTokenProvider(driveVault, googleDriveOAuth)
					driveDest, destErr := services.NewGoogleDriveDestination(
						driveSessionRepo,
						driveTokenProvider,
						a.Encryptor,
						&http.Client{Timeout: 30 * time.Second},
						16*1024*1024,
					)
					if destErr == nil {
						if driveAdapter, adapterErr := services.NewGoogleDriveDeliveryAdapter(driveDest); adapterErr == nil {
							if regErr := deliveryRegistry.Register(driveAdapter); regErr != nil {
								slog.Error("publish worker: register google drive delivery adapter", "error", regErr)
							}
						} else {
							slog.Error("publish worker: build google drive delivery adapter", "error", adapterErr)
						}
					} else {
						slog.Error("publish worker: build google drive destination", "error", destErr)
					}
				}
			}
			_ = deliveryRegistry.Register(services.NewVeloxCallbackDeliveryAdapter(false))
			pw = pw.WithDeliveryRegistry(deliveryRegistry)
			slog.Info("publish worker: delivery registry wired", "providers", deliveryRegistry.Names())
			return pw.Run(ctx)
		},
	})

	// 2. Reconcile worker — publishing → published | failed transition
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "reconcile",
		Critical: true,
		Run: func(ctx context.Context) error {
			rw := worker.NewReconcileWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				a.WorkerID,
				a.MemoryLimiter,
				time.Duration(a.Cfg.ReconcileWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return rw.Run(ctx)
		},
	})

	// 3. Outbox dispatcher — materialises publish_jobs audit rows
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "outbox",
		Critical: true,
		Run: func(ctx context.Context) error {
			ds := outbox.NewDispatcher(outbox.DispatcherConfig{
				OutboxStore:  repository.NewOutboxRepository(a.DB),
				Process:      processors.NewPublishJobsMaterialiser(a.DB),
				Logger:       slog.Default(),
				TickInterval: outbox.DefaultTickInterval,
			})
			return ds.Run(ctx)
		},
	})

	// 4. Webhook worker — drains webhook_deliveries
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "webhook",
		Critical: true,
		Run: func(ctx context.Context) error {
			ww := worker.NewWebhookWorker(a.WebhookRepo, time.Duration(a.Cfg.WebhookWorkerIntervalSeconds)*time.Second)
			return ww.Run(ctx)
		},
	})

	// 5. Metrics collector — periodic gauges
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "metrics",
		Critical: true,
		Run: func(ctx context.Context) error {
			return metrics.RunPeriodicCollector(ctx, a.DB, metrics.DefaultCollectorInterval, slog.Default())
		},
	})

	// 6. Sessions cleanup worker — retention policy
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "sessions_cleanup",
		Critical: true,
		Run: func(ctx context.Context) error {
			scw := worker.NewSessionsCleanupWorker(
				a.SessionsSvc,
				time.Duration(a.Cfg.SessionCleanupIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return scw.Run(ctx)
		},
	})

	// 7. Velox handoff consumer — API enqueue → upload_jobs registration
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "velox_downloader",
		Critical: true,
		Run: func(ctx context.Context) error {
			deliveryRepo := repository.NewExternalDeliveryRepository(a.DB)
			downloader := worker.NewVeloxArtifactDownloader(
				deliveryRepo,
				deliveryRepo,
				worker.NewIngestFSM(deliveryRepo, slog.Default()),
				slog.Default(),
			)
			destinationRepo := repository.NewExternalDestinationRepository(a.DB)
			workspaceRepo := repository.NewWorkspaceRepository(a.DB)
			resolve := func(ctx context.Context, delivery models.ExternalDelivery) (worker.VeloxDownloadJob, bool) {
				dst, err := destinationRepo.GetByID(ctx, delivery.ExternalDestinationID)
				if err != nil || dst == nil {
					return worker.VeloxDownloadJob{}, false
				}
				ws, err := workspaceRepo.FindByID(dst.WorkspaceID)
				if err != nil || ws == nil {
					return worker.VeloxDownloadJob{}, false
				}
				var meta map[string]any
				_ = json.Unmarshal(delivery.Metadata, &meta)
				j := worker.VeloxDownloadJob{ExternalDeliveryID: delivery.ExternalDeliveryID, UserID: ws.OwnerID, WorkspaceID: ws.ID,
					ArtifactSHA256: delivery.ExpectedSHA256, SizeBytes: delivery.ExpectedSizeBytes, MimeType: delivery.ExpectedMimeType,
					DownloadURL: valueString(delivery.DownloadURL), Title: valueStringMap(meta, "title"), Caption: valueStringMap(meta, "caption"),
					Targets: valueIntsMap(meta, "target_account_ids"), DriveAccountID: valueIntPtrMap(meta, "drive_account_id"), FolderID: valueStringPtrMap(meta, "folder_id"), PublishAt: delivery.PublishAt}
				return j, j.DownloadURL != ""
			}
			return downloader.RunPersistent(ctx, a.VeloxDownloadJobs, resolve)
		},
	})

	// 8. Upload worker — background import of public or authenticated
	// Google Drive videos into S3 + posts + publish queue.
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "upload",
		Critical: true,
		Run: func(ctx context.Context) error {
			uploadOpts := worker.UploadWorkerOptions{
				IngestConcurrency: a.Cfg.UploadIngestConcurrency,
				UploadConcurrency: a.Cfg.YouTubeUploadConcurrency,
				LeaseTTL:          time.Duration(a.Cfg.UploadLeaseTTLSeconds) * time.Second,
				HeartbeatInterval: time.Duration(a.Cfg.UploadHeartbeatIntervalSeconds) * time.Second,
				ReclaimInterval:   time.Duration(a.Cfg.UploadReclaimIntervalSeconds) * time.Second,
				ReclaimOnStart:    a.Cfg.UploadReclaimOnStart,
			}
			sourceRegistry := worker.NewArtifactSourceRegistry()
			if provider, ok := a.CapRouter.Get("google-drive"); ok {
				if driveImporter, typeOK := provider.(services.DriveImporter); typeOK {
					if authDriveSrc, buildErr := worker.NewAuthenticatedDriveSource(driveImporter, a.Vault); buildErr == nil {
						if regErr := sourceRegistry.Register(authDriveSrc); regErr != nil {
							a.Logger.Error("upload worker: register authenticated drive source", "error", regErr)
						}
					} else {
						a.Logger.Error("upload worker: build authenticated drive source", "error", buildErr)
					}
				}
			}
			if regErr := sourceRegistry.Register(worker.NewVeloxSource(a.Logger, a.Cfg.VeloxAPIToken)); regErr != nil {
				a.Logger.Error("upload worker: register velox source", "error", regErr)
			}
			a.Logger.Info("upload worker: source registry built", "sources_registered", sourceRegistry.Names())

			uw := worker.NewUploadWorker(
				repository.NewUploadJobRepository(a.DB),
				repository.NewMediaAssetRepository(a.DB),
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.StorageProvider,
				a.CapRouter,
				a.Vault,
				sourceRegistry,
				repository.NewExternalDeliveryRepository(a.DB),
				time.Duration(a.Cfg.UploadWorkerIntervalSeconds)*time.Second,
				slog.Default(),
				uploadOpts,
			)
			return uw.Run(ctx)
		},
	})

	// 9. Drive batch crawler — drains import_batches rows
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "drive_batch_crawler",
		Critical: true,
		Run: func(ctx context.Context) error {
			crawlerOpts := worker.DriveBatchCrawlerOptions{
				ClaimInterval:     5 * time.Second,
				LeaseTTL:          5 * time.Minute,
				HeartbeatInterval: 100 * time.Second,
				ReclaimInterval:   30 * time.Second,
				ReclaimOnStart:    true,
			}
			dbcc := worker.NewDriveBatchCrawler(
				repository.NewImportBatchRepository(a.DB),
				repository.NewUploadJobRepository(a.DB),
				a.Vault,
				a.CapRouter,
				"drive-batch-crawler",
				crawlerOpts,
				slog.Default(),
			)
			return dbcc.Run(ctx)
		},
	})

	slog.Info("9 background workers registered: publish / reconcile / outbox / webhook / metrics / sessions_cleanup / velox_downloader / upload / drive_batch_crawler")

	criticalErrCh := a.WorkerRegistry.StartAll(ctx)

	select {
	case err := <-criticalErrCh:
		if err != nil {
			slog.Error("critical worker exited unexpectedly", "error", err)
			a.WorkerRegistry.StopAll(15 * time.Second)
			if a.OneTimeCodes != nil {
				a.OneTimeCodes.Stop()
			}
			return err
		}
	case <-ctx.Done():
		slog.Info("context cancelled, broadcasting shutdown to all workers")
	}

	shutdownErr := a.WorkerRegistry.StopAll(15 * time.Second)
	if shutdownErr != nil {
		slog.Warn("worker shutdown did not complete cleanly", "error", shutdownErr)
	} else {
		slog.Info("all background workers drained")
	}

	if a.OneTimeCodes != nil {
		a.OneTimeCodes.Stop()
		slog.Info("OneTimeCodeStore sweeper stopped")
	}

	return shutdownErr
}

func valueString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func valueStringMap(m map[string]any, k string) string { v, _ := m[k].(string); return v }
func valueStringPtrMap(m map[string]any, k string) *string {
	v := valueStringMap(m, k)
	if v == "" {
		return nil
	}
	return &v
}
func valueIntPtrMap(m map[string]any, k string) *int64 {
	v, ok := m[k].(float64)
	if !ok {
		return nil
	}
	n := int64(v)
	return &n
}
func valueIntsMap(m map[string]any, k string) []int64 {
	raw, _ := m[k].([]any)
	out := make([]int64, 0, len(raw))
	for _, x := range raw {
		if v, ok := x.(float64); ok {
			out = append(out, int64(v))
		}
	}
	return out
}

type connectionStateStoreWrapper struct {
	repo *repository.ConnectionStateRepository
}

func (w *connectionStateStoreWrapper) Create(state *repository.ConnectionState) error {
	return w.repo.Create(state)
}

func (w *connectionStateStoreWrapper) Consume(id string, expectedNonce string, jwtWorkspaceID int64) (*repository.ConnectionState, error) {
	parsedID, err := uuid.Parse(id)
	if err != nil {
		return nil, fmt.Errorf("invalid uuid: %w", err)
	}
	return w.repo.Consume(parsedID, expectedNonce, jwtWorkspaceID)
}

type auditLogStoreWrapper struct {
	repo *repository.AuditLogRepository
}

// StartMetricsServer starts an optional internal HTTP server for the
// /metrics endpoint when cfg.MetricsPort > 0. It binds to
// cfg.MetricsHost (default 127.0.0.1) and serves the same
// basic-auth-gated handler used by /api/v1/metrics. Returns a shutdown
// function that callers MUST invoke during graceful shutdown. When
// MetricsPort is 0 the returned shutdown is a no-op.
func StartMetricsServer(cfg *config.Config, logger *slog.Logger) (shutdown func(context.Context) error) {
	if cfg.MetricsPort == 0 {
		return func(context.Context) error { return nil }
	}

	host := cfg.MetricsHost
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.MetricsPort)

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.MetricsHandler(cfg.MetricsBasicAuthUser, cfg.MetricsBasicAuthPass),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if logger == nil {
		logger = slog.Default()
	}

	go func() {
		logger.Info("metrics server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	return srv.Shutdown
}

func (w *auditLogStoreWrapper) Log(ctx context.Context, eventType, actorID string, resourceType, resourceID string, metadata map[string]interface{}) error {
	var userID int64
	if actorID != "" && actorID != "system" {
		_, _ = fmt.Sscan(actorID, &userID)
	}
	var resID int64
	if resourceID != "" {
		_, _ = fmt.Sscan(resourceID, &resID)
	}

	result := "success"
	if r, ok := metadata["result"].(string); ok {
		result = r
	}

	ipHash := ""
	if ip, ok := metadata["ip_hash"].(string); ok {
		ipHash = ip
	}

	sessionID := ""
	if sid, ok := metadata["session_id"].(string); ok {
		sessionID = sid
	}

	logEntry := &models.AuditLog{
		UserID:       userID,
		SessionID:    sessionID,
		Action:       eventType,
		ResourceType: resourceType,
		ResourceID:   resID,
		Result:       result,
		IPHash:       ipHash,
		Metadata:     metadata,
	}

	return w.repo.Insert(logEntry)
}
