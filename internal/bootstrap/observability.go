package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/getsentry/sentry-go"
	"github.com/prometheus/client_golang/prometheus"
	"log/slog"
	"net/http"
	"time"
)

// configureSentry initialises the Sentry SDK once and returns the
// current hub. An empty DSN is treated as "Sentry disabled" and
// returns a nil hub with no error. Any SDK init failure is surfaced
// as an error so the caller (Wire) can decide whether to fail closed
// or continue without Sentry. This helper is extracted to make the
// bootstrap wiring testable with a fake transport.
func configureSentry(opts sentry.ClientOptions) (*sentry.Hub, error) {
	if opts.Dsn == "" {
		return nil, nil
	}
	if err := sentry.Init(opts); err != nil {
		return nil, err
	}
	return sentry.CurrentHub(), nil
}

// RegisterWorkerMetrics registers the worker registry as a Prometheus
// collector. It is safe to call multiple times; subsequent calls are
// no-ops. Callers that expose /metrics (cmd/worker) should invoke this
// before bootstrap.StartMetricsServer so the worker_state metric is
// available from the first scrape.
func (a *App) RegisterWorkerMetrics() error {
	if a.WorkerRegistry == nil {
		return nil
	}
	if err := prometheus.Register(a.WorkerRegistry); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			return err
		}
	}
	return nil
}

// StartMetricsServer starts an optional internal HTTP server for the
// /metrics endpoint when cfg.Monitoring.MetricsPort > 0. It binds to
// cfg.Monitoring.MetricsHost (default 127.0.0.1) and serves the same
// basic-auth-gated handler used by /api/v1/metrics. Returns a shutdown
// function that callers MUST invoke during graceful shutdown. When
// MetricsPort is 0 the returned shutdown is a no-op.
func StartMetricsServer(cfg *config.Config, logger *slog.Logger) (shutdown func(context.Context) error) {
	if cfg.Monitoring.MetricsPort == 0 {
		return func(context.Context) error { return nil }
	}

	host := cfg.Monitoring.MetricsHost
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, cfg.Monitoring.MetricsPort)

	srv := &http.Server{
		Addr:         addr,
		Handler:      api.MetricsHandler(cfg.Monitoring.MetricsBasicAuthUser, cfg.Monitoring.MetricsBasicAuthPass),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	if logger == nil {
		logger = slog.Default()
	}

	go func() {
		logger.Info("metrics server listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	return srv.Shutdown
}
