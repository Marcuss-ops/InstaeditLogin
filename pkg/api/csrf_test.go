// Package api — Blocco #1.3 CSRF double-submit integration tests.
//
// Threat model recap:
//   - Access JWT lives in HttpOnly session cookie, browser attaches
//     it across origins when credentials:'include' +
//     CORS allow-credentials are configured.
//   - Without CSRF, any malicious page could POST with the victim's
//     cookie. Blocco #1.3 closes this with double-submit-cookie:
//     csrf_token cookie + X-CSRF-Token request header must agree.
//   - Exemptions (mirrors internal/auth/csrf.go docstring):
//     1. Safe methods (GET/HEAD/OPTIONS) — never require CSRF.
//     2. Bearer-prefixed requests (JWT or API-key).
//     3. Unauthenticated requests (no SessionCookieName cookie +
//        no Bearer) — auth.Middleware 401s them; CSRF is moot when
//        there's no session-cookie context.
//     4. Lifecycle endpoints /api/v1/auth/{refresh,logout} sit
//        OUTSIDE r.protected so CSRF middleware is not in their
//        chain.
//
// Each test drives the wrapped http.Handler returned by r.Setup()
// through httptest.NewRecorder so the full middleware chain
// (CSRF outermost -> auth.Middleware -> handler) is exercised.
//
// Test design note: csrf_test.go wires a *Router, calls Setup()
// EXACTLY ONCE, and captures both r and the wrapped h. Custom
// routes are added to r.mux AFTER Setup() — since chi.Mux is the
// captured one inside h.Handler, those routes ARE reachable via
// h.Handler. Calling Setup() again would replace r.mux and orphan
// the wrapped h.Handler's route table. The original version of
// this file tripped over that re-setup pitfall.

package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// csrfHarness bundles a Router + the wrapped handler produced by
// Setup(). Tests add custom routes to h.Router.mux via Mount()
// — the mux captured inside h.Handler — and serve them via
// h.Handler.ServeHTTP. Re-calling r.Setup() on the same harness
// would replace r.mux and strand h.Handler.
type csrfHarness struct {
	Router  *Router
	Handler http.Handler
}

// csrfHarnessNew builds a minimal Router pre-wired for both the
// POST and GET success paths (createFn + listByWorkspaceFn) and
// returns the captured harness.
func csrfHarnessNew(t *testing.T) *csrfHarness {
	t.Helper()
	r := NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithOneTimeCodeStore(NewOneTimeCodeStore(60)),
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, Name: "Personal", OwnerID: 1}, nil
			},
		}),
		WithPostStore(&mockPostStore{
			createFn: func(p *models.Post, tgts []*models.PostTarget) error {
				p.ID = 999
				p.CreatedAt = time.Now()
				for i, tg := range tgts {
					tg.ID = int64(800 + i)
					tg.PostID = p.ID
				}
				return nil
			},
			listByWorkspaceFn: func(workspaceID int64) ([]models.Post, error) {
				return []models.Post{{ID: 1, WorkspaceID: workspaceID}}, nil
			},
		}),
		WithCredentialVault(&mockCredentialVault{}),
	)
	return &csrfHarness{Router: r, Handler: r.Setup()}
}

// Mount registers a route on r.mux — the same chi.Mux captured
// inside h.Handler. Calling r.Setup() again would orphan this
// route. Tests MUST NOT do that.
func (h *csrfHarness) Mount(method, path string, hf http.HandlerFunc) {
	h.Router.mux.Method(method, path, hf)
}

// csrfTokenFor returns a fresh 32-byte hex string. Tests use
// these to align csrf_token cookie values with X-CSRF-Token
// request headers / cookie values when validating match.
func csrfTokenFor(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("csrf rand: %v", err)
	}
	return hex.EncodeToString(b)
}

// TestCsrf_MissingCookie_403 — POST with ONLY a session cookie
// (no csrf_token cookie + no X-CSRF-Token header) must be rejected.
func TestCsrf_MissingCookie_403(t *testing.T) {
	h := csrfHarnessNew(t)
	h.Mount(http.MethodPost, "/api/v1/posts", h.Router.protected(h.Router.handleCreatePost))
	jwt := issueTestJWT(t, 1)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		strings.NewReader(`{"workspace_id":1,"title":"x","targets":[{"platform_account_id":10}]}`))
	req.Header.Set("Content-Type", "application/json")
	// session cookie present, csrf NOT.
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 csrf_rejected, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "csrf") {
		t.Errorf("response body should mention csrf; got %q", w.Body.String())
	}
}

// TestCsrf_MissingHeader_403 — csrf cookie present but the
// X-CSRF-Token request header is missing — still rejected.
func TestCsrf_MissingHeader_403(t *testing.T) {
	h := csrfHarnessNew(t)
	h.Mount(http.MethodPost, "/api/v1/posts", h.Router.protected(h.Router.handleCreatePost))
	jwt := issueTestJWT(t, 1)
	cv := csrfTokenFor(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		strings.NewReader(`{"workspace_id":1,"title":"x","targets":[{"platform_account_id":10}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	req.AddCookie(&http.Cookie{Name: auth.CSRFTokenCookieName, Value: cv})
	// No X-CSRF-Token header.
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 csrf_rejected (missing header), got %d: %s", w.Code, w.Body.String())
	}
}

// TestCsrf_MismatchedToken_403 — csrf cookie value differs from
// the X-CSRF-Token request header: rejected by secure-compare.
func TestCsrf_MismatchedToken_403(t *testing.T) {
	h := csrfHarnessNew(t)
	h.Mount(http.MethodPost, "/api/v1/posts", h.Router.protected(h.Router.handleCreatePost))
	jwt := issueTestJWT(t, 1)
	cv := csrfTokenFor(t)
	hdr := csrfTokenFor(t) // different value

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		strings.NewReader(`{"workspace_id":1,"title":"x","targets":[{"platform_account_id":10}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	req.AddCookie(&http.Cookie{Name: auth.CSRFTokenCookieName, Value: cv})
	req.Header.Set(auth.CSRFHeader, hdr)
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 csrf_rejected (mismatch), got %d: %s", w.Code, w.Body.String())
	}
}

// TestCsrf_MatchingToken_CookieAuth_201 — csrf cookie + header
// match -> request reaches the handler and returns 201.
func TestCsrf_MatchingToken_CookieAuth_201(t *testing.T) {
	h := csrfHarnessNew(t)
	h.Mount(http.MethodPost, "/api/v1/posts", h.Router.protected(h.Router.handleCreatePost))
	jwt := issueTestJWT(t, 1)
	cv := csrfTokenFor(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		strings.NewReader(`{"workspace_id":1,"title":"x","targets":[{"platform_account_id":10}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	req.AddCookie(&http.Cookie{Name: auth.CSRFTokenCookieName, Value: cv})
	req.Header.Set(auth.CSRFHeader, cv)
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("matching CSRF + session cookie: want 201 from handler, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCsrf_BearerPrefix_SkipsCheck — Authorization: Bearer <jwt>
// short-circuits CSRF (same as Authorization: Bearer sk_* for API
// keys). The handler runs as usual.
func TestCsrf_BearerPrefix_SkipsCheck(t *testing.T) {
	h := csrfHarnessNew(t)
	h.Mount(http.MethodPost, "/api/v1/posts", h.Router.protected(h.Router.handleCreatePost))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts",
		strings.NewReader(`{"workspace_id":1,"title":"x","targets":[{"platform_account_id":10}]}`))
	req.Header.Set("Content-Type", "application/json")
	// Bearer only (no session cookie, no csrf cookie).
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Bearer-authenticated POST: CSRF should be skipped, want 201, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCsrf_GET_NoToken_Passes — safe methods skip CSRF regardless.
// GET /api/v1/posts is the route handleListPosts already mounted
// by Setup() with r.protected. We serve a fresh request — no csrf
// cookies — and expect 200 because GET is a safe method.
func TestCsrf_GET_NoToken_Passes(t *testing.T) {
	h := csrfHarnessNew(t)
	jwt := issueTestJWT(t, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: jwt})
	// No csrf cookie. GET is safe — CSRF should skip.
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET without csrf_token cookie: safe method should pass, want 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCsrf_Refresh_NoToken_OK — /api/v1/auth/refresh is OUTSIDE
// r.protected so CSRF middleware does not apply. The refresh
// cookie is the credential; presenting it is the anti-replay
// device. We assert the response is NOT 403 — sessionsSvc nil
// causes a 500 server error, which still proves CSRF did not
// intervene.
func TestCsrf_Refresh_NoToken_OK(t *testing.T) {
	h := csrfHarnessNew(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	// No Authorization, no csrf cookie, only refresh cookie.
	req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: "abc"})
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("/refresh must NOT be csrf-gated (lifecycle exception); got 403: %s", w.Body.String())
	}
}

// TestCsrf_Logout_NoToken_OK — same exemption for /auth/logout.
func TestCsrf_Logout_NoToken_OK(t *testing.T) {
	h := csrfHarnessNew(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: issueTestJWT(t, 1)})
	req.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: "abc"})
	// No csrf cookie. logout should still run (sessionsSvc nil -> 500,
	// confirming CSRF did NOT short-circuit).
	w := httptest.NewRecorder()
	h.Handler.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("/logout must NOT be csrf-gated; got 403: %s", w.Body.String())
	}
}

// TestSetSessionCookie_AlsoSetsCsrfCookie — the email/password login
// helper setSessionCookie must issue BOTH cookies so the SPA can
// echo the csrf_token on its first post-login POST.
func TestSetSessionCookie_AlsoSetsCsrfCookie(t *testing.T) {
	w := httptest.NewRecorder()
	setSessionCookie(w, "fake-jwt")
	cookies := w.Result().Cookies()
	var session, csrf *http.Cookie
	for _, c := range cookies {
		if c.Name == auth.SessionCookieName {
			session = c
		}
		if c.Name == auth.CSRFTokenCookieName {
			csrf = c
		}
	}
	if session == nil {
		t.Fatal("session cookie not set by setSessionCookie")
	}
	if csrf == nil {
		t.Fatal("csrf_token cookie not set by setSessionCookie (Blocco #1.3 contract)")
	}
	if csrf.HttpOnly {
		t.Errorf("csrf_token must NOT be HttpOnly (SPA reads via document.cookie): %+v", csrf)
	}
	if !csrf.Secure {
		t.Errorf("csrf_token must be Secure in production: %+v", csrf)
	}
	if csrf.SameSite != http.SameSiteNoneMode {
		t.Errorf("csrf_token SameSite must be None (cross-origin SPA): %+v", csrf)
	}
	// 64-char hex (32 bytes).
	if len(csrf.Value) != 64 {
		t.Errorf("csrf_token length: want 64 hex chars, got %d (%q)", len(csrf.Value), csrf.Value)
	}
	if _, err := hex.DecodeString(csrf.Value); err != nil {
		t.Errorf("csrf_token must be valid hex: %v", err)
	}
}
