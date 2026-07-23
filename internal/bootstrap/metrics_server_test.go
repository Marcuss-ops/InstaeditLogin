package bootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
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

// TestMetricsHandler_ServesFailClosedMetrics verifies the metrics
// handler's fail-closed behavior end-to-end without binding a real
// port. Using httptest avoids fixed-port collisions on shared CI
// runners that previously caused flaky/racy failures.
func TestMetricsHandler_ServesFailClosedMetrics(t *testing.T) {
	handler := api.MetricsHandler("admin", "secret")
	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := &http.Client{Timeout: 2 * time.Second}

	// Unauthenticated request should return 401.
	resp, err := client.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("unauth request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth request: want 401, got %d: %s", resp.StatusCode, string(body))
	}

	// Valid credentials should succeed (metrics handler returns 200).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	req.SetBasicAuth("admin", "secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("authenticated request failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid auth: want 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Wrong credentials should return 401.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	req2.SetBasicAuth("admin", "wrong")
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("wrong-creds request failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong creds: want 401, got %d: %s", resp2.StatusCode, string(body2))
	}
}
