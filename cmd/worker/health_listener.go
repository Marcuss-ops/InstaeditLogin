// internal worker health listener — Blocco #4.1.
//
// The worker process exposes its own readiness and health endpoints,
// separate from the API /ready endpoint. The API readiness endpoint
// only checks DB/schema; the worker readiness endpoint reflects the
// state of the background workers supervised by the WorkerRegistry.
//
// Fly.io's [[services.tcp_checks]] (declared on fly.toml's worker
// [[services]] block) needs a TCP port that accepts connections.
// Switching to HTTP keeps tcp_checks passing (the probe still
// completes a TCP handshake) while also allowing richer HTTP probes.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

// startWorkerHealthListener binds a tiny HTTP server on
// WORKER_HEALTH_PORT (env). It exposes /ready and /health. Dev
// default WORKER_HEALTH_PORT=0 disables the listener.
//
// On ctx-cancel the listener is closed and the goroutine returns.
func startWorkerHealthListener(ctx context.Context, registry *worker.Registry, logger *slog.Logger) {
	raw := os.Getenv("WORKER_HEALTH_PORT")
	if raw == "" {
		raw = "0" // dev default: OFF
	}
	if raw == "0" || raw == "off" || raw == "disabled" {
		logger.Info("worker: health listener disabled (WORKER_HEALTH_PORT=0)")
		return
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		logger.Warn("worker: WORKER_HEALTH_PORT invalid, defaulting to 0 (off)", "value", raw)
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		statuses := registry.GetStatus()
		code := http.StatusOK
		for _, s := range statuses {
			if s.State == worker.StateFailed {
				code = http.StatusServiceUnavailable
				break
			}
		}
		// A worker process with no registered workers should report
		// not-ready until it has been properly initialised.
		if len(statuses) == 0 {
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(statuses)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		statuses := registry.GetStatus()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"workers": statuses,
		})
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		logger.Warn("worker: health listener bind failed, continuing without tcp_checks target",
			"addr", srv.Addr, "error", err)
		return
	}

	go func() {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			logger.Warn("worker: health listener close error", "error", err)
		}
	}()

	go func() {
		logger.Info("worker: health listener started", "addr", srv.Addr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Warn("worker: health listener serve error", "error", err)
		}
	}()
}
