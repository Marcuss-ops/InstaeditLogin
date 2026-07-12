// Package api — Blocco #1.2: exhaustive e2e coverage for
// oauthSessionRedirect, the middleware that gates
// /api/v1/auth/{provider}/{login,callback} behind an InstaEdit
// session.
//
// SPRINT 7.1 (P0#14, commit 7ae58c8) shipped the middleware and
// added the two foundational tests:
//   - TestHandleLogin_RequireSession_RedirectsToLogin
//   - TestHandleCallback_RequireSession_RedirectsToLogin
//
// Blocco #1.2 extends that into a thorough matrix that documents
// every shape of "no valid session" the `extractSessionIdentity`
// helper accepts or rejects:
//
//   Group A — Multi-provider coverage (table-driven across the 5 core
//             providers: instagram, twitter, tiktok, linkedin, facebook).
//             Proves the redirect's next-path is correctly bound to
//             the requested provider, AND that the provider roster is
//             not enumerable from a no-session probe.
//   Group B — Bearer Authorization variants on /login: wrong-secret
//             JWT, malformed JWT, sessionID=0 JWT, api-key Bearer,
//             empty Bearer, non-Bearer-prefix header.
//   Group C — Session-cookie variants on /login: wrong-secret cookie
//             JWT, malformed cookie JWT, empty-cookie value.
//   Group D — Edge cases: frontendURL empty (CLI/test fallback
//             returns 401, not 302); no state-cookie hygiene (the
//             oauth_state_{provider} cookie must NOT be issued when
//             session is missing); valid session cookie
//             AUTHENTICATES (positive case proving the cookie path).
//   Group E — Callback paranoia: /callback without an oauth_state
//             cookie + no session still 302s; /callback for an
//             unsupported provider + no session still 302s (no roster
//             leak); /callback with an api-key Bearer still 302s
//             (api-key doesn't auth OAuth social).
//
// All tests use the existing mock primitives from routes_test.go
// (mockProvider, mockUserStore, mockCredentialVault) — the only new
// helper is newOAuthSessionRedirectRouter which registers exactly
// the providers a test needs (vs. newTestRouter which registers a
// fixed instagram+tiktok+twitter+Name() set).

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// newOAuthSessionRedirectRouter builds a Router pre-wired with one
// mockProvider per provider name passed in. Used by Blocco #1.2
// tests to drive the oauthSessionRedirect middleware with exactly
// the supported-platform surface each test needs — e.g. a "twitter"
// test registers only twitter so the unknown-provider path is
// unreachable from that test.
func newOAuthSessionRedirectRouter(t *testing.T, providers []string, frontendURL string, opts ...RouterOption) *Router {
	t.Helper()
	capRouter := services.NewCapabilityRouter()
	for _, p := range providers {
		capRouter.Register(p, &mockProvider{
			platform: p,
			loginURL: "https://provider.example.com/oauth",
		})
	}
	defaultOpts := []RouterOption{
		WithOneTimeCodeStore(NewOneTimeCodeStore(60 * time.Second)),
		WithCredentialVault(&mockCredentialVault{}),
	}
	return NewRouter(
		capRouter,
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		frontendURL,
		nil,
		append(defaultOpts, opts...)...,
	)
}

// issueJWTWithSecret mints a (userID, wsID=1, sessionID=1) JWT signed
// with an arbitrary secret. Used to drive Manager.Verify's HMAC
// mismatch path (wrong-secret → Verify errors → identity nil → 302).
func issueJWTWithSecret(t *testing.T, secret string, userID int64) string {
	t.Helper()
	tok, _, _, err := auth.NewManager(secret, 24).IssueAccess(userID, 1, 1)
	if err != nil {
		t.Fatalf("issue jwt with custom secret: %v", err)
	}
	return tok
}

// mintLegacySessionIDZeroJWT hand-crafts a sessionID=0 JWT signed
// with the supplied secret. SPRINT 7.4 (P0#14-blocco-1.4) hardened
// Manager.Issue to refuse sessionID<=0, so the issuance-side helper
// is no longer useful for tests that need to verify Verify's
// rejection of legacy sid<=0 tokens. This test-only helper uses
// the lower-level jwt.NewWithClaims(HS256, auth.Claims{...}) path
// to sign a JWT whose sid claim is exactly 0 — exercising the
// Manager.Verify contract (HMAC valid → sid<=0 rejected) without
// depending on a deprecated issuance API. The JTI is a fresh
// auth.RandomHex so two calls for the same user produce distinct
// JWTs (audit + replay-trace friendly).
func mintLegacySessionIDZeroJWT(t *testing.T, secret string, userID, wsID int64) string {
	t.Helper()
	jti, err := auth.RandomHex(16)
	if err != nil {
		t.Fatalf("rand jti: %v", err)
	}
	now := time.Now()
	claims := auth.Claims{
		UserID:      userID,
		WorkspaceID: wsID,
		SessionID:   0, // <-- the entire point of this helper
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
			ID:        jti,
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign legacy sid=0 JWT: %v", err)
	}
	return signed
}

// assertNoSessionRedirect verifies the recorder reflects a 302 to
// FRONTEND_URL/login?next=... with the provider correctly encoded in
// the next-path. Group A / B / C / E tests use this.
//
// The middleware URL-escapes the next-path value (url.QueryEscape
// encodes `/` to `%2F`), so the assertion matches the encoded form
// `%2Fconnections%2F{provider}` to avoid false positives on a
// literal "connections" substring.
func assertNoSessionRedirect(t *testing.T, w *httptest.ResponseRecorder, frontendURL, provider string) {
	t.Helper()
	if w.Code != http.StatusFound {
		t.Fatalf("want 302 to FRONTEND_URL/login, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	expectedPrefix := strings.TrimRight(frontendURL, "/") + "/login?next="
	if !strings.HasPrefix(loc, expectedPrefix) {
		t.Fatalf("redirect URL must land on %q; got %s", expectedPrefix, loc)
	}
	wantNext := "%2Fconnections%2F" + provider
	if !strings.Contains(loc, wantNext) {
		t.Errorf("next-path should encode /connections/%s (URL-escaped) so the SPA can resume; got %s", provider, loc)
	}
}

// ---------------------------------------------------------------------------
// Group A — Multi-provider coverage (table-driven)
// ---------------------------------------------------------------------------

// TestOAuthSessionRedirect_Blocco12_NoSession_AllSupportedProviders_Redirects
// proves the middleware applies uniformly to every supported platform
// and that the redirect's next-path is correctly bound to the
// requested provider. Without a session, EVERY provider path lands
// on FRONTEND_URL/login?next=/connections/{provider} — so the SPA
// can show the login UI and resume the OAuth connect after auth.
// Defence-in-depth: a no-session probe cannot enumerate the supported
// platform roster (all paths return the same 302 shape).
func TestOAuthSessionRedirect_Blocco12_NoSession_AllSupportedProviders_Redirects(t *testing.T) {
	providers := []string{"instagram", "twitter", "tiktok", "linkedin", "facebook"}
	for _, p := range providers {
		p := p
		t.Run(p, func(t *testing.T) {
			r := newOAuthSessionRedirectRouter(t, []string{p}, "https://app.example.com")
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/"+p+"/login", nil)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)
			assertNoSessionRedirect(t, w, "https://app.example.com", p)
		})
	}
}

// ---------------------------------------------------------------------------
// Group B — Bearer Authorization variants on /login
// ---------------------------------------------------------------------------

// TestOAuthSessionRedirect_Blocco12_NoSession_BearerJwt_WrongSecret_Redirects
// proves a JWT signed with the WRONG secret is rejected by
// Manager.Verify (HMAC signature mismatch) → extractSessionIdentity
// returns nil → middleware 302s to /login. Defends against JWT
// forgeries — an attacker who guesses or copies a token from another
// service cannot reuse it here because the HMAC won't validate.
func TestOAuthSessionRedirect_Blocco12_NoSession_BearerJwt_WrongSecret_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("Authorization", "Bearer "+issueJWTWithSecret(t, "wrong-secret-must-be-long-enough-for-hs256", 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_BearerJwt_Malformed_Redirects
// proves a Bearer value that doesn't parse as a JWT (junk structure)
// is rejected → 302.
func TestOAuthSessionRedirect_Blocco12_NoSession_BearerJwt_Malformed_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_BearerJwt_SessionIDZero_Redirects
// proves the post-SPRINT-2.1 contract: Manager.Verify rejects any
// JWT where sessionID<=0 (the legacy Manager.Issue(u, wsID) path
// minted tokens with sessionID=0). → middleware 302s.
//
// SPRINT 7.4 (P0#14-blocco-1.4): Manager.Issue now REFUSES
// sessionID=0 (it requires sessionID>0 for every issuance). The test
// therefore hand-crafts a sessionID=0 JWT using the lower-level
// jwt.NewWithClaims(HS256, auth.Claims{SessionID: 0}) signing helper
// — this isolates the test from Manager.Issue so we can still
// verify the Verify layer rejects sid<=0 independently of the
// issuance-side hardening.
func TestOAuthSessionRedirect_Blocco12_NoSession_BearerJwt_SessionIDZero_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	legacyTok := mintLegacySessionIDZeroJWT(t, testJWTSecret, 1, 1)
	req.Header.Set("Authorization", "Bearer "+legacyTok)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_Bearer_ApiKeyFormat_Redirects
// proves api-key Bearer tokens (sk_test_…/sk_live_…) are NOT accepted
// as OAuth social auth. extractSessionIdentity detects the sk_ prefix
// via auth.IsApiKeyBearer and returns nil → 302. Api-key sessions
// have no sessionID, so even if they weren't filtered here, Verify
// would reject them — defence-in-depth.
func TestOAuthSessionRedirect_Blocco12_NoSession_Bearer_ApiKeyFormat_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("Authorization", "Bearer sk_test_abcdef0123456789")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_Bearer_EmptyToken_Redirects
// proves "Authorization: Bearer " with no token is rejected (Verify
// errors on empty input) → 302.
func TestOAuthSessionRedirect_Blocco12_NoSession_Bearer_EmptyToken_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_Authorization_NonBearerPrefix_Redirects
// proves a non-Bearer-prefixed Authorization header doesn't even
// reach the JWT verifier — extractSessionIdentity short-circuits on
// the missing "Bearer " prefix → identity nil → 302. The current
// implementation is case-sensitive (RFC 7235 says scheme is
// case-insensitive in theory but we accept the conventional
// capital-B single-space form; that's a documented behaviour, not
// a defect we're fixing in this Blocco).
func TestOAuthSessionRedirect_Blocco12_NoSession_Authorization_NonBearerPrefix_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("Authorization", "my-custom-token not-a-bearer-format")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// ---------------------------------------------------------------------------
// Group C — Session-cookie variants on /login
// ---------------------------------------------------------------------------

// TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_WrongSecret_Redirects
// proves session cookie JWT signed with wrong secret → Verify HMAC
// mismatch → identity nil → 302.
func TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_WrongSecret_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.AddCookie(&http.Cookie{
		Name:  auth.SessionCookieName,
		Value: issueJWTWithSecret(t, "wrong-secret-must-be-long-enough-for-hs256", 1),
	})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_Malformed_Redirects
// proves session cookie with a non-JWT-format value → Verify errors
// → identity nil → 302.
func TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_Malformed_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "this-is-not-a-jwt"})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_EmptyValue_Redirects
// proves session cookie with empty value is treated as no cookie at
// all (the helper short-circuits on `c.Value == ""`). → identity
// nil → 302.
func TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_EmptyValue_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: ""})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_SessionIDZero_Redirects
// is the session-cookie parity of Test..._BearerJwt_SessionIDZero:
// a session cookie carrying a JWT with sessionID=0 (the legacy
// Manager.Issue(u, wsID) shape) is rejected by Manager.Verify → 302.
// Documents that the cookie path enforces the same post-SPRINT-2.1
// invariant as the Bearer path.
//
// SPRINT 7.4 (P0#14-blocco-1.4): same hand-crafted JWT pattern as
// the Bearer variant — Manager.Issue refuses sid≤0, so this test
// uses mintLegacySessionIDZeroJWT to construct the cookie value.
func TestOAuthSessionRedirect_Blocco12_NoSession_SessionCookie_SessionIDZero_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: mintLegacySessionIDZeroJWT(t, testJWTSecret, 1, 1)})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// ---------------------------------------------------------------------------
// Group D — Edge cases
// ---------------------------------------------------------------------------

// TestOAuthSessionRedirect_Blocco12_NoSession_FrontendURLEmpty_401
// proves the CLI / test-mode fallback path: when frontendURL is "" the
// middleware can't redirect (no SPA URL to land on), so it falls
// through to writeError(401, ...) with a helpful message — same
// shape as protected endpoints use. The 302 path is irrelevant in
// CLI mode where there's no SPA to land the user on.
func TestOAuthSessionRedirect_Blocco12_NoSession_FrontendURLEmpty_401(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "") // empty frontendURL
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("frontendURL=\"\" + no session: want 401 (CLI fallback), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "InstaEdit session") {
		t.Errorf("response body should explain the OAuth-session requirement so CLI users understand; got %s", w.Body.String())
	}
}

// TestOAuthSessionRedirect_Blocco12_NoSession_StateCookieNotIssued
// proves defence-in-depth: when the request is rejected because of a
// missing session, the oauth_state_{provider} cookie MUST NOT be
// issued. Otherwise an attacker could probe the platform roster
// without auth AND still receive a usable state cookie that lets
// them complete an intercepted flow.
func TestOAuthSessionRedirect_Blocco12_NoSession_StateCookieNotIssued(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("setup: want 302, got %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") && c.MaxAge > 0 {
			t.Errorf("oauth_state_instagram cookie was issued despite missing session — state must only bind to authenticated users: %+v", c)
		}
	}
}

// TestOAuthSessionRedirect_Blocco12_SessionCookie_Authenticates is
// the positive companion to the no-session redirect tests — proves
// the cookie auth path WORKS when the session is valid (the
// middleware lets the inner handleLogin handler drive the OAuth
// redirect). Without this, the negative tests would be vacuous.
func TestOAuthSessionRedirect_Blocco12_SessionCookie_Authenticates(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	// Valid session cookie (IssueAccess(u, 1, 1) → Manager.Verify accepts).
	jwt := issueTestJWT(t, 1)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("valid session cookie + /login: want 302 (redirects to provider), got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.example.com/oauth?state=") {
		t.Fatalf("expected redirect to provider's auth dialog, got %s", loc)
	}
}

// ---------------------------------------------------------------------------
// Group E — Callback paranoia
// ---------------------------------------------------------------------------

// TestOAuthSessionRedirect_Blocco12_Callback_NoSession_NoStateCookie_StillRedirects:
// the /callback route is mid-flow (a state cookie would normally be
// present from the /login step). An attacker who hits /callback
// WITHOUT a state cookie AND without a session must still get 302 —
// oauthSessionRedirect runs before state-cookie verification, so the
// no-session check catches it first and the inner handleCallback
// never runs. This shuts down the "drive-by callback fabrication"
// attack vector from SPRINT 7.1 P0#14.
func TestOAuthSessionRedirect_Blocco12_Callback_NoSession_NoStateCookie_StillRedirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=forged-code&state=forged-state", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_Callback_NoSession_UnsupportedProvider_StillRedirects:
// the same 302 applies even for an UNSUPPORTED platform name. The
// provider-roster check (which would normally 404) runs AFTER
// session validation, so without auth all paths look identical
// (302 → /login) and the attacker cannot enumerate supported
// platforms from a no-session probe.
func TestOAuthSessionRedirect_Blocco12_Callback_NoSession_UnsupportedProvider_StillRedirects(t *testing.T) {
	// Register only "instagram" so "myspace" is unsupported.
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/myspace/callback?code=abc&state=xyz", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	// Same 302 path; the provider in the next-path is the requested
	// one, not the supported one (intermediate post-login the SPA
	// will retry login and re-discover the unsupported-ness).
	if w.Code != http.StatusFound {
		t.Fatalf("unsupported provider + no session: want 302 (no roster leak), got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "myspace") {
		t.Errorf("next-path should mention the requested provider %q (SPA needs to know which flow to retry); got %s", "myspace", loc)
	}
}

// TestOAuthSessionRedirect_Blocco12_Callback_NoSession_ApiKeyBearer_Redirects
// proves an api-key Bearer on /callback is REJECTED for OAuth social
// the same way as on /login — extractSessionIdentity detects sk_*
// prefix and returns nil → 302. Api-key sessions don't bind to a
// human user, so OAuth social would be orphaned anyway.
func TestOAuthSessionRedirect_Blocco12_Callback_NoSession_ApiKeyBearer_Redirects(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=xyz", nil)
	setOAuthStateCookieForTest(req, "instagram", "xyz")
	req.Header.Set("Authorization", "Bearer sk_test_abcdef0123456789")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}

// TestOAuthSessionRedirect_Blocco12_Precedence_BearerWinsEvenWithValidCookie
// documents the auth-channel precedence rule in extractSessionIdentity:
// Bearer is checked FIRST, fallback to cookie only if no Bearer is
// present. If a request carries BOTH a malformed Bearer AND a valid
// session cookie, the middleware still 302s to /login — the malformed
// Bearer wins the identity check and rejects the request.
//
// This is non-obvious enough that we pin it here: an attacker who
// adds garbage to Authorization hoping to "deny the cookie" still
// loses — the cookie isn't even consulted.
func TestOAuthSessionRedirect_Blocco12_Precedence_BearerWinsEvenWithValidCookie(t *testing.T) {
	r := newOAuthSessionRedirectRouter(t, []string{"instagram"}, "https://app.example.com")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.jwt")                            // junk Bearer
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: issueTestJWT(t, 1)}) // valid cookie alongside
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	assertNoSessionRedirect(t, w, "https://app.example.com", "instagram")
}
