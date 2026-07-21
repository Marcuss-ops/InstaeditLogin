package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// handleLogin tests
// ---------------------------------------------------------------------------

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
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("oauth state cookie SameSite: want Lax, got %v", cookie.SameSite)
	}
	if cookie.MaxAge != int(oauthStateMaxAge.Seconds()) {
		t.Errorf("oauth state cookie MaxAge: want %d, got %d (must match oauthStateMaxAge)", int(oauthStateMaxAge.Seconds()), cookie.MaxAge)
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

// ---------------------------------------------------------------------------
// handleCallback tests
// ---------------------------------------------------------------------------

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

// TestHandleCallback_AttachError_409 proves SPRINT 7.1 (P0#14):
// ErrAccountAlreadyLinked surfaces as HTTP 409 to the client. The
// (platform, platform_user_id) tuple was previously linked to a
// different InstaEdit user; we never silently rebind. The legal
// owner of the link must disconnect via
// DELETE /api/v1/accounts/{id} before re-link is possible.
//
// The mock returns the sentinel directly so errors.Is in the
// handler matches the chain (a wrapped fmt.Errorf("%s: ...")
// without %w would silently 500 instead of 409).
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

// TestHandleCallback_AttachError_500 covers other AttachPlatformAccount
// failures (db error, lookup error, create error) that map to 500 —
// distinct from the ErrAccountAlreadyLinked 409 path above.
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

// TestHandleCallback_AuthorizeChannelError asserts that an error from
// the Task 1/10 atomic authorizer surfaces as a 500. The test wires a
// fakeChannelAuthorizer with an authorizeErr that simulates a
// token-write failure mid-transaction (the production service's
// ROLLBACK path). This is the non-discoverer analog of the legacy
// TestHandleCallback_SaveTokenError: the SAME invariant ("cipher
// write failure ⇒ 500") is enforced via the atomic primitive rather
// than the old separate vault.Save call.
//
// ALSO asserts that, on early failure (authorizeErr fires before
// tokens are recorded), zero cipher writes land — proving that the
// ROLLBACK semantics of the production service are honoured by the
// router's call-site: a successful pre-tx guard + an empty
// tokenWrites slice == platform_accounts row stays at
// pending_authorization (the legacy "active without cipher"
// failure mode is FORBIDDEN by the spec).
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

// TestAcceptance_NonDiscovererUsesAtomicAuthorizer is the regression-closure
// acceptance test for Task 1/10. It proves that the OAuth callback's
// non-discoverer branch (the legacy r.vault.Save path before the
// refactor) now routes through r.authorizer.AuthorizeChannel
// atomically — the SAME primitive used by the discoverer branch —
// not through a direct r.vault.Save call.
//
// Three pre-conditions are asserted:
//  1. r.authorizer.AuthorizeChannel is invoked exactly once
//     (NO direct r.vault.Save call paths remain on this code path).
//  2. The argument shape matches the documented Service contract:
//     (account.ID, expectedChannelID="", tokenData.Scopes, tokenData…).
//     expectedChannelID="" tells the YouTube channels.list(mine=true)
//     binder to short-circuit (this is the non-YouTube flow).
//  3. Exactly one cipher write lands in tokenWrites — matching the
//     legacy "1 vault.Save call" semantic the production service
//     replaces.
func TestAcceptance_NonDiscovererUsesAtomicAuthorizer(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	// (1) exactly one AuthorizeChannel call on the non-discoverer path.
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel must be called exactly once on the non-discoverer path; got %d (legacy direct vault.Save path is BACK)", authorizer.authorizeCalls.Load())
	}
	// (2) argument shape: account.ID (single account), no YouTube
	// expected_channel (empty string), variadic token list contains
	// the principal TokenData. Scopes MAY be nil for the Instagram
	// happy-path fixture (successCallback omits the Scopes field)
	// and that is documented and OK — the production service passes
	// the slice through pq.Array, which serialises nil as NULL.
	if authorizer.lastAccountID != 10 {
		t.Errorf("lastAccountID: want 10 (from successAttach), got %d", authorizer.lastAccountID)
	}
	if authorizer.lastExpectedCh != "" {
		t.Errorf("lastExpectedCh: want \"\" (non-YouTube path; binder short-circuits), got %q", authorizer.lastExpectedCh)
	}
	if got := len(authorizer.lastTokens); got != 1 {
		t.Fatalf("lastTokens len: want 1 (principal token only on non-YouTube path), got %d", got)
	}
	if authorizer.lastTokens[0] == nil || authorizer.lastTokens[0].AccessToken != "at-secret" {
		t.Errorf("lastTokens[0]: want TokenData{AccessToken: \"at-secret\"}, got %+v", authorizer.lastTokens[0])
	}
	// (3) tokenWrites independent audit: exactly one cipher row
	// written for this single-account Instagram happy path.
	if n := authorizer.tokenWriteCount(); n != 1 {
		t.Errorf("tokenWrites len: want 1 (single principal token on non-YouTube path), got %d", n)
	}
	if w := authorizer.tokenWrites[0]; w.AccountID != 10 || w.TokenType != "bearer" || w.AccessToken != "at-secret" {
		t.Errorf("tokenWrites[0]: want (accountID=10, tokenType=bearer, access=at-secret), got %+v", w)
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

// TestHandleCallback_Facebook_SavesPageAccessToken verifies that when a
// provider exposes AccountDiscoverer (Facebook Pages), the callback handler
// creates one PlatformAccount per discovered page and persists both the
// page-scoped access token (TokenTypePageAccess) and the user-level long-lived
// token for each account.
func TestHandleCallback_Facebook_SavesPageAccessToken(t *testing.T) {
	const userLongLivedToken = "user-long-lived-token"
	pages := []*services.DiscoveredAccount{
		{
			Profile: models.PlatformProfile{PlatformUserID: "page-1", Username: "Page One"},
			SupplementalTokens: []*models.TokenData{
				{AccessToken: "page-token-1", TokenType: models.TokenTypePageAccess, ExpiresIn: 60 * 60 * 24 * 365 * 10, Scopes: []string{"pages_manage_posts", "pages_read_engagement", "pages_show_list"}},
			},
		},
		{
			Profile: models.PlatformProfile{PlatformUserID: "page-2", Username: "Page Two"},
			SupplementalTokens: []*models.TokenData{
				{AccessToken: "page-token-2", TokenType: models.TokenTypePageAccess, ExpiresIn: 60 * 60 * 24 * 365 * 10, Scopes: []string{"pages_manage_posts", "pages_read_engagement", "pages_show_list"}},
			},
		},
	}

	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "facebook",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "fb-user-123", Username: "FB User"}, &models.TokenData{
					AccessToken: userLongLivedToken,
					TokenType:   models.TokenTypeLongLived,
					ExpiresIn:   5184000,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			if accessToken != userLongLivedToken {
				t.Errorf("DiscoverAccounts accessToken: want %q, got %q", userLongLivedToken, accessToken)
			}
			return pages, nil
		},
	}

	var attachCount int
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			attachCount++
			return &models.PlatformAccount{
				ID:             int64(10 + attachCount),
				UserID:         userID,
				Platform:       platform,
				PlatformUserID: profile.PlatformUserID,
				Username:       profile.Username,
			}, nil
		},
	}
	// Task 1/10 — atomic OAuth finalize: token-write visibility is
	// owned by the fakeChannelAuthorizer (independent audit trail
	// in tokenWrites). The vault mock is no longer in this code
	// path's call chain so we don't even need WithCredentialVault
	// override for the cipher count.
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/facebook/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "facebook", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Expect 4 token writes: page token + user token for each of the 2 pages.
	// The atomic AuthorizeChannel call records both principal
	// (user long-lived) AND supplemental (page access) tokens in the
	// SAME call — same surface contract as the legacy non-atomic
	// path that issued two separate r.vault.Save calls per page.
	if authorizer.tokenWriteCount() != 4 {
		t.Fatalf("want 4 token writes (2 page + 2 user), got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	// Build a map keyed by (accountID, tokenType) to avoid relying on save order.
	writtenByType := make(map[int64]map[string]string)
	authorizer.mu.Lock()
	for _, w := range authorizer.tokenWrites {
		if writtenByType[w.AccountID] == nil {
			writtenByType[w.AccountID] = make(map[string]string)
		}
		writtenByType[w.AccountID][w.TokenType] = w.AccessToken
	}
	authorizer.mu.Unlock()
	for _, p := range pages {
		// The account IDs are generated by attachFn as 10, 11, ...
		// SupplementalTokens carry the page token — find by matching
		// the AccessToken from SupplementalTokens[0].
		var foundID int64
		expectedPageToken := p.SupplementalTokens[0].AccessToken
		for id, tokens := range writtenByType {
			if tokens[models.TokenTypePageAccess] == expectedPageToken {
				foundID = id
				break
			}
		}
		if foundID == 0 {
			t.Fatalf("missing page token save for page %s", p.Profile.PlatformUserID)
		}
		if writtenByType[foundID][models.TokenTypePageAccess] != expectedPageToken {
			t.Errorf("page %s: want page token %q, got %q", p.Profile.PlatformUserID, expectedPageToken, writtenByType[foundID][models.TokenTypePageAccess])
		}
		if writtenByType[foundID][models.TokenTypeLongLived] != userLongLivedToken {
			t.Errorf("page %s: want user token %q, got %q", p.Profile.PlatformUserID, userLongLivedToken, writtenByType[foundID][models.TokenTypeLongLived])
		}
	}
}

// TestHandleLogin_YouTube_ExpectedChannelID_SetsSiblingCookie proves
// the login half of the YouTube P0 fix: a validated
// ?expected_channel_id=UC... round-trips through the sibling
// oauth_state_youtube_expected_channel cookie and also forces
// prompt=consent select_account so a cached grant cannot bind to
// a different Brand Account on consent.
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
	if sib.SameSite != http.SameSiteLaxMode {
		t.Errorf("sibling cookie SameSite: want Lax, got %v", sib.SameSite)
	}
}

// TestHandleLogin_YouTube_ExpectedChannelID_InvalidFormat_NotSet proves
// that a malformed ?expected_channel_id= (not UC + 22 base64url chars)
// is silently dropped: no sibling cookie issued, OAuth flow still
// proceeds. attachDiscoveredAccounts at callback time catches a real
// mismatch instead — we don't want a 400 here on a typo because the
// real check is downstream.
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

// TestHandleLogin_YouTube_ExpectedChannelID_IgnoredForNonYouTube proves
// ?expected_channel_id= is silently ignored on non-YouTube providers.
// Instagram / TikTok / Facebook don't have Brand Accounts and don't
// need the binding hint.
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

// TestHandleCallback_YouTube_OneChannel_OneSave proves the P0 fix in its
// simplest form: a single-channel grant (the common case) saves the root
// bearer token exactly once on the only discovered channel.
func TestHandleCallback_YouTube_OneChannel_OneSave(t *testing.T) {
	const bearerToken = "yt-bearer-token-1"
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCsoloChannel", Username: "Solo Channel"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc-1", Username: "G Acc"}, &models.TokenData{
					AccessToken: bearerToken, TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	type saveCall struct {
		accountID int64
		token     string
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID: 10, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID, Username: profile.Username,
			}, nil
		},
	}
	// Task 1/10 — atomically via r.authorizer.AuthorizeChannel.
	// tokenWrites is the independent audit trail in the fake.
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 1 {
		t.Fatalf("tokenWrites must be exactly 1 (single channel, atomic), got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	w0 := authorizer.tokenWrites[0]
	if w0.AccountID != 10 || w0.AccessToken != bearerToken {
		t.Errorf("tokenWrites[0]: want (accountID=10, access=%q), got %+v", bearerToken, w0)
	}
}

// TestHandleCallback_YouTube_MultipleChannels_NoExpected_Conflict proves
// the BUG fix: an ambiguous multi-channel grant returns HTTP 409 and
// DOES NOT save the token on ANY account. Without the fix, every
// discovered channel would receive a PlatformAccount row + a clone of
// the root bearer token — exactly the misroute risk Google warns about
// when a third-party app ignores Brand Account selection.
func TestHandleCallback_YouTube_MultipleChannels_NoExpected_Conflict(t *testing.T) {
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa1", Username: "Channel A"}},
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa2", Username: "Channel B"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
					AccessToken: "bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			// attachFn must NOT be called when discovery is ambiguous —
			// if it is, the bug is back.
			return &models.PlatformAccount{
				ID: 10, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID,
			}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("ambiguous grant: want 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("ambiguous grant must NOT write tokens on ANY channel; got %d write(s)", authorizer.tokenWriteCount())
	}
	if authorizer.authorizeCalls.Load() != 0 {
		t.Fatalf("ambiguous grant must NOT invoke AuthorizeChannel at all (channels.list guard rejects pre-tx); got %d call(s)", authorizer.authorizeCalls.Load())
	}
	if !strings.Contains(w.Body.String(), "ambiguous") {
		t.Errorf("response body should explain the ambiguity, got %q", w.Body.String())
	}
}

// TestHandleCallback_YouTube_MultipleChannels_ExpectedMatches_OneSave
// proves the canonical use case: 3 channels discovered, expected
// matches the second — the token is saved exactly once on that
// single channel, NEVER on the other two.
func TestHandleCallback_YouTube_MultipleChannels_ExpectedMatches_OneSave(t *testing.T) {
	const expectedID = "UCaaaaaaaaaaaaaaaaaaaaa2"
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa1", Username: "Channel A"}},
		{Profile: models.PlatformProfile{PlatformUserID: expectedID, Username: "Channel B"}},
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa3", Username: "Channel C"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
					AccessToken: "yt-bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	// Fixed account-ID <-> channel-ID mapping so vault.Save can be
	// reverse-traced to the channel it was attached to.
	accountIDsByChannel := map[string]int64{
		"UCaaaaaaaaaaaaaaaaaaaaa1": 101,
		expectedID:                 102,
		"UCaaaaaaaaaaaaaaaaaaaaa3": 103,
	}
	attachedChannels := []string{}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			id, ok := accountIDsByChannel[profile.PlatformUserID]
			if !ok {
				return nil, fmt.Errorf("unexpected channel %q in attachFn", profile.PlatformUserID)
			}
			attachedChannels = append(attachedChannels, profile.PlatformUserID)
			return &models.PlatformAccount{
				ID: id, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID, Username: profile.Username,
			}, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	setOAuthExpectedChannelCookieForTest(req, "youtube", "test-state", expectedID)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(attachedChannels) != 1 {
		t.Fatalf("attachFn must be called exactly once (only expected channel); got %d calls for channels %v", len(attachedChannels), attachedChannels)
	}
	if attachedChannels[0] != expectedID {
		t.Errorf("attachFn must target expected channel %q; got %q", expectedID, attachedChannels[0])
	}
	if authorizer.tokenWriteCount() != 1 {
		t.Fatalf("tokenWrites must be exactly once; got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	w0 := authorizer.tokenWrites[0]
	if w0.AccountID != accountIDsByChannel[expectedID] {
		t.Errorf("tokenWrites[0] accountID: want %d (channel %q), got %d", accountIDsByChannel[expectedID], expectedID, w0.AccountID)
	}
	if w0.AccessToken != "yt-bearer" {
		t.Errorf("tokenWrites[0] access: want yt-bearer, got %q", w0.AccessToken)
	}
}

// TestHandleCallback_YouTube_ExpectedNoMatch_Conflict proves that an
// expected_channel_id which does NOT appear in channels.list(mine=true)
// returns 409 and saves no token — the operator authenticated the wrong
// Google account (or the inventory imported a Brand Account that has
// since been moved / removed).
func TestHandleCallback_YouTube_ExpectedNoMatch_Conflict(t *testing.T) {
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa1", Username: "Channel A"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
					AccessToken: "bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	setOAuthExpectedChannelCookieForTest(req, "youtube", "test-state", "UCaaaaaaaaaaaaaaaaaaaaaZ")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("mismatched expected: want 409, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("mismatch must NOT write tokens; got %d write(s)", authorizer.tokenWriteCount())
	}
	if authorizer.authorizeCalls.Load() != 0 {
		t.Fatalf("mismatch must NOT invoke AuthorizeChannel (channels.list guard rejects pre-tx); got %d call(s)", authorizer.authorizeCalls.Load())
	}
	if !strings.Contains(w.Body.String(), "does not match expected channel") {
		t.Errorf("response body should reference the mismatch, got %q", w.Body.String())
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

// ---------------------------------------------------------------------------
// OAuth state CSRF protection (verdict §2) tests
// ---------------------------------------------------------------------------

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

// TestPlatformMetaIsRejected (Taglio 5c) proves that a request with
// platform="meta" returns 404 unsupported_platform. The legacy composite
// Meta provider was split into instagram, facebook, and threads — the
// "meta" string must no longer be a valid platform identifier anywhere.
//
// SPRINT 7.1 (P0#14): the OAuth routes are now mounted behind
// oauthSessionRedirect, so a request without an InstaEdit session to
// an unsupported platform is 302'd to /login (no leak of the provider
// roster). When a valid session IS present, the inner handleLogin /
// handleCallback returns 404 unsupported_provider as before — that's
// the contract the test asserts below.
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

// TestHandleLogin_RequireSession_RedirectsToLogin (SPRINT 7.1 P0#14):
// the OAuth start route 302-redirects to FRONTEND_URL/login?next=...
// when no InstaEdit session is present. The platform roster is no
// longer enumerable by unauthenticated probes — both supported and
// unsupported providers behave identically (redirect) without a
// session, so an attacker can't tell registered platforms from
// unregistered ones just by hitting /login. The supported-provider
// check runs AFTER session validation, so a valid session is
// required to differentiate.
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

// TestHandleCallback_RequireSession_RedirectsToLogin (SPRINT 7.1
// P0#14): the OAuth callback route mirrors the login route — any
// hit without a valid InstaEdit session is a 302 to /login. This
// closes the path where an attacker can simply open the browser
// at /api/v1/auth/{provider}/callback?code=...&state=test-state
// without ever being authenticated.
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
