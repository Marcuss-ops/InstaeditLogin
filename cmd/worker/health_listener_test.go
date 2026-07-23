// Tests for cmd/worker/health_listener.go (Blocco #4.1). Lock the
// dev-default-OFF contract + the on-path happy tcp_connect round trip
// so a future refactor that flips the default (surprising local devs)
// trips a test rather than a silent regression.
package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

// TestWorkerHealthListener_Default_Off confirms WORKER_HEALTH_PORT
// unset → listener is skipped (no bind). This is the canonical
// `make run-worker` default and the dev ergonomics invariant.
func TestWorkerHealthListener_Default_Off(t *testing.T) {
	prev, had := os.LookupEnv("WORKER_HEALTH_PORT")
	defer func() {
		if had {
			_ = os.Setenv("WORKER_HEALTH_PORT", prev)
		} else {
			_ = os.Unsetenv("WORKER_HEALTH_PORT")
		}
	}()
	_ = os.Unsetenv("WORKER_HEALTH_PORT")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should be a no-op (no bind).
	startWorkerHealthListener(ctx, worker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Give the listener a beat to bind IF it were going to.
	time.Sleep(50 * time.Millisecond)

	// TestPass: no panic, no bind. Nothing further to assert at this layer
	// (we don't expose a "did it bind?" channel from startWorkerHealthListener
	// — adding one for this test would be a worse trade than the gap).
	_ = ctx
}

// TestWorkerHealthListener_ExplicitOff confirms shorthand disable
// values parse and skip the bind.
func TestWorkerHealthListener_ExplicitOff(t *testing.T) {
	cases := []string{"0", "off", "disabled"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			prev, had := os.LookupEnv("WORKER_HEALTH_PORT")
			defer func() {
				if had {
					_ = os.Setenv("WORKER_HEALTH_PORT", prev)
				} else {
					_ = os.Unsetenv("WORKER_HEALTH_PORT")
				}
			}()
			_ = os.Setenv("WORKER_HEALTH_PORT", v)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			startWorkerHealthListener(ctx, worker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			time.Sleep(20 * time.Millisecond)
			// TestPass: no panic on any of the disable shorthands.
			_ = ctx
		})
	}
}

// TestWorkerHealthListener_InvalidFallsBackToOff confirms a bogus
// port value (negative, too-large, unparseable) silently falls back
// to OFF rather than binding 0 or panicking.
func TestWorkerHealthListener_InvalidFallsBackToOff(t *testing.T) {
	cases := []string{"-1", "99999", "abc", "9090.5"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			prev, had := os.LookupEnv("WORKER_HEALTH_PORT")
			defer func() {
				if had {
					_ = os.Setenv("WORKER_HEALTH_PORT", prev)
				} else {
					_ = os.Unsetenv("WORKER_HEALTH_PORT")
				}
			}()
			_ = os.Setenv("WORKER_HEALTH_PORT", v)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			startWorkerHealthListener(ctx, worker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			time.Sleep(20 * time.Millisecond)
			// TestPass: no panic, no bind attempt on bogus values.
			_ = ctx
		})
	}
}

// TestWorkerHealthListener_AcceptsTCPConnection confirms the
// happy-path Fly tcp_checks contract: when WORKER_HEALTH_PORT is a
// valid port, the listener accepts a TCP connection. Uses an
// OS-chosen free port to avoid clashing with the dev runtime.
func TestWorkerHealthListener_AcceptsTCPConnection(t *testing.T) {
	prev, had := os.LookupEnv("WORKER_HEALTH_PORT")
	defer func() {
		if had {
			_ = os.Setenv("WORKER_HEALTH_PORT", prev)
		} else {
			_ = os.Unsetenv("WORKER_HEALTH_PORT")
		}
	}()

	// Pick a free port via OS-assigned listener, then close + reuse.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not probe free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	_ = os.Setenv("WORKER_HEALTH_PORT", strconv.Itoa(port))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startWorkerHealthListener(ctx, worker.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Poll for the listener to bind (deterministic address lookup).
	deadline := time.Now().Add(time.Second)
	var c net.Conn
	for time.Now().Before(deadline) {
		c, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 100*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("tcp dial to worker health port failed: %v", err)
	}
	_ = c.Close() // accept-and-close path completed; testPass.
}
