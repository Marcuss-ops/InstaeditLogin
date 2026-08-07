// cmd/server — InstaEditLogin dev / single-bundle wrapper (Blacco #2.1)
//
// Single-bundle dev path that runs:
//  1. bootstrap.Wire (config + DB + repos + services + Router)
//  2. database.Migrate (dev wrapper assumes exclusive DB access)
//  3. HTTP server (same path as cmd/api)
//  4. Optional 13 background workers (gated by RUN_WORKERS env)
//
// Production topology (cmd/api + cmd/worker + cmd/migrate as separate
// pods) does NOT use this wrapper. This binary survives for two reasons:
//   - Local-dev convenience ("just run cmd/server, everything works")
//   - Backward compatibility with the pre-Blocco #2.1 deploy shape
//     (Railway / Render single-process models)
//
// RUN_WORKERS=false disables the 13 background workers but keeps the
// HTTP server. Default true. Production-shaped binary deploys should
// use cmd/api + cmd/worker instead so per-service scaling is correct.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server: process failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// The bundled development wrapper shares one process, so use its
	// dedicated combined pool budget rather than API or worker alone.
	// This bundled process owns the combined API+worker pool budget.
	_ = os.Setenv("DB_POOL_ROLE", "server")
	_, _ = fmt.Fprintln(os.Stdout, "Starting InstaEditLogin dev wrapper (api + workers + migrate)")
	slog.Warn("cmd/server is deprecated; use cmd/migrate + cmd/api + cmd/worker (or make dev)", "scope", "development/recovery")

	app, err := bootstrap.Wire(context.Background())
	if err != nil {
		return fmt.Errorf("wire: %w", err)
	}
	defer func() {
		if err := app.DB.Close(); err != nil {
			slog.Warn("server: db close failed", "error", err)
		}
	}()

	// Migrate: dev wrapper assumes exclusive DB access. Production
	// deployments run cmd/migrate as a one-shot pre-deploy job.
	if err := database.MigrateWithExpectedInstallationUUID(app.DB, app.Cfg.Database.ExpectedInstallationUUID); err != nil {
		return fmt.Errorf("database migrate: %w", err)
	}
	if err := database.VerifyInstallationIdentity(context.Background(), app.DB, app.Cfg.Database.ExpectedInstallationUUID); err != nil {
		return fmt.Errorf("database identity verification (%s): %w", "DATABASE_IDENTITY_MISMATCH", err)
	}

	// RUN_WORKERS env (default true): false → API-only mode.
	// Only meaningful for this wrapper — cmd/api + cmd/worker are
	// strictly single-purpose by architectural design.
	runWorkers := true
	if v := os.Getenv("RUN_WORKERS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil && !b {
			runWorkers = false
		}
	}

	// Register the worker registry before starting any goroutine. A
	// registration failure must not leave workers or HTTP serving against
	// an App whose run() path is about to close the database.
	if runWorkers {
		if err := app.RegisterWorkerMetrics(); err != nil {
			return fmt.Errorf("register worker metrics: %w", err)
		}
	}

	var wg sync.WaitGroup
	workerErrCh := make(chan error, 1)

	// 13 background workers: only if RUN_WORKERS=true.
	var workersCancel context.CancelFunc = func() {} // no-op default
	if runWorkers {
		ctxWorkers, cancel := context.WithCancel(context.Background())
		workersCancel = cancel
		wg.Add(1)
		go func() {
			defer wg.Done()
			slog.Info("server: launching 13 background workers (RUN_WORKERS=true)")
			if err := app.RunWorkers(ctxWorkers); err != nil && err != context.Canceled {
				workerErrCh <- err
			}
		}()
	} else {
		slog.Info("server: RUN_WORKERS=false, skipping background workers (API-only mode)")
	}

	// HTTP server — same shape as cmd/api.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      app.HTTPHandler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		slog.Info("server: http listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server: http failed", "error", err)
		}
	}()

	// RegisterWorkerMetrics ran before any goroutine started, so the
	// metrics server can now safely expose the registry collector.
	metricsShutdown := bootstrap.StartMetricsServer(app.Cfg, app.Logger)

	// Single-channel signal handling drives BOTH drain paths
	// concurrently. The cancel/Wait pair matches the pre-Blocco #2.1
	// shape: stop-signal → parallel cancel + srv.Shutdown → wg.Wait
	// only completes when ALL spawned goroutines return.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	var workerErr error
	select {
	case sig := <-quit:
		slog.Info("server: received signal, initiating parallel shutdown", "signal", sig.String())
	case workerErr = <-workerErrCh:
		slog.Error("server: critical worker failed, initiating shutdown", "error", workerErr)
	}

	// Cancel workers (triggers 15s internal drain per leaf in app.RunWorkers).
	workersCancel()

	// HTTP drain: 30s budget.
	ctxHTTP, cancelHTTP := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelHTTP()
	if err := srv.Shutdown(ctxHTTP); err != nil {
		slog.Error("server: http forced to shutdown", "error", err)
	} else {
		slog.Info("server: http stopped cleanly")
	}

	ctxMetrics, cancelMetrics := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelMetrics()
	if err := metricsShutdown(ctxMetrics); err != nil {
		slog.Error("server: metrics server forced to shutdown", "error", err)
	}

	wg.Wait()
	// A signal and a critical worker failure may arrive concurrently. The
	// signal branch above intentionally starts the drain immediately; once
	// every worker goroutine has returned, collect any buffered critical
	// error so it cannot be mistaken for a clean shutdown.
	if workerErr == nil {
		select {
		case workerErr = <-workerErrCh:
		default:
		}
	}
	slog.Info("server: graceful shutdown complete")
	if workerErr != nil {
		return fmt.Errorf("critical worker failed: %w", workerErr)
	}
	return nil
}
