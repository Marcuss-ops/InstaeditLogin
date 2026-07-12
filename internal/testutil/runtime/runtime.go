// Package runtime provides shared testcontainers-runtime helpers —
// the cross-cutting concerns every ephemeral-container integration
// test needs regardless of which engine (Postgres, Redis, Kafka, …)
// it spins up.
//
// Public surface (kept small on purpose):
//
//   - RequireDocker(t)   — Docker-availability guard (binary on PATH
//     + daemon reachable). t.Skipf on failure so dev environments
//     without Docker don't see false failures.
//
//   - WaitReady(t, ping, deadline, backoff) — generic readiness-poll
//     loop. Retries the caller's `ping` function every `backoff`
//     duration until it returns nil OR `deadline` elapses. Used to
//     absorb testcontainers-go's log-based-ready-vs-TCP-listen race.
//
//   - WaitReadyDefaultDeadline / WaitReadyDefaultBackoff — canonical
//     timed-budget constants (15s / 200ms). New callers either
//     explicitly pass these constants or override per-container.
//
// Why a separate package:
//
//   Postgres-specific helpers in internal/testutil/postgres
//   compose these primitives (RequireDocker → tpostgres.Run →
//   WaitReady(db.Ping)). New containers (Redis, Kafka, …) coming in
//   the future ALSO need RequireDocker + a readiness-poll loop.
//   Keeping the generic primitives in their own package avoids
//   duplicating the loop into every testutil/<engine>/ package and
//   keeps the convention DRY across future integrations.
//
// The package compiles unconditionally (no //go:build integration
// tag): only the standard library is referenced. The
// integration-tagged TEST FILES trigger actual Docker usage; run
// with: go test -tags=integration ./...
package runtime

import (
	"os/exec"
	"testing"
	"time"
)

// WaitReadyDefaultDeadline is the canonical deadline WaitReady's
// Postgres caller uses (15 seconds). Override per-container if a
// particular engine's startup profile demands a larger or smaller
// budget — most engines we plan to integrate (Redis, Kafka) tolerate
// the same 15s window.
const WaitReadyDefaultDeadline = 15 * time.Second

// WaitReadyDefaultBackoff is the canonical poll interval (200ms).
// Short enough that a healthy container's first probe after listen
// is usually caught in 1–3 attempts; long enough that very-early
// (pre-listen) probes don't hammer the kernel with rapid
// connection-refused errors.
const WaitReadyDefaultBackoff = 200 * time.Millisecond

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
// RequireDocker is also called as the first step of every
// <engine>.StartTest<Engine> helper that composes it, so test files
// don't need to invoke it separately. A test that needs Docker
// without spinning up a specific container (e.g. checks
// docker-compose fixtures) can call this helper directly.
func RequireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// WaitReady polls the provided ping function until it succeeds OR
// the deadline elapses. Used by every <engine> integration test to
// absorb the testcontainers-go ready-vs-listen race: the log-based
// "ready" message can fire BEFORE the TCP listener is up, so the
// first probe often fails with connection-refused / reset. The poll
// retries until the service is actually accepting the protocol the
// caller is checking.
//
// On success: silent (no t.Logf). A successful first-attempt poll is
// the common path; logging it under -v across all callers adds noise
// without information. The failure-side Fatalf already names the
// last ping error, which is what a maintainer actually needs in CI
// logs.
//
// On timeout: t.Fatalf with the attempt count, the configured
// deadline, and the last error from ping — the [lastErr] string is
// what a maintainer will see in CI logs when a future engine's
// readiness probe fails for a non-transient reason.
//
// Parameters:
//
//   - t:        the test handle (used for t.Helper + t.Fatalf).
//     Accepts testing.TB rather than *testing.T for testability:
//     a fake TB can capture the Fatalf call (format string + args)
//     in unit tests without leaking failure to the parent test, and
//     any real *testing.T satisfies testing.TB so existing callers
//     (postgres.go, redis.go, etc.) are unaffected by the
//     widening.
//   - ping:     zero-arg function returning nil iff the service is
//     ready. Canonical contacts are db.Ping for Postgres, the PING
//     command for Redis, broker metadata requests for Kafka. The
//     caller controls the protocol-level readiness check; WaitReady
//     controls only the timing.
//   - deadline: maximum wall-clock duration for the poll. Zero or
//     negative → WaitReadyDefaultDeadline.
//   - backoff:  sleep duration between failed probes. Zero or
//     negative → WaitReadyDefaultBackoff. A backoff > deadline (after
//     default-resolution) is harmless — the loop's last failed
//     probe simply exits without sleeping.
func WaitReady(t testing.TB, ping func() error, deadline, backoff time.Duration) {
	t.Helper()

	if deadline <= 0 {
		deadline = WaitReadyDefaultDeadline
	}
	if backoff <= 0 {
		backoff = WaitReadyDefaultBackoff
	}

	absDeadline := time.Now().Add(deadline)

	var lastErr error
	for attempt := 1; ; attempt++ {
		lastErr = ping()
		if lastErr == nil {
			return
		}

		if time.Now().After(absDeadline) {
			t.Fatalf("WaitReady: timeout after %d attempt(s) over %v (last ping error: %v)",
				attempt, deadline, lastErr)
			// Defensive return: a real *testing.T.Fatalf calls
			// runtime.Goexit and never reaches this line, but a
			// fake testing.TB (e.g. unit-test stubs that capture
			// the Fatalf call) will return normally — without this
			// return, the loop would keep sleeping + retrying
			// against a fake that never fails, hanging the test.
			return
		}
		time.Sleep(backoff)
	}
}
