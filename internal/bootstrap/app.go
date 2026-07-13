// Package bootstrap owns the shared startup wiring for every InstaEditLogin
// binary (cmd/api, cmd/worker, cmd/migrate, cmd/server).
//
// Blocco #2.1 split cmd/server/main.go into:
//   - cmd/api     — HTTP only
//   - cmd/worker  — 5 background goroutines
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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/providers"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// App is the wired runtime holding every dependency that the api and
// worker binaries share. cmd/api reads App.HTTPHandler (and App.Cfg for
// PORT); cmd/worker reads App.DB / App.Vault / App.CapRouter /
// App.WebhookRepo to construct + supervise the 5 goroutines; cmd/server
// (the wrapper) reads both halves.
type App struct {
	Cfg         *config.Config
	DB          *sql.DB
	Vault       credentials.VaultAPI
	CapRouter   *services.CapabilityRouter
	WebhookRepo *repository.WebhookRepository
	HTTPHandler http.Handler
	Logger      *slog.Logger

	// WorkerStatus (Blocco #5.3) tracks per-goroutine startup
	// signals; consumed by /ready. Always non-nil after Wire().
	// The type lives in pkg/api (not bootstrap) because pkg/api
	// owns the /ready handler that reads it; bootstrap only
	// constructs + stores a reference.
	WorkerStatus *api.WorkerStatus

	// SentryHub (Blocco #5.3). Nil when SENTRY_DSN is empty
	// (operator-disables-by-omission contract). When non-nil, the
	// panic-catching middleware uses sentryhttp.New() against
	// this hub so CaptureException flows correct on every panic.
	SentryHub *sentry.Hub
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
	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

	registry, err := providers.BuildRegistry(cfg)
	if err != nil {
		return nil, fmt.Errorf("build provider registry: %w", err)
	}
	capRouter := registry

	authMgr := auth.NewManager(cfg.JWTSecret, cfg.JWTTTLHours).WithEnv(cfg.AppEnv)
	oneTimeCodes := api.NewOneTimeCodeStore(60 * time.Second)
	// oneTimeCodes ticker terminates with the process — no explicit
	// Stop() needed. The original cmd/server/main.go did `defer
	// oneTimeCodes.Stop()` as defensive cleanup; we drop that here
	// because process termination collects the ticker anyway.

	corsOrigins := cfg.AllowedCORSOrigins
	if len(corsOrigins) == 0 && cfg.FrontendURL != "" {
		corsOrigins = []string{cfg.FrontendURL}
	}

	storageProvider, err := services.NewS3Provider(
		cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region,
		cfg.S3AccessKey, cfg.S3SecretKey, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("construct S3 provider: %w", err)
	}
	slog.Info("storage provider: S3-compatible configured",
		"endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket, "region", cfg.S3Region)

	userWorkspaceHelper := api.RepoUserWorkspaceHelper(workspaceRepo, teamRepo)
	authEmailBackend := services.NewAuthService(userRepo, workspaceRepo, teamRepo, authMgr, cfg.JWTSecret)
	authEmailSvc := api.NewAuthEmailServiceAdapter(authEmailBackend)

	sessionRepo := repository.NewSessionRepository(db)
	sessionsSvc := services.NewSessionsService(sessionRepo, authMgr)

	rateLimitRepo := repository.NewRateLimitRepository(db)
	rateLimitSvc := services.NewRateLimitService(rateLimitRepo)

	webhookRepo := repository.NewWebhookRepository(db)

	opts := []api.RouterOption{
		api.WithCredentialVault(vault),
		api.WithStorageProvider(storageProvider),
		api.WithMaxUploadBytes(cfg.MaxUploadBytes),
		api.WithApiKeyStore(apiKeyRepo),
		api.WithApiKeyAuthenticator(apiKeyAuth),
		api.WithIdempotencyStore(idempotencyRepo),
		api.WithUserWorkspaceHelper(userWorkspaceHelper),
		api.WithTeamStore(teamRepo),
		api.WithAuthEmailService(authEmailSvc),
		api.WithSessionsService(sessionsSvc),
		api.WithCookieSecure(true),
		api.WithRateLimitService(rateLimitSvc),
		api.WithWebhookStore(webhookRepo),
	}
	// Set the worker_id singleton BEFORE any goroutine comes up so log
	// lines emitted from each worker tick carry the canonical
	// process-local worker_id.
	metrics.InitWorkerID()

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
	}

	// Inject the Sentry hub into the router options so the recovery
	// middleware can read it via the Router field (not via the App
	// field — pkg/api stays decoupled from internal/bootstrap).
	opts = append(opts, api.WithSentryHub(hub))

	// Blocco #5.3 — wire the DB + worker status into /ready's
	// contract. The DB is consumed via PingContext + SchemaHealthy;
	// the WorkerStatus is consumed via AllStarted. The worker
	// status instance is constructed HERE (not inside the App)
	// so the router reference AND the App.WorkerStatus reference
	// point at the SAME *api.WorkerStatus — flip a goroutine's
	// flag in RunWorkers and the /ready handler observes the
	// change in the same atomic map.
	workerStatus := api.NewWorkerStatus(api.WorkerNames)
	opts = append(opts, api.WithDB(db), api.WithWorkerStatus(workerStatus))

	router := api.NewRouter(capRouter, userRepo, authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(oneTimeCodes)}, opts...)...)

	slog.Info("Router configured",
		"jwt_ttl_hours", cfg.JWTTTLHours,
		"frontend_url", cfg.FrontendURL,
		"cors_origins", corsOrigins,
		"platforms", capRouter.Names(),
		"api_keys_enabled", apiKeyRepo != nil,
		"sentry_enabled", hub != nil,
		"ready_endpoint", "/ready")

	return &App{
		Cfg:          cfg,
		DB:           db,
		Vault:        vault,
		CapRouter:    capRouter,
		WebhookRepo:  webhookRepo,
		HTTPHandler:  router.Setup(),
		Logger:       logger,
		WorkerStatus: workerStatus,
		SentryHub:    hub,
	}, nil
}

// RunWorkers starts the 5 background goroutines (publish worker, reconcile
// worker, outbox dispatcher, webhook worker, metrics collector) and
// blocks until ctx is cancelled. On cancellation it cancels every
// goroutine concurrently and waits up to 15s per goroutine for their
// Run loops to drain gracefully.
//
// Insertion order (publish → reconcile → outbox → webhook → metrics)
// mirrors the pre-split cmd/server/main.go shape so the runtime ordering
// of the first log-line per goroutine is unchanged.
func (a *App) RunWorkers(ctx context.Context) error {
	type goroutineCtx struct {
		name   string
		cancel context.CancelFunc
		done   chan struct{}
	}

	children := []*goroutineCtx{}

	// 1. Publish worker driver — queued → publishing transition
	{
		c, cancel := context.WithCancel(ctx)
		d := make(chan struct{})
		go func() {
			defer close(d)
			// Blocco #5.3: flip the "started" flag as the FIRST
			// executable line in the goroutine. /ready uses this
			// for the no-deadlock assertion. The Mark call MUST
			// happen inside the goroutine (not before go) so the
			// published "started" state actually proves the
			// goroutine reached its first executable line.
			a.WorkerStatus.Mark("publish")
			pw := worker.NewPublishWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				time.Duration(a.Cfg.PublishWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			if err := pw.Run(c); err != nil && err != context.Canceled {
				slog.Error("publish worker exited with error", "error", err)
			}
		}()
		children = append(children, &goroutineCtx{"publish", cancel, d})
	}

	// 2. Reconcile worker — publishing → published | failed transition
	{
		c, cancel := context.WithCancel(ctx)
		d := make(chan struct{})
		go func() {
			defer close(d)
			a.WorkerStatus.Mark("reconcile")
			rw := worker.NewReconcileWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				time.Duration(a.Cfg.ReconcileWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			if err := rw.Run(c); err != nil && err != context.Canceled {
				slog.Error("reconcile worker exited with error", "error", err)
			}
		}()
		children = append(children, &goroutineCtx{"reconcile", cancel, d})
	}

	// 3. Outbox dispatcher — materialises publish_jobs audit rows
	{
		c, cancel := context.WithCancel(ctx)
		d := make(chan struct{})
		go func() {
			defer close(d)
			a.WorkerStatus.Mark("outbox")
			ds := outbox.NewDispatcher(outbox.DispatcherConfig{
				OutboxStore:  repository.NewOutboxRepository(a.DB),
				Process:      processors.NewPublishJobsMaterialiser(a.DB),
				Logger:       slog.Default(),
				TickInterval: outbox.DefaultTickInterval,
			})
			if err := ds.Run(c); err != nil && err != context.Canceled {
				slog.Error("outbox dispatcher exited with error", "error", err)
			}
		}()
		children = append(children, &goroutineCtx{"outbox", cancel, d})
	}

	// 4. Webhook worker — drains webhook_deliveries
	{
		c, cancel := context.WithCancel(ctx)
		d := make(chan struct{})
		go func() {
			defer close(d)
			a.WorkerStatus.Mark("webhook")
			ww := worker.NewWebhookWorker(a.WebhookRepo, time.Duration(a.Cfg.WebhookWorkerIntervalSeconds)*time.Second)
			if err := ww.Run(c); err != nil && err != context.Canceled {
				slog.Error("webhook worker exited with error", "error", err)
			}
		}()
		children = append(children, &goroutineCtx{"webhook", cancel, d})
	}

	// 5. Metrics collector — periodic gauges
	{
		c, cancel := context.WithCancel(ctx)
		d := make(chan struct{})
		go func() {
			defer close(d)
			a.WorkerStatus.Mark("metrics")
			if err := metrics.RunPeriodicCollector(c, a.DB, metrics.DefaultCollectorInterval, slog.Default()); err != nil && err != context.Canceled {
				slog.Error("metrics collector exited with error", "error", err)
			}
		}()
		children = append(children, &goroutineCtx{"metrics", cancel, d})
	}

	slog.Info("5 background goroutines started: publish / reconcile / outbox / webhook / metrics")

	// Block until ctx is cancelled.
	<-ctx.Done()
	slog.Info("context cancelled, broadcasting shutdown to all 5 goroutines")
	for _, child := range children {
		child.cancel()
	}
	// Drain concurrently — 15s per goroutine, parallel execution.
	for _, child := range children {
		select {
		case <-child.done:
			slog.Info("worker drained cleanly", "name", child.name)
		case <-time.After(15 * time.Second):
			slog.Warn("worker drain timeout, continuing shutdown", "name", child.name)
		}
	}
	slog.Info("all background goroutines drained")
	return nil
}
