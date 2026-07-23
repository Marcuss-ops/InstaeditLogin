package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandleMetrics_FailClosed_RequiresAuthWhenEnvMissing proves the
// regression fix: /api/v1/metrics must reject requests when the auth
// credentials are not configured, instead of serving metrics publicly.
func TestHandleMetrics_FailClosed_RequiresAuthWhenEnvMissing(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when metrics auth is unconfigured, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Error("expected WWW-Authenticate header on 503 response")
	}
}

// TestHandleMetrics_FailClosed_RequiresAuthWhenOnlyUserSet proves the
// endpoint stays closed if only one of the two auth variables is set.
func TestHandleMetrics_FailClosed_RequiresAuthWhenOnlyUserSet(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithMetricsAuth("admin", ""), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when only user is set, got %d", rec.Code)
	}
}

// TestHandleMetrics_FailClosed_RequiresAuthWhenOnlyPassSet proves the
// endpoint stays closed if only the password variable is set.
func TestHandleMetrics_FailClosed_RequiresAuthWhenOnlyPassSet(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithMetricsAuth("", "secret"), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when only pass is set, got %d", rec.Code)
	}
}

// TestHandleMetrics_RespectsConfiguredBasicAuth proves metrics are
// served when valid credentials are supplied.
func TestHandleMetrics_RespectsConfiguredBasicAuth(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithMetricsAuth("admin", "secret"), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.SetBasicAuth("admin", "secret")
	r.handleMetrics(rec, req)

	// Metrics handler returns text/plain metrics; status may be 200.
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for valid basic auth, got %d", rec.Code)
	}
}

// TestHandleMetrics_RejectsInvalidBasicAuth proves that supplying the
// wrong password when credentials are configured returns 401.
func TestHandleMetrics_RejectsInvalidBasicAuth(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithMetricsAuth("admin", "secret"), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	req.SetBasicAuth("admin", "wrong")
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for invalid basic auth, got %d", rec.Code)
	}
}

// TestTrustedClientIP_UntrustedProxy_IgnoresForwardedHeaders proves
// that X-Forwarded-For / X-Real-IP are ignored when the peer is not in
// the trusted proxy list.
func TestTrustedClientIP_UntrustedProxy_IgnoresForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	req.Header.Set("X-Real-IP", "198.51.100.1")
	req.RemoteAddr = "192.0.2.1:12345"

	ip := trustedClientIP(req, nil)
	if ip != "192.0.2.1" {
		t.Fatalf("want direct peer when no trusted proxies, got %s", ip)
	}
}

// TestTrustedClientIP_UntrustedProxy_SpoofedHeaderDropped proves that a
// client directly connected to the API cannot spoof its IP via
// X-Forwarded-For when the peer is not a trusted proxy.
func TestTrustedClientIP_UntrustedProxy_SpoofedHeaderDropped(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.0/8")

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.42")
	req.RemoteAddr = "192.0.2.1:12345"

	ip := trustedClientIP(req, trusted)
	if ip != "192.0.2.1" {
		t.Fatalf("want direct peer when peer is untrusted, got %s", ip)
	}
}

// TestTrustedClientIP_TrustedProxy_HonorsForwardedHeaders proves that
// X-Forwarded-For is honored when the peer is in the trusted list.
func TestTrustedClientIP_TrustedProxy_HonorsForwardedHeaders(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")
	req.RemoteAddr = "10.0.0.1:12345"

	ip := trustedClientIP(req, trusted)
	if ip != "203.0.113.42" {
		t.Fatalf("want leftmost X-Forwarded-For from trusted proxy, got %s", ip)
	}
}

// TestTrustedClientIP_TrustedProxy_FallsBackToXRealIP proves the
// fallback to X-Real-IP when X-Forwarded-For is absent.
func TestTrustedClientIP_TrustedProxy_FallsBackToXRealIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("192.168.0.0/16")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "203.0.113.99")
	req.RemoteAddr = "192.168.1.1:12345"

	ip := trustedClientIP(req, trusted)
	if ip != "203.0.113.99" {
		t.Fatalf("want X-Real-IP from trusted proxy, got %s", ip)
	}
}

// TestParseTrustedProxies_MixedIPsAndCIDRs verifies parsing of both
// single IPs and CIDR ranges, including trimming whitespace.
func TestParseTrustedProxies_MixedIPsAndCIDRs(t *testing.T) {
	trusted, err := ParseTrustedProxies(" 10.0.0.0/8 , 127.0.0.1 ")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(trusted) != 2 {
		t.Fatalf("want 2 networks, got %d", len(trusted))
	}
}

// TestParseTrustedProxies_InvalidInput_ReturnsError verifies that an
// invalid entry is rejected at startup.
func TestParseTrustedProxies_InvalidInput_ReturnsError(t *testing.T) {
	_, err := ParseTrustedProxies("not-an-ip")
	if err == nil {
		t.Fatal("expected error for invalid trusted proxy")
	}
}

// TestIsTLSRequest_DirectTLS_AlwaysTrue proves that a request arriving
// over a real TLS connection is reported as TLS even if forwarded
// headers or the absence of trusted proxies would suggest otherwise.
func TestIsTLSRequest_DirectTLS_AlwaysTrue(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	req.TLS = &tls.ConnectionState{}

	if !r.isTLSRequest(req) {
		t.Fatal("want true for direct TLS connection")
	}
}

// TestIsTLSRequest_UntrustedProxy_IgnoresForwardedProto proves a
// direct client cannot force HSTS by sending X-Forwarded-Proto.
func TestIsTLSRequest_UntrustedProxy_IgnoresForwardedProto(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https")

	if r.isTLSRequest(req) {
		t.Fatal("want false when untrusted peer sends X-Forwarded-Proto=https")
	}
}

// TestIsTLSRequest_TrustedProxy_HonorsForwardedProto proves that an
// upstream proxy in the trusted list can report HTTPS via the
// X-Forwarded-Proto header.
func TestIsTLSRequest_TrustedProxy_HonorsForwardedProto(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}
	r := MustNewRouter(nil, nil, nil, "", nil, WithTrustedProxies(trusted), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https")

	if !r.isTLSRequest(req) {
		t.Fatal("want true when trusted proxy sends X-Forwarded-Proto=https")
	}
}

// TestIsTLSRequest_TrustedProxy_HonorsForwardedSsl proves the legacy
// X-Forwarded-Ssl header is honored from a trusted proxy.
func TestIsTLSRequest_TrustedProxy_HonorsForwardedSsl(t *testing.T) {
	trusted, err := ParseTrustedProxies("192.168.0.0/16")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}
	r := MustNewRouter(nil, nil, nil, "", nil, WithTrustedProxies(trusted), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-Forwarded-Ssl", "on")

	if !r.isTLSRequest(req) {
		t.Fatal("want true when trusted proxy sends X-Forwarded-Ssl=on")
	}
}

// TestIsTLSRequest_TrustedProxy_ForwardedHttp_ReturnsFalse proves the
// header is interpreted literally: an explicit http value from a
// trusted proxy keeps the request non-TLS.
func TestIsTLSRequest_TrustedProxy_ForwardedHttp_ReturnsFalse(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}
	r := MustNewRouter(nil, nil, nil, "", nil, WithTrustedProxies(trusted), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-Proto", "http")

	if r.isTLSRequest(req) {
		t.Fatal("want false when trusted proxy sends X-Forwarded-Proto=http")
	}
}

// TestIsTLSRequest_TrustedProxy_MultipleValues proves that when the
// header contains a comma-separated list, the first (leftmost) value is
// used, matching common proxy behavior.
func TestIsTLSRequest_TrustedProxy_MultipleValues(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}
	r := MustNewRouter(nil, nil, nil, "", nil, WithTrustedProxies(trusted), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https, http")

	if !r.isTLSRequest(req) {
		t.Fatal("want true for leftmost https value in X-Forwarded-Proto")
	}
}

// TestIsTLSRequest_UntrustedProxy_ForwardedSslIgnored proves a direct
// client cannot force HSTS via X-Forwarded-Ssl either.
func TestIsTLSRequest_UntrustedProxy_ForwardedSslIgnored(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	req.Header.Set("X-Forwarded-Ssl", "on")

	if r.isTLSRequest(req) {
		t.Fatal("want false when untrusted peer sends X-Forwarded-Ssl=on")
	}
}

// TestSecurityHeadersMiddleware_HSTS_TrustedProxyHTTPS proves the full
// middleware chain emits Strict-Transport-Security when a trusted proxy
// reports HTTPS via X-Forwarded-Proto.
func TestSecurityHeadersMiddleware_HSTS_TrustedProxyHTTPS(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatalf("parse trusted proxies: %v", err)
	}
	r := MustNewRouter(nil, nil, nil, "", nil, WithTrustedProxies(trusted), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()

	handler := r.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=63072000; includeSubDomains" {
		t.Fatalf("want HSTS header for trusted HTTPS request, got %q", got)
	}
}

// TestSecurityHeadersMiddleware_NoHSTS_UntrustedProxyHTTPS proves the
// middleware does NOT emit Strict-Transport-Security when an untrusted
// peer tries to spoof HTTPS via X-Forwarded-Proto.
func TestSecurityHeadersMiddleware_NoHSTS_UntrustedProxyHTTPS(t *testing.T) {
	r := MustNewRouter(nil, nil, nil, "", nil, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
	defer r.rateLimiter.Shutdown()

	handler := r.securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:12345"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("did not expect Strict-Transport-Security header from untrusted peer, got %s", got)
	}
}
