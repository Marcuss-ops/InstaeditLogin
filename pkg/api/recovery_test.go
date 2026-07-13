package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
)

// hs builds a minimal Router with the given Sentry hub. The other
// dependencies (capabilities, userRepo, etc.) are nil — the
// recovery middleware does NOT call any of them when recovering
// from a panic in the wrapped handler. We only need a valid *Router
// so we can call recoverMiddleware directly via the fields the
// helper reads.
func hs(hub *sentry.Hub) *Router {
	return &Router{sentryHub: hub}
}

// TestRecovery_NoSentry_NormalPassthrough confirms the no-Sentry
// middleware does not interfere with a happy-path handler.
func TestRecovery_NoSentry_NormalPassthrough(t *testing.T) {
	r := hs(nil)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(r.recoverMiddleware(inner))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: want 200, got %d", resp.StatusCode)
	}
}

// TestRecovery_NoSentry_PanicWrites500 confirms the unguarded path
// catches a panic and returns 500 + JSON even when no Sentry hub is
// wired. The handler below panics unconditionally; the middleware
// must absorb it.
func TestRecovery_NoSentry_PanicWrites500(t *testing.T) {
	r := hs(nil)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	srv := httptest.NewServer(r.recoverMiddleware(inner))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["error"] == "" {
		t.Errorf("body: want JSON with non-empty 'error' field, got %v", body)
	}
}

// TestRecovery_WithSentry_PanicWrites500 confirms the Sentry-aware
// path also returns 500 + JSON (the SDK's default Repanic=false
// gives us this surface). We use a fresh Hub paired with the
// SDK's no-op transport so sentry.Init is happy on the test
// goroutine. We deliberately do NOT assert CaptureException was
// called — that's the SDK's internal contract; here we only test
// the Router-level behaviour.
func TestRecovery_WithSentry_PanicWrites500(t *testing.T) {
	// Init Sentry with a transport that the SDK won't actually post to
	// (we use the default HTTP transport against a localhost endpoint
	// AND set a sample rate of 0 so the SDK drops the event entirely).
	// That keeps the test free of outbound network without coupling to
	// sentry-go internals.
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:         "https://public@127.0.0.1/1",
		SampleRate:  0,
		Environment: "test",
	}); err != nil {
		t.Fatalf("sentry.Init: %v", err)
	}
	hub := sentry.CurrentHub()

	r := hs(hub)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})
	srv := httptest.NewServer(r.recoverMiddleware(inner))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", resp.StatusCode)
	}
	body := make(map[string]string)
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(body["error"], "internal server error") {
		t.Errorf("body: want 'internal server error', got %v", body)
	}
}
