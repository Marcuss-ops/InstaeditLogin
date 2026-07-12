package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

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
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Taglio 3.1: S3 storage is mandatory. The config validation
	// already rejects a missing S3_ENDPOINT/S3_BUCKET/S3_ACCESS_KEY/
	// S3_SECRET_KEY with a descriptive error, but we panic here too as
	// belt-and-suspenders: if a future refactor relaxes the validation
	// (or someone calls NewCredentialVault before config.validate
	// runs), the server must still refuse to start.
	if cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" {
		panic("S3 storage is required: set S3_ENDPOINT, S3_BUCKET, S3_ACCESS_KEY, S3_SECRET_KEY")
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Starting InstaEditLogin server v2.0.0...")

	slog.Info("Environment", "app_env", cfg.AppEnv)

	db, err := database.Connect(cfg)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("Database connection established")

	if err := database.Migrate(db); err != nil {
		slog.Error("Failed to run migrations", "error", err)
		os.Exit(1)
	}

	slog.Info("Database migrations completed")

	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		slog.Error("Failed to initialize encryptor", "error", err)
		os.Exit(1)
	}

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)
	// Taglio 4.6 — API key repository. Bound to the same *sql.DB
	// used by every other repo so it participates in the standard
	// connection pool + metrics counter. The Authenticator below
	// uses exactly two of its methods (FindByHash, MarkUsed) — the
	// rest are reachable through the ApiKeyStore interface exposed
	// to the HTTP handlers via WithApiKeyStore.
	apiKeyRepo := repository.NewApiKeyRepository(db)
	apiKeyAuth := auth.NewApiKeyAuthenticator(apiKeyRepo)
	// Zernio Milestone publish-state-machine — Idempotency-Key
	// cache backing for /api/v1/posts. Two methods exposed through
	// the IdempotencyStore interface (FindActiveByKey + Insert).
	// OPTIONAL wiring: the handler falls through silently if the
	// store is absent, so dev environments without migration 021
	// still work. Production deployments must wire it (this main.go
	// is production).
	idempotencyRepo := repository.NewIdempotencyRepository(db)

	// Taglio 2.2: the central CredentialVault. It owns the encryptor +
	// the *sql.DB (for pg_advisory_xact_lock during refresh) + the
	// TokenStore interface (adapted from *repository.TokenRepository).
	// No provider or consumer is allowed to import the internal
	// repository directly — they go through this vault.
	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

	// Taglio 4c: the one-shot YouTube refresh-token backfill was converted
	// to migration 013_backfill_youtube_refresh_tokens.sql and removed
	// from startup. No legacy records remain — the migration is idempotent.

	// Taglio 2.5: all platform-specific registration is encapsulated
	// in providers.BuildRegistry. The returned *CapabilityRegistry is a
	// type alias for *services.CapabilityRouter, so api.NewRouter
	// and worker.NewPublishWorker accept it without any import change.
	// Per-platform "registered / skipped" log lines are gone (the
	// single `platforms:` summary in the Router-configured line below
	// is enough for operators).
	registry, err := providers.BuildRegistry(cfg)
	if err != nil {
		slog.Error("Failed to build provider registry", "error", err)
		os.Exit(1)
	}
	capRouter := registry

	authMgr := auth.NewManager(cfg.JWTSecret, cfg.JWTTTLHours)
	oneTimeCodes := api.NewOneTimeCodeStore(60 * time.Second)
	defer oneTimeCodes.Stop()

	// Auto-add the configured FrontendURL to the CORS allowlist when none
	// was provided via CORS_ALLOWED_ORIGINS, so a single env var is enough
	// for local dev. Production deployments still set the explicit list.
	corsOrigins := cfg.AllowedCORSOrigins
	if len(corsOrigins) == 0 && cfg.FrontendURL != "" {
		corsOrigins = []string{cfg.FrontendURL}
	}

	// Taglio 3.1: S3 storage is the ONLY storage backend. The
	// config validation + startup panic above guarantee all four env
	// vars are set; we can build the provider unconditionally. There
	// is always available.
	storageProvider, err := services.NewS3Provider(
		cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Region,
		cfg.S3AccessKey, cfg.S3SecretKey, slog.Default())
	if err != nil {
		slog.Error("Failed to construct S3 storage provider (check S3_ENDPOINT format)", "error", err)
		os.Exit(1)
	}
	slog.Info("storage provider: S3-compatible configured",
		"endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket, "region", cfg.S3Region)

	opts := []api.RouterOption{
		api.WithCredentialVault(vault),
		api.WithStorageProvider(storageProvider),
		api.WithMaxUploadBytes(cfg.MaxUploadBytes),
		// Taglio 4.6 — API key wiring. Repository is exposed to
		// the /api/v1/api-keys handlers; Authenticator is the
		// middleware that turns Authorization: Bearer sk_* into
		// an authenticated request. Both are required in
		// production; tests can inject fakes via the same options
		// (see routes_test.go patterns).
		api.WithApiKeyStore(apiKeyRepo),
		api.WithApiKeyAuthenticator(apiKeyAuth),
		// Zernio Milestone publish-state-machine — idempotency cache
		// backing for handleCreatePost. Uses the Idempotency-Key
		// request header for at-most-once POST semantics; payload
		// hash mismatch on replay → 409.
		api.WithIdempotencyStore(idempotencyRepo),
	}
	router := api.NewRouter(capRouter, userRepo, authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(oneTimeCodes)}, opts...)...)
	slog.Info("Router configured",
		"jwt_ttl_hours", cfg.JWTTTLHours,
		"frontend_url", cfg.FrontendURL,
		"cors_origins", corsOrigins,
		"platforms", capRouter.Names(),
		"api_keys_enabled", apiKeyRepo != nil)
	handler := router.Setup()

	// Listen on PORT (Vercel / Railway / Render standard). Fallback to :8080.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("Server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Spawn the publish worker goroutine: picks up scheduled post_targets
	// whose scheduled_at <= NOW() and dispatches them through the per-platform
	// Publisher / OAuthProvider implementations registered above. The
	// worker shares the same CredentialVault as the HTTP router so
	// concurrent refreshes (e.g. worker tick + dashboard publish-now
	// button) serialise on the same Postgres advisory lock.
	//
	// Taglio 5.x: runOnce calls only tick() — the publish DRIVER's
	// 3-step transition queued → publishing → published|failed.
	// The async publishing → published|failed side is owned by the
	// separate ReconcileWorker goroutine spawned below.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		publishWorker := worker.NewPublishWorker(
			repository.NewPostRepository(db),
			repository.NewUserRepository(db),
			capRouter,
			vault,
			time.Duration(cfg.PublishWorkerIntervalSeconds)*time.Second,
			slog.Default(),
		)
		if err := publishWorker.Run(workerCtx); err != nil && err != context.Canceled {
			slog.Error("publish worker exited with error", "error", err)
		}
	}()

	// Taglio 5.x: spawn the RECONCILE worker goroutine — independent
	// cadence from the publish driver, same shape as the outbox
	// dispatcher (background goroutine + ctx-cancellable Run + Done
	// channel for parallel shutdown drains). Polls ListPublishing
	// (post_targets WHERE status='publishing' AND platform_post_id IS
	// NOT NULL) every cfg.ReconcileWorkerIntervalSeconds (default 5s)
	// and calls AsyncPublisher.Reconcile on each row — the canonical
	// platform-decoupled state-transition detector that wraps
	// CheckPublishStatus and decides published | failed | in-flight.
	//
	// Multi-replica safety lives at the platform's per-publish_id
	// state idempotency (and post_targets.provider_idempotency_key
	// for providers that use the Idempotency-Key model); two
	// reconcilers racing the same row will both write the same
	// terminal state on the same UpdateStatus and the second UPDATE
	// is a no-op.
	reconcileCtx, reconcileCancel := context.WithCancel(context.Background())
	reconcileDone := make(chan struct{})
	go func() {
		defer close(reconcileDone)
		reconcileWorker := worker.NewReconcileWorker(
			repository.NewPostRepository(db),
			repository.NewUserRepository(db),
			capRouter,
			vault,
			time.Duration(cfg.ReconcileWorkerIntervalSeconds)*time.Second,
			slog.Default(),
		)
		if err := reconcileWorker.Run(reconcileCtx); err != nil && err != context.Canceled {
			slog.Error("reconcile worker exited with error", "error", err)
		}
	}()

	// Spawn the outbox dispatcher goroutine: reads outbox_events rows
	// written atomically by PostRepository.Create and materialises
	// publish_jobs audit rows via the publish-jobs processor. This is
	// STEP 3 of the transactional-outbox pipeline (Taglio 5.x):
	//
	//   STEP 1 (post_repo::Create) → posts + post_targets + outbox_events
	//                              in one BEGIN/COMMIT tx
	//   STEP 2 (dispatcher Run)   → claim outbox row, heartbeat, process
	//   STEP 3 (this materialiser) → INSERT publish_jobs (audit-only);
	//                                post_targets.status remains the SoT
	//
	// The dispatcher is a SECOND background goroutine alongside the
	// publish worker. Both share the *sql.DB connection pool; the
	// worker reads post_targets.status='queued' (driver) while the
	// dispatcher writes publish_jobs.status='pending' (audit).
	// Multi-replica safety is delegated to the dispatcher's SKIP LOCKED
	// claim — see internal/outbox/dispatcher.go + repository/outbox_repo.go.
	dispatcherCtx, dispatcherCancel := context.WithCancel(context.Background())
	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		dispatcher := outbox.NewDispatcher(outbox.DispatcherConfig{
			OutboxStore:  repository.NewOutboxRepository(db),
			Process:      processors.NewPublishJobsMaterialiser(db),
			Logger:       slog.Default(),
			TickInterval: outbox.DefaultTickInterval,
		})
		if err := dispatcher.Run(dispatcherCtx); err != nil && err != context.Canceled {
			slog.Error("outbox dispatcher exited with error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// THREE independent drains — the publish worker, the reconcile
	// worker, and the outbox dispatcher have no shared shutdown
	// dependency. Cancelling in parallel + giving each its own 15s
	// budget is race-free vs a single join-channel that could fire
	// the 15s timeout while one goroutine is mid-MarkProcessed but
	// another has already closed. (Same shape as the prior 2-way
	// version; adding the third goroutine stacks another 15s budget
	// on the shutdown path — fast on graceful drain, only stretches
	// on hard hangs.)
	slog.Info("Shutting down: cancelling publish worker + reconcile worker + outbox dispatcher in parallel")
	workerCancel()
	reconcileCancel()
	dispatcherCancel()
	select {
	case <-workerDone:
		slog.Info("publish worker drained cleanly")
	case <-time.After(15 * time.Second):
		slog.Warn("publish worker drain timeout, continuing shutdown")
	}
	select {
	case <-reconcileDone:
		slog.Info("reconcile worker drained cleanly")
	case <-time.After(15 * time.Second):
		slog.Warn("reconcile worker drain timeout, continuing shutdown")
	}
	select {
	case <-dispatcherDone:
		slog.Info("outbox dispatcher drained cleanly")
	case <-time.After(15 * time.Second):
		slog.Warn("outbox dispatcher drain timeout, continuing shutdown")
	}

	slog.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server stopped")
}
