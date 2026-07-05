package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logLevel := slog.LevelInfo
	if cfg.LogLevel == "debug" {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	slog.Info("Starting InstaEditLogin server v2.0.0...")

	// Connect to PostgreSQL
	db, err := database.Connect(cfg)
	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("Database connection established")

	// Run migrations
	if err := database.Migrate(db); err != nil {
		slog.Error("Failed to run migrations", "error", err)
		os.Exit(1)
	}

	slog.Info("Database migrations completed")

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	tokenRepo := repository.NewTokenRepository(db)

	// Initialize all platform providers
	platforms := make(map[string]services.PlatformService)

	// Meta / Facebook + Instagram (always required)
	metaSvc, err := services.NewFacebookOAuthService(cfg, userRepo, tokenRepo)
	if err != nil {
		slog.Error("Failed to create Meta OAuth service", "error", err)
		os.Exit(1)
	}
	platforms[metaSvc.GetPlatform()] = metaSvc
	slog.Info("Meta/Facebook OAuth provider registered")

	// TikTok (optional — only if credentials are set)
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

	// Twitter/X (optional)
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

	// YouTube (optional)
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

	// LinkedIn (optional)
	if cfg.LinkedInClientID != "" {
		linkedinSvc, err := services.NewLinkedInOAuthService(cfg, userRepo, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create LinkedIn OAuth service", "error", err)
		} else {
			platforms[linkedinSvc.GetPlatform()] = linkedinSvc
			slog.Info("LinkedIn OAuth provider registered")
		}
	} else {
		slog.Info("LinkedIn OAuth provider skipped (no credentials)")
	}

	// Pinterest (optional)
	if cfg.PinterestAppID != "" {
		pinterestSvc, err := services.NewPinterestOAuthService(cfg, userRepo, tokenRepo)
		if err != nil {
			slog.Warn("Failed to create Pinterest OAuth service", "error", err)
		} else {
			platforms[pinterestSvc.GetPlatform()] = pinterestSvc
			slog.Info("Pinterest OAuth provider registered")
		}
	} else {
		slog.Info("Pinterest OAuth provider skipped (no credentials)")
	}

	// Setup HTTP router
	router := api.NewRouter(platforms, userRepo)
	handler := router.Setup()

	// Create HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", cfg.ServerHost, cfg.ServerPort),
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

	// Graceful shutdown
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
