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

	// One-shot backfill: migrate any YouTube refresh tokens stored as legacy
	// "refresh_token:..." scopes into the dedicated encrypted_refresh_token column.
	// The backfill uses the encryptor directly (not the vault) because it
	// predates the vault and is a startup-time one-shot — a one-off
	// migration that touches rows the vault has no business reading.
	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		slog.Error("Failed to initialize encryptor for backfill", "error", err)
		os.Exit(1)
	}
	if err := database.BackfillYouTubeRefreshTokens(db, enc); err != nil {
		slog.Warn("YouTube refresh-token backfill failed (non-fatal)", "error", err)
	}

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)

	// Taglio 2.2: the central CredentialVault. It owns the encryptor +
	// the *sql.DB (for pg_advisory_xact_lock during refresh) + the
	// TokenStore interface (adapted from *repository.TokenRepository).
	// No provider or consumer is allowed to import the internal
	// repository directly — they go through this vault.
	vault := credentials.NewCredentialVault(enc, db, tokenRepo)

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
	// is no "no storage + 501" mode — /api/v1/storage/upload-url is
	// always available.
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
	}
	router := api.NewRouter(capRouter, userRepo, authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(oneTimeCodes)}, opts...)...)
	slog.Info("Router configured",
		"jwt_ttl_hours", cfg.JWTTTLHours,
		"frontend_url", cfg.FrontendURL,
		"cors_origins", corsOrigins,
		"platforms", capRouter.Names())
	handler := router.Setup()

	// Vercel injects PORT; fall back to config or 8080
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	} else if cfg.ServerPort != "" {
		addr = cfg.ServerHost + ":" + cfg.ServerPort
	}

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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down: cancelling publish worker first")
	workerCancel()
	select {
	case <-workerDone:
		slog.Info("publish worker drained cleanly")
	case <-time.After(15 * time.Second):
		slog.Warn("publish worker drain timeout, continuing shutdown")
	}

	slog.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server stopped")
}
