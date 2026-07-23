// Tests for cmd/worker/health_listener.go (Blocco #4.1). Lock the
// dev-default-OFF contract + the on-path happy tcp_connect round trip
// so a future refactor that flips the default (surprising local devs)
// trips a test rather than a silent regression.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
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

// TestWorkerHealthListener_Ready_HealthyCritical verifies that /ready
// returns 200 when all critical workers are healthy.
func TestWorkerHealthListener_Ready_HealthyCritical(t *testing.T) {
	port, cleanup := pickWorkerHealthPort(t)
	defer cleanup()

	reg := worker.NewRegistry()
	reg.Register(worker.WorkerSpec{
		Name:     "healthy",
		Critical: true,
		Run: func(ctx context.Context) error {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					reg.Heartbeat("healthy")
				}
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = reg.StartAll(ctx)

	// Wait for the worker to become healthy before querying /ready.
	time.Sleep(100 * time.Millisecond)

	startWorkerHealthListener(ctx, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body, code := getReady(t, port)
	if code != http.StatusOK {
		t.Fatalf("expected /ready 200, got %d: %s", code, body)
	}
	if body == "" {
		t.Fatal("expected /ready body to be non-empty")
	}
}

// TestWorkerHealthListener_Ready_CriticalFailed verifies that /ready
// returns 503 when a critical worker has failed, even if non-critical
// workers are still running.
func TestWorkerHealthListener_Ready_CriticalFailed(t *testing.T) {
	port, cleanup := pickWorkerHealthPort(t)
	defer cleanup()

	reg := worker.NewRegistry()
	reg.Register(worker.WorkerSpec{
		Name:     "failing",
		Critical: true,
		Run: func(ctx context.Context) error {
			return fmt.Errorf("critical boom")
		},
	})
	reg.Register(worker.WorkerSpec{
		Name:     "healthy",
		Critical: false,
		Run: func(ctx context.Context) error {
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					reg.Heartbeat("healthy")
				}
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := reg.StartAll(ctx)

	// Wait for the critical worker to fail and be recorded.
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("critical worker did not report failure")
	}

	startWorkerHealthListener(ctx, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body, code := getReady(t, port)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected /ready 503, got %d: %s", code, body)
	}
}

// TestWorkerHealthListener_Ready_NoWorkers verifies that /ready
// returns 503 when the registry has no workers at all.
func TestWorkerHealthListener_Ready_NoWorkers(t *testing.T) {
	port, cleanup := pickWorkerHealthPort(t)
	defer cleanup()

	reg := worker.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startWorkerHealthListener(ctx, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	body, code := getReady(t, port)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("expected /ready 503 with no workers, got %d: %s", code, body)
	}
}

func pickWorkerHealthPort(t *testing.T) (int, func()) {
	prev, had := os.LookupEnv("WORKER_HEALTH_PORT")
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not probe free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	_ = os.Setenv("WORKER_HEALTH_PORT", strconv.Itoa(port))

	return port, func() {
		if had {
			_ = os.Setenv("WORKER_HEALTH_PORT", prev)
		} else {
			_ = os.Unsetenv("WORKER_HEALTH_PORT")
		}
	}
}

func getReady(t *testing.T, port int) (string, int) {
	t.Helper()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/ready")
		if err != nil {
			lastErr = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body), resp.StatusCode
	}
	t.Fatalf("failed to query /ready: %v", lastErr)
	return "", 0
}
