package api

import (
	"encoding/base64"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleLogin_RedirectsToProviderURL(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.example.com/oauth?state=") {
		t.Fatalf("unexpected redirect: %s", loc)
	}
	_, after, ok := strings.Cut(loc, "state=")
	if !ok {
		t.Fatalf("state= not found in redirect: %s", loc)
	}
	stateParam, _, _ := strings.Cut(after, "&")
	if stateParam == "meta_default" {
		t.Fatalf("state should be a random token, not the old meta_default placeholder: %s", loc)
	}
	if len(stateParam) != 43 {
		t.Fatalf("state length: want 43 chars (32 bytes base64 URL-safe), got %d (%q)", len(stateParam), stateParam)
	}
	if _, err := base64.RawURLEncoding.DecodeString(stateParam); err != nil {
		t.Fatalf("state must be base64 URL-safe: %v (state=%q)", err, stateParam)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("instagram") {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("oauth_state_meta cookie not set (verdict §2 CSRF protection requires the server to bind the state to a browser session)")
	}
	if cookie.Value != stateParam {
		t.Errorf("cookie state != redirect state: cookie=%q, redirect=%q", cookie.Value, stateParam)
	}
	if !cookie.HttpOnly {
		t.Error("oauth state cookie must be HttpOnly (XSS exfiltration defense)")
	}
	if !cookie.Secure {
		t.Error("oauth state cookie must be Secure (HTTPS-only)")
	}
	if cookie.SameSite != http.SameSiteNoneMode {
		t.Errorf("oauth state cookie SameSite: want None, got %v", cookie.SameSite)
	}
	if cookie.MaxAge != int(oauthStateMaxAge.Seconds()) {
		t.Errorf("oauth state cookie MaxAge: want %d, got %d (must match oauthStateMaxAge)", int(oauthStateMaxAge.Seconds()), cookie.MaxAge)
	}
}

func TestHandleLogin_ConsentIsLimitedToReconnect(t *testing.T) {
	for _, tc := range []struct {
		name        string
		query       string
		wantConsent bool
		wantSelect  bool
	}{
		{name: "add selects account without consent", query: "mode=add", wantSelect: true},
		{name: "reconnect forces consent", query: "mode=reconnect", wantConsent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got services.OAuthLoginOptions
			svc := &mockProvider{
				platform: "youtube",
				loginURL: "https://auth.example.com/oauth",
				loginWithOptionsFn: func(state string, options services.OAuthLoginOptions) string {
					got = options
					return "https://auth.example.com/oauth?state=" + state
				},
			}
			r := newTestRouter(svc, &mockUserStore{}, "")
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login?"+tc.query, nil)
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusFound {
				t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
			}
			if got.ForceConsent != tc.wantConsent {
				t.Errorf("ForceConsent: want %v, got %v", tc.wantConsent, got.ForceConsent)
			}
			if got.SelectAccount != tc.wantSelect {
				t.Errorf("SelectAccount: want %v, got %v", tc.wantSelect, got.SelectAccount)
			}
		})
	}
}

func TestHandleLogin_UnsupportedProvider(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.example.com"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown/login", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestHandleLogin_IgnoresClientState(t *testing.T) {
	svc := &mockProvider{platform: "twitter", loginURL: "https://auth.twitter.com/auth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/twitter/login?state=my-custom-state", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	loc := w.Header().Get("Location")
	if strings.Contains(loc, "state=my-custom-state") {
		t.Fatalf("server should IGNORE the client's ?state= (verdict §2); redirect leaked the client value: %s", loc)
	}
	_, after, ok := strings.Cut(loc, "state=")
	if !ok {
		t.Fatalf("state= not found in redirect: %s", loc)
	}
	stateParam, _, _ := strings.Cut(after, "&")
	if len(stateParam) != 43 {
		t.Fatalf("server-generated state length: want 43, got %d (%q)", len(stateParam), stateParam)
	}
}

func TestHandleLogin_YouTube_ExpectedChannelID_SetsSiblingCookie(t *testing.T) {
	svc := &mockProvider{platform: "youtube", loginURL: "https://auth.youtube.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login?expected_channel_id=UCabcdefghijklmnopqrstuv", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.youtube.com/oauth?") {
		t.Fatalf("redirect URL must target the YouTube auth dialog, got %q", loc)
	}
	// State length must still be 43 chars (CSRF nonce invariant verified
	// by TestHandleLogin_RedirectsToProviderURL).
	_, after, ok := strings.Cut(loc, "state=")
	if !ok {
		t.Fatalf("redirect must carry a state= param, got %q", loc)
	}
	stateParam, _, _ := strings.Cut(after, "&")
	if len(stateParam) != 43 {
		t.Errorf("state length: want 43 (32-byte base64 URL-safe), got %d (%q)", len(stateParam), stateParam)
	}
	// Sibling cookie must carry the channel ID and use the same
	// HttpOnly / Secure / SameSite=Lax attributes as the state cookie.
	var sib *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateExpectedChannelCookieName("youtube") {
			sib = c
			break
		}
	}
	if sib == nil {
		t.Fatal("oauth_state_youtube_expected_channel cookie not set; the operator's intended channel ID cannot round-trip to the callback")
	}
	want := stateParam + ":UCabcdefghijklmnopqrstuv"
	if sib.Value != want {
		t.Errorf("sibling cookie value: want %q (state + %q:UCabcdefghijklmnopqrstuv), got %q", want, stateParam, sib.Value)
	}
	if !sib.HttpOnly {
		t.Error("sibling cookie must be HttpOnly (XSS exfiltration defense)")
	}
	if !sib.Secure {
		t.Error("sibling cookie must be Secure (HTTPS-only)")
	}
	if sib.SameSite != http.SameSiteNoneMode {
		t.Errorf("sibling cookie SameSite: want None, got %v", sib.SameSite)
	}
}

func TestHandleLogin_YouTube_ExpectedChannelID_InvalidFormat_NotSet(t *testing.T) {
	svc := &mockProvider{platform: "youtube", loginURL: "https://auth.youtube.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login?expected_channel_id=not-a-real-channel-id", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateExpectedChannelCookieName("youtube") && c.MaxAge > 0 {
			t.Errorf("malformed expected_channel_id must NOT issue the sibling cookie: %+v", c)
		}
	}
}

func TestHandleLogin_YouTube_ExpectedChannelID_IgnoredForNonYouTube(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.instagram.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login?expected_channel_id=UCtest123channelID", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateExpectedChannelCookieName("instagram") && c.MaxAge > 0 {
			t.Errorf("expected_channel_id must be ignored on non-YouTube providers: %+v", c)
		}
	}
}

func TestHandleLogin_StateIsRandomAcrossRequests(t *testing.T) {
	svc := &mockProvider{platform: "instagram", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	extractState := func(w *httptest.ResponseRecorder) string {
		loc := w.Header().Get("Location")
		_, after, ok := strings.Cut(loc, "state=")
		if !ok {
			t.Fatalf("state= not found in redirect: %s", loc)
		}
		stateParam, _, _ := strings.Cut(after, "&")
		return stateParam
	}

	// SPRINT 7.1 (P0#14): the OAuth login route is now behind
	// oauthSessionRedirect — a request without an InstaEdit session
	// is 302'd to /login (verified separately by
	// TestHandleLogin_RequireSession_RedirectsToLogin). To drive
	// the actual handleLogin handler, attach a valid Bearer before
	// each call so redirect lands on the provider's auth dialog
	// (state-cookie entropy can then be measured).
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	withBearerJWT(t, req1, 1)
	r.Setup().ServeHTTP(w1, req1)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil)
	withBearerJWT(t, req2, 1)
	r.Setup().ServeHTTP(w2, req2)

	s1 := extractState(w1)
	s2 := extractState(w2)
	if s1 == s2 {
		t.Errorf("two logins produced the SAME state %q (must be cryptographically random to defeat pre-computation)", s1)
	}
	if len(s1) != 43 || len(s2) != 43 {
		t.Errorf("states should be 43 chars (32 bytes base64 URL-safe); got %d and %d", len(s1), len(s2))
	}
}
