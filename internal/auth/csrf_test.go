// Blocco #7.2 follow-up — TestCsrf_NoSessionCookie_SkipsCheck.
//
// Documents the SPRINT 7.2 exemption added to NewCSRF: an
// unsafe-method request that carries NO session cookie is
// passed through to the next handler. Rationale (mirrors the
// top-of-file docstring in csrf.go):
//
//   - The attack vector CSRF defends against is a cross-site
//     request the browser attaches the victim's session cookie
//     to. A no-cookie request has no victim-cookie context for
//     CSRF to protect.
//   - auth.Middleware will reject the unauthenticated request
//     with 401 (no session = no auth). CSRF enforcement is moot
//     — the request is already refused, the cookie state is
//     the canonical source of truth.
//   - Enforcing CSRF here would just turn a 401 into a 403 for
//     a request the server already refuses to process. The
//     narrower defence (no-cookie → pass through) precisely
//     targets the cross-site forgery scenario.
//
// The negative case (session cookie present + missing CSRF →
// 403) is already covered by TestCsrf_MissingCookie_403 in
// pkg/api/csrf_test.go — that test pins the active-CSRF
// enforcement path. This test pins the pass-through path so
// future refactors of NewCSRF cannot silently re-narrow the
// exemption (e.g. "let's enforce CSRF on every unsafe method
// regardless" — a regression that would turn every 401 from
// the auth chain into a 403 with no operator-visible signal).
package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCsrf_NoSessionCookie_SkipsCheck sends an unsafe (POST)
// request with NO SessionCookieName cookie and asserts:
//  1. the next handler IS called (CSRF does NOT short-circuit).
//  2. the response is NOT 403 (CSRF rejection reason).
//
// What this test does NOT cover (intentionally, per the
// thinker's validation):
//   - The negative case (session cookie + missing CSRF) is
//     covered by TestCsrf_MissingCookie_403.
//   - Safe methods (GET/HEAD/OPTIONS) are covered by
//     TestCsrf_GET_NoToken_Passes in pkg/api/csrf_test.go.
//   - Bearer-prefixed requests are covered by
//     TestCsrf_BearerPrefix_SkipsCheck in pkg/api/csrf_test.go.
//
// Keeping the test narrow (one assertion) makes the failure
// mode obvious: a regression that turns the no-cookie path
// into a 403 fails this test loudly with a clear message.
func TestCsrf_NoSessionCookie_SkipsCheck(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cfg := CSRFConfig{
		Secure:   true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
	h := NewCSRF(cfg, next)

	// Unsafe method (POST), no SessionCookieName cookie, no
	// X-CSRF-Token header. CSRF must pass through.
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("CSRF rejected no-cookie unsafe request (SPRINT 7.2 exemption should pass through): body=%s",
			w.Body.String())
	}
	if !called {
		t.Fatal("next handler was not called for no-cookie unsafe request — SPRINT 7.2 exemption broken")
	}
}

// TestCsrf_EmptyCookieValue_403 — belt-and-suspenders (SPRINT 7.2):
// a CSRF cookie whose value is the empty string is rejected with
// a DISTINCT reason code (empty_csrf_cookie_value) so an operator
// scanning logs can tell "the browser never received the cookie"
// (missing_csrf_cookie) from "a third-party middleware stripped
// the cookie value" (empty_csrf_cookie_value). The status is the
// same 403 either way — the distinction is in the body string.
// Without this test, a future refactor that silently re-bundles
// the two branches under "missing_csrf_cookie" would still pass
// TestCsrf_MissingCookie_403 (which tests the cookie-absent case).
func TestCsrf_EmptyCookieValue_403(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cfg := CSRFConfig{
		Secure:   true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	}
	h := NewCSRF(cfg, next)

	// Unsafe method + session cookie (so CSRF check is activated)
	// + CSRF cookie with EMPTY value. Must reject with the
	// canonical empty-value reason code.
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "session-jwt"})
	req.AddCookie(&http.Cookie{Name: CSRFTokenCookieName, Value: ""})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("empty CSRF cookie value: want 403, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "empty_csrf_cookie_value") {
		t.Errorf("body: want distinct reason code %q, got %q",
			"empty_csrf_cookie_value", w.Body.String())
	}
	if called {
		t.Error("next handler must not run when CSRF cookie is empty")
	}
}
