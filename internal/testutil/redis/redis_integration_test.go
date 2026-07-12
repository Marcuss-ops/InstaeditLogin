//go:build integration

// Package redis_test exercises the public surface of
// internal/testutil/redis: PING → PONG (already verified by
// runtime.WaitReady's nil-err check at the end of StartTestRedis),
// plus SET → GET roundtrip to confirm the *redis.Client returned
// is genuinely usable (not just connectable).
//
// Run with: go test -tags=integration -run '.*' ./internal/testutil/redis/...
package redis_test

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/redis"
)

// TestStartTestRedis_PingSetGetRoundTrip is the canonical smoke
// test for the new testutil/redis helper.
//
// Asserts:
//  1. client.Ping(ctx).Result() returns "PONG" — the WaitReady path
//     already checks for nil err; this catches a regression where
//     the *StatusCmd's .Result() deserialization is broken on the
//     testcontainer RESP path (e.g., a future go-redis minor
//     release that returns a different shape).
//  2. Set + Get roundtrip preserves the written value byte-for-byte
//     — proves the *redis.Client is wired correctly end-to-end,
//     not just connectable. This is the canonical "is the
//     abstraction a real abstraction or just a TCP-dial
//     abstraction" gate.
func TestStartTestRedis_PingSetGetRoundTrip(t *testing.T) {
	client, cleanup := redis.StartTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	pong, err := client.Ping(ctx).Result()
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if pong != "PONG" {
		t.Errorf("Ping Result: want %q, got %q", "PONG", pong)
	}

	const key = "testutil:smoke:hello"
	const want = "world"
	if err := client.Set(ctx, key, want, 0).Err(); err != nil {
		t.Fatalf("Set %q=%q: %v", key, want, err)
	}

	got, err := client.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("Get %q: %v", key, err)
	}
	if got != want {
		t.Errorf("Get %q: want %q, got %q", key, want, got)
	}
}
