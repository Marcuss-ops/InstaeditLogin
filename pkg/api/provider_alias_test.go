package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestTwitterAliasUsesCanonicalOAuthStateCookies(t *testing.T) {
	if got, want := models.NormalizePlatformIdentifier("x"), models.PlatformTwitter; got != want {
		t.Fatalf("normalized provider: want %q, got %q", want, got)
	}
	if got, want := OAuthStateCookieName("x"), OAuthStateCookieName(models.PlatformTwitter); got != want {
		t.Fatalf("state cookie alias: want %q, got %q", want, got)
	}
	if got, want := OAuthStateExpectedChannelCookieName("x"), OAuthStateExpectedChannelCookieName(models.PlatformTwitter); got != want {
		t.Fatalf("expected-channel cookie alias: want %q, got %q", want, got)
	}
	if got, want := OAuthStateOAuthClientCookieName("x"), OAuthStateOAuthClientCookieName(models.PlatformTwitter); got != want {
		t.Fatalf("client cookie alias: want %q, got %q", want, got)
	}
}

func TestTwitterAliasLoginDispatchesCanonicalProvider(t *testing.T) {
	svc := &mockProvider{platform: models.PlatformTwitter, loginURL: "https://auth.example.com/twitter"}
	r := newTestRouter(svc, &mockUserStore{}, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/x/login", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("Location"), "https://auth.example.com/twitter") {
		t.Fatalf("X alias did not dispatch Twitter provider: %q", w.Header().Get("Location"))
	}
}

func TestTwitterAliasLoginCallbackRoundTrip(t *testing.T) {
	svc := &mockProvider{platform: models.PlatformTwitter, loginURL: "https://auth.example.com/twitter", handleCallback: successCallback}
	r := newTestRouter(svc, &mockUserStore{attachFn: successAttach}, "")

	startReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/x/login", nil)
	withBearerJWT(t, startReq, 1)
	startResp := httptest.NewRecorder()
	r.Setup().ServeHTTP(startResp, startReq)
	if startResp.Code != http.StatusFound {
		t.Fatalf("start status: want 302, got %d", startResp.Code)
	}
	location := startResp.Header().Get("Location")
	_, state, ok := strings.Cut(location, "state=")
	if !ok {
		t.Fatalf("start redirect has no state: %q", location)
	}
	state, _, _ = strings.Cut(state, "&")
	var stateCookie *http.Cookie
	for _, cookie := range startResp.Result().Cookies() {
		if cookie.Name == OAuthStateCookieName(models.PlatformTwitter) && cookie.MaxAge > 0 {
			stateCookie = cookie
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("/auth/x/login did not issue the canonical twitter state cookie")
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/x/callback?code=abc&state="+state, nil)
	callbackReq.AddCookie(stateCookie)
	withBearerJWT(t, callbackReq, 1)
	callbackResp := httptest.NewRecorder()
	r.Setup().ServeHTTP(callbackResp, callbackReq)
	if callbackResp.Code != http.StatusOK {
		t.Fatalf("callback status: want 200, got %d: %s", callbackResp.Code, callbackResp.Body.String())
	}
}

func TestTwitterAliasCallbackReturnsCanonicalProvider(t *testing.T) {
	svc := &mockProvider{platform: models.PlatformTwitter, handleCallback: successCallback}
	store := &mockUserStore{attachFn: successAttach}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/x/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "x", "test-state")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode callback response: %v", err)
	}
	if got := body["provider"]; got != models.PlatformTwitter {
		t.Fatalf("callback provider: want %q, got %v", models.PlatformTwitter, got)
	}
}
