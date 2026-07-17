// Package worker — sessions_cleanup.go (commit: retention policy).
//
// Background goroutine that periodically DELETEs stale rows from
// the `sessions` table. The retention policy is implemented in
// internal/services/sessions_service.go::Cleanup; this worker just
// owns the cadence + log surface.
//
// Mirrors the canonical pattern from publish_worker.go /
// webhook_worker.go: one struct, one Run(ctx) loop, ctx-cancellable.
// Multi-replica safety is delegated to the underlying Postgres
// DELETE statement (it's naturally idempotent — running twice in
// parallel is harmless; each invocation sees a consistent
// snapshot).
package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// DefaultSessionsCleanupInterval is the fallback tick interval.
// Production cadence comes from cfg.SessionCleanupIntervalSeconds
// (env: SESSION_CLEANUP_INTERVAL_SECONDS, default 300s = 5 min).
const DefaultSessionsCleanupInterval = 5 * time.Minute

// SessionCleaner is the narrow interface SessionsCleanupWorker
// depends on. Defined inline (not in repository) so the worker can
// be unit-tested with a fake without touching the database.
//
// The interface intentionally matches services.CleanupResult — same
// shape, same ctx.Context — so the production wiring is a direct
// pass-through and tests can swap in a deterministic fake.
type SessionCleaner interface {
	Cleanup(ctx context.Context) (services.CleanupResult, error)
}

// SessionsCleanupWorker periodically hard-deletes stale sessions.
// One struct, one Run loop, ctx-cancellable. Tick interval comes
// from cfg.SessionCleanupIntervalSeconds; the constructor enforces
// a sane default if the configured value is <= 0 (we do NOT want
// a tight loop from a typo).
type SessionsCleanupWorker struct {
	svc      SessionCleaner
	interval time.Duration
	logger   *slog.Logger
}

// NewSessionsCleanupWorker wires the dependencies. interval <= 0
// falls back to DefaultSessionsCleanupInterval (5 min). nil logger
// inherits slog.Default(). svc must be non-nil — a nil will panic
// on the first tick (fail-fast for misconfigured wiring).
func NewSessionsCleanupWorker(svc SessionCleaner, interval time.Duration, logger *slog.Logger) *SessionsCleanupWorker {
	if interval <= 0 {
		interval = DefaultSessionsCleanupInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &SessionsCleanupWorker{svc: svc, interval: interval, logger: logger}
}

// Run blocks until ctx is cancelled. Initial tick runs BEFORE the
// first ticker fire so a fresh-start process doesn't have to wait
// the full interval for the first cleanup pass (useful for
// catching up on stale rows accumulated during downtime).
//
// Errors are logged at WARN but do NOT stop the loop — a transient
// DB blip should not kill the cadence. Returns ctx.Err() on
// shutdown.
func (w *SessionsCleanupWorker) Run(ctx context.Context) error {
	w.logger.Info("sessions cleanup worker started",
		"interval_seconds", w.interval.Seconds())
	defer w.logger.Info("sessions cleanup worker stopped")

	// Initial sweep — recover from downtime backlog.
	w.tick(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.tick(ctx)
		}
	}
}

// tick executes one Cleanup pass and logs the result. Errors are
// swallowed at the worker level (logged as WARN); the caller (Run
// loop) is not interrupted.
func (w *SessionsCleanupWorker) tick(ctx context.Context) {
	result, err := w.svc.Cleanup(ctx)
	if err != nil {
		w.logger.Warn("sessions cleanup tick failed (will retry at next interval)",
			"error", err)
		return
	}
	if result.Deleted > 0 {
		w.logger.Info("sessions cleanup deleted stale rows",
			"deleted", result.Deleted,
			"grace_revoked_days", result.GraceRevokedDays,
			"grace_expired_days", result.GraceExpiredDays)
	}
}
