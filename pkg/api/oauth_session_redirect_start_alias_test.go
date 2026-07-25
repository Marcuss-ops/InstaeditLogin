// Package api — backwards-compat /start alias test matrix.
//
// Blocco 1.2's exhaustive test matrix in oauth_session_redirect_test.go
// covers /api/v1/auth/{provider}/login and /callback. Some external
// scripts and older docs reference /api/v1/auth/{provider}/start as the
// OAuth initiation URL. This file mirrors the no-session, Bearer, cookie,
// state-cookie hygiene and positive-session invariants for the /start
// alias to prove the parallel-mount wiring produces identical behavior:
// the FRONTEND/login?next=%2Fconnections%2F{provider} redirect shape,
// the no-state-cookie-when-no-session invariant, and the positive
// session-cookie-authenticates path.
//
// If /start and /login ever diverge, these tests fail loudly.

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// TestOAuthSessionRedirect_StartAlias_NoSession_AllSupportedProviders_Redirects
// mirrors Group A in oauth_session_redirect_test.go for the /start alias.
// Proves the no-session → 302 contract applies symmetrically across
// instagram/twitter/tiktok/linkedin/facebook and the redirect's next-path
// is correctly bound to the requested provider. Defence-in-depth: a
// no-session probe cannot enumerate the supported platform roster from
// /start either — every path produces the same 302 shape.
func TestOAuthSessionRedirect_StartAlias_NoSession_AllSupportedProviders_Redirects(t *testing.T) {
	providers := []string{"instagram", "twitter", "tiktok", "linkedin", "facebook"}
	for _, p := range providers {
		p := p
		t.Run(p, func(t *testing.T) {
			r := newOAuthSessionRedirectRouter(t, []string{p}, "https://app.example.com")
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/"+p+"/start", nil)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)
			assertNoSessionRedirect(t, w, "https://app.example.com", p)
		})
	}
}

// TestOAuthSessionRedirect_StartAlias_NoSession_BearerJwt_WrongSecret_Redirects
// proves a JWT signed with the WRONG secret on /start is rejected by
// Manager.Verify → extractSessionIdentity returns nil → middleware 302s
// to /login. Symmetric to the /login Bearer variant.
func TestOAuthSessionRedirect_StartAlias_NoSession_BearerJwt_WrongSecret_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/start", nil)
	req.Header.Set("Authorization", "Bearer "+issueJWTWithSecret(t, "wrong-secret-must-be-long-enough-for-hs256", 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_StartAlias_NoSession_SessionCookie_WrongSecret_Redirects
// proves a session cookie JWT signed with the wrong secret on /start is
// rejected → 302. Symmetric to the /login cookie variant.
func TestOAuthSessionRedirect_StartAlias_NoSession_SessionCookie_WrongSecret_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/start", nil)
	req.AddCookie(&http.Cookie{
		Name:  auth.SessionCookieName,
		Value: issueJWTWithSecret(t, "wrong-secret-must-be-long-enough-for-hs256", 1),
	})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_StartAlias_NoSession_StateCookieNotIssued
// mirrors Group D's state-cookie hygiene invariant for the /start alias.
// Proves defence-in-depth: when /start is rejected for missing session,
// the oauth_state_{provider} cookie MUST NOT be issued. Otherwise an
// attacker could probe the platform roster via /start without auth AND
// still receive a usable state cookie to complete an intercepted flow.
func TestOAuthSessionRedirect_StartAlias_NoSession_StateCookieNotIssued(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/start", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("setup: want 302, got %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") && c.MaxAge > 0 {
			t.Errorf("oauth_state_instagram cookie was issued on /start despite missing session — state-cookie hygiene invariant must hold for the alias too: %+v", c)
		}
	}
}

// TestOAuthSessionRedirect_StartAlias_SessionCookie_Authenticates is
// the positive companion: proves the cookie auth path on /start WORKS
// when the session is valid (the middleware lets the inner handleLogin
// handler drive the OAuth redirect to the provider). Without this, the
// negative tests would be vacuous — a reader couldn't tell whether /start
// ever reaches the inner handler.
func TestOAuthSessionRedirect_StartAlias_SessionCookie_Authenticates(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/start", nil)
	jwt := issueTestJWT(t, 1)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("valid session cookie + /start: want 302 (redirects to provider), got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.example.com/oauth?state=") {
		t.Fatalf("expected redirect to provider's auth dialog via /start alias, got %s", loc)
	}
}
