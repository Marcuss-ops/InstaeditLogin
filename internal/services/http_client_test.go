package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sequencedHandler returns a programmed sequence of HTTP responses.
// Each call to ServeHTTP pops the next tuple. When the sequence is
// exhausted it returns 200 + "ok" so retry loops terminate.
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

// retryOnlyClient builds an *http.Client with retryRoundTripper directly
// wrapping the test transport (the slog logging wrapper is skipped to
// keep test output noise-free).
//
// Timeout is 15s: TestRetry_RetryAfterIsCappedAtCapDelay exercises a
// 5s capped retry-after plus a retry, easily exceeding 5s total client
// wall time; we leave 3x headroom over the 5s capDelay.
func retryOnlyClient(t *testing.T, srvURL string) *http.Client {
	t.Helper()
	return retryClientWithCap(t, 0)
}

// retryClientWithCap builds a test client with a configurable maximum
// backoff cap. A cap of 0 means the production default is used.
func retryClientWithCap(t *testing.T, cap time.Duration) *http.Client {
	t.Helper()
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &retryRoundTripper{
			next:       &http.Transport{MaxIdleConns: 10, MaxIdleConnsPerHost: 10},
			maxBackoff: cap,
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

// TestRetry_RetriesPOSTOn429_WithOptIn verifies that a POST is retried
// only when the caller explicitly opts in via WithRetryOptIn.
func TestRetry_RetriesPOSTOn429_WithOptIn(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 429}, {status: 200, body: "ok"}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	ctx := WithRetryOptIn(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
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

// TestRetry_DoesNotRetryPOSTOn429WithoutOptIn confirms that a POST is not
// retried on 429 unless the caller opts in.
func TestRetry_DoesNotRetryPOSTOn429WithoutOptIn(t *testing.T) {
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
	if resp.StatusCode != 429 {
		t.Fatalf("status: want 429 (passthrough), got %d", resp.StatusCode)
	}
	if got := h.calls.Load(); got != 1 {
		t.Errorf("handler calls: want 1, got %d", got)
	}
}

// TestRetry_RetriesAllAllowedStatusesForIdempotentMethods checks that each
// of the configured retryable status codes triggers a retry for GET.
func TestRetry_RetriesAllAllowedStatusesForIdempotentMethods(t *testing.T) {
	codes := []int{408, 429, 500, 502, 503, 504}
	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var h sequencedHandler
			h.scenario = []response{{status: code}, {status: 200, body: "ok"}}
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
			if got := h.calls.Load(); got != 2 {
				t.Errorf("handler calls: want 2, got %d", got)
			}
		})
	}
}

// TestRetry_DoesNotRetryNonRetryableStatusForGET confirms that 4xx codes
// not in the retry set are returned immediately for idempotent GETs.
func TestRetry_DoesNotRetryNonRetryableStatusForGET(t *testing.T) {
	codes := []int{400, 401, 403, 404, 409, 422}
	for _, code := range codes {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var h sequencedHandler
			h.scenario = []response{{status: code}, {status: 200, body: "ok"}}
			srv := httptest.NewServer(&h)
			defer srv.Close()

			c := retryOnlyClient(t, srv.URL)
			resp, err := c.Get(srv.URL)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if resp.StatusCode != code {
				t.Fatalf("status: want %d (passthrough), got %d", code, resp.StatusCode)
			}
			if got := h.calls.Load(); got != 1 {
				t.Errorf("handler calls: want 1, got %d", got)
			}
		})
	}
}

// TestRetry_RetriesHEADAndOPTIONS verifies that other idempotent methods
// are also retried.
func TestRetry_RetriesHEADAndOPTIONS(t *testing.T) {
	methods := []string{http.MethodHead, http.MethodOptions, http.MethodTrace}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			var h sequencedHandler
			h.scenario = []response{{status: 503}, {status: 200, body: "ok"}}
			srv := httptest.NewServer(&h)
			defer srv.Close()

			c := retryOnlyClient(t, srv.URL)
			req, _ := http.NewRequest(method, srv.URL, nil)
			resp, err := c.Do(req)
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			if resp.StatusCode != 200 {
				t.Fatalf("status: want 200, got %d", resp.StatusCode)
			}
			if got := h.calls.Load(); got != 2 {
				t.Errorf("handler calls: want 2, got %d", got)
			}
		})
	}
}

// TestRetry_RetriesPUTWithOptIn verifies that PUT also requires opt-in to
// be retried.
func TestRetry_RetriesPUTWithOptIn(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 504}, {status: 200, body: "ok"}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	ctx := WithRetryOptIn(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, srv.URL, nil)
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

// TestRetry_HonorsRetryAfter checks that Retry-After is respected (capped
// and with jitter). A 1-second Retry-After should delay the retry by at
// least that long.
func TestRetry_HonorsRetryAfter(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503, headers: map[string]string{"Retry-After": "1"}},
		{status: 200, body: "ok"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	start := time.Now()
	resp, err := c.Get(srv.URL)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("Retry-After not honored: elapsed=%v", elapsed)
	}
	if got := h.calls.Load(); got != 2 {
		t.Errorf("handler calls: want 2, got %d", got)
	}
}

// TestRetry_RetryAfterIsCapped verifies that an excessive Retry-After is
// capped to the configured maximum backoff, so the test can still complete
// in reasonable time. We use a small test cap (200ms) to avoid a long wait.
func TestRetry_RetryAfterIsCapped(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503, headers: map[string]string{"Retry-After": "300"}},
		{status: 200, body: "ok"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryClientWithCap(t, 200*time.Millisecond)
	start := time.Now()
	resp, err := c.Get(srv.URL)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	// The effective cap (200ms) plus jitter must be far less than the
	// 300s Retry-After the server sent.
	if elapsed > 2*time.Second {
		t.Errorf("Retry-After cap not respected: elapsed=%v", elapsed)
	}
	if got := h.calls.Load(); got != 2 {
		t.Errorf("handler calls: want 2, got %d", got)
	}
}

// TestRetry_FinalResponsePreservesBodyAndHeader verifies that the last
// response (even a retryable one when attempts are exhausted) is
// returned with its body and headers intact.
func TestRetry_FinalResponsePreservesBodyAndHeader(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503},
		{status: 503},
		{status: 503, headers: map[string]string{"X-Final": "yes"}, body: "final"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Final"); got != "yes" {
		t.Errorf("X-Final header: want 'yes', got %q", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "final" {
		t.Errorf("body: want 'final', got %q", body)
	}
	if got := h.calls.Load(); got != 3 {
		t.Errorf("handler calls: want 3, got %d", got)
	}
}

// TestRetry_RebuildsRequestBody ensures that a request body is replayed
// on retry when the request is marked idempotent via opt-in.
func TestRetry_RebuildsRequestBody(t *testing.T) {
	var calls atomic.Int32
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received = append(received, string(b))
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	ctx := WithRetryOptIn(context.Background())
	body := "payload"
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(body))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if len(received) != 2 {
		t.Fatalf("requests count: want 2, got %d", len(received))
	}
	if received[0] != body || received[1] != body {
		t.Errorf("body not replayed correctly: got %v", received)
	}
}

// TestRetry_MaxAttemptsExhausted verifies that after max attempts the
// last response is returned, not an error.
func TestRetry_MaxAttemptsExhausted(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503},
		{status: 503},
		{status: 503, body: "last"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("status: want 503, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "last" {
		t.Errorf("body: want 'last', got %q", body)
	}
	if got := h.calls.Load(); got != 3 {
		t.Errorf("handler calls: want 3, got %d", got)
	}
}

// TestRetry_TransportErrorForIdempotent verifies that transport-level
// errors are retried for idempotent methods.
func TestRetry_TransportErrorForIdempotent(t *testing.T) {
	inner := &failingTransport{failuresLeft: 1}
	c := &http.Client{
		Transport: &retryRoundTripper{next: inner},
	}

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if got := inner.calls.Load(); got != 2 {
		t.Errorf("round trips: want 2, got %d", got)
	}
}

// failingTransport fails a configurable number of times before returning
// a synthetic 200 OK. It is used to test transport-level retries.
type failingTransport struct {
	failuresLeft int
	calls        atomic.Int32
}

func (ft *failingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ft.calls.Add(1)
	if ft.failuresLeft > 0 {
		ft.failuresLeft--
		return nil, errors.New("simulated transport error")
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// TestRetry_TransportErrorForNonIdempotent verifies that transport-level
// errors are not retried for non-idempotent methods.
func TestRetry_TransportErrorForNonIdempotent(t *testing.T) {
	inner := &failingTransport{failuresLeft: 2}
	c := &http.Client{
		Transport: &retryRoundTripper{next: inner},
	}

	req, _ := http.NewRequest(http.MethodPost, "http://example.com", nil)
	resp, err := c.Do(req)
	if err == nil {
		defer resp.Body.Close()
		t.Fatalf("expected transport error, got status %d", resp.StatusCode)
	}
	if got := inner.calls.Load(); got != 1 {
		t.Errorf("round trips: want 1, got %d", got)
	}
}

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
