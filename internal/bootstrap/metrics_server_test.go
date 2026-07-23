package bootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// TestStartMetricsServer_Disabled_ReturnsNoOpShutdown proves that when
// MetricsPort is 0 the returned shutdown function is a no-op and does
// not start any listener.
func TestStartMetricsServer_Disabled_ReturnsNoOpShutdown(t *testing.T) {
	cfg := &config.Config{Monitoring: config.MonitoringConfig{MetricsPort: 0}}
	shutdown := StartMetricsServer(cfg, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("no-op shutdown should return nil, got %v", err)
	}
}

// TestStartMetricsServer_Listener_ServesFailClosedMetrics starts an
// internal metrics listener on a random high port and verifies the
// fail-closed behavior end-to-end.
func TestStartMetricsServer_Listener_ServesFailClosedMetrics(t *testing.T) {
	// Use a fixed high port in the hope it is free; the test is
	// lightweight and fails fast if the port is unavailable.
	port := 19090
	cfg := &config.Config{
		Monitoring: config.MonitoringConfig{
			MetricsPort:          port,
			MetricsBasicAuthUser: "admin",
			MetricsBasicAuthPass: "secret",
		},
	}

	shutdown := StartMetricsServer(cfg, nil)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()

	addr := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)
	client := &http.Client{Timeout: 2 * time.Second}

	// The listener may need a moment to bind; retry briefly.
	var lastErr error
	for i := 0; i < 20; i++ {
		resp, err := client.Get(addr)
		if err != nil {
			lastErr = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauth request with configured credentials: want 401, got %d: %s", resp.StatusCode, string(body))
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		t.Fatalf("metrics listener did not become reachable: %v", lastErr)
	}

	// Valid credentials should succeed (metrics handler returns 200).
	req, _ := http.NewRequest(http.MethodGet, addr, nil)
	req.SetBasicAuth("admin", "secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("authenticated request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid auth: want 200, got %d", resp.StatusCode)
	}

	// Wrong credentials should return 401.
	req2, _ := http.NewRequest(http.MethodGet, addr, nil)
	req2.SetBasicAuth("admin", "wrong")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("wrong-creds request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong creds: want 401, got %d", resp2.StatusCode)
	}
}
