// cmd/server — InstaEditLogin dev / single-bundle wrapper (Blacco #2.1)
//
// Single-bundle dev path that runs:
//  1. bootstrap.Wire (config + DB + repos + services + Router)
//  2. database.Migrate (dev wrapper assumes exclusive DB access)
//  3. HTTP server (same path as cmd/api)
//  4. Optional 5 background goroutines (gated by RUN_WORKERS env)
//
// Production topology (cmd/api + cmd/worker + cmd/migrate as separate
// pods) does NOT use this wrapper. This binary survives for two reasons:
//   - Local-dev convenience ("just run cmd/server, everything works")
//   - Backward compatibility with the pre-Blocco #2.1 deploy shape
//     (Railway / Render single-process models)
//
// RUN_WORKERS=false disables the 5 background goroutines but keeps the
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
	_, _ = fmt.Fprintln(os.Stdout, "Starting InstaEditLogin dev wrapper (api + workers + migrate)")

	app, err := bootstrap.Wire(context.Background())
	if err != nil {
		slog.Error("server: wire failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := app.DB.Close(); err != nil {
			slog.Warn("server: db close failed", "error", err)
		}
	}()

	// Migrate: dev wrapper assumes exclusive DB access. Production
	// deployments run cmd/migrate as a one-shot pre-deploy job.
	if err := database.Migrate(app.DB); err != nil {
		slog.Error("server: database migrate failed", "error", err)
		os.Exit(1)
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

	var wg sync.WaitGroup

	// 9 background workers: only if RUN_WORKERS=true.
	var workersCancel context.CancelFunc = func() {} // no-op default
	if runWorkers {
		ctxWorkers, cancel := context.WithCancel(context.Background())
		workersCancel = cancel
		wg.Add(1)
		go func() {
			defer wg.Done()
			slog.Info("server: launching 9 background workers (RUN_WORKERS=true)")
			if err := app.RunWorkers(ctxWorkers); err != nil && err != context.Canceled {
				slog.Error("server: RunWorkers exited with error", "error", err)
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

	metricsShutdown := bootstrap.StartMetricsServer(app.Cfg, app.Logger)

	// Single-channel signal handling drives BOTH drain paths
	// concurrently. The cancel/Wait pair matches the pre-Blocco #2.1
	// shape: stop-signal → parallel cancel + srv.Shutdown → wg.Wait
	// only completes when ALL spawned goroutines return.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("server: received signal, initiating parallel shutdown", "signal", sig.String())

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
	slog.Info("server: graceful shutdown complete")
}
