package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestHandleCallback_MissingCode(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleCallbackRouteForTest_YouTubeBindsProvider(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback", nil)
	w := httptest.NewRecorder()
	r.HandleOAuthCallbackRouteForTest().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("/api/v1/auth/youtube/callback via test seam: want 400 missing code, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "unsupported provider") {
		t.Fatalf("test seam failed to bind provider from callback path: %s", w.Body.String())
	}
}

func TestHandleCallbackRouteForTest_YouTubeDispatchesProvider(t *testing.T) {
	svc := &mockProvider{
		platform: "youtube",
		handleCallback: func(context.Context, string, string) (*models.PlatformProfile, *models.TokenData, error) {
			return nil, nil, fmt.Errorf("youtube callback sentinel")
		},
	}
	r := newTestRouter(svc, &mockUserStore{}, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=mock-code&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	w := httptest.NewRecorder()
	r.HandleOAuthCallbackRouteForTest().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("YouTube callback via test seam: want 500 from provider sentinel, got %d: %s", w.Code, w.Body.String())
	}
	if svc.handleCallbackCalls != 1 {
		t.Fatalf("YouTube provider HandleCallback calls: want 1, got %d", svc.handleCallbackCalls)
	}
	if strings.Contains(w.Body.String(), "unsupported provider") {
		t.Fatalf("test seam did not dispatch YouTube provider: %s", w.Body.String())
	}
}

func TestHandleCallback_UnsupportedProvider(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown/callback?code=abc", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestHandleCallback_HandleCallbackError(t *testing.T) {
	svc := &mockProvider{
		platform: "twitter",
		handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
			return nil, nil, fmt.Errorf("platform auth error")
		},
	}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/twitter/callback?code=bad&state=test-state", nil)
	setOAuthStateCookieForTest(req, "twitter", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCallback_AttachError_409(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return nil, fmt.Errorf("%w: platform=%s owned_by=999 requested_by=%d",
				repository.ErrAccountAlreadyLinked, platform, userID)
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "platform account") {
		t.Errorf("response body should explain the link conflict; got %q", w.Body.String())
	}
}

func TestHandleCallback_AttachError_500(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return nil, fmt.Errorf("db error")
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCallback_AuthorizeChannelError(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	authorizer := &fakeChannelAuthorizer{
		authorizeErr: fmt.Errorf("token save error"),
	}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel must be called exactly once; got %d", authorizer.authorizeCalls.Load())
	}
	// Acceptance-closure on the legacy failure mode: zero cipher
	// writes when authorizeErr fires means the production ROLLBACK
	// along the row's pending_authorization stay is reproduced.
	if n := authorizer.tokenWriteCount(); n != 0 {
		t.Errorf("tokenWrites len on authorizeErr: want 0 (ROLLBACK semantic), got %d", n)
	}
}

func TestHandleCallback_Success_FrontendRedirect_UsesRedirectCookie(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	req.AddCookie(&http.Cookie{
		Name: OAuthStateRedirectCookieName("instagram"), Value: "/app/groups",
		Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "https://app.example.com/app/groups?") {
		t.Fatalf("redirect URL must land on the cookie's /app/groups return path: %s", loc)
	}
	if !strings.Contains(loc, "provider=instagram") || !strings.Contains(loc, "status=connected") {
		t.Fatalf("expected provider=instagram and status=connected in redirect params: %s", loc)
	}
	// Single-use contract: the callback must delete the redirect cookie
	// on read so a replay of the same callback cannot re-trigger the
	// /app/groups redirect (it would fall back to /app/linking).
	foundDelete := false
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateRedirectCookieName("instagram") && c.MaxAge < 0 {
			foundDelete = true
		}
	}
	if !foundDelete {
		t.Fatal("callback must clear the redirect cookie on read (single-use)")
	}
}

func TestHandleCallback_Success_FrontendRedirect_RejectsInvalidRedirectCookie(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	req.AddCookie(&http.Cookie{
		Name: OAuthStateRedirectCookieName("instagram"), Value: "https://evil.example.com",
		Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	// A forged / invalid redirect cookie must fall back to the default
	// /app/linking landing — never an open redirect.
	if !strings.Contains(loc, "https://app.example.com/app/linking?") {
		t.Fatalf("invalid redirect cookie must fall back to /app/linking (no open redirect): %s", loc)
	}
}

func TestHandleCallback_Success_JSONResponse(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	r := newTestRouter(svc, store, "") // empty frontendURL → JSON

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// SPRINT 7.1 (P0#14): the OAuth callback is now an "attach to
	// existing session" operation — no one-time code is issued, no
	// JWT is minted, and no user is auto-created. The typed JSON
	// response in CLI / test mode reports the link.
	if body["status"] != "connected" {
		t.Fatalf("status: want connected, got %v (SPRINT 7.1 contract)", body["status"])
	}
	if body["provider"] != "instagram" {
		t.Fatalf("provider: want instagram, got %v", body["provider"])
	}
	if _, present := body["code"]; present {
		t.Fatalf("code field must NOT appear in OAuth callback response (SPRINT 7.1: no one-time code path): %v", body)
	}
	if _, present := body["jwt"]; present {
		t.Fatalf("jwt field must NEVER appear (Taglio 1.2 + SPRINT 7.1): %v", body)
	}
	if uid, ok := body["user_id"].(float64); !ok || uid != 1 {
		t.Fatalf("user_id: want 1 (the session user), got %v (SPRINT 7.1: must equal JWT uid)", body["user_id"])
	}
	if accountID, ok := body["account_id"].(float64); !ok || accountID != 10 {
		t.Fatalf("account_id: want 10, got %v", body["account_id"])
	}
}

func TestHandleCallback_Success_FrontendRedirect(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	// SPRINT 7.1 (P0#14): the redirect target is the SPA's connections
	// page with provider + status=connected query params — no one-time
	// code, no JWT. The session cookie that validated at the top of
	// the handler IS the active session.
	if !strings.Contains(loc, "https://app.example.com/app/linking?") {
		t.Fatalf("redirect URL must land on /app/linking (SPRINT 7.1): %s", loc)
	}
	if strings.Contains(loc, "jwt=") {
		t.Fatalf("JWT must never appear in the redirect URL: %s", loc)
	}
	if strings.Contains(loc, "code=") {
		t.Fatalf("one-time code must NOT appear in the OAuth callback redirect (SPRINT 7.1): %s", loc)
	}
	if !strings.Contains(loc, "provider=instagram") {
		t.Fatalf("expected provider=instagram in redirect params: %s", loc)
	}
	if !strings.Contains(loc, "status=connected") {
		t.Fatalf("expected status=connected in redirect params: %s", loc)
	}
}
