// Package redis provides shared testcontainers-based helpers for
// integration tests across the InstaeditLogin codebase — the
// first concrete second-engine consumer of internal/testutil/runtime.
//
// This package mirrors internal/testutil/postgres exactly, with
// the Redis PING command as the readiness probe instead of the
// SQL driver's db.Ping. The composition proves that
// internal/testutil/runtime's RequireDocker + WaitReady primitives
// apply cleanly to non-SQL backends — there is nothing SQL-specific
// in either helper. Future testutil/<engine> packages (Kafka, etc.)
// follow the same path: import runtime, compose a t<engine>.Run,
// pass the engine's native "is the listener up?" probe into
// runtime.WaitReady.
//
// The package compiles unconditionally (no //go:build integration
// tag): the standard library plus testcontainers-go,
// the redis module, and go-redis/v9 are always present in go.mod.
// The integration-tagged TEST FILES trigger actual Docker usage;
// run with: go test -tags=integration ./internal/testutil/redis/...
package redis

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	tredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/runtime"
)

// StartTestRedis spins up an ephemeral Redis 7-alpine via
// testcontainers-go and returns a *redis.Client + a cleanup
// function that closes the client and terminates the container.
//
// Configuration:
//
//   - Image:      redis:7-alpine (matches the postgres.go major-only
//                 pinning convention)
//   - Credentials: none (testcontainer-default unsecured)
//   - DB index:    0 (the Redis default)
//
// The first step is an internal runtime.RequireDocker call: tests
// that don't need a separate docker-availability guard can call
// this as their only Docker-touching helper.
//
// The readiness-poll loop is delegated to runtime.WaitReady with
// client.Ping(ctx).Err() as the probe (and the canonical 15s/200ms
// defaults). The closure returns nil iff Redis replied PONG to the
// PING command — the analogue of the db.Ping closure that
// postgres.go uses; the PONG reply is what testcontainers-go/Redis
// calls "ready" once the TCP listener is accepting RESP frames.
//
// Each call produces a FRESH ephemeral container — there is no
// cross-test state sharing. The container is killed by either the
// returned cleanup func (defer-friendly) OR by wiring it into
// t.Cleanup at rig construction sites.
//
// No functional options today (YAGNI — postgres.WithDatabase was
// added because the prior inline helper had a hard-coded database
// name; Redis has no equivalent migration-test concern driving a
// default name). When a future test needs logical isolation across
// DBs, a password, or a different image, add a With... pattern
// mirroring postgres.WithDatabase.
func StartTestRedis(t *testing.T) (*redis.Client, func()) {
	t.Helper()

	// Docker-availability guard is canonical in the runtime
	// package so a future testutil/kafka/etc. helper can compose
	// it the same way.
	runtime.RequireDocker(t)

	ctx := context.Background()

	rC, err := tredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}

	connStr, err := rC.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	// redis.ParseURL handles the `redis://[:password@]host:port`
	// URL that testcontainers-go/modules/redis emits. Constructing
	// via redis.Options would be more verbose and offers no
	// flexibility we need today (no TLS, no Redis Cluster, no
	// Sentinel — all of which the testcontainer doesn't expose
	// anyway).
	opts, err := redis.ParseURL(connStr)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	client := redis.NewClient(opts)

	// Readiness-poll delegated to the runtime helper. Probe
	// closure returns client.Ping(ctx).Err() — nil on PONG reply,
	// non-nil on connection-refused / reset / timeout. This is
	// the analogue of db.Ping for SQL backends; the runtime
	// helper's deadline + backoff are identical.
	runtime.WaitReady(t, func() error { return client.Ping(ctx).Err() },
		runtime.WaitReadyDefaultDeadline, runtime.WaitReadyDefaultBackoff)

	cleanup := func() {
		// Order: close the client first (drains in-flight
		// commands cleanly), THEN terminate the container. Same
		// shape as postgres.StartTestPostgres's cleanup (db.Close
		// before pgC.Terminate).
		_ = client.Close()
		_ = rC.Terminate(ctx)
	}
	return client, cleanup
}
