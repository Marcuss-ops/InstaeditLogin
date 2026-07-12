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

	// Taglio 2.1: the composite PlatformService is gone. The CapabilityRouter
	// holds providers by name and dispatches per-capability lookups
	// (OAuth / Publisher / AccountDiscoverer / ContentValidator /
	// PublishReconciler). The router uses type assertions on Register so each
	// provider only carries the methods it actually supports.
	capRouter := services.NewCapabilityRouter()
	tokenSvc := services.NewTokenService(enc, tokenRepo)

	// Facebook: registers when FACEBOOK_REDIRECT_URI is configured.
	// Uses the shared Meta OAuth credentials (META_APP_ID / META_APP_SECRET).
	facebookSvc, err := services.NewFacebookOAuthService(cfg)
	if err != nil {
		slog.Error("Failed to create Facebook OAuth service", "error", err)
		os.Exit(1)
	}
	if facebookSvc != nil {
		capRouter.Register(facebookSvc.Name(), facebookSvc)
		slog.Info("Facebook OAuth provider registered", "capabilities", describeCapabilities(facebookSvc))
	} else {
		slog.Info("Facebook OAuth provider skipped (FACEBOOK_REDIRECT_URI not set)")
	}

	if cfg.TikTokClientKey != "" {
		tiktokSvc, err := services.NewTikTokOAuthService(cfg)
		if err != nil {
			slog.Warn("Failed to create TikTok OAuth service", "error", err)
		} else {
			capRouter.Register(tiktokSvc.Name(), tiktokSvc)
			slog.Info("TikTok OAuth provider registered", "capabilities", describeCapabilities(tiktokSvc))
		}
	} else {
		slog.Info("TikTok OAuth provider skipped (no credentials)")
	}

	if cfg.TwitterClientID != "" {
		twitterSvc, err := services.NewTwitterOAuthService(cfg)
		if err != nil {
			slog.Warn("Failed to create Twitter OAuth service", "error", err)
		} else {
			capRouter.Register(twitterSvc.Name(), twitterSvc)
			slog.Info("Twitter OAuth provider registered", "capabilities", describeCapabilities(twitterSvc))
		}
	} else {
		slog.Info("Twitter OAuth provider skipped (no credentials)")
	}

	if cfg.YouTubeClientID != "" {
		youtubeSvc, err := services.NewYouTubeOAuthService(cfg)
		if err != nil {
			slog.Warn("Failed to create YouTube OAuth service", "error", err)
		} else {
			capRouter.Register(youtubeSvc.Name(), youtubeSvc)
			slog.Info("YouTube OAuth provider registered", "capabilities", describeCapabilities(youtubeSvc))
		}
	} else {
		slog.Info("YouTube OAuth provider skipped (no credentials)")
	}

	if cfg.LinkedInClientID != "" {
		linkedinSvc, err := services.NewLinkedInOAuthService(cfg)
		if err != nil {
			slog.Warn("Failed to create LinkedIn OAuth service", "error", err)
		} else {
			capRouter.Register(linkedinSvc.Name(), linkedinSvc)
			slog.Info("LinkedIn OAuth provider registered", "capabilities", describeCapabilities(linkedinSvc))
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
	opts := []api.RouterOption{
		api.WithTokenService(tokenSvc),
	}
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
	router := api.NewRouter(capRouter, userRepo, authMgr, cfg.FrontendURL, corsOrigins,
		append([]api.RouterOption{api.WithOneTimeCodeStore(oneTimeCodes)}, opts...)...)
	slog.Info("Router configured",
		"jwt_ttl_hours", cfg.JWTTTLHours,
		"auth", "strict (JWT bearer required)",
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
	// Publisher / OAuthProvider implementations registered above. Taglio 2.1:
	// the worker takes the CapabilityRouter + a TokenStorage (so the
	// EnsureFreshToken path is shared with the HTTP router).
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		publishWorker := worker.NewPublishWorker(
			repository.NewPostRepository(db),
			repository.NewUserRepository(db),
			capRouter,
			tokenSvc,
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

// describeCapabilities returns a human-readable list of the small
// capability interfaces a provider value implements. It exists purely
// for the startup log so operators can see at a glance which providers
// support which surfaces (e.g. Facebook has AccountDiscoverer, TikTok
// has PublishReconciler, LinkedIn does not).
func describeCapabilities(p any) string {
	out := []string{}
	if _, ok := p.(services.OAuthProvider); ok {
		out = append(out, "OAuth")
	}
	if _, ok := p.(services.AccountDiscoverer); ok {
		out = append(out, "Discover")
	}
	if _, ok := p.(services.ContentValidator); ok {
		out = append(out, "Validate")
	}
	if _, ok := p.(services.Publisher); ok {
		out = append(out, "Publish")
	}
	if _, ok := p.(services.PublishReconciler); ok {
		out = append(out, "Reconcile")
	}
	return fmt.Sprintf("%v", out)
}
