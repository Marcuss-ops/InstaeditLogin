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
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// authModeLabel returns a short banner used in the startup log line so an
// operator can immediately tell whether the server is in strict mode (safe
// default) or legacy fallback (rollback window, accepts user_id from body).
func authModeLabel(strict bool) string {
	if strict {
		return "strict (Bearer required)"
	}
	return "legacy (publish trusts user_id — rollback only)"
}

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
	platforms := make(map[string]services.PlatformService)

	metaSvc, err := services.NewFacebookOAuthService(cfg, tokenRepo)
	if err != nil {
		slog.Error("Failed to create Meta OAuth service", "error", err)
		os.Exit(1)
	}
	platforms[metaSvc.GetPlatform()] = metaSvc
	slog.Info("Meta/Facebook OAuth provider registered")

	if cfg.TikTokClientKey != "" {
		tiktokSvc, err := services.NewTikTokOAuthService(cfg, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create TikTok OAuth service", "error", err)
		} else {
			platforms[tiktokSvc.GetPlatform()] = tiktokSvc
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
			platforms[twitterSvc.GetPlatform()] = twitterSvc
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
			platforms[youtubeSvc.GetPlatform()] = youtubeSvc
			slog.Info("YouTube OAuth provider registered")
		}
	} else {
		slog.Info("YouTube OAuth provider skipped (no credentials)")
	}

	authMgr := auth.NewManager(cfg.JWTSecret, cfg.JWTTTLHours)
	// Auto-add the configured FrontendURL to the CORS allowlist when none
	// was provided via CORS_ALLOWED_ORIGINS, so a single env var is enough
	// for local dev. Production deployments still set the explicit list.
	corsOrigins := cfg.AllowedCORSOrigins
	if len(corsOrigins) == 0 && cfg.FrontendURL != "" {
		corsOrigins = []string{cfg.FrontendURL}
	}
	router := api.NewRouter(platforms, userRepo, authMgr, cfg.StrictJWTAuth, cfg.FrontendURL, corsOrigins)
	slog.Info("Router configured",
		"jwt_ttl_hours", cfg.JWTTTLHours,
		"strict_jwt_auth", cfg.StrictJWTAuth,
		"auth_mode", authModeLabel(cfg.StrictJWTAuth),
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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server stopped")
}
