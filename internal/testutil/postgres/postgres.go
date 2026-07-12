// Package postgres provides shared testcontainers-based helpers for
// integration tests across the InstaeditLogin codebase. The helpers
// here are the canonical source-of-truth for "spin up an ephemeral
// Postgres 17-alpine for a single test" — both
// internal/database/*_integration_test.go and
// internal/worker/publish_reconcile_integration_test.go import them,
// so a Postgres version bump, credential change, or testcontainers
// API move happens in ONE place rather than drifting between files.
//
// Cross-cutting concerns (Docker availability, the 15-second/200ms
// readiness-poll loop) live in internal/testutil/runtime. This
// package composes those primitives — it adds ONLY Postgres-specific
// wiring (image/credentials/DSN, the WithDatabase option, the cleanup
// closure). Future testutil/<engine> packages (Redis, Kafka, …) follow
// the same composition path.
//
// The package compiles unconditionally (no //go:build integration
// tag): the standard library plus testcontainers-go and lib/pq are
// always present in go.mod. The integration-tagged TEST FILES
// trigger actual Docker usage; run with: go test -tags=integration ./...
//
// design notes:
//
//   - StartTestPostgres internally calls runtime.RequireDocker as
//     its first step. Test files therefore have a single canonical
//     call site (db, cleanup := postgres.StartTestPostgres(t)) and
//     don't need a separate docker-availability guard.
//
//   - The database name is configurable via the WithDatabase option
//     (functional-option pattern). The default is "instaedit_test",
//     matching the prior helper in
//     internal/database/migrations_integration_test.go. The worker
//     test passes WithDatabase("instaedit_test_worker") to keep its
//     pre-refactor name (clear logical separation in shared test
//     logs even though each testcontainer is ephemeral).
//
//   - (history) The local RequireDocker helper previously exported
//     by this package moved to internal/testutil/runtime when the
//     cross-cutting Docker-check + readiness-poll conventions were
//     extracted. No external callers of postgres.RequireDocker
//     existed at extraction time (verified by grep), so the move
//     was direct rather than via a deprecation alias. Future
//     readers who grep for RequireDocker should land on
//     runtime.RequireDocker instead.
package postgres

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/lib/pq"
	tpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/runtime"
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

// StartTestPostgres spins up an ephemeral Postgres 17-alpine via
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
// The first step is an internal runtime.RequireDocker call: tests
// that don't need a separate docker-availability guard can call
// this as their only Docker-touching helper.
//
// The readiness-poll loop is delegated to runtime.WaitReady with
// db.Ping as the probe (and the canonical 15s/200ms defaults) —
// see runtime.WaitReady's doc for the rationale on why testcontainers'
// log-based "ready" message races the TCP listener and why the
// 15s/200ms timing budget is right.
//
// Each call produces a FRESH ephemeral container — there is no
// cross-test state sharing. The container is killed by either the
// returned cleanup func (defer-friendly) OR by wiring it into
// t.Cleanup at rig construction sites.
func StartTestPostgres(t *testing.T, opts ...Option) (*sql.DB, func()) {
	t.Helper()

	// Docker-availability guard is canonical in the runtime package
	// so a future testutil/redis/etc. helper can compose it the
	// same way without duplicating the exec.LookPath + docker info
	// dance.
	runtime.RequireDocker(t)

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

	// Readiness-poll delegated to the runtime helper: same
	// 15s/200ms contract, attempt count surfaced via t.Logf on
	// success, last error surfaced via t.Fatalf on timeout.
	runtime.WaitReady(t, func() error { return db.Ping() },
		runtime.WaitReadyDefaultDeadline, runtime.WaitReadyDefaultBackoff)

	cleanup := func() {
		_ = db.Close()
		_ = pgC.Terminate(ctx)
	}
	return db, cleanup
}
