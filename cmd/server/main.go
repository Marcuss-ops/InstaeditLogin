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
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// authModeLabel removed in Taglio 1.1: the JWT middleware is strict by
// construction (every protected request must carry a valid Bearer token,
// no synthetic fallback, no body/query user_id). The startup log no
// longer needs a "mode" banner.

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Starting InstaEditLogin server v2.0.0...")

	slog.Info("Environment", "app_env", cfg.AppEnv, "auth", "strict (JWT Bearer required on every protected route)")

	// Taglio 1.1: the legacy-mode fail-fast guard is gone. There is no
	// fallback that trusts request bodies/queries — the JWT middleware is
	// strict by construction in every environment.

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
	registry := services.NewPlatformRegistry()

	// Instagram: registers when INSTAGRAM_REDIRECT_URI is configured.
	// Uses the shared Meta OAuth credentials (META_APP_ID / META_APP_SECRET).
	instagramSvc, err := services.NewInstagramOAuthService(cfg, tokenRepo)
	if err != nil {
		slog.Error("Failed to create Instagram OAuth service", "error", err)
		os.Exit(1)
	}
	if instagramSvc != nil {
		registry.RegisterPlatformService(instagramSvc.Name(), instagramSvc)
		slog.Info("Instagram OAuth provider registered")
	} else {
		slog.Info("Instagram OAuth provider skipped (INSTAGRAM_REDIRECT_URI not set)")
	}

	// Facebook: registers when FACEBOOK_REDIRECT_URI is configured.
	facebookSvc, err := services.NewFacebookOAuthService(cfg, tokenRepo)
	if err != nil {
		slog.Error("Failed to create Facebook OAuth service", "error", err)
		os.Exit(1)
	}
	if facebookSvc != nil {
		registry.RegisterPlatformService(facebookSvc.Name(), facebookSvc)
		slog.Info("Facebook OAuth provider registered")
	} else {
		slog.Info("Facebook OAuth provider skipped (FACEBOOK_REDIRECT_URI not set)")
	}

	// Threads: registers when THREADS_REDIRECT_URI is configured.
	threadsSvc, err := services.NewThreadsOAuthService(cfg, tokenRepo)
	if err != nil {
		slog.Error("Failed to create Threads OAuth service", "error", err)
		os.Exit(1)
	}
	if threadsSvc != nil {
		registry.RegisterPlatformService(threadsSvc.Name(), threadsSvc)
		slog.Info("Threads OAuth provider registered")
	} else {
		slog.Info("Threads OAuth provider skipped (THREADS_REDIRECT_URI not set)")
	}

	if cfg.TikTokClientKey != "" {
		tiktokSvc, err := services.NewTikTokOAuthService(cfg, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create TikTok OAuth service", "error", err)
		} else {
			registry.RegisterPlatformService(tiktokSvc.Name(), tiktokSvc)
			slog.Info("TikTok OAuth provider registered")
		}
	} else {
		slog.Info("TikTok OAuth provider skipped (no credentials)")
	}

	if cfg.TwitterClientID != "" {
		twitterSvc, err := services.NewTwitterOAuthService(cfg, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create Twitter OAuth service", "error", err)
		} else {
			registry.RegisterPlatformService(twitterSvc.Name(), twitterSvc)
			slog.Info("Twitter OAuth provider registered")
		}
	} else {
		slog.Info("Twitter OAuth provider skipped (no credentials)")
	}

	if cfg.YouTubeClientID != "" {
		youtubeSvc, err := services.NewYouTubeOAuthService(cfg, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create YouTube OAuth service", "error", err)
		} else {
			registry.RegisterPlatformService(youtubeSvc.Name(), youtubeSvc)
			slog.Info("YouTube OAuth provider registered")
		}
	} else {
		slog.Info("YouTube OAuth provider skipped (no credentials)")
	}

	if cfg.LinkedInClientID != "" {
		linkedinSvc, err := services.NewLinkedInOAuthService(cfg, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create LinkedIn OAuth service", "error", err)
		} else {
			registry.RegisterPlatformService(linkedinSvc.Name(), linkedinSvc)
			slog.Info("LinkedIn OAuth provider registered")
		}
	} else {
		slog.Info("LinkedIn OAuth provider skipped (no credentials)")
	}

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

	// Build the optional router options for storage. The provider is
	// selected at startup via env vars: Supabase (URL+KEY+BUCKET) OR
	// AWS S3 (REGION+KEY_ID+SECRET+BUCKET). When neither is fully set
	// the storage handlers return 501 Not Implemented so the rest of
	// the server still boots.
	opts := []api.RouterOption{}
	if cfg.SupabaseURL != "" && cfg.SupabaseServiceKey != "" && cfg.SupabaseBucket != "" {
		opts = append(opts,
			api.WithStorageProvider(services.NewSupabaseProvider(
				cfg.SupabaseURL, cfg.SupabaseServiceKey, cfg.SupabaseBucket, slog.Default())),
			api.WithMaxUploadBytes(cfg.MaxUploadBytes))
		slog.Info("storage provider: Supabase configured", "bucket", cfg.SupabaseBucket)
	} else if cfg.AWSRegion != "" && cfg.AWSAccessKeyID != "" && cfg.AWSSecretAccessKey != "" && cfg.AWSBucket != "" {
		opts = append(opts,
			api.WithStorageProvider(services.NewS3Provider(
				cfg.AWSRegion, cfg.AWSBucket, cfg.AWSAccessKeyID, cfg.AWSSecretAccessKey, slog.Default())),
			api.WithMaxUploadBytes(cfg.MaxUploadBytes))
		slog.Info("storage provider: AWS S3 configured", "bucket", cfg.AWSBucket, "region", cfg.AWSRegion)
	} else {
		slog.Warn("storage provider: none configured (set SUPABASE_URL+SUPABASE_SERVICE_KEY+SUPABASE_BUCKET OR AWS_REGION+AWS_ACCESS_KEY_ID+AWS_SECRET_ACCESS_KEY+AWS_S3_BUCKET for /api/v1/storage/upload-url)")
	}
	router := api.NewRouter(registry, userRepo, authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(oneTimeCodes)}, opts...)...)
	slog.Info("Router configured",
		"jwt_ttl_hours", cfg.JWTTTLHours,
		"auth", "strict (JWT bearer required)",
		"frontend_url", cfg.FrontendURL,
		"cors_origins", corsOrigins)
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
	// PlatformService implementations registered above. Cancelled before
	// srv.Shutdown drains in-flight HTTP requests so the worker gets first
	// dibs on DB connections during graceful shutdown.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		publishWorker := worker.NewPublishWorker(
			repository.NewPostRepository(db),
			repository.NewUserRepository(db),
			registry,
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
