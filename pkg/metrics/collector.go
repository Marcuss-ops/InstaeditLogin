// Package metrics — collector.go (SPRINT 6.1 — Observability with SLO).
//
// Periodic metric collector goroutine. Refreshes the 5 production
// gauges from Phase 1's metric definitions:
//
//	publish_queue_depth            (Gauge, no labels)
//	publish_queue_lag_seconds      (Gauge, no labels)
//	publish_targets_by_status      (GaugeVec{status})
//	dead_letter_count              (GaugeVec{source})
//	database_pool_usage            (GaugeVec{state})
//
// Lifecycle: RunPeriodicCollector is a blocking loop driven by a
// time.Ticker; ctx-cancellable; integrates with cmd/server/main.go's
// parallel-drain shutdown pattern alongside the publish worker,
// reconcile worker, outbox dispatcher, and webhook worker.
//
// Multi-replica safety: the DB-backed gauges (queue_depth /
// queue_lag_seconds / targets_by_status / dead_letter_count) are
// single-flighted across replicas by acquiring a PostgreSQL
// advisory xact lock (pg_try_advisory_xact_lock(CollectorLockID))
// inside the collect tx. If the lock is held by another replica,
// the function returns immediately without re-running the queries —
// only 1 replica hammers the DB per tick. The pool gauges
// (database_pool_usage) are local-only (sql.DB.Stats() call) and
// always emit, no lock needed.
//
// Why pg_try_advisory_xact_lock and not pg_advisory_lock or
// Redlock:
//   - xact_lock auto-releases on COMMIT/ROLLBACK —no manual
//     unlock + no leak path if the collector crashes mid-tick
//     (the tx is rolled back by Go's defer pattern, releasing
//     the session-scoped lock).
//   - pg_try_advisory_xact_lock is NONBLOCKING (returns
//     bool immediately); the alternative pg_advisory_xact_lock
//     would block the replica until the holder commits, turning
//     "100 replicas hammering" into "100 replicas waiting on a
//     single bottleneck". The boolean is the difference between
//     "distributed coordination" and "serialised single point of
//     contention".
//   - Redlock adds a Redis dependency; the user spec mandates
//     single-flight be DB-backed to survive a Redis outage.
//     Composite locks (DB only) are the simpler alternative.
//
// Why pre-setting all status/source gauges to 0 every tick:
//
//	Prometheus GaugeVec.WithLabelValues(s).Set(...) only creates a
//	series if Set is called. A status that hasn't been emitted yet
//	(e.g. a fresh DB with no dlq rows) would have NO metric line
//	in the scrape, and dashboards would silently break for
//	"no data". Pre-setting 0 every tick guarantees the series
//	always emits. Trade-off: 5x more Set calls per tick
//	(~negligible cost) for dashboards that NEVER lose their metric.
package metrics

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// DefaultCollectorInterval is the default tick interval for the
// periodic collector goroutine. Phase 7 will revisit with a
// config-driven value; 10s is the initial cadence and the value
// the Grafana dashboard's `$__rate_interval` resolution aligns to.
const DefaultCollectorInterval = 10 * time.Second

// CollectorLockID is the Postgres advisory-lock key used to
// single-flight the periodic collector across replicas. Fixed
// numeric so every replica uses the same lock. Picked as an
// arbitrary non-zero int64 — search the codebase to verify it
// does not collide with any other advisory lock in the project
// (today: only this collector uses pg_try_advisory_xact_lock; the
// CredentialVault in Taglio 2.2 uses a UUID-derived int64 sourced
// from a per-platform-acct key, which is structurally distinct
// because it's runtime-derived from string hashes, not a hard-coded
// literal).
const CollectorLockID int64 = 7283948576

// knownTargetStatuses is the canonical post_targets.status value-set
// that targets_by_status{status} tracks. Pre-set to 0 every tick +
// overwrite observed counts (see package docstring for rationale).
// The 6 values cover the post-SPRINT 5.0-5.2 lifecycle:
//
//	queued           — created, scheduled for future publish
//	waiting_provider — async state machine mid-flight (async platforms
//	                   that respond with a PENDING state)
//	publishing       — actively being claimed + executed
//	published        — terminal, success
//	failed           — terminal, failure (terminal-class error)
//	dlq              — terminal, dead-letter (max-attempts exhausted)
//
// "waiting_provider" is included because the async publishing
// path sets it (migration 012 + Taglio 4.2 — TikTok's PROCESSING_UPLOAD
// → PENDING_PUBLISH flow lives here).
var knownTargetStatuses = []string{
	"queued",
	"waiting_provider",
	"publishing",
	"published",
	"failed",
	"dlq",
}

// knownDeadLetterSources is the canonical source label set for
// dead_letter_count{source}. Pre-set to 0 every tick + overwrite.
var knownDeadLetterSources = []string{
	DeadLetterSourcePublish,
	DeadLetterSourceOutbox,
	DeadLetterSourceWebhook,
}

// RunPeriodicCollector runs the metrics-collector tick loop until
// ctx is cancelled. The first tick runs BEFORE the first ticker
// fire so a freshly-started process has metrics within
// interval+epsilon instead of interval*2.
//
// interval <= 0 falls back to DefaultCollectorInterval (10s).
// nil logger inherits slog.Default().
//
// Returns ctx.Err() on shutdown — same shape as the other
// background goroutines (publish_worker, reconcile_worker,
// outbox dispatcher, webhook worker) so main.go's parallel
// shutdown drain can reuse the existing 15s-per-leaf timeout
// pattern.
func RunPeriodicCollector(ctx context.Context, db *sql.DB, interval time.Duration, logger *slog.Logger) error {
	if interval <= 0 {
		interval = DefaultCollectorInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("metrics collector started", "interval_seconds", interval.Seconds())
	defer logger.Info("metrics collector stopped")

	if err := CollectOnce(ctx, db, logger); err != nil {
		logger.Warn("metrics collector initial tick error", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := CollectOnce(ctx, db, logger); err != nil {
				logger.Warn("metrics collector tick error", "error", err)
			}
		}
	}
}

// CollectOnce runs ONE round of metric collection. Returned errors
// are logged at WARN by the caller but never abort the loop — a
// transient DB blip drops ONE tick's worth of metric updates, the
// next tick rebuilds.
//
// Steps (in order):
//  1. pool gauges (database_pool_usage{state}) — local, no lock
//  2. begin tx + pg_try_advisory_xact_lock for single-flight
//  3. if lock NOT acquired → return nil (skipped this tick)
//  4. queue_depth + queue_lag_seconds (single combined query)
//  5. targets_by_status (pre-set 6 statuses to 0, overwrite observed)
//  6. dead_letter_count (pre-set 3 sources to 0, overwrite observed;
//     webhook source is best-effort — table may not exist pre-migration 030)
//  7. commit tx (lock auto-released)
//
// nil *sql.DB: returns an error WITHOUT logging internally — the
// caller (RunPeriodicCollector) logs the WARN. Avoids double-logging
// across all error paths; the WARN line is the single canonical
// "this tick failed" emit.
func CollectOnce(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	if db == nil {
		return fmt.Errorf("metrics collector: nil *sql.DB")
	}
	if logger == nil {
		logger = slog.Default()
	}

	collectPoolGauges(db)
	return collectDBGaugesXact(ctx, db, logger)
}

// collectPoolGauges reads *sql.DB.Stats() and writes the 4
// database_pool_usage{state} gauges. Safe on a non-nil *sql.DB
// (Stats returns a zero-valued struct, never panics, per the
// database/sql stdlib contract — verified in the stdlib source).
func collectPoolGauges(db *sql.DB) {
	if db == nil {
		return
	}
	stats := db.Stats()
	// stats.InUse, .Idle, .OpenConnections are instantaneous. .WaitCount
	// is cumulative (the number of times a caller has waited for a
	// connection since process start) — exposed as a gauge-style
	// "the process has waited N times in its lifetime", useful as
	// "is the pool undersized?" diagnostic. Cumulative-gauge
	// semantics are an established Prometheus pattern (see
	// process_cpu_seconds_total which is also monotonic).
	SetDatabasePoolUsage(PoolStateInUse, stats.InUse)
	SetDatabasePoolUsage(PoolStateIdle, stats.Idle)
	SetDatabasePoolUsage(PoolStateOpen, stats.OpenConnections)
	SetDatabasePoolUsage(PoolStateWait, int(stats.WaitCount))
}

// collectDBGaugesXact runs the 4 DB-backed gauges inside a single
// transaction single-flighted by pg_try_advisory_xact_lock.
// Webhook DLQ count is best-effort — the webhook_deliveries table
// may not exist on pre-migration-030 databases; a missing-table
// error is logged at DEBUG and the rest of the tick proceeds.
func collectDBGaugesXact(ctx context.Context, db *sql.DB, logger *slog.Logger) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var gotLock bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, CollectorLockID).Scan(&gotLock); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}
	if !gotLock {
		// Another replica is collecting this tick — skip the 4 DB
		// queries so we don't multiply load by N replicas.
		return nil
	}

	// 1. publish_queue_depth — every row not yet in a terminal state.
	var queueDepth int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM post_targets
		 WHERE status IN ('queued','waiting_provider','publishing')`,
	).Scan(&queueDepth); err != nil {
		return fmt.Errorf("queue_depth query: %w", err)
	}
	SetQueueDepth(queueDepth)

	// 2. publish_queue_lag_seconds — age of the oldest queued row, 0
	// when empty. The MIN+NULLS handling relies on COALESCE so the
	// query returns a real 0 on empty rather than NULL (which would
	// force the Gauge.Set call to skip via the helper's silent-zero path).
	var queueLagSeconds float64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(p.publish_at))), 0)
		 FROM post_targets pt
		 JOIN posts p ON p.id = pt.post_id
		 WHERE pt.status IN ('queued','waiting_provider')`,
	).Scan(&queueLagSeconds); err != nil {
		return fmt.Errorf("queue_lag_seconds query: %w", err)
	}
	SetQueueLagSeconds(queueLagSeconds)

	// 3. publish_targets_by_status{status} — pre-set all 6 known
	// statuses to 0 so the series is always present in /metrics,
	// then overwrite with the observed counts.
	for _, status := range knownTargetStatuses {
		SetTargetsByStatus(status, 0)
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM post_targets GROUP BY status`,
	)
	if err != nil {
		return fmt.Errorf("targets_by_status query: %w", err)
	}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			rows.Close()
			return fmt.Errorf("targets_by_status scan: %w", err)
		}
		SetTargetsByStatus(status, count)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("targets_by_status rows: %w", err)
	}
	rows.Close()

	// 4. dead_letter_count{source} — 3 sources. Pre-set all to 0.
	for _, source := range knownDeadLetterSources {
		SetDeadLetterCount(source, 0)
	}
	var nPublish int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM post_targets WHERE status='dlq'`,
	).Scan(&nPublish); err != nil {
		return fmt.Errorf("dlq publish query: %w", err)
	}
	SetDeadLetterCount(DeadLetterSourcePublish, nPublish)
	var nOutbox int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM outbox_events WHERE status='dead_letter'`,
	).Scan(&nOutbox); err != nil {
		return fmt.Errorf("dlq outbox query: %w", err)
	}
	SetDeadLetterCount(DeadLetterSourceOutbox, nOutbox)
	// Webhook source is best-effort — webhook_deliveries is part of
	// migration 030 (SPRINT 4.2). On pre-030 DBs the table doesn't
	// exist and the query returns "relation does not exist" — we
	// surface at DEBUG (operator visibility) and skip the metric
	// for this tick (subsequent ticks retry; once migration 030
	// lands, the table exists and the metric starts emitting).
	// COUNT(*) returns BIGINT NOT NULL, never NULL — no NullInt64
	// wrapping needed (per code-reviewer's dead-complexity catch
	// on the previous revision).
	var nWebhook int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_deliveries WHERE status='dead'`,
	).Scan(&nWebhook); err != nil {
		logger.Debug("dlq webhook skipped (table may not exist on pre-migration-030 database)",
			"error", err)
	} else {
		SetDeadLetterCount(DeadLetterSourceWebhook, nWebhook)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}
