package services

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// sequencedHandler returns a programmed sequence of HTTP responses.
// Each call to ServeHTTP pops the next tuple. When the sequence is
// exhausted it returns 200 + "ok" so retry loops terminate. scenario is
// read-only after construction, so no mutex is needed.
type sequencedHandler struct {
	scenario []response
	calls    atomic.Int32
}

type response struct {
	status  int
	headers map[string]string
	body    string
}

func (h *sequencedHandler) next() response {
	h.calls.Add(1)
	idx := int(h.calls.Load() - 1)
	if idx >= len(h.scenario) {
		return response{status: 200, body: "ok"}
	}
	return h.scenario[idx]
}

func (h *sequencedHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	r := h.next()
	for k, v := range r.headers {
		w.Header().Set(k, v)
	}
	if r.status > 0 {
		w.WriteHeader(r.status)
	}
	if r.body != "" {
		_, _ = io.WriteString(w, r.body)
	}
}

// infiniteHandler returns the same status code on every call. Used by
// tests that need to exhaust the retry loop without crafting a long
// slice literal in place.
type infiniteHandler struct {
	status int
}

func (h *infiniteHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(h.status)
}

// retryOnlyClient builds an *http.Client with retryRoundTripper directly
// wrapping the test transport (the slog logging wrapper is skipped to
// keep test output noise-free).
//
// Timeout is 15s: TestRetry_RetryAfterIsCappedAtCapDelay exercises a
// 5s capped retry-after plus a retry, easily exceeding 5s total client
// wall time; we leave 3x headroom over the 5s capDelay.
func retryOnlyClient(t *testing.T, srvURL string) *http.Client {
	t.Helper()
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &retryRoundTripper{
			next: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
			},
		},
	}
}

// TestRetry_RecoversAfterIdempotentTransient_GET verifies the happy
// retry path: GET + 503 then GET + 200 → success after one retry.
func TestRetry_RecoversAfterIdempotentTransient_GET(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 503}, {status: 200, body: "ok"}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body: want 'ok', got %q", body)
	}
	if got := h.calls.Load(); got != 2 {
		t.Errorf("handler calls: want 2, got %d", got)
	}
}

// TestRetry_DoesNotRetryPOSTOn5xx confirms non-idempotent methods bail
// on 5xx after one attempt.
func TestRetry_DoesNotRetryPOSTOn5xx(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 503}, {status: 503}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 503 {
		t.Fatalf("status: want 503 (passthrough), got %d", resp.StatusCode)
	}
	if got := h.calls.Load(); got != 1 {
		t.Errorf("handler calls: want 1, got %d", got)
	}
}

// TestRetry_AlwaysRetriesOn429 confirms RFC 6585: ANY method backs off
// on 429 regardless of idempotence.
func TestRetry_AlwaysRetriesOn429(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 429}, {status: 200, body: "ok"}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if got := h.calls.Load(); got != 2 {
		t.Errorf("handler calls: want 2, got %d", got)
	}
}

// TestRetry_RetriesOnRequestTimeout_408 verifies 408 Request Timeout
// (RFC 7231 §6.5.7) is treated as retryable for any method because the
// TestNewHTTPClient_Wiring sanity-checks the production constructor.
func TestNewHTTPClient_Wiring(t *testing.T) {
	c := NewHTTPClient()
	if c == nil {
		t.Fatal("NewHTTPClient returned nil")
	}
	if c.Timeout != 30*time.Second {
		t.Errorf("timeout: want 30s, got %v", c.Timeout)
	}
	if _, ok := c.Transport.(*retryRoundTripper); !ok {
		t.Errorf("Transport should be *retryRoundTripper, got %T", c.Transport)
	}
}
