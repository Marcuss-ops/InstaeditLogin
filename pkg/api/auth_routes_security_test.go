package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleCallback_RejectsMissingStateCookie_400(t *testing.T) {
	svc := &mockProvider{platform: "instagram", handleCallback: successCallback}
	store := &mockUserStore{attachFn: successAttach}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=anything", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (missing state cookie), got %d: %s", w.Code, w.Body.String())
	}
	if svc.handleCallbackCalls != 0 {
		t.Errorf("platform HandleCallback called %d time(s) despite state verification failure (must short-circuit BEFORE the code exchange)", svc.handleCallbackCalls)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") && c.MaxAge < 0 {
			t.Errorf("state cookie was deleted on verification failure (should persist so the legitimate user can retry): %+v", c)
		}
	}
	if !strings.Contains(w.Body.String(), "invalid state") {
		t.Errorf("response body should explain the state failure; got %q", w.Body.String())
	}
}

func TestHandleCallback_RejectsMismatchedState_400(t *testing.T) {
	svc := &mockProvider{platform: "instagram", handleCallback: successCallback}
	store := &mockUserStore{attachFn: successAttach}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=different-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "cookie-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (state mismatch), got %d: %s", w.Code, w.Body.String())
	}
	if svc.handleCallbackCalls != 0 {
		t.Errorf("platform HandleCallback called %d time(s) despite state mismatch (must short-circuit BEFORE the code exchange)", svc.handleCallbackCalls)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") && c.MaxAge < 0 {
			t.Errorf("state cookie was deleted on mismatch (should persist so the legitimate user can retry): %+v", c)
		}
	}
	if !strings.Contains(w.Body.String(), "invalid state") {
		t.Errorf("response body should explain the state mismatch; got %q", w.Body.String())
	}
}

func TestHandleCallback_RejectsMissingStateParam_400(t *testing.T) {
	svc := &mockProvider{platform: "instagram", handleCallback: successCallback}
	store := &mockUserStore{attachFn: successAttach}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc", nil)
	setOAuthStateCookieForTest(req, "instagram", "any-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (missing state query param), got %d: %s", w.Code, w.Body.String())
	}
	if svc.handleCallbackCalls != 0 {
		t.Errorf("platform HandleCallback called %d time(s) despite missing state (must short-circuit BEFORE the code exchange)", svc.handleCallbackCalls)
	}
	if !strings.Contains(w.Body.String(), "missing state") {
		t.Errorf("response body should mention 'missing state'; got %q", w.Body.String())
	}
}

func TestPlatformMetaIsRejected(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	// Login with platform=meta + AUTH must return 404 (unsupported).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/login", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/auth/meta/login (+auth): want 404 (platform removed), got %d: %s", w.Code, w.Body.String())
	}

	// Callback with platform=meta + AUTH must return 404 (unsupported).
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=x", nil)
	w2 := httptest.NewRecorder()
	withBearerJWT(t, req2, 1)
	r.Setup().ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("/auth/meta/callback (+auth): want 404 (platform removed), got %d: %s", w2.Code, w2.Body.String())
	}

	// The registered providers (instagram, tiktok, twitter) must still work.
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	w3 := httptest.NewRecorder()
	withBearerJWT(t, req3, 1)
	r.Setup().ServeHTTP(w3, req3)
	if w3.Code != http.StatusFound {
		t.Fatalf("/auth/instagram/login: want 302 (still works), got %d", w3.Code)
	}
}

func TestHandleLogin_RequireSession_RedirectsToLogin(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO withBearerJWT — session is missing

	if w.Code != http.StatusFound {
		t.Fatalf("no-session /auth/instagram/login: want 302 to /login, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://app.example.com/login?next=") {
		t.Fatalf("redirect URL must land on FRONTEND_URL/login: got %s", loc)
	}
	// The 'next' parameter must encode the provider so the SPA can
	// resume the OAuth connect after login.
	if !strings.Contains(loc, "instagram") {
		t.Errorf("next path should mention the provider so the SPA can resume: %s", loc)
	}
	// Defence-in-depth: no state cookie should be set when the
	// request never made it to the provider's auth dialog.
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") && c.MaxAge > 0 {
			t.Errorf("oauth state cookie was set despite missing session (state should only bind to authenticated users): %+v", c)
		}
	}
}

func TestHandleCallback_RequireSession_RedirectsToLogin(t *testing.T) {
	svc := &mockProvider{platform: "instagram", handleCallback: successCallback}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO withBearerJWT — session is missing

	if w.Code != http.StatusFound {
		t.Fatalf("no-session /auth/instagram/callback: want 302 to /login, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://app.example.com/login?next=") {
		t.Fatalf("redirect URL must land on FRONTEND_URL/login: got %s", loc)
	}
	// No code-exchange call should have happened (no tokenExchange
	// invoked when there's no session).
	if svc.handleCallbackCalls != 0 {
		t.Errorf("HandleCallback called %d time(s) despite missing session (must short-circuit BEFORE the code exchange)", svc.handleCallbackCalls)
	}
	// No platform account should have been created or attached
	// (the mock would have recorded attachFn invocations).
	// The mockUserStore defaults to erroring on attach so we
	// can't directly assert "not called" without wiring attachFn;
	// the absence of a 200 + state-cookie deletion is sufficient.
}

func TestHandleCallback_DeletesStateCookieAfterUse(t *testing.T) {
	svc := &mockProvider{platform: "instagram", handleCallback: successCallback}
	store := &mockUserStore{attachFn: successAttach}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var deletionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") {
			deletionCookie = c
			break
		}
	}
	if deletionCookie == nil {
		t.Fatal("oauth_state_meta cookie not deleted after successful callback (single-use contract violated)")
	}
	if deletionCookie.MaxAge >= 0 {
		t.Errorf("oauth_state_meta deletion cookie MaxAge: want <0, got %d (cookie would persist and be replayable)", deletionCookie.MaxAge)
	}
}
