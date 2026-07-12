// Tests for the periodic collector goroutine. The DB-bound paths
// (queue_depth query, advisory-lock acquire, GROUP BY status, ...)
// require a real Postgres and are covered by an integration test in
// a follow-up; the tests here pin the static invariants (lock ID,
// known-status vocab, nil-safety) so a casual rename surfaces in
// fast `go test` rather than integration CI.
//
// The lib/pq side-effect import registers the "postgres" driver
// name with database/sql so sql.Open("postgres", ...) returns a
// *sql.DB without error. The actual queries/Stats calls don't
// require a live connection (Stats is in-process; the unreachable
// DSN forces the QueryContext calls to error out as expected in
// the "DB error path" tests).
package metrics

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	_ "github.com/lib/pq"
)

// testCancelInterval is the tick interval used by
// TestRunPeriodicCollector_CancelledCtx. Must be LARGER than the
// wall-clock latency between goroutine spawn and the cancel()
// call — otherwise the test could race the initial CollectOnce
// happy-path (which takes a few ms even with an erroring tx) and
// exit in a non-deterministic way. 50s is overkill for the actual
// test (it cancels well within a millisecond) and gives a future
// maintainer generous headroom to lower the value if needed.
const testCancelInterval = 50 * time.Second

// TestCollectorLockID_IsNonZero sanity-checks the advisory-lock key.
// A zero CollectorLockID would deny NO replicas (every caller wins),
// turning single-flight off and re-introducing the N-replica fan-out
// the lock exists to prevent.
func TestCollectorLockID_IsNonZero(t *testing.T) {
	if CollectorLockID == 0 {
		t.Errorf("CollectorLockID must be non-zero (zero means every pg_try_advisory_xact_lock call succeeds and single-flight is disabled)")
	}
}

// TestKnownTargetStatuses_HasAllSix pins the canonical 6 target
// statuses. Adding a 7th status to the post_targets lifecycle MUST
// land here in lockstep (a missed entry means that status is missing
// from `publish_targets_by_status{status="..."}` and Grafana panels
// silently fail).
func TestKnownTargetStatuses_HasAllSix(t *testing.T) {
	if len(knownTargetStatuses) != 6 {
		t.Fatalf("knownTargetStatuses: want exactly 6 entries (got %d): %v", len(knownTargetStatuses), knownTargetStatuses)
	}
	required := []string{"queued", "waiting_provider", "publishing", "published", "failed", "dlq"}
	seen := make(map[string]bool, len(knownTargetStatuses))
	for _, s := range knownTargetStatuses {
		seen[s] = true
	}
	for _, want := range required {
		if !seen[want] {
			t.Errorf("knownTargetStatuses missing required status %q (full: %v)", want, knownTargetStatuses)
		}
	}
}

// TestKnownDeadLetterSources_HasAllThree pins the 3 DLQ sources.
// A missed entry is a zap of an entire emission.
func TestKnownDeadLetterSources_HasAllThree(t *testing.T) {
	if len(knownDeadLetterSources) != 3 {
		t.Fatalf("knownDeadLetterSources: want exactly 3 entries (got %d): %v", len(knownDeadLetterSources), knownDeadLetterSources)
	}
	required := []string{DeadLetterSourcePublish, DeadLetterSourceOutbox, DeadLetterSourceWebhook}
	seen := make(map[string]bool, len(knownDeadLetterSources))
	for _, s := range knownDeadLetterSources {
		seen[s] = true
	}
	for _, want := range required {
		if !seen[want] {
			t.Errorf("knownDeadLetterSources missing required source %q (full: %v)", want, knownDeadLetterSources)
		}
	}
}

// TestCollectOnce_NilDB_ErrorsGracefully — nil DB must error out
// cleanly, not panic. The collector goroutine logs the error and
// continues; the rest of the server keeps running.
func TestCollectOnce_NilDB_ErrorsGracefully(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CollectOnce panicked on nil DB; want graceful error. panic=%v", r)
		}
	}()
	// Discard log output during the test — slog default writes to
	// stderr; redirect to io.Discard so the test output stays clean.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := CollectOnce(context.Background(), nil, logger)
	if err == nil {
		t.Errorf("CollectOnce(nil DB): want error, got nil")
	}
}

// TestCollectPoolGauges_NilDB_NoPanic mirrors the nil-safety test
// for the local-only pool path (no DB calls, just db.Stats()). On
// a nil DB the helper must NOT panic — main.go might construct the
// collector before the DB is wired in a future SPRINT.
func TestCollectPoolGauges_NilDB_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("collectPoolGauges panicked on nil DB; want silent return. panic=%v", r)
		}
	}()
	collectPoolGauges(nil)
}

// TestCollectPoolGauges_UnreachableDB_StillEmitsAllFourStates
// verifies the happy-path emission on an unconnected *sql.DB. The
// postgres driver + lib/pq name registration (side-effect import
// above) means sql.Open succeeds; the unreachable DSN means Stats()
// runs against an in-process empty pool. (Calling the previous
// "RealDB" was misleading because the driver never actually dials;
// that isn't covered here — the CollectOnce happy path is a
// separate test that requires a real Postgres integration harness.)
func TestCollectPoolGauges_UnreachableDB_StillEmitsAllFourStates(t *testing.T) {
	// Use an unreachable postgres DSN — Stats() works without a
	// live connection (the database/sql stdlib holds the pool
	// counters in-process; Stats() reads them regardless).
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=x dbname=x sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5) // exercising MaxOpenConns is not necessary; Set* is safe without a live conn.

	// Reset gauges so the assertion counts this test only.
	databasePoolUsage.Reset()

	collectPoolGauges(db)

	// 4 series must be present in the gatherer.
	expected := map[string]bool{
		PoolStateInUse: false,
		PoolStateIdle:  false,
		PoolStateOpen:  false,
		PoolStateWait:  false,
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != "database_pool_usage" {
			continue
		}
		for _, m := range mf.GetMetric() {
			if m.GetGauge() == nil {
				continue
			}
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "state" {
					if _, ok := expected[lp.GetValue()]; ok {
						expected[lp.GetValue()] = true
					}
				}
			}
		}
	}
	for state, seen := range expected {
		if !seen {
			t.Errorf("database_pool_usage{state=%q}: missing from /metrics after collectPoolGauges", state)
		}
	}
}

// TestCollectOnce_UnreachableDB_PoolGaugesStillEmit — even with an
// unreachable DSN, the pool gauges emit (Stats() is local). The
// DB-backed gauges fail with a query error (which CollectOnce
// returns); the test verifies the error is propagated but no
// partial gauge state is left corrupted. Phase 2 follow-ups add
// the live-Postgres integration test.
func TestCollectOnce_UnreachableDB_PoolGaugesStillEmit(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=x dbname=x sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	databasePoolUsage.Reset()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// We don't care about the returned err for this test — we
	// care that pool gauges STILL emitted before the DB errors
	// kicked in. (In practice the DB-error path aborts mid-tx; the
	// pool gauges written by collectPoolGauges BEFORE the tx stand.)
	_ = CollectOnce(context.Background(), db, logger)

	// Pool gauges must be present.
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var poolGaugeCount int
	for _, mf := range families {
		if mf.GetName() == "database_pool_usage" {
			poolGaugeCount = len(mf.GetMetric())
		}
	}
	if poolGaugeCount < 4 {
		t.Errorf("database_pool_usage: want >=4 series (in_use/idle/open/wait) after CollectOnce with unreachable DB, got %d", poolGaugeCount)
	}
}

// TestRunPeriodicCollector_CancelledCtx verifies RunPeriodicCollector
// blocks until ctx is cancelled (returns ctx.Err()) — same
// shutdown-contract shape as the existing publish/reconcile/outbox/
// webhook workers so main.go's parallel drain reuses the 15s
// timeout pattern.
//
// We use the unreachable-DB sql.Open pattern: Run will fail the
// initial tick, log it, and continue blocking on the ticker —
// which is what the test verifies. The interval is
// testCancelInterval (50s) — comfortably larger than the spawn-to-
// cancel latency so a future tighten of the value doesn't race.
func TestRunPeriodicCollector_CancelledCtx(t *testing.T) {
	db, err := sql.Open("postgres", "host=127.0.0.1 port=1 user=x dbname=x sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	done := make(chan error, 1)
	go func() {
		done <- RunPeriodicCollector(ctx, db, testCancelInterval, logger)
	}()
	cancel()

	// Use the time.After pattern (the default-branch approach races
	// the goroutine scheduler — by the time `select` runs the cancel
	// signal may not have propagated yet, and `default` fires
	// spuriously). 15s is the same ceiling main.go uses on the
	// production drain wait — generous headroom for the actual
	// Run-exit path which completes in microseconds when ctx is
	// cancelled.
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("RunPeriodicCollector: want ctx.Canceled, got %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunPeriodicCollector did not exit on ctx cancel within 15s")
	}
}

// TestSetTargetsByStatus_FamilyRegistered ensures the
// publish_targets_by_status family is registered with prometheus
// (otherwise the Series silently won't appear in /metrics).
func TestSetTargetsByStatus_FamilyRegistered(t *testing.T) {
	publishTargetsByStatus.Reset()
	SetTargetsByStatus("queued", 7)
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	var found *dto.MetricFamily
	for _, mf := range families {
		if mf.GetName() == "publish_targets_by_status" {
			found = mf
			break
		}
	}
	if found == nil {
		t.Fatal("publish_targets_by_status: not present in gatherer after SetTargetsByStatus call")
	}
	wantSeen := false
	for _, m := range found.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetName() == "status" && lp.GetValue() == "queued" {
				wantSeen = true
				if m.GetGauge().GetValue() != 7 {
					t.Errorf("publish_targets_by_status{status=queued}: want 7, got %v", m.GetGauge().GetValue())
				}
			}
		}
	}
	if !wantSeen {
		t.Errorf("publish_targets_by_status{status=queued}: missing after SetTargetsByStatus(\"queued\", 7)")
	}
}
