package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockPoolProvider extends mockProvider with the two optional
// YouTube OAuth Client Pool capabilities (YouTubePoolAwareLogin +
// YouTubePoolAwareCallback) plus AccountDiscoverer so the callback can
// exercise the full YouTube attach path. It records the pool client
// used to build the login URL and the client used for the code→token
// exchange so tests can assert the "same client from state" invariant.
type mockPoolProvider struct {
	mockProvider
	discoverFn func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error)
	// poolLoginOptions (R7) captures the OAuthLoginOptions the login
	// handler passed to the pool login URL builder, so tests can assert
	// the prompt=consent reduction (healthy reconnect → SelectAccount
	// only).
	poolLoginClient               *services.YouTubeOAuthClientConfig
	poolLoginOptions              services.OAuthLoginOptions
	poolCallbackClient            *services.YouTubeOAuthClientConfig
	poolCallbackFn                func(ctx context.Context, state, code string, client *services.YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error)
	handleCallbackWithClientCalls int
}

func (m *mockPoolProvider) DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
	if m.discoverFn != nil {
		return m.discoverFn(ctx, accessToken, platformUserID)
	}
	return nil, fmt.Errorf("DiscoverAccounts not implemented")
}

func (m *mockPoolProvider) GetLoginURLWithPoolClient(state string, options services.OAuthLoginOptions, client *services.YouTubeOAuthClientConfig) string {
	m.poolLoginClient = client
	m.poolLoginOptions = options
	return "https://auth.youtube.com/oauth?state=" + state + "&client=" + client.ClientID
}

func (m *mockPoolProvider) HandleCallbackWithClient(ctx context.Context, state, code string, client *services.YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error) {
	m.handleCallbackWithClientCalls++
	m.poolCallbackClient = client
	if m.poolCallbackFn != nil {
		return m.poolCallbackFn(ctx, state, code, client)
	}
	return successCallback(ctx, state, code)
}

// newTestPoolRegistry returns a two-client pool registry used by the
// pool-state handler tests. It wires a ZERO-usage counter (every client
// reports 0 active grants): SelectForNewConnection is capacity-aware
// but, with an empty subject or equal usage, deterministically returns
// youtube_pool_a (registration-order tie-break).
func newTestPoolRegistry(t *testing.T) *services.YouTubeOAuthClientRegistry {
	t.Helper()
	return newTestPoolRegistryWithUsage(t, nil)
}

// fakePoolUsageCounter is a deterministic services.OAuthClientUsageCounter
// for handler tests: it returns the configured per-client active-grant
// count for any subject. The subject is ignored (fixtures are
// single-subject) — the tests assert the selector received a subject
// AND picked by capacity, which is the least-loaded math the counter
// feeds. Setting countErr makes every CountActiveRefreshTokens call
// fail (pins the registry's fail-closed contract at the router layer).
type fakePoolUsageCounter struct {
	usage    map[string]int64 // oauthClientKey -> active refresh grants
	countErr error
}

func (f *fakePoolUsageCounter) CountActiveRefreshTokens(_ context.Context, _, oauthClientKey string) (int64, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	if f == nil || f.usage == nil {
		return 0, nil
	}
	return f.usage[oauthClientKey], nil
}

var _ services.OAuthClientUsageCounter = (*fakePoolUsageCounter)(nil)

// newTestPoolRegistryWithUsage returns the two-client pool registry
// with a capacity counter wired (usage maps client key → active
// refresh grants). Pass nil to keep the deterministic no-counter
// fallback (newTestPoolRegistry).
func newTestPoolRegistryWithUsage(t *testing.T, usage map[string]int64) *services.YouTubeOAuthClientRegistry {
	t.Helper()
	reg, err := services.NewYouTubeOAuthClientRegistry([]services.YouTubeOAuthClientConfig{
		{
			Key:          "youtube_pool_a",
			ClientID:     "pool-a-client-id",
			ClientSecret: "pool-a-client-secret-at-least-32-chars!!",
			RedirectURI:  "https://instaedit.example.com/oauth/youtube/callback",
		},
		{
			Key:          "youtube_pool_b",
			ClientID:     "pool-b-client-id",
			ClientSecret: "pool-b-client-secret-at-least-32-chars!!",
			RedirectURI:  "https://instaedit.example.com/oauth/youtube/callback",
		},
	}, services.WithYouTubeOAuthClientUsageCounter(&fakePoolUsageCounter{usage: usage}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	return reg
}

func stateParamFromURL(t *testing.T, loc string) string {
	t.Helper()
	_, after, ok := strings.Cut(loc, "state=")
	if !ok {
		t.Fatalf("state= not found in redirect: %s", loc)
	}
	stateParam, _, _ := strings.Cut(after, "&")
	return stateParam
}

// TestHandleLogin_YouTubePool_SignedStateCarriesPoolClient verifies that
// with a pool registry wired, /auth/youtube/login:
//   - selects a pool client (youtube_pool_a — the least-loaded fallback)
//     and builds the consent URL against it;
//   - issues a signed JWT state (2 dots) instead of the 43-char CSRF
//     nonce, carrying workspace_id (from the session identity) + a
//     single-use jti persisted in the nonce store;
//   - round-trips the selected client key in the sibling
//     oauth_state_youtube_oauth_client cookie as "<jti>:<clientKey>"
//     (NOT in the JWT — Google echoes the URL state verbatim);
//   - sets NO cookie-backed CSRF state (the signed state is
//     self-verifying).
func TestHandleLogin_YouTubePool_SignedStateCarriesPoolClient(t *testing.T) {
	poolProvider := &mockPoolProvider{mockProvider: mockProvider{platform: "youtube"}}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, &mockUserStore{}, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.youtube.com/oauth?") {
		t.Fatalf("login must target the YouTube consent URL, got %q", loc)
	}
	if poolProvider.poolLoginClient == nil || poolProvider.poolLoginClient.Key != "youtube_pool_a" {
		t.Fatalf("login URL must be built with pool client youtube_pool_a, got %+v", poolProvider.poolLoginClient)
	}

	state := stateParamFromURL(t, loc)
	if strings.Count(state, ".") != 2 {
		t.Fatalf("pool-mode state must be a signed JWT (2 dots), got %q", state)
	}
	// The state verifies against the router's own auth Manager and
	// carries the expected claims (workspace_id + jti; the client key
	// is deliberately NOT a claim).
	claims, verr := r.auth.VerifyOAuthFlowState(state)
	if verr != nil {
		t.Fatalf("verify issued oauth flow state: %v", verr)
	}
	if claims.WorkspaceID != 1 {
		t.Errorf("state workspace_id: want 1 (from session identity), got %d", claims.WorkspaceID)
	}
	// The jti was persisted for single-use consumption.
	if len(nonceStore.created) != 1 {
		t.Fatalf("nonce store: want 1 created nonce, got %d", len(nonceStore.created))
	}
	if !nonceStore.created[claims.ID].expiresAt.After(claims.ExpiresAt.Time.Add(-2 * time.Minute)) {
		t.Errorf("nonce store expiry (%s) must track the JWT expiry (%s)", nonceStore.created[claims.ID].expiresAt, claims.ExpiresAt.Time)
	}
	// The selected client key travels in the sibling cookie, bound to
	// the jti — never in the signed state.
	var clientCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateOAuthClientCookieName("youtube") {
			clientCookie = c
		}
	}
	if clientCookie == nil || clientCookie.MaxAge <= 0 {
		t.Fatalf("pool mode must set the sibling oauth_state_youtube_oauth_client cookie; got %+v", clientCookie)
	}
	wantCookieValue := claims.ID + ":youtube_pool_a"
	if clientCookie.Value != wantCookieValue {
		t.Errorf("sibling client cookie: want %q, got %q", wantCookieValue, clientCookie.Value)
	}
	if !clientCookie.HttpOnly || !clientCookie.Secure {
		t.Errorf("sibling client cookie must be HttpOnly+Secure; got %+v", clientCookie)
	}
	// No CSRF cookie in pool mode.
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("youtube") && c.MaxAge > 0 {
			t.Errorf("pool mode must NOT set the cookie-backed CSRF state; got %+v", c)
		}
	}
}

// TestHandleLogin_YouTubePool_ExpectedChannelBakedIntoState verifies the
// channel-binding hint travels inside the signed state (not a sibling
// cookie) when a pool registry is wired.
func TestHandleLogin_YouTubePool_ExpectedChannelBakedIntoState(t *testing.T) {
	const expectedChannel = "UCabcdefghijklmnopqrstuv"
	poolProvider := &mockPoolProvider{mockProvider: mockProvider{platform: "youtube"}}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, &mockUserStore{}, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login?expected_channel_id="+expectedChannel, nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	state := stateParamFromURL(t, w.Header().Get("Location"))
	claims, verr := r.auth.VerifyOAuthFlowState(state)
	if verr != nil {
		t.Fatalf("verify issued oauth flow state: %v", verr)
	}
	if claims.ExpectedChannelID != expectedChannel {
		t.Errorf("state expected_channel_id: want %q, got %q", expectedChannel, claims.ExpectedChannelID)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateExpectedChannelCookieName("youtube") && c.MaxAge > 0 {
			t.Errorf("pool mode must NOT issue the sibling expected-channel cookie (the hint is in the state); got %+v", c)
		}
	}
}

// TestHandleCallback_YouTubePool_ExchangesWithStateClient is the core
// "callback always uses the state's client" test: the code→token
// exchange must route to HandleCallbackWithClient with exactly
// youtube_pool_a (never the legacy HandleCallback), the single-use
// nonce must be consumed, and the account attach completes.
func TestHandleCallback_YouTubePool_ExchangesWithStateClient(t *testing.T) {
	const expectedChannel = "UC012345678901234567890123"
	poolProvider := &mockPoolProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				// The legacy exchange MUST NOT run for a pool-backed state.
				t.Error("legacy HandleCallback must not be called for a pool-backed state")
				return nil, nil, fmt.Errorf("legacy path called")
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{{Profile: models.PlatformProfile{PlatformUserID: expectedChannel, Username: "Pool Channel"}}}, nil
		},
		poolCallbackFn: func(ctx context.Context, state, code string, client *services.YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error) {
			return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
				AccessToken: "pool-bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
			}, nil
		},
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
		WithChannelAuthorizer(authorizer),
	)

	// Issue the state exactly as handleLogin would (client selected at
	// login time + nonce persisted + sibling client cookie bound to the
	// jti).
	signed, nonce, expiresAt, err := r.auth.IssueOAuthFlowState(expectedChannel, 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if err := nonceStore.Create(nonce, expectedChannel, expiresAt); err != nil {
		t.Fatalf("Create nonce: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state="+url.QueryEscape(signed), nil)
	setOAuthClientCookieForTest(req, "youtube", nonce, "youtube_pool_a")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if poolProvider.poolCallbackClient == nil || poolProvider.poolCallbackClient.Key != "youtube_pool_a" {
		t.Fatalf("callback must exchange with the state-selected client youtube_pool_a, got %+v", poolProvider.poolCallbackClient)
	}
	if poolProvider.handleCallbackWithClientCalls != 1 {
		t.Errorf("HandleCallbackWithClient calls: want 1, got %d", poolProvider.handleCallbackWithClientCalls)
	}
	if !nonceStore.consumed[nonce] {
		t.Fatal("state nonce must be consumed after the callback (single-use)")
	}
	if authorizer.tokenWriteCount() != 1 {
		t.Fatalf("tokenWrites: want 1, got %d", authorizer.tokenWriteCount())
	}
	// R7 — the pool client that issued the grant is threaded from the
	// signed state into AuthorizeChannel so the reconnect persists the
	// SAME client that must later refresh the token.
	if authorizer.lastClientKey != "youtube_pool_a" {
		t.Errorf("authorizer must receive the state's oauth_client_key youtube_pool_a, got %q", authorizer.lastClientKey)
	}
}

// TestHandleCallback_YouTubePool_ReplayReturns410 verifies the
// single-use contract end-to-end: a second callback with the same
// signed state is rejected with 410 Gone.
func TestHandleCallback_YouTubePool_ReplayReturns410(t *testing.T) {
	const expectedChannel = "UC012345678901234567890123"
	poolProvider := &mockPoolProvider{
		mockProvider: mockProvider{platform: "youtube"},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{{Profile: models.PlatformProfile{PlatformUserID: expectedChannel, Username: "Pool Channel"}}}, nil
		},
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	signed, nonce, expiresAt, err := r.auth.IssueOAuthFlowState(expectedChannel, 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if err := nonceStore.Create(nonce, expectedChannel, expiresAt); err != nil {
		t.Fatalf("Create nonce: %v", err)
	}

	doCallback := func() int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state="+url.QueryEscape(signed), nil)
		setOAuthClientCookieForTest(req, "youtube", nonce, "youtube_pool_a")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		return w.Code
	}

	if code := doCallback(); code != http.StatusOK {
		t.Fatalf("first callback: want 200, got %d", code)
	}
	if code := doCallback(); code != http.StatusGone {
		t.Fatalf("second callback: want 410 Gone, got %d", code)
	}
}

// TestHandleCallback_YouTubePool_ProviderNotPoolAware_FailsClosed pins
// the fail-closed posture: if the state carries an oauth_client_key but
// the provider cannot exchange with a pool client, the callback must
// fail with 500 — never silently fall back to the legacy exchange.
func TestHandleCallback_YouTubePool_ProviderNotPoolAware_FailsClosed(t *testing.T) {
	// Plain mockProvider: no HandleCallbackWithClient capability.
	legacy := &mockProvider{
		platform: "youtube",
		handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
			t.Error("legacy HandleCallback must NOT run when a pool state is present")
			return nil, nil, fmt.Errorf("legacy path called")
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(legacy, &mockUserStore{}, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	signed, nonce, expiresAt, err := r.auth.IssueOAuthFlowState("", 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if err := nonceStore.Create(nonce, "", expiresAt); err != nil {
		t.Fatalf("Create nonce: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state="+url.QueryEscape(signed), nil)
	setOAuthClientCookieForTest(req, "youtube", nonce, "youtube_pool_a")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 (fail-closed), got %d: %s", w.Code, w.Body.String())
	}
	if legacy.handleCallbackCalls != 0 {
		t.Errorf("legacy HandleCallback must not be called; got %d calls", legacy.handleCallbackCalls)
	}
}

// TestHandleCallback_YouTubePool_MissingClientCookie_FailsClosed pins
// the cookie transport contract: an oauth-flow state whose sibling
// oauth_state_youtube_oauth_client cookie is missing (or carries a
// stale jti prefix) must fail closed with 400 — the callback must
// never guess or silently fall back to the legacy single-client
// exchange when the state was issued against a pool client.
func TestHandleCallback_YouTubePool_MissingClientCookie_FailsClosed(t *testing.T) {
	const expectedChannel = "UC012345678901234567890123"
	poolProvider := &mockPoolProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				t.Error("legacy HandleCallback must not be called when the pool client cookie is missing")
				return nil, nil, fmt.Errorf("legacy path called")
			},
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, &mockUserStore{}, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	signed, nonce, expiresAt, err := r.auth.IssueOAuthFlowState(expectedChannel, 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if err := nonceStore.Create(nonce, expectedChannel, expiresAt); err != nil {
		t.Fatalf("Create nonce: %v", err)
	}

	// No sibling client cookie at all.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state="+url.QueryEscape(signed), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing client cookie: want 400, got %d: %s", w.Code, w.Body.String())
	}
	if poolProvider.handleCallbackWithClientCalls != 0 {
		t.Errorf("no pool exchange must run without the client cookie; got %d calls", poolProvider.handleCallbackWithClientCalls)
	}
}

// TestHandleCallback_YouTubePool_ReconnectReusesExistingConnection is
// the R7 end-to-end reconnect certification at the router layer: a
// SECOND callback for the SAME expected_channel — same subject, same
// oauth_client_key from the state — must NOT create a second
// platform_account. The attach layer returns ErrAccountAlreadyLinked,
// the callback loads the existing row, and AuthorizeChannel is invoked
// again on the SAME account id with the SAME pool client, persisting
// the refreshed token again (one channel → one active connection →
// one canonical refresh token).
//
// Connection-identity reuse itself (the subject-keyed UPSERT returning
// the SAME oauth_connection_id) is certified at the service layer
// (TestAuthorizeChannel_ReconnectSameSubjectReusesConnection) and the
// migration 100 uniqueness constraint
// (TestMigration100_ActiveGrantUniquePerChannelClient); the router
// layer certifies the plumbing: existing account re-threaded, no
// parallel row created, client key stable across reconnects.
func TestHandleCallback_YouTubePool_ReconnectReusesExistingConnection(t *testing.T) {
	const expectedChannel = "UC012345678901234567890123"
	poolProvider := &mockPoolProvider{
		mockProvider: mockProvider{platform: "youtube"},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{{Profile: models.PlatformProfile{PlatformUserID: expectedChannel, Username: "Pool Channel"}}}, nil
		},
		poolCallbackFn: func(ctx context.Context, state, code string, client *services.YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error) {
			return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
				AccessToken:       "pool-bearer-reconnect",
				ProviderSubjectID: "google-subject-reconnect",
				TokenType:         models.TokenTypeBearer,
				ExpiresIn:         3600,
			}, nil
		},
	}

	attachCalls := 0
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			attachCalls++
			if attachCalls == 1 {
				// First callback: brand-new channel → create the account.
				return &models.PlatformAccount{ID: 10, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
			}
			// Reconnect: already linked — the callback must load the
			// existing row instead of creating a parallel account.
			return nil, repository.ErrAccountAlreadyLinked
		},
		findPlatformAccountByTupleFn: func(platform, platformUserID string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, UserID: 1, Platform: platform, PlatformUserID: platformUserID}, nil
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
		WithChannelAuthorizer(authorizer),
	)

	doCallback := func() int {
		signed, nonce, expiresAt, err := r.auth.IssueOAuthFlowState(expectedChannel, 1)
		if err != nil {
			t.Fatalf("IssueOAuthFlowState: %v", err)
		}
		if err := nonceStore.Create(nonce, expectedChannel, expiresAt); err != nil {
			t.Fatalf("Create nonce: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state="+url.QueryEscape(signed), nil)
		setOAuthClientCookieForTest(req, "youtube", nonce, "youtube_pool_a")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		return w.Code
	}

	// First connect: account 10 is created and authorized.
	if code := doCallback(); code != http.StatusOK {
		t.Fatalf("first callback: want 200, got %d", code)
	}
	// Reconnect: SAME channel, SAME client key from state → the existing
	// account 10 is re-authorized on the SAME connection (777).
	if code := doCallback(); code != http.StatusOK {
		t.Fatalf("reconnect callback: want 200, got %d", code)
	}

	if authorizer.authorizeCalls.Load() != 2 {
		t.Errorf("AuthorizeChannel calls: want 2 (connect + reconnect), got %d", authorizer.authorizeCalls.Load())
	}
	// The reconnect must thread the SAME account id — no parallel row.
	if authorizer.lastAccountID != 10 {
		t.Errorf("last account id: want 10 (existing account reused), got %d", authorizer.lastAccountID)
	}
	if authorizer.lastClientKey != "youtube_pool_a" {
		t.Errorf("reconnect must keep the state's oauth_client_key youtube_pool_a, got %q", authorizer.lastClientKey)
	}
	if attachCalls != 2 {
		t.Errorf("attach calls: want 2 (create + already-linked sentinel), got %d", attachCalls)
	}
	// Both passes must persist the (refreshed) canonical token for the
	// SAME account — the reconnect rewrites the grant's token in place
	// instead of leaving a second token lineage behind.
	if writes := authorizer.tokenWriteCount(); writes != 2 {
		t.Errorf("token writes: want 2 (connect + reconnect), got %d", writes)
	}
}

// TestHandleCallback_YouTubePool_StaleClientCookie_FailsClosed pins that
// a sibling client cookie bound to a DIFFERENT jti (stale flow) cannot
// steer the callback: the jti prefix must match the current state's
// single-use nonce.
func TestHandleCallback_YouTubePool_StaleClientCookie_FailsClosed(t *testing.T) {
	const expectedChannel = "UC012345678901234567890123"
	poolProvider := &mockPoolProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				t.Error("legacy HandleCallback must not be called with a stale client cookie")
				return nil, nil, fmt.Errorf("legacy path called")
			},
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, &mockUserStore{}, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	signed, nonce, expiresAt, err := r.auth.IssueOAuthFlowState(expectedChannel, 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if err := nonceStore.Create(nonce, expectedChannel, expiresAt); err != nil {
		t.Fatalf("Create nonce: %v", err)
	}

	// Sibling cookie bound to a DIFFERENT (stale) jti.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state="+url.QueryEscape(signed), nil)
	setOAuthClientCookieForTest(req, "youtube", "deadbeefdeadbeef", "youtube_pool_b")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("stale client cookie: want 400, got %d: %s", w.Code, w.Body.String())
	}
	if poolProvider.handleCallbackWithClientCalls != 0 {
		t.Errorf("no pool exchange must run with a stale client cookie; got %d calls", poolProvider.handleCallbackWithClientCalls)
	}
}
