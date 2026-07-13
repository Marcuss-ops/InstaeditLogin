// internal worker health listener — Blocco #4.1.
//
// Fly.io's [[services.tcp_checks]] (declared on fly.toml's worker
// [[services]] block) needs a TCP port inside the worker VM that
// accepts connections. The worker has no HTTP server today, so we
// bind a tiny accept-and-close listener on WORKER_HEALTH_PORT.
//
// Dev ergonomics: WORKER_HEALTH_PORT defaults to "0" (off) so
// `make run-worker` on a local laptop does NOT accidentally bind
// 9090. Fly [processes.worker.env] sets WORKER_HEALTH_PORT="9090"
// when deploying, opt-ing the worker into the tcp_checks target.
//
// Lives in cmd/worker/ (same package as main.go) per the repo
// convention that main.go stays a thin entrypoint and helpers go in
// sibling *_test.go / *_thing.go files alongside.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
)

// startWorkerHealthListener binds a tiny TCP-only accept-and-close
// loop on WORKER_HEALTH_PORT (env). It exists purely as a Fly.io
// tcp_checks target — see fly.toml::[[services.tcp_checks]] on the
// worker [[services]]. WORKER_HEALTH_PORT=0 (or "off"/"disabled")
// disables the listener entirely (the dev `make run-worker` path
// inherits this default).
//
// On ctx-cancel the deferred ln.Close() unblocks the Accept loop and
// the goroutine returns. The listener NEVER logs during normal
// operation so it doesn't add noise to slog; warn-level events fire
// only on unexpected listen/accept errors.
func startWorkerHealthListener(ctx context.Context, logger *slog.Logger) {
	raw := os.Getenv("WORKER_HEALTH_PORT")
	if raw == "" {
		raw = "0" // dev default: OFF
	}
	// Explicit disable shorthands used by docs / CI helpers.
	if raw == "0" || raw == "off" || raw == "disabled" {
		logger.Info("worker: health listener disabled (WORKER_HEALTH_PORT=0)")
		return
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		logger.Warn("worker: WORKER_HEALTH_PORT invalid, defaulting to 0 (off)",
			"value", raw)
		return
	}
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Warn("worker: health listener bind failed, continuing without tcp_checks target",
			"addr", addr, "error", err)
		return
	}
	// ctx-cancel closes the listener and unblocks Accept below.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		logger.Info("worker: health listener started", "addr", addr)
		for {
			c, err := ln.Accept()
			if err != nil {
				// Listener closed (ctx-cancel) or transient error.
				// ctx-cancel is the expected exit; only WARN on the
				// unexpected path so slog isn't drowned.
				if ctx.Err() == nil {
					logger.Warn("worker: health listener accept failed", "error", err)
				}
				return
			}
			_ = c.Close() // tcp_checks just needs successful connect; no protocol exchange.
		}
	}()
}
