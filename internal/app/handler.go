package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// InitHandler sets up the full application: config, DB, migrations, platform
// providers, router. Returns the HTTP handler and a cleanup function.
// Used by both the standalone server (cmd/server) and Vercel serverless (api/).
func InitHandler() (http.Handler, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("config load: %w", err)
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
		return nil, nil, fmt.Errorf("database connect: %w", err)
	}

	slog.Info("Database connection established")

	if err := database.Migrate(db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("database migrate: %w", err)
	}

	slog.Info("Database migrations completed")

	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)

	platforms := make(map[string]services.PlatformService)

	metaSvc, err := services.NewFacebookOAuthService(cfg, userRepo, tokenRepo)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("meta oauth: %w", err)
	}
	platforms[metaSvc.GetPlatform()] = metaSvc
	slog.Info("Meta/Facebook OAuth provider registered")

	if cfg.TikTokClientKey != "" {
		tiktokSvc, err := services.NewTikTokOAuthService(cfg, userRepo, tokenRepo)
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
		twitterSvc, err := services.NewTwitterOAuthService(cfg, userRepo, tokenRepo)
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
		youtubeSvc, err := services.NewYouTubeOAuthService(cfg, userRepo, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create YouTube OAuth service", "error", err)
		} else {
			platforms[youtubeSvc.GetPlatform()] = youtubeSvc
			slog.Info("YouTube OAuth provider registered")
		}
	} else {
		slog.Info("YouTube OAuth provider skipped (no credentials)")
	}

	router := api.NewRouter(platforms, userRepo)
	handler := router.Setup()

	cleanup := func() {
		slog.Info("Shutting down...")
		db.Close()
	}

	return handler, cleanup, nil
}
