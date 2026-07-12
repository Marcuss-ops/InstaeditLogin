// Package postgres provides shared testcontainers-based helpers for
// integration tests across the InstaeditLogin codebase. The helpers
// here are the canonical source-of-truth for "spin up an ephemeral
// Postgres 16-alpine for a single test" — both
// internal/database/*_integration_test.go and
// internal/worker/publish_reconcile_integration_test.go import them,
// so a Postgres version bump, credential change, or testcontainers
// API move happens in ONE place rather than drifting between files.
//
// The package compiles unconditionally (no //go:build integration
// tag): the standard library plus testcontainers-go and lib/pq are
// always present in go.mod. The integration-tagged TEST FILES
// trigger actual Docker usage; run with: go test -tags=integration ./...
//
// design notes:
//
//   - StartTestPostgres internally calls RequireDocker as its first
//     step. Test files therefore have a single canonical call site
//     (db, cleanup := postgres.StartTestPostgres(t)) and don't need a
//     separate postgres.RequireDocker(t) guard.
//
//   - The database name is configurable via the WithDatabase option
//     (functional-option pattern). The default is "instaedit_test",
//     matching the prior helper in
//     internal/database/migrations_integration_test.go. The worker
//     test passes WithDatabase("instaedit_test_worker") to keep its
//     pre-refactor name (clear logical separation in shared test
//     logs even though each testcontainer is ephemeral).
//
//   - RequireDocker is exported so future tests that spin up a
//     non-Postgres testcontainer (Redis, Kafka, etc.) can use the
//     same Docker-availability guard without having to call
//     StartTestPostgres first.
package postgres

import (
	"context"
	"database/sql"
	"os/exec"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// config is the internal struct that the functional options mutate.
// Unexported because callers compose it via StartTestPostgres's
// variadic opts — direct access to the struct is not part of the
// public API.
type config struct {
	databaseName string
}

// defaultConfig returns the canonical StartTestPostgres defaults:
// database name "instaedit_test" (matches the prior helper in
// internal/database/migrations_integration_test.go). Override via
// WithDatabase.
func defaultConfig() config {
	return config{
		databaseName: "instaedit_test",
	}
}

// Option is a functional option that mutates the internal config used
// by StartTestPostgres. The most common option is WithDatabase —
// override the database name when a test wants logical isolation
// from other tests that share Postgres naming conventions in their
// logs.
type Option func(*config)

// WithDatabase overrides the database name created in the ephemeral
// Postgres container. An empty name is ignored (the default applies
// instead). The last WithDatabase call in a StartTestPostgres
// invocation wins.
func WithDatabase(name string) Option {
	return func(c *config) {
		if name != "" {
			c.databaseName = name
		}
	}
}

// RequireDocker short-circuits the calling test if Docker isn't
// available so dev environments without Docker don't see false
// failures. Two-step check:
//
//  1. exec.LookPath("docker") confirms the binary is on PATH.
//  2. docker info confirms the daemon is reachable (a missing or
//     stopped daemon fails this step, not the binary lookup).
//
// Either failing calls t.Skipf — the conventional SKIPPED-not-FAILED
// signal that the environment intentionally isn't running the test.
//
// RequireDocker is also called as the first step of StartTestPostgres,
// so test files don't need to invoke it separately.
func RequireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// StartTestPostgres spins up an ephemeral Postgres 16-alpine via
// testcontainers-go and returns the *sql.DB + a cleanup function
// that terminates the container. The default database name is
// "instaedit_test"; override with WithDatabase(name).
//
// Configuration:
//
//   - Image: postgres:17-alpine
//   - Credentials: test / test
//   - SSL: disabled (testcontainer-internal connection only)
//
// The first step is an internal RequireDocker call: tests that don't
// need a separate requireDocker(t) guard can call this as their only
// Docker-touching helper.
//
// The 15s/200ms ping-backoff loop absorbs testcontainers' built-in
// readiness-check race: the log-based "database system is ready to
// accept connections" message can fire BEFORE the TCP listener is
// actually bound on some Docker configs — the first Ping() then hits
// "connection reset by peer", and subsequent pings (typically within
// 1–3 attempts) succeed once the listener is up.
//
// Each call produces a FRESH ephemeral container — there is no
// cross-test state sharing. The container is killed by either the
// returned cleanup func (defer-friendly) OR by wiring it into
// t.Cleanup at rig construction sites.
func StartTestPostgres(t *testing.T, opts ...Option) (*sql.DB, func()) {
	t.Helper()

	// Skip the test if Docker isn't available — same shape as the
	// prior inlined requireDocker guard at every test's first line.
	RequireDocker(t)

	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	ctx := context.Background()

	pgC, err := tpostgres.Run(ctx,
		"postgres:17-alpine",
		tpostgres.WithDatabase(cfg.databaseName),
		tpostgres.WithUsername("test"),
		tpostgres.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}

	// Retry db.Ping with a short backoff. See the function doc for
	// the rationale on the 15s/200ms loop.
	pingDeadline := time.Now().Add(15 * time.Second)
	for attempt := 1; ; attempt++ {
		if pingErr := db.Ping(); pingErr == nil {
			break
		}
		if time.Now().After(pingDeadline) {
			t.Fatalf("db.Ping: timeout after %d attempts over 15s", attempt)
		}
		time.Sleep(200 * time.Millisecond)
	}

	cleanup := func() {
		_ = db.Close()
		_ = pgC.Terminate(ctx)
	}
	return db, cleanup
}
