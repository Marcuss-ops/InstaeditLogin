// cmd/api — InstaEditLogin HTTP API server (Blocco #2.1)
//
// Runs ONLY the HTTP listener + chi router. Background goroutines
// (publish worker, reconcile worker, outbox dispatcher, webhook
// worker, metrics collector) are NOT started here — they live in the
// cmd/worker binary, which runs separately in production deployments.
//
// /api/v1/health is exposed (used by container orchestrators).
//
// Production deploy: cmd/migrate as a one-shot pre-deploy, then one or
// more cmd/api pods behind a load balancer. cmd/worker pods run
// independent of cmd/api pods.
//
// dev: cmd/server (wrapper) launches BOTH api + worker in one process.
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

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
)

func main() {
	_, _ = fmt.Fprintln(os.Stdout, "Starting InstaEditLogin API (Blocco #2.1: split from cmd/server)")

	app, err := bootstrap.Wire(context.Background())
	if err != nil {
		slog.Error("api: wire failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := app.DB.Close(); err != nil {
			slog.Warn("api: db close failed", "error", err)
		}
	}()

	// PORT is the Vercel/Railway/Render standard. Defaults to :8080
	// for local-dev systems. Container-orchestrators (Dockerfile
	// target `api`) inject PORT via env.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port

	srv := &http.Server{
		Addr:         addr,
		Handler:      app.HTTPHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("api: server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("api: server failed", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("api: graceful shutdown initiated (30s HTTP drain budget)")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("api: server forced to shutdown", "error", err)
	}
	slog.Info("api: server stopped")
}
