// Per-tier rate-limit middleware end-to-end tests (SPRINT 2.2).
//
// Each tier is exercised via httptest with a fake RateLimitRepo
// (no Postgres) for the Postgres tiers and a real MemoryLimiter
// (in-process) for the in-memory tiers. The tests assert:
//   - first N requests pass with X-RateLimit-Limit / Remaining / Reset
//   - N+1 returns 429 with X-RateLimit-* + Retry-After
//   - independent scopes are independent
//   - identity resolution (workspaceID, keyID) drives the scope string
package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeRateLimitRepo mirrors the service's RateLimitRepo interface
// (defined in internal/services/ratelimit.go) without the
// dependency on internal/repository. The test uses a per-test
// counter map and supports a forced error for fail-open coverage.
type fakeRateLimitRepo struct {
	mu       sync.Mutex
	counters map[string]int
	winStart time.Time
	forceErr error
	calls    int64
}

func newFakeRateLimitRepo() *fakeRateLimitRepo {
	return &fakeRateLimitRepo{
		counters: make(map[string]int),
		winStart: time.Now(),
	}
}

func (f *fakeRateLimitRepo) Increment(_ context.Context, scope string, window time.Duration) (int, time.Time, error) {
	atomic.AddInt64(&f.calls, 1)
	if f.forceErr != nil {
		return 0, time.Time{}, f.forceErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(f.winStart) >= window {
		f.counters = make(map[string]int)
		f.winStart = time.Now()
	}
	f.counters[scope]++
	return f.counters[scope], f.winStart.Add(window), nil
}

func (f *fakeRateLimitRepo) setCount(scope string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters[scope] = n
}

// okHandler is the inner handler the rate-limit middleware wraps.
// Returns 200 OK with an empty body.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
})

// -----------------------------------------------------------------------
// Per-workspace POST /posts (Postgres tier, 60/min/workspace)
// -----------------------------------------------------------------------

func TestWorkspacePostLimit_AllowsFirstN_DeniesNPlusOne(t *testing.T) {
	repo := newFakeRateLimitRepo()
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := WorkspacePostLimit(svc)

	// 60 requests under the budget — all 200 with X-RateLimit-* headers.
	for i := 0; i < 60; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
		req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 1, 1)))
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d (body=%s)", i, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("X-RateLimit-Limit") != "60" {
			t.Errorf("request %d: X-RateLimit-Limit want 60, got %s", i, rec.Header().Get("X-RateLimit-Limit"))
		}
		if rec.Header().Get("X-RateLimit-Remaining") == "" {
			t.Errorf("request %d: X-RateLimit-Remaining missing", i)
		}
	}
	// 61st request: 429 + Retry-After.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 1, 1)))
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("61st: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Errorf("61st: Retry-After missing")
	}
	if rec.Header().Get("X-RateLimit-Limit") != "60" {
		t.Errorf("61st: X-RateLimit-Limit want 60, got %s", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("61st: X-RateLimit-Remaining want 0, got %s", rec.Header().Get("X-RateLimit-Remaining"))
	}
}

func TestWorkspacePostLimit_IndependentWorkspaces(t *testing.T) {
	repo := newFakeRateLimitRepo()
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := WorkspacePostLimit(svc)

	// Pre-seed workspace 1 at the limit.
	repo.setCount("ws_post:1", 60)

	// Workspace 1: 61st is denied.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 1, 1)))
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("ws 1: want 429, got %d", rec.Code)
	}
	// Workspace 2: first request must succeed (independent scope).
	req = httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 2, 1)))
	rec = httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("ws 2: want 200, got %d", rec.Code)
	}
}

func TestWorkspacePostLimit_NoIdentity_Bypass(t *testing.T) {
	// When no identity is in context the middleware passes through
	// (the auth layer will 401 on its own).
	repo := newFakeRateLimitRepo()
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := WorkspacePostLimit(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("no identity: want 200 (pass-through), got %d", rec.Code)
	}
}

func TestWorkspacePostLimit_ApiKeyCaller_Bypass(t *testing.T) {
	// API-key callers don't write posts via this endpoint — the
	// middleware must bypass.
	repo := newFakeRateLimitRepo()
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := WorkspacePostLimit(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(7, 99, 1, nil)))
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("api key: want 200 (pass-through), got %d", rec.Code)
	}
}

// -----------------------------------------------------------------------
// Per-API-key reads (Postgres tier, 600/min/key)
// -----------------------------------------------------------------------

func TestAPIKeyReadLimit_AllowsFirstN_DeniesNPlusOne(t *testing.T) {
	repo := newFakeRateLimitRepo()
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := APIKeyReadLimit(svc)

	// 600 requests under the budget.
	for i := 0; i < 600; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil)
		req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(7, 99, 1, nil)))
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i, rec.Code)
		}
	}
	// 601st: 429.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(7, 99, 1, nil)))
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("601st: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Limit") != "600" {
		t.Errorf("601st: X-RateLimit-Limit want 600, got %s", rec.Header().Get("X-RateLimit-Limit"))
	}
}

func TestAPIKeyReadLimit_JwtUser_Bypass(t *testing.T) {
	// JWT dashboard users are not rate-limited at the per-key tier.
	repo := newFakeRateLimitRepo()
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := APIKeyReadLimit(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 1, 1)))
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("jwt user: want 200 (pass-through), got %d", rec.Code)
	}
}

// -----------------------------------------------------------------------
// Per-endpoint media presign (in-memory tier, 30/min)
// -----------------------------------------------------------------------

func TestMediaPresignLimit_AllowsFirstN_DeniesNPlusOne(t *testing.T) {
	svc := services.NewRateLimitService(newFakeRateLimitRepo())
	defer svc.Shutdown()
	mw := MediaPresignLimit(svc)

	for i := 0; i < 30; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", nil)
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", nil)
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("31st: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Limit") != "30" {
		t.Errorf("31st: X-RateLimit-Limit want 30, got %s", rec.Header().Get("X-RateLimit-Limit"))
	}
}

// -----------------------------------------------------------------------
// Per-IP OAuth start (in-memory tier, 20/min/IP)
// -----------------------------------------------------------------------

func TestOAuthStartLimit_AllowsFirstN_DeniesNPlusOne(t *testing.T) {
	svc := services.NewRateLimitService(newFakeRateLimitRepo())
	defer svc.Shutdown()
	mw := OAuthStartLimit(svc)

	for i := 0; i < 20; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
		req.RemoteAddr = "203.0.113.42:12345"
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.RemoteAddr = "203.0.113.42:12345"
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("21st: want 429, got %d", rec.Code)
	}
}

func TestOAuthStartLimit_IndependentIPs(t *testing.T) {
	svc := services.NewRateLimitService(newFakeRateLimitRepo())
	defer svc.Shutdown()
	mw := OAuthStartLimit(svc)

	// IP A: 21st denied.
	for i := 0; i < 21; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
		req.RemoteAddr = "198.51.100.1:11111"
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		_ = rec.Code
	}
	// IP A: confirmed 429.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.RemoteAddr = "198.51.100.1:11111"
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("IP A: want 429, got %d", rec.Code)
	}
	// IP B: fresh — must succeed.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.RemoteAddr = "198.51.100.2:22222"
	rec = httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("IP B: want 200, got %d", rec.Code)
	}
}

func TestOAuthStartLimit_XForwardedFor_UsesLeftmostIP(t *testing.T) {
	svc := services.NewRateLimitService(newFakeRateLimitRepo())
	defer svc.Shutdown()
	mw := OAuthStartLimit(svc)

	// Burn the leftmost IP's budget (21 hits, the 21st is 429).
	for i := 0; i < 21; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.1")
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		_ = rec.Code
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.99, 10.0.0.1")
	rec := httptest.NewRecorder()
	mw(okHandler).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("X-Forwarded-For leftmost: want 429, got %d", rec.Code)
	}
}

// -----------------------------------------------------------------------
// Fail-open on Postgres error (any tier)
// -----------------------------------------------------------------------

func TestWorkspacePostLimit_FailOpenOnRepoError(t *testing.T) {
	repo := newFakeRateLimitRepo()
	repo.forceErr = errors.New("simulated db blip")
	svc := services.NewRateLimitService(repo)
	defer svc.Shutdown()
	mw := WorkspacePostLimit(svc)

	// 10 requests under the budget; the repo errors on every Increment.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", nil)
		req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 1, 1)))
		rec := httptest.NewRecorder()
		mw(okHandler).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: want 200 (fail-open), got %d", i, rec.Code)
		}
	}
}

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------
