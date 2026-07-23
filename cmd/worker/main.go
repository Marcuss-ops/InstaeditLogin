// cmd/worker — InstaEditLogin background workers (Blocco #2.1)
//
// Runs ONLY the 9 background goroutines (publish, reconcile, outbox,
// webhook, metrics, sessions_cleanup, velox_downloader, upload and
// drive_batch_crawler). The HTTP server is NOT started here — it lives
// in the cmd/api binary, which runs separately in production deployments.
//
// All 9 goroutines share App.DB / App.Vault / App.CapRouter /
// App.WebhookRepo from internal/bootstrap.Wire. The shutdown sequence
// mirrors the pre-split cmd/server/main.go shape: 9 concurrent cancels,
// single 15s drain budget, parallel execution.
//
// Signal handling: install signal.Notify BEFORE RunWorkers so that
// SIGINT/SIGTERM can drive the ctx-cancel that RunWorkers is blocked
// on. A separate goroutine watches the signal channel and calls
// cancel() — this is the canonical Go pattern (signal-before-block).
//
// Production deploy: cmd/migrate as a one-shot pre-deploy, then one or
// more cmd/worker pods (independent of cmd/api pods).
//
// dev: cmd/server (wrapper) launches BOTH api + worker in one process.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
)

// startWorkerHealthListener lives in cmd/worker/health_listener.go
// (same package main) — kept here in the repo's small-main-file
// convention so cmd/worker/main.go stays a thin entrypoint.

func main() {
	_, _ = fmt.Fprintln(os.Stdout, "Starting InstaEditLogin workers (Blocco #2.1: split from cmd/server)")

	app, err := bootstrap.Wire(context.Background())
	if err != nil {
		slog.Error("worker: wire failed", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := app.DB.Close(); err != nil {
			slog.Warn("worker: db close failed", "error", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Health listener for Fly [[services.tcp_checks]] (see
	// fly.worker.toml + startWorkerHealthListener godoc). Bound BEFORE
	// RunWorkers blocks so the listener is reachable for the whole
	// window the worker is alive.
	startWorkerHealthListener(ctx, app.WorkerRegistry, slog.Default())

	// Register the worker registry as a Prometheus collector so the
	// /metrics endpoint exposes per-worker lifecycle state via
	// worker_state{}. Must happen before StartMetricsServer.
	if err := app.RegisterWorkerMetrics(); err != nil {
		slog.Error("worker: worker registry metric registration failed", "error", err)
		os.Exit(1)
	}

	// Optional internal /metrics endpoint.
	metricsShutdown := bootstrap.StartMetricsServer(app.Cfg, app.Logger)

	// Signal handler drives ctx cancel. MUST be installed before
	// RunWorkers blocks on <-ctx.Done(), otherwise SIGARM is lost.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-quit
		slog.Info("worker: received signal, cancelling runtime", "signal", sig.String())
		cancel()
	}()

	// RunWorkers blocks until ctx is cancelled or a critical worker
	// exits unexpectedly, then drains the goroutines. A non-nil error
	// means a critical worker failed and the process must exit
	// non-zero so the orchestrator can restart it.
	runErr := app.RunWorkers(ctx)

	// Drain the internal metrics listener before exiting so in-flight
	// scrapes complete and the port is released cleanly.
	ctxMetrics, cancelMetrics := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelMetrics()
	if err := metricsShutdown(ctxMetrics); err != nil {
		slog.Error("worker: metrics server forced to shutdown", "error", err)
	}

	if runErr != nil {
		slog.Error("worker: RunWorkers exited with error", "error", runErr)
		os.Exit(1)
	}

	slog.Info("worker: stopped")
}
