package services

import (
	"context"
	"errors"
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
// keep test output noise-free). maxBytes=0 means default 10 MB cap.
//
// Timeout is 15s: TestRetry_RetryAfterIsCappedAtCapDelay exercises a
// 5s capped retry-after plus a retry, easily exceeding 5s total client
// wall time; we leave 3x headroom over the 5s capDelay.
func retryOnlyClient(t *testing.T, srvURL string, maxBytes int64) *http.Client {
	t.Helper()
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &retryRoundTripper{
			next: &http.Transport{
				MaxIdleConns:        10,
				MaxIdleConnsPerHost: 10,
			},
			maxResponseBytes: maxBytes,
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

	c := retryOnlyClient(t, srv.URL, 0)
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

	c := retryOnlyClient(t, srv.URL, 0)
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

	c := retryOnlyClient(t, srv.URL, 0)
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
// server explicitly tells us the request never completed.
func TestRetry_RetriesOnRequestTimeout_408(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 408}, {status: 200, body: "ok"}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 0)
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
		t.Errorf("handler calls: want 2 (POST+408 must retry), got %d", got)
	}
}

// TestRetry_RetriesOnTooEarly_425 verifies 425 Too Early (RFC 8470)
// is treated as retryable for any method.
func TestRetry_RetriesOnTooEarly_425(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{{status: 425}, {status: 200, body: "ok"}}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 0)
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
		t.Errorf("handler calls: want 2 (POST+425 must retry), got %d", got)
	}
}

// TestRetry_HonorsRetryAfterHeaderSeconds verifies Retry-After in integer
// seconds overrides the decorrelated backoff.
func TestRetry_HonorsRetryAfterHeaderSeconds(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503, headers: map[string]string{"Retry-After": "3"}},
		{status: 200, body: "ok"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if elapsed < 2500*time.Millisecond {
		t.Errorf("retry should sleep ~3s honoring Retry-After, elapsed=%v", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("retry slept too long, elapsed=%v", elapsed)
	}
}

// TestRetry_RetryAfterIsCappedAtCapDelay confirms Retry-After > capDelay
// is clipped so a hostile server can't make our client sleep forever.
// Lower bound 3500ms tolerates CI scheduler jitter — the sibling test
// (TestRetry_HonorsRetryAfterHeaderSeconds) uses the same tolerance.
func TestRetry_RetryAfterIsCappedAtCapDelay(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		// 9999 seconds is way above capDelay (5s) — must be clipped to 5s.
		{status: 503, headers: map[string]string{"Retry-After": "9999"}},
		{status: 200, body: "ok"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 0)
	start := time.Now()
	resp, err := c.Get(srv.URL)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	if elapsed < 3500*time.Millisecond {
		t.Errorf("Retry-After clipped, but elapsed %v < 3.5s", elapsed)
	}
	if elapsed > 6*time.Second {
		t.Errorf("Retry-After=9999 must be capped at 5s; slept %v", elapsed)
	}
}

// TestRetry_GivesUpAfterMaxAttempts verifies the 5-attempt cap.
// We use a counting handler that records every hit; the RoundTripper
// must surface a 503-status error after exactly 5 hits (1 initial + 4
// retries).
func TestRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 0)
	resp, err := c.Get(srv.URL)
	if err == nil {
		defer resp.Body.Close()
		t.Fatalf("expected error after max attempts, got status %d", resp.StatusCode)
	}
	if got := calls.Load(); got != 5 {
		t.Errorf("handler calls: want 5 (1 + 4 retries), got %d", got)
	}
	if err.Error() == "" {
		t.Errorf("retryableHTTPError should not have empty message")
	}
}

// TestRetry_MaxBytesReaderCapsResponseBody verifies the body cap.
// Scenario: 503 retry, then 200 with a body exceeding the cap. The
// RoundTripper wraps the body in MaxBytesReader; when the caller reads
// past the limit they get *http.MaxBytesError (asserted via errors.As
// to stay robust across Go std-lib phrasing changes).
func TestRetry_MaxBytesReaderCapsResponseBody(t *testing.T) {
	bigBody := make([]byte, 200)
	for i := range bigBody {
		bigBody[i] = 'A'
	}
	var h sequencedHandler
	h.scenario = []response{
		{status: 503, body: ""},
		{status: 200, body: string(bigBody)},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 100) // 100-byte cap
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: want 200, got %d", resp.StatusCode)
	}
	_, err = io.ReadAll(resp.Body)
	if err == nil {
		t.Fatal("expected MaxBytesError on body read past cap, got nil")
	}
	var mbErr *http.MaxBytesError
	if !errors.As(err, &mbErr) {
		t.Errorf("expected *http.MaxBytesError, got %T: %v", err, err)
	}
}

// TestRetry_ContextCancellationBreaksLoop verifies ctx.Done() during a
// Retry-After sleep returns immediately rather than waiting the full
// window.
func TestRetry_ContextCancellationBreaksLoop(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503, headers: map[string]string{"Retry-After": "30"}},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	c := retryOnlyClient(t, srv.URL, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected ctx error after cancel, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("retry should abort on ctx cancel (~200ms), elapsed=%v", elapsed)
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.Canceled or context.DeadlineExceeded; got %T(%v)", err, err)
	}
}

// TestRetry_DecorrelatedJitterRangeDelays confirms AWS-decorrelated
// formula: each sleep ∈ [baseDelay, min(capDelay, prevSleep*3)].
// randFloat64 pinned to 0.5 (midpoint) lets us verify the layered
// midpoint sum against a tight tolerance.
func TestRetry_DecorrelatedJitterRangeDelays(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503},
		{status: 503},
		{status: 503},
		{status: 200, body: "ok"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	// Pin jitter to 0.5: first midpoint 200ms (100+300)/2; second 400ms
	// (200+600)/2; third 800ms (400+1200)/2. Total ≈ 1400ms.
	restore := setRandFloat64(func() float64 { return 0.5 })
	defer restore()

	c := retryOnlyClient(t, srv.URL, 0)
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
	expected := 1400 * time.Millisecond
	tol := 400 * time.Millisecond
	if elapsed < expected-tol || elapsed > expected+tol {
		t.Errorf("3 retries at midpoint-jitter: want ~%v, got %v", expected, elapsed)
	}
}

// TestRetry_IgnoresNonIntegerRetryAfter ensures HTTP-date form (not
// supported by strconv.Atoi) is ignored. Decorrelated jitter runs.
func TestRetry_IgnoresNonIntegerRetryAfter(t *testing.T) {
	var h sequencedHandler
	h.scenario = []response{
		{status: 503, headers: map[string]string{"Retry-After": "Thu, 01 Jan 2099 00:00:00 GMT"}},
		{status: 200, body: "ok"},
	}
	srv := httptest.NewServer(&h)
	defer srv.Close()

	restore := setRandFloat64(func() float64 { return 0 })
	defer restore()

	c := retryOnlyClient(t, srv.URL, 0)
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
	if elapsed > 1500*time.Millisecond {
		t.Errorf("HTTP-date Retry-After must be ignored; elapsed=%v", elapsed)
	}
}

// TestNewHTTPClientWithMaxAttempts_NoRetries proves n=1 fully disables
// the retry loop — a latency-sensitive caller (publish_worker, batches)
// can opt back to the pre-D1 behaviour with a single constructor call.
func TestNewHTTPClientWithMaxAttempts_NoRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewHTTPClientWithMaxAttempts(1)
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want 503 passthrough when retries disabled, got %d", resp.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls: want 1 (n=1 disables retries), got %d", got)
	}
}

// TestNewHTTPClientWithMaxAttempts_DefaultWhenZero pins the fallback:
// n<=0 must produce the canonical 5-attempt budget so a config value of
// 0 (unset) preserves the documented retry behaviour. Because retries
// ARE attempted, failure surfaces as *retryableHTTPError (not the
// raw response) — that's the canonical fail-loud signal callers
// detect via errors.As(&retryableHTTPError{}).
func TestNewHTTPClientWithMaxAttempts_DefaultWhenZero(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	srvURL := srv.URL

	c := NewHTTPClientWithMaxAttempts(0)
	c.Timeout = 15 * time.Second

	// Pin jitter to 0 so the 4 retry sleeps use the base delay of 100ms
	// each. Worst-case total wall time at default 5 attempts ≈ 4*100ms
	// = 400ms; we leave the client timeout at 15s with plenty of
	// headroom for CI scheduler jitter.
	restore := setRandFloat64(func() float64 { return 0 })
	defer restore()

	start := time.Now()
	resp, err := c.Get(srvURL)
	elapsed := time.Since(start)
	if err == nil {
		if resp != nil {
			resp.Body.Close()
		}
		t.Fatalf("expected *retryableHTTPError after default-5 attempts, got nil err (resp=%v)", resp)
	}
	var retryErr *retryableHTTPError
	if !errors.As(err, &retryErr) {
		t.Errorf("expected *retryableHTTPError, got %T: %v", err, err)
	}
	if retryErr.status != http.StatusServiceUnavailable {
		t.Errorf("retryErr.status: want 503, got %d", retryErr.status)
	}
	if got := calls.Load(); got != 5 {
		t.Errorf("handler calls: want 5 (n<=0 falls back to default 5), got %d", got)
	}
	if elapsed > 5*time.Second {
		t.Errorf("retry should complete well under 15s timeout, elapsed=%v", elapsed)
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
