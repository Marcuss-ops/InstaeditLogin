package services

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

func TestGoogleDriveOAuthService_Name(t *testing.T) {
	svc, err := NewGoogleDriveOAuthService(&config.Config{
		Auth: config.AuthConfig{
			GoogleDriveClientID:     "client-id",
			GoogleDriveClientSecret: "client-secret-01234567890123456789012345678901",
			GoogleDriveRedirectURI:  "http://localhost/callback",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc == nil {
		t.Fatal("expected service to be non-nil")
	}
	if got := svc.Name(); got != "google-drive" {
		t.Fatalf("expected name google-drive, got %s", got)
	}
}

func TestGoogleDriveOAuthService_DisabledWhenNoClientID(t *testing.T) {
	svc, err := NewGoogleDriveOAuthService(&config.Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc != nil {
		t.Fatal("expected service to be nil when client id is empty")
	}
}

func TestGoogleDriveOAuthService_GetLoginURL(t *testing.T) {
	svc, _ := NewGoogleDriveOAuthService(&config.Config{
		Auth: config.AuthConfig{
			GoogleDriveClientID:     "client-id",
			GoogleDriveClientSecret: "client-secret-01234567890123456789012345678901",
			GoogleDriveRedirectURI:  "http://localhost/callback",
		},
	})
	url := svc.GetLoginURL("my-state")
	if !strings.Contains(url, "accounts.google.com/o/oauth2/v2/auth") {
		t.Fatalf("expected google oauth host, got %s", url)
	}
	if !strings.Contains(url, "scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fdrive.readonly") {
		t.Fatalf("expected drive.readonly scope, got %s", url)
	}
	if !strings.Contains(url, "state=my-state") {
		t.Fatalf("expected state parameter, got %s", url)
	}
}

// ---- Task 3/10 tokeninfo contract tests -----------------------------------
//
// Acceptance: VerifyDriveTokenIsReadonly succeeds ONLY when the Google
// tokeninfo scope claim includes the canonical drive.readonly scope
// (space-delimited equality match, NOT substring); drive.file alone is
// rejected with the typed ErrDriveTokenScopeMismatch sentinel; non-200
// HTTP, malformed JSON, and empty access tokens are also rejected with
// typed errors.
//
// The helper fakeTokenInfoServer returns a status + scope JSON. Tests
// flip the scenario by tweaking `scope` / `status` and assert the
// service's behaviour matches the Task 3/10 contract.

func fakeTokenInfoServer(t *testing.T, status int, scope string, responseJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"invalid_token","error_description":"simulated"}`))
			return
		}
		if responseJSON != "" {
			_, _ = w.Write([]byte(responseJSON))
			return
		}
		_, _ = fmt.Fprintf(w, `{"aud":"client-abc","scope":%q,"expires_in":3599}`, scope)
	}))
}

func newDriveServiceForTokenInfo(t *testing.T, tokenInfoURL string) *GoogleDriveOAuthService {
	t.Helper()
	return &GoogleDriveOAuthService{
		cfg: &config.Config{
			Auth: config.AuthConfig{
				GoogleDriveClientID:     "client-id",
				GoogleDriveClientSecret: "client-secret-01234567890123456789012345678901",
				GoogleDriveRedirectURI:  "http://localhost/callback",
			},
		},
		httpClient:   &http.Client{},
		tokenInfoURL: tokenInfoURL,
	}
}

// TestVerifyDriveTokenIsReadonly_Success covers the happy path: token
// claim contains the canonical drive.readonly (alongside userinfo.profile
// — the canonical production scope combo). VerifyDriveTokenIsReadonly
// MUST return nil.
func TestVerifyDriveTokenIsReadonly_Success(t *testing.T) {
	srv := fakeTokenInfoServer(t, http.StatusOK, "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/userinfo.profile", "")
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	if err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer"); err != nil {
		t.Fatalf("VerifyDriveTokenIsReadonly must accept drive.readonly in scope; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_AcceptsScopesInAnyOrder locks the
// splitting-invariant the verifier relies on (Task 3/10 L1 — review
// feedback). Google documents scope claims as space-delimited sets
// where the ORDER of scopes carries no semantic meaning. A tokeninfo
// response with drive.readonly placed last (or middle) MUST pass just
// like the canonical "drive.readonly + userinfo.profile" ordering.
// Without this test a future refactor that hard-codes a
// "drive.readonly must be first" assumption would silently regress
// for any user with reordered scopes.
func TestVerifyDriveTokenIsReadonly_AcceptsScopesInAnyOrder(t *testing.T) {
	srv := fakeTokenInfoServer(t, http.StatusOK,
		"openid email profile https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/userinfo.profile",
		"")
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	if err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer-reordered"); err != nil {
		t.Fatalf("VerifyDriveTokenIsReadonly must accept drive.readonly in any position; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_RejectsDriveFile is the regression
// test for Task 3/10: a token whose scope claim is the legacy
// drive.file (the wrong scope the docs were advertising) MUST be
// rejected with the typed ErrDriveTokenScopeMismatch sentinel so
// callers can errors.Is it. Acceptance bar — if a future refactor
// widens the scope check (e.g. falls back to substring matching or
// accepts drive.file), this test fails.
func TestVerifyDriveTokenIsReadonly_RejectsDriveFile(t *testing.T) {
	srv := fakeTokenInfoServer(t, http.StatusOK, "https://www.googleapis.com/auth/drive.file", "")
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer-with-wrong-scope")
	if err == nil {
		t.Fatal("VerifyDriveTokenIsReadonly must REJECT a token whose scope claim is drive.file (not drive.readonly)")
	}
	if !errors.Is(err, ErrDriveTokenScopeMismatch) {
		t.Errorf("error must wrap ErrDriveTokenScopeMismatch for caller triage; got: %v", err)
	}
	if !strings.Contains(err.Error(), "drive.file") {
		t.Errorf("error must mention the actual scope claim so the operator sees what was wrong; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_RejectsDriveWrite covers Task 3/10's
// verifier tightening: the canonical scope claim must be EXACTLY
// drive.readonly. A token whose scope claim is `drive` (write = full
// Drive access) is REJECTED by the Importer surface, because granting
// write here would (a) expose every file in the operator's Drive (vs
// drive.readonly which is enumeration-only), (b) trigger a deeper
// restricted-scope audit at Google, (c) be useless for the folder
// crawler (the importer never writes back). If a future regression
// re-broadens the verifier to accept drive.write (e.g. by accident
// when the Task 9/10 Exporter surface introduces the cross-package
// const), this test fails. The Exporter surface (GoogleDriveDestination)
// keeps its own OAuth client + its own verifier that DOES accept
// drive.write — layering is intentional so the two surfaces cannot
// poison each other's token claims.
func TestVerifyDriveTokenIsReadonly_RejectsDriveWrite(t *testing.T) {
	srv := fakeTokenInfoServer(t, http.StatusOK, "https://www.googleapis.com/auth/drive", "")
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer-with-full-drive-scope")
	if err == nil {
		t.Fatal("VerifyDriveTokenIsReadonly must REJECT a token whose scope claim is drive (write) — the Importer surface is enumeration-only")
	}
	if !errors.Is(err, ErrDriveTokenScopeMismatch) {
		t.Errorf("error must wrap ErrDriveTokenScopeMismatch for caller triage; got: %v", err)
	}
	if !strings.Contains(err.Error(), "https://www.googleapis.com/auth/drive.readonly") {
		t.Errorf("error must name the canonical required scope (drive.readonly) so the operator knows what to re-grant; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_ConstFallbackToProductionURL covers
// the production code path that EVERY other test in this file
// bypasses: when s.tokenInfoURL is empty (its zero value, i.e. the
// production default), VerifyDriveTokenIsReadonly falls back to the
// driveTokenInfoURL const ("https://oauth2.googleapis.com/v3/tokeninfo").
// Without this test, a future refactor that accidentally deletes the
// `if target == ""` branch would pass CI silently (every other test
// sets tokenInfoURL = test-server-URL explicitly). The mock handler
// mounts at /v3/tokeninfo because that's what the const resolves to
// post-tripper-rewriting.
func TestVerifyDriveTokenIsReadonly_ConstFallbackToProductionURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
		// Locks the verifier's HTTP wire contract:
		//   (1) GET method (Google's tokeninfo endpoint is documented as GET)
		//   (2) ?access_token=… query (NOT Authorization header)
		// mux.HandleFunc accepts all methods by default, so a regression
		// that swaps to POST would not be caught without the GET check.
		if r.Method != http.MethodGet {
			t.Errorf("tokeninfo verifier must use GET (Google documents tokeninfo as GET); got: %s", r.Method)
		}
		if got := r.URL.Query().Get("access_token"); got != "fake-bearer" {
			t.Errorf("tokeninfo verifier must send access_token as ?access_token=... query (not Authorization header); got: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"aud":"client-abc","scope":%q,"expires_in":3599}`,
			canonicalDriveReadonlyScope+" "+userinfoProfileScope)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	svc := &GoogleDriveOAuthService{
		cfg: &config.Config{
			Auth: config.AuthConfig{
				GoogleDriveClientID:     "client-id",
				GoogleDriveClientSecret: "secret-01234567890123456789012345678901",
				GoogleDriveRedirectURI:  "http://localhost/callback",
			},
		},
		httpClient: &http.Client{Transport: &rewriteRoundTripper{inner: http.DefaultTransport, base: base}},
		// tokenInfoURL INTENTIONALLY zero-valued — this is the
		// production default. The verifier's `if target == ""`
		// fallback to driveTokenInfoURL is what we're verifying.
	}
	if err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer"); err != nil {
		t.Fatalf("verify via const fallback must accept drive.readonly; got: %v", err)
	}
}
func TestVerifyDriveTokenIsReadonly_RejectsFullDrive(t *testing.T) {
	srv := fakeTokenInfoServer(t, http.StatusOK, "https://www.googleapis.com/auth/drive", "")
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer-full-drive")
	if err == nil {
		t.Fatal("VerifyDriveTokenIsReadonly must REJECT a token whose scope is the unrestricted drive scope")
	}
	if !errors.Is(err, ErrDriveTokenScopeMismatch) {
		t.Errorf("error must wrap ErrDriveTokenScopeMismatch for caller triage; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_RejectsEmptyAccessToken is a
// pre-flight guard: an empty access token never reaches Google and
// must surface as the typed ErrDriveTokenEmpty sentinel so callers
// can render a "session expired; reconnect" message instead of an
// indeterminate "network failed".
func TestVerifyDriveTokenIsReadonly_RejectsEmptyAccessToken(t *testing.T) {
	svc := newDriveServiceForTokenInfo(t, "")
	err := svc.VerifyDriveTokenIsReadonly(t.Context(), "")
	if err == nil {
		t.Fatal("VerifyDriveTokenIsReadonly must REJECT empty access token")
	}
	if !errors.Is(err, ErrDriveTokenEmpty) {
		t.Errorf("error must wrap ErrDriveTokenEmpty for caller triage; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_RejectsFailedHTTPStatus covers the
// 4xx/5xx path: tokeninfo returns 401 invalid_token when the access
// token has expired or been revoked. The verifier must surface a
// descriptive error mentioning the HTTP status, NOT silently pass
// (a 401 with empty body should NOT be confused with success).
func TestVerifyDriveTokenIsReadonly_RejectsFailedHTTPStatus(t *testing.T) {
	srv := fakeTokenInfoServer(t, http.StatusUnauthorized, "", "")
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	err := svc.VerifyDriveTokenIsReadonly(t.Context(), "expired-bearer")
	if err == nil {
		t.Fatal("VerifyDriveTokenIsReadonly must REJECT when tokeninfo returns non-200")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error must include the HTTP status for ops triage; got: %v", err)
	}
}

// TestVerifyDriveTokenIsReadonly_RejectsMalformedJSON covers the
// pathological path: tokeninfo occasionally returns HTML error pages
// or empty bodies during Google outages. The verifier must surface
// a parse error, not silently pass.
func TestVerifyDriveTokenIsReadonly_RejectsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>503 Service Unavailable</body></html>"))
	}))
	defer srv.Close()
	svc := newDriveServiceForTokenInfo(t, srv.URL)
	err := svc.VerifyDriveTokenIsReadonly(t.Context(), "fake-bearer")
	if err == nil {
		t.Fatal("VerifyDriveTokenIsReadonly must REJECT malformed JSON even on HTTP 200")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error must mention parse failure for ops triage; got: %v", err)
	}
}

// ---- Task 3/10 H1 HandleCallback integration tests -----------------------
//
// These tests prove the production wiring: HandleCallback calls
// VerifyDriveTokenIsReadonly against the freshly-exchanged access
// token and BLOCKS the OAuth flow when the tokeninfo scope claim
// doesn't include drive.readonly. A wrong-scope token otherwise
// would only fail at the first Drive.list / Drive.files.get call —
// invisible to the operator until they trigger an import.
//
// The test infra uses a rewriteRoundTripper that re-points every
// outbound request (https://oauth2.googleapis.com/token, the
// tokeninfo URL, https://www.googleapis.com/oauth2/v2/userinfo)
// at a single httptest.Server, so we can assert both
// exchangeCodeForToken + VerifyDriveTokenIsReadonly + getUserInfo
// behaviour in one flow.

// rewriteRoundTripper sends every outbound request through a single
// host:port. It's how the integration tests below mock the three
// Google OAuth endpoints (token, tokeninfo, userinfo) in a single
// httptest.Server without refactoring the production code to read
// base URLs from a config field. net/http.Client.Transport is the
// documented hook for request rewriting; it's stable across Go
// versions.
type rewriteRoundTripper struct {
	inner http.RoundTripper
	base  *url.URL
}

func (r *rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone so we don't mutate the caller's request (req is the
	// canonical object net/http reuses across retries/redirects).
	testReq := req.Clone(req.Context())
	testReq.URL.Scheme = r.base.Scheme
	testReq.URL.Host = r.base.Host
	return r.inner.RoundTrip(testReq)
}

// newHandleCallbackTest sets up a service whose exchangeCodeForToken /
// getUserInfo calls are routed to an httptest.Server mock via
// rewriteRoundTripper, plus a controllable scope-on-tokeninfo so the
// test can flip the verifier's outcome. tokeninfoStatus MUST be
// explicit (no zero-value default) so every caller documents the
// status code it expects from tokeninfo — the MED review feedback
// on parameterized mocks prefers explicit-over-implicit. The
// transient test below uses this knob to pass through a
// non-200 response without duplicating ~30 lines of mux setup.
// Returns the service + the server (caller defers Close).
func newHandleCallbackTest(t *testing.T, scopeOnTokeninfo string, tokeninfoStatus int) (*GoogleDriveOAuthService, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Use the canonical scope set so exchangeCodeForToken's
		// response reflects what a real Google exchange returns
		// for a drive.readonly + userinfo.profile consent.
		_, _ = fmt.Fprintf(w,
			`{"access_token":"fake-access-abc","refresh_token":"fake-refresh-xyz","expires_in":3600,"scope":"%s %s"}`,
			canonicalDriveReadonlyScope, userinfoProfileScope)
	})
	mux.HandleFunc("/tokeninfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tokeninfoStatus != http.StatusOK {
			w.WriteHeader(tokeninfoStatus)
			_, _ = w.Write([]byte(`{"error":"backend_error","error_description":"simulated outage"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"aud":"client-abc","scope":%q,"expires_in":3599}`, scopeOnTokeninfo)
	})
	mux.HandleFunc("/oauth2/v2/userinfo", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"google-user-123","name":"Test Operator","email":"op@example.test"}`))
	})
	srv := httptest.NewServer(mux)
	base, _ := url.Parse(srv.URL)
	return &GoogleDriveOAuthService{
		cfg: &config.Config{
			Auth: config.AuthConfig{
				GoogleDriveClientID:     "client-id",
				GoogleDriveClientSecret: "secret-01234567890123456789012345678901",
				GoogleDriveRedirectURI:  "http://localhost/callback",
			},
		},
		httpClient:   &http.Client{Transport: &rewriteRoundTripper{inner: http.DefaultTransport, base: base}},
		tokenInfoURL: srv.URL + "/tokeninfo",
	}, srv
}

// TestHandleCallback_RejectsWrongScope is the H1 acceptance bar.
// Drive's tokeninfo says the issued token has the legacy drive.file
// scope (NOT the canonical drive.readonly) — HandleCallback MUST
// surface the failure so the caller knows the OAuth flow produced
// a non-functional token.
func TestHandleCallback_RejectsWrongScope(t *testing.T) {
	svc, srv := newHandleCallbackTest(t, "https://www.googleapis.com/auth/drive.file", http.StatusOK)
	defer srv.Close()
	profile, token, err := svc.HandleCallback(t.Context(), "state", "auth-code")
	if err == nil {
		t.Fatal("HandleCallback must REJECT a token whose scope claim is drive.file (not drive.readonly)")
	}
	if profile != nil {
		t.Errorf("profile must be nil on scope rejection; got: %+v", profile)
	}
	if token != nil {
		t.Errorf("token must be nil on scope rejection; got: %+v", token)
	}
	if !errors.Is(err, ErrDriveTokenScopeMismatch) {
		t.Errorf("HandleCallback error must wrap ErrDriveTokenScopeMismatch; got: %v", err)
	}
	if !strings.Contains(err.Error(), "oauth callback scope check") {
		t.Errorf("HandleCallback error must mention 'oauth callback scope check' for ops triage; got: %v", err)
	}
}

// TestHandleCallback_AcceptsCorrectScope is the H1 happy path.
// When the issued token's scope claim contains the canonical
// drive.readonly, HandleCallback must complete successfully and
// return both the profile + token. This proves the wiring didn't
// accidentally reject valid tokens.
func TestHandleCallback_AcceptsCorrectScope(t *testing.T) {
	svc, srv := newHandleCallbackTest(t, canonicalDriveReadonlyScope+" "+userinfoProfileScope, http.StatusOK)
	defer srv.Close()
	profile, token, err := svc.HandleCallback(t.Context(), "state", "auth-code")
	if err != nil {
		t.Fatalf("HandleCallback must accept a token whose scope includes drive.readonly; got: %v", err)
	}
	if profile == nil {
		t.Fatal("profile must be non-nil on success")
	}
	if token == nil {
		t.Fatal("token must be non-nil on success")
	}
	if profile.PlatformUserID != "google-user-123" {
		t.Errorf("profile.PlatformUserID = %q; want google-user-123", profile.PlatformUserID)
	}
	if token.AccessToken != "fake-access-abc" {
		t.Errorf("token.AccessToken = %q; want fake-access-abc", token.AccessToken)
	}
}

// TestHandleCallback_TransientTokeninfoFailureDoesNotBlock covers
// the WARN+PROCEED policy split: a transient tokeninfo failure
// (here simulated by a 500 response, which the verifier rejects as
// "drive tokeninfo failed (status 500)") must NOT abort the OAuth
// flow. The mock still returns the right /userinfo profile so the
// callback can complete; the operator only sees a WARN log line.
// Uses the parameterized newHandleCallbackTest (tokeninfoStatus=500)
// instead of a parallel mock — the MED feedback from review.
func TestHandleCallback_TransientTokeninfoFailureDoesNotBlock(t *testing.T) {
	svc, srv := newHandleCallbackTest(t, canonicalDriveReadonlyScope, http.StatusInternalServerError)
	defer srv.Close()
	profile, token, err := svc.HandleCallback(t.Context(), "state", "auth-code")
	if err != nil {
		t.Fatalf("HandleCallback must NOT block on transient tokeninfo failure (per H1 WARN+PROCEED policy); got: %v", err)
	}
	if profile == nil || token == nil || token.AccessToken != "fake-access-abc" {
		t.Fatalf("HandleCallback must complete despite transient tokeninfo failure; got profile=%v token=%v", profile, token)
	}
}
