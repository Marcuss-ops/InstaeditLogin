// cmd/worker — InstaEditLogin background workers (Blocco #2.1)
//
// Runs ONLY the 5 background goroutines (publish worker + reconcile
// worker + outbox dispatcher + webhook worker + metrics collector).
// The HTTP server is NOT started here — it lives in the cmd/api
// binary, which runs separately in production deployments.
//
// All 5 goroutines share App.DB / App.Vault / App.CapRouter /
// App.WebhookRepo from internal/bootstrap.Wire. The shutdown sequence
// mirrors the pre-split cmd/server/main.go shape: 5 concurrent cancels,
// 15s drain budget per goroutine, parallel execution.
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

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
)

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

	// Signal handler drives ctx cancel. MUST be installed before
	// RunWorkers blocks on <-ctx.Done(), otherwise SIGARM is lost.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-quit
		slog.Info("worker: received signal, cancelling runtime", "signal", sig.String())
		cancel()
	}()

	// RunWorkers blocks until ctx is cancelled, then drains the 5
	// goroutines with a 15s budget per leaf. Returns a wrapped error
	// if any goroutine exited non-cleanly before the cancel (rare).
	//
	// The signal goroutine above drives ctx cancellation; this call
	// blocks on <-ctx.Done() inside app.RunWorkers.
	if err := app.RunWorkers(ctx); err != nil {
		slog.Error("worker: RunWorkers exited with error", "error", err)
	}

	slog.Info("worker: stopped")
}
