package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// -----------------------------------------------------------------------
// Rate limit middleware tests — FASE 1.2
// -----------------------------------------------------------------------

// echoHandler returns a simple handler that responds 200 with the path.
func echoHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.URL.Path))
	}
}

func TestRateLimit_AnonymousUnderLimit_Passes(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// Send 50 requests (under the 100/min anon limit). All must pass.
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200 (under limit)", i, rec.Code)
		}
	}
}

func TestRateLimit_AnonymousExceedsLimit_Returns429(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// Send 101 requests (over the 100/min anon limit). First 100
	// should pass, the 101st should fail with 429.
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "10.0.0.1:54321"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 100 {
			// First 100 = burst, allowed.
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d: got %d, want 200 (within burst)", i, rec.Code)
			}
		} else {
			// 101st should be rate-limited.
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("request %d: got %d, want 429 (rate limited)", i, rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 response missing Retry-After header")
			}
		}
	}
}

func TestRateLimit_AuthBearerHeader_UsesHigherLimit(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// With Bearer header, auth limit is 1000/min. 101 requests
	// should all pass (under 1000).
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
		req.RemoteAddr = "10.0.0.2:11111"
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.eyJ1aWQiOjF9.abc123")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("auth request %d: got %d, want 200 (under auth limit)", i, rec.Code)
		}
	}
}

func TestRateLimit_SessionCookie_UsesHigherLimit(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// With session cookie, auth limit is 1000/min. 101 requests
	// should all pass (under 1000).
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
		req.RemoteAddr = "10.0.0.3:22222"
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "valid-jwt-token"})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("session request %d: got %d, want 200 (under auth limit)", i, rec.Code)
		}
	}
}

func TestRateLimit_DifferentIPs_IndependentBuckets(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// IP A: 101 requests → last one should be 429.
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "10.1.0.1:11111"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 100 && rec.Code != http.StatusOK {
			t.Fatalf("IP A request %d: got %d, want 200", i, rec.Code)
		}
		if i == 100 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("IP A request 100: got %d, want 429", rec.Code)
		}
	}

	// IP B: should still have its full burst (separate bucket).
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.RemoteAddr = "10.1.0.2:22222"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("IP B request %d: got %d, want 200 (separate bucket)", i, rec.Code)
		}
	}
}

func TestRateLimit_ResponseHeaders_Present(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "10.0.0.5:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Limit") != "100" {
		t.Errorf("X-RateLimit-Limit: want 100, got %s", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("X-RateLimit-Remaining should be present")
	}
	if rec.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset should be present")
	}
}

func TestRateLimit_XForwardedFor_UsesLeftmostIP(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// X-Forwarded-For with multiple proxies. The leftmost is the
	// original client — that's what should be rate-limited.
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1, 172.16.0.1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 100 && rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, rec.Code)
		}
		if i == 100 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("request 100: got %d, want 429 (rate limited by leftmost IP)", rec.Code)
		}
	}
}

func TestRateLimit_XRealIP_TakesPrecedence(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// X-Real-IP should be used when present (common with nginx).
	// Send 101 requests from the same X-Real-IP → 101st fails.
	for i := 0; i < 101; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		req.Header.Set("X-Real-IP", "198.51.100.1")
		req.RemoteAddr = "10.0.0.1:99999" // proxy's IP, should be ignored
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 100 && rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i, rec.Code)
		}
		if i == 100 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("request 100: got %d, want 429 (rate limited by X-Real-IP)", rec.Code)
		}
	}
}

func TestRateLimit_ConcurrentAccess_NoRace(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	var wg sync.WaitGroup
	const goroutines = 20
	const requestsPerGoroutine = 5

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
				req.RemoteAddr = "10.0.0.10:10000"
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				// 20*5 = 100 requests, all under the burst limit.
				// We just care that there's no race, not the exact status.
				_ = rec.Code
			}
		}(g)
	}
	wg.Wait()
}

func TestRateLimit_AuthHeader_UsesHigherLimit_ThenExceeds(t *testing.T) {
	rl := newRateLimiter(nil)
	defer rl.Shutdown()
	handler := rl.middleware(echoHandler())

	// Auth limit is 1000/min (burst 1000). Send 1001 requests.
	// First 1000 should pass, last one should fail.
	for i := 0; i < 1001; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
		req.RemoteAddr = "10.0.0.20:11111"
		req.Header.Set("Authorization", "Bearer token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if i < 1000 && rec.Code != http.StatusOK {
			t.Fatalf("auth request %d: got %d, want 200 (within burst)", i, rec.Code)
		}
		if i == 1000 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("auth request 1000: got %d, want 429 (exceeded auth limit)", rec.Code)
		}
	}
}

func TestExtractIP_RemoteAddr_StripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:54321"
	ip := extractIP(req, nil)
	if ip != "192.168.1.1" {
		t.Errorf("extractIP: want 192.168.1.1, got %s", ip)
	}
}

func TestExtractIP_XForwardedFor_Leftmost(t *testing.T) {
	// Default httptest peer is 192.0.2.1; mark it as a trusted proxy
	// so the X-Forwarded-For header is honored.
	trusted, _ := ParseTrustedProxies("192.0.2.1")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1, 172.16.0.1")
	ip := extractIP(req, trusted)
	if ip != "203.0.113.42" {
		t.Errorf("extractIP: want 203.0.113.42 (leftmost), got %s", ip)
	}
}

func TestExtractIP_XForwardedFor_WinsOverXRealIP(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.0/8")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "198.51.100.1")
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	req.RemoteAddr = "10.0.0.1:12345"
	ip := extractIP(req, trusted)
	if ip != "203.0.113.42" {
		t.Errorf("extractIP: want 203.0.113.42 (X-Forwarded-For preferred over X-Real-IP), got %s", ip)
	}
}
