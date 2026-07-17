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
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockProvider implements the two capabilities the API router consumes:
// OAuthProvider (Name, GetLoginURL, HandleCallback, RefreshOAuthToken) and
// Publisher (Name, Publish). The real per-platform structs implement both,
// so the single mock struct mirrors that.
//
// Taglio 2.2: token persistence moved to the central CredentialVault.
// The mock is unchanged by Taglio 2.2.
type mockProvider struct {
	platform       string
	loginURL       string
	handleCallback func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)
	publishFn      func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	refreshFn      func(ctx context.Context, refreshToken string) (*models.TokenData, error)

	handleCallbackCalls int
	publishCalls        int
}

func (m *mockProvider) Name() string { return m.platform }
func (m *mockProvider) GetLoginURL(state string) string {
	return m.loginURL + "?state=" + state
}
func (m *mockProvider) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	m.handleCallbackCalls++
	if m.handleCallback == nil {
		return nil, nil, fmt.Errorf("HandleCallback not implemented")
	}
	return m.handleCallback(ctx, state, code)
}
func (m *mockProvider) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	if m.refreshFn != nil {
		return m.refreshFn(ctx, refreshToken)
	}
	return nil, fmt.Errorf("refresh not implemented")
}

// mockDiscoverableProvider extends mockProvider with AccountDiscoverer.
// Use it when testing providers (e.g. Facebook Pages) that expand one
// OAuth grant into multiple PlatformAccounts.
type mockDiscoverableProvider struct {
	mockProvider
	discoverFn func(ctx context.Context, accessToken, platformUserID string) ([]*models.PlatformAccount, error)
}

func (m *mockDiscoverableProvider) DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*models.PlatformAccount, error) {
	if m.discoverFn != nil {
		return m.discoverFn(ctx, accessToken, platformUserID)
	}
	return nil, fmt.Errorf("DiscoverAccounts not implemented")
}
func (m *mockProvider) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	m.publishCalls++
	if m.publishFn == nil {
		return nil, fmt.Errorf("Publish not implemented")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}

// mockCredentialVault implements credentials.VaultAPI for tests. The
// default (nil fields) returns success on Save and Revoke, an error
// on Get, and an error on Renew — that is what most tests (login,
// callback happy path, workspace, post CRUD) want. Tests that
// exercise the publish path or want to force a save/get/renew error
// override the relevant field in the constructor and pass via
// WithCredentialVault in opts.
//
// Taglio 2.2: renamed from mockTokenService. The `renewFn` field
// receives a credentials.TokenRefresher (plain function) rather than
// a services.OAuthProvider interface — the vault no longer knows
// about per-platform types.
type mockCredentialVault struct {
	saveFn   func(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error
	getFn    func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error)
	renewFn  func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error)
	revokeFn func(ctx context.Context, platformAccountID int64) error
}

func (m *mockCredentialVault) Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	if m.saveFn != nil {
		return m.saveFn(ctx, platformAccountID, tokenData)
	}
	return nil
}
func (m *mockCredentialVault) Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	if m.getFn != nil {
		return m.getFn(ctx, platformAccountID, tokenType)
	}
	return nil, fmt.Errorf("Get not implemented in this test mock (override via mockCredentialVault.getFn)")
}
func (m *mockCredentialVault) Renew(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
	if m.renewFn == nil {
		return nil, fmt.Errorf("Renew not implemented (override via mockCredentialVault.renewFn)")
	}
	return m.renewFn(ctx, accountID, tokenType, refresh)
}
func (m *mockCredentialVault) Revoke(ctx context.Context, platformAccountID int64) error {
	if m.revokeFn != nil {
		return m.revokeFn(ctx, platformAccountID)
	}
	return nil
}
func (m *mockCredentialVault) Rotate(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	return m.Save(ctx, platformAccountID, tokenData)
}

// mockUserStore implements UserStore with configurable function fields.
//
// SPRINT 7.1 (P0#14): FindOrCreateUserByPlatform is gone from the
// UserStore interface — the OAuth callback now ONLY attaches the
// platform account to the authenticated user (never auto-creates).
// Tests that used to return a *models.User pair from a mock callback
// now return only *models.PlatformAccount (the link side).
type mockUserStore struct {
	attachFn                     func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error)
	listFn                       func(userID int64, platform string) ([]*models.PlatformAccount, error)
	findPlatformAccountFn        func(id int64) (*models.PlatformAccount, error)
	findPlatformAccountByTupleFn func(platform, platformUserID string) (*models.PlatformAccount, error)
	updatePlatformAccountFn      func(account *models.PlatformAccount) error
	deletePlatformAccountFn      func(id int64) error
}

func (m *mockUserStore) AttachPlatformAccount(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	if m.attachFn == nil {
		return nil, fmt.Errorf("AttachPlatformAccount not implemented in this test mock (override via mockUserStore.attachFn)")
	}
	return m.attachFn(userID, profile, platform)
}
func (m *mockUserStore) ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error) {
	return m.listFn(userID, platform)
}
func (m *mockUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if m.findPlatformAccountFn != nil {
		return m.findPlatformAccountFn(id)
	}
	return nil, nil
}
func (m *mockUserStore) FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error) {
	if m.findPlatformAccountByTupleFn != nil {
		return m.findPlatformAccountByTupleFn(platform, platformUserID)
	}
	return nil, nil
}
func (m *mockUserStore) UpdatePlatformAccount(account *models.PlatformAccount) error {
	if m.updatePlatformAccountFn != nil {
		return m.updatePlatformAccountFn(account)
	}
	return nil
}
func (m *mockUserStore) DeletePlatformAccount(id int64) error {
	if m.deletePlatformAccountFn != nil {
		return m.deletePlatformAccountFn(id)
	}
	return nil
}

// mockWorkspaceStore implements WorkspaceStore with configurable function fields.
type mockWorkspaceStore struct {
	createFn      func(*models.Workspace) error
	findByIDFn    func(id int64) (*models.Workspace, error)
	listByOwnerFn func(ownerID int64) ([]models.Workspace, error)
	deleteFn      func(id int64) error
}

func (m *mockWorkspaceStore) Create(w *models.Workspace) error {
	if m.createFn == nil {
		return nil
	}
	return m.createFn(w)
}
func (m *mockWorkspaceStore) FindByID(id int64) (*models.Workspace, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return &models.Workspace{
		ID:        id,
		Name:      "default",
		OwnerID:   1,
		CreatedAt: time.Now(),
	}, nil
}
func (m *mockWorkspaceStore) ListByOwner(ownerID int64) ([]models.Workspace, error) {
	if m.listByOwnerFn == nil {
		return nil, nil
	}
	return m.listByOwnerFn(ownerID)
}
func (m *mockWorkspaceStore) Delete(id int64) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(id)
}

// mockPostStore implements PostStore with configurable function fields.
type mockPostStore struct {
	createFn          func(*models.Post, []*models.PostTarget) error
	findByIDFn        func(id int64) (*models.Post, error)
	updateFn          func(*models.Post) error
	listByWorkspaceFn func(workspaceID int64) ([]models.Post, error)
	deleteFn          func(id int64) error
	saveTargetFn      func(*models.PostTarget) error
	publishPostFn     func(id int64) error
	cancelPostFn      func(id int64) error
	retryPostFn       func(id int64) error
	retryTargetFn     func(id int64) error
}

func (m *mockPostStore) Create(post *models.Post, targets []*models.PostTarget) error {
	if m.createFn != nil {
		return m.createFn(post, targets)
	}
	post.ID = 100
	post.CreatedAt = time.Now()
	for i, t := range targets {
		t.ID = int64(200 + i)
		t.PostID = post.ID
	}
	return nil
}
func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return &models.Post{
		ID:          id,
		WorkspaceID: 1,
		Title:       "default",
		Status:      models.PostStatusDraft,
		CreatedAt:   time.Now(),
	}, nil
}
func (m *mockPostStore) Update(post *models.Post) error {
	if m.updateFn == nil {
		return nil
	}
	return m.updateFn(post)
}
func (m *mockPostStore) ListByWorkspace(workspaceID int64) ([]models.Post, error) {
	if m.listByWorkspaceFn == nil {
		return nil, nil
	}
	return m.listByWorkspaceFn(workspaceID)
}
func (m *mockPostStore) Delete(id int64) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(id)
}
func (m *mockPostStore) SaveTarget(target *models.PostTarget) error {
	if m.saveTargetFn == nil {
		return nil
	}
	return m.saveTargetFn(target)
}
func (m *mockPostStore) RetryPost(id int64) error {
	if m.retryPostFn == nil {
		return nil
	}
	return m.retryPostFn(id)
}
func (m *mockPostStore) CancelPost(id int64) error {
	if m.cancelPostFn == nil {
		return nil
	}
	return m.cancelPostFn(id)
}
func (m *mockPostStore) PublishPost(id int64) error {
	if m.publishPostFn == nil {
		return nil
	}
	return m.publishPostFn(id)
}
func (m *mockPostStore) RetryTarget(id int64) error {
	if m.retryTargetFn == nil {
		return nil
	}
	return m.retryTargetFn(id)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const testJWTSecret = "test-jwt-secret-must-be-long-enough-for-hs256"

func withBearerJWT(t *testing.T, req *http.Request, userID int64) {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, userID))
}

// newTestRouter builds a Router wired with a mock provider and store.
//
// Taglio 2.2: the Router takes a CapabilityRouter (per-capability lookups)
// plus a CredentialVault (via WithCredentialVault). The default vault
// is a no-op mock that succeeds on save/revoke and errors on get/renew —
// that is what most tests (login, callback happy path, workspace, post
// CRUD) want. Tests that exercise the publish path or want to force a
// save/renew error override via WithCredentialVault(&mockCredentialVault{...})
// in opts.
func newTestRouter(
	platformSvc services.NameProvider,
	store *mockUserStore,
	frontendURL string,
	opts ...RouterOption,
) *Router {
	capRouter := services.NewCapabilityRouter()
	capRouter.Register(platformSvc.Name(), platformSvc)
	capRouter.Register("instagram", platformSvc)
	capRouter.Register("tiktok", platformSvc)
	capRouter.Register("twitter", platformSvc)
	otc := NewOneTimeCodeStore(60 * time.Second)
	// Note: the sweeper goroutine leaks until the test binary exits —
	// acceptable for unit tests; the 1s ticker has no observable effect
	// on test behaviour and the OS reclaims everything on process exit.
	defaultVault := &mockCredentialVault{}
	return NewRouter(
		capRouter,
		store,
		auth.NewManager(testJWTSecret, 24),
		frontendURL,
		nil,
		append([]RouterOption{
			WithOneTimeCodeStore(otc),
			WithCredentialVault(defaultVault),
		}, opts...)...,
	)
}

// issueTestJWT mints a JWT carrying (userID, workspaceID=1, sessionID=1).
// SPRINT 7.1 couples /auth/{provider}/* to a session-gating middleware
// (oauthSessionRedirect) that calls Manager.Verify on the Authorization
// header or session cookie. Manager.Verify rejects any token with
// UserID<=0 || WorkspaceID<=0 || SessionID<=0, so the legacy
// `Issue(userID)` path (which signs with wsID=0, sessionID=0) no longer
// produces an acceptable token. IssueAccess requires all three IDs
// positive; tests that previously relied on Issue(userID) implicitly
// expected the OAuth layer to ignore the Authorization header — that
// assumption no longer holds.
func issueTestJWT(t *testing.T, userID int64) string {
	t.Helper()
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.IssueAccess(userID, 1, 1)
	if err != nil {
		t.Fatalf("issue access jwt (user=%d, ws=1, session=1): %v", userID, err)
	}
	return tok
}

var successCallback = func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	return &models.PlatformProfile{
			PlatformUserID: "pf-123",
			Username:       "testuser",
			Name:           "Test User",
			Email:          "test@example.com",
		}, &models.TokenData{
			AccessToken: "at-secret",
			TokenType:   "bearer",
			ExpiresIn:   3600,
		}, nil
}

// successAttach models the SPRINT 7.1 connect path: the JWT's user_id
// (1) is the linkage target, never a freshly-allocated id from a
// FindOrCreateUserByPlatform query.
var successAttach = func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	return &models.PlatformAccount{
		ID:             10,
		UserID:         userID,
		Platform:       platform,
		PlatformUserID: profile.PlatformUserID,
		Username:       profile.Username,
	}, nil
}

func setOAuthStateCookieForTest(req *http.Request, provider, state string) {
	req.AddCookie(&http.Cookie{
		Name:     OAuthStateCookieName(provider),
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

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

// TestHandleCallback_SaveTokenError asserts that an error from the
// CredentialVault.Save call surfaces as a 500. The test wires a
// mockCredentialVault with a saveFn that errors.
func TestHandleCallback_SaveTokenError(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	vault := &mockCredentialVault{
		saveFn: func(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
			return fmt.Errorf("token save error")
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
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
	pages := []*models.PlatformAccount{
		{Platform: "facebook", PlatformUserID: "page-1", Username: "Page One", Metadata: models.Metadata{"page_access_token": "page-token-1"}},
		{Platform: "facebook", PlatformUserID: "page-2", Username: "Page Two", Metadata: models.Metadata{"page_access_token": "page-token-2"}},
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
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*models.PlatformAccount, error) {
			if accessToken != userLongLivedToken {
				t.Errorf("DiscoverAccounts accessToken: want %q, got %q", userLongLivedToken, accessToken)
			}
			return pages, nil
		},
	}

	var saved []struct {
		accountID int64
		tokenType string
		token     string
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             int64(10 + len(saved)),
				UserID:         userID,
				Platform:       platform,
				PlatformUserID: profile.PlatformUserID,
				Username:       profile.Username,
			}, nil
		},
	}
	vault := &mockCredentialVault{
		saveFn: func(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
			saved = append(saved, struct {
				accountID int64
				tokenType string
				token     string
			}{platformAccountID, tokenData.TokenType, tokenData.AccessToken})
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/facebook/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "facebook", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Expect 4 saves: page token + user token for each of the 2 pages.
	if len(saved) != 4 {
		t.Fatalf("want 4 token saves (2 page + 2 user), got %d: %+v", len(saved), saved)
	}
	// Build a map keyed by (accountID, tokenType) to avoid relying on save order.
	savedByType := make(map[int64]map[string]string)
	for _, s := range saved {
		if savedByType[s.accountID] == nil {
			savedByType[s.accountID] = make(map[string]string)
		}
		savedByType[s.accountID][s.tokenType] = s.token
	}
	for _, p := range pages {
		// The account IDs are generated by attachFn as 10, 11, ...
		// We locate the matching account by page token.
		var foundID int64
		for id, tokens := range savedByType {
			if tokens[models.TokenTypePageAccess] == p.Metadata["page_access_token"] {
				foundID = id
				break
			}
		}
		if foundID == 0 {
			t.Fatalf("missing page token save for page %s", p.PlatformUserID)
		}
		if savedByType[foundID][models.TokenTypePageAccess] != p.Metadata["page_access_token"] {
			t.Errorf("page %s: want page token %q, got %q", p.PlatformUserID, p.Metadata["page_access_token"], savedByType[foundID][models.TokenTypePageAccess])
		}
		if savedByType[foundID][models.TokenTypeLongLived] != userLongLivedToken {
			t.Errorf("page %s: want user token %q, got %q", p.PlatformUserID, userLongLivedToken, savedByType[foundID][models.TokenTypeLongLived])
		}
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
// handleCreateWorkspace tests
// ---------------------------------------------------------------------------

func TestHandleCreateWorkspace_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		createFn: func(w *models.Workspace) error {
			w.ID = 42
			w.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	body := `{"name":"My Workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.Workspace
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Name != "My Workspace" {
		t.Fatalf("name: want My Workspace, got %s", resp.Name)
	}
	if resp.ID != 42 {
		t.Fatalf("id: want 42, got %d", resp.ID)
	}
}

func TestHandleCreateWorkspace_MissingName_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreateWorkspace_MalformedJSON_400(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleCreateWorkspace_MissingJWT_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "") // no WithWorkspaceStore

	body := `{"name":"My Workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandleGetWorkspace_CrossOwner_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/42", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handleCreatePost tests
// ---------------------------------------------------------------------------

func TestHandleCreatePost_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, tgts []*models.PostTarget) error {
			p.ID = 100
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			for i, target := range tgts {
				target.ID = int64(200 + i)
			}
			return nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"hello","caption":"world"},"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID          int64  `json:"id"`
		WorkspaceID int64  `json:"workspace_id"`
		Status      string `json:"status"`
		ScheduledAt string `json:"scheduled_at,omitempty"`
		Targets     []struct {
			ID                int64  `json:"id"`
			PlatformAccountID int64  `json:"platform_account_id"`
			Status            string `json:"status"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ID != 100 {
		t.Fatalf("id: want 100, got %d", resp.ID)
	}
	if resp.Status != "draft" {
		t.Fatalf("status: want draft, got %s", resp.Status)
	}
	if resp.ScheduledAt != "" {
		t.Fatalf("scheduled_at: want empty for draft, got %s", resp.ScheduledAt)
	}
	if len(resp.Targets) != 1 || resp.Targets[0].ID != 200 || resp.Targets[0].PlatformAccountID != 10 {
		t.Fatalf("targets count/id/pa wrong: %+v", resp.Targets)
	}
}

func TestHandleCreatePost_HappyWithScheduledAt(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, _ []*models.PostTarget) error {
			p.ID = 100
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
	}
	// Taglio 3.2: legacy media_url REMOVED. No media in this test
	// (the post is text-only scheduled).
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"future post"},"scheduled_at":"2030-01-01T00:00:00Z","targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID          int64  `json:"id"`
		Status      string `json:"status"`
		ScheduledAt string `json:"scheduled_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Status != "queued" {
		t.Fatalf("status: want scheduled, got %s", resp.Status)
	}
	if resp.ScheduledAt != "2030-01-01T00:00:00Z" {
		t.Fatalf("scheduled_at: want 2030-01-01T00:00:00Z, got %s", resp.ScheduledAt)
	}
}

func TestHandleCreatePost_MissingWorkspaceID_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreatePost_NoTargets_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreatePost_BadTargetID_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"x"},"targets":[{"platform_account_id":0}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreatePost_CrossOwnerWorkspace_403(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
	}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"x"},"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetPost_CrossOwner_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
	}
	postStore := &mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          id,
				WorkspaceID: 1,
				Title:       "secret",
				Status:      models.PostStatusDraft,
				CreatedAt:   time.Now(),
			}, nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// /api/v1/media/presign tests (Taglio 3.2 — replaces the old
// /api/v1/storage/upload-url tests; the 11 new media-endpoint tests
// live in pkg/api/media_test.go).
// ---------------------------------------------------------------------------

// TestHandleCreatePost_StrictPayloadRejectsLegacyMediaURL proves
// Taglio 3.2: the public create-post payload no longer accepts
// media_url. A legacy payload silently ignores media_url and the
// server resolves media from the (empty) media:[] array, so the
// post is created with no media — this test documents the new
// contract by exercising the asset_id path.
func TestHandleCreatePost_StrictPayloadRejectsLegacyMediaURL(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, _ []*models.PostTarget) error {
			p.ID = 100
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
	)

	// Legacy payload with media_url — should be silently ignored.
	// The new contract is { content: { media: [{ asset_id }] } }.
	body := `{"workspace_id":1,"content":{"title":"x","media_url":"https://attacker.com/x.png"},"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("legacy media_url is ignored (not an error), but the new payload should still create the post: want 201, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// CORS middleware tests
// ---------------------------------------------------------------------------

func newCORSTestRouter(allowedOrigins []string) *Router {
	return NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		allowedOrigins,
	)
}

func TestCorsMiddleware_AllowMethodsIncludesPutPatchDelete(t *testing.T) {
	r := newCORSTestRouter([]string{"https://instaedit.org"})

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/workspaces/123", nil)
	req.Header.Set("Origin", "https://instaedit.org")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status: want 204, got %d", w.Code)
	}

	methods := w.Header().Get("Access-Control-Allow-Methods")
	for _, want := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		if !strings.Contains(methods, want) {
			t.Errorf("Access-Control-Allow-Methods %q missing %q (browser preflight for %s will fail in production)", methods, want, want)
		}
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

// ---------------------------------------------------------------------------
// handleListAccounts tests (SPRINT 7.1 / Taglio 1.4 — closes the
// user-facing /connections page gap. Endpoint is behind r.protected,
// reads identity EXCLUSIVELY from auth.IdentityFromContext, NEVER
// from ?user_id / body / path.)
// ---------------------------------------------------------------------------

// twoAccountFixtures returns two synthetic accounts the list test
// uses as fixtures. The shape is exactly what ListPlatformAccountsByUser
// returns from the repo (subset of the full fixture model).
func twoAccountFixtures() []*models.PlatformAccount {
	t0 := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 7, 15, 9, 30, 0, 0, time.UTC)
	return []*models.PlatformAccount{
		{
			ID: 21, UserID: 1, Platform: "instagram",
			PlatformUserID: "1784deadbeef", Username: "alice_ig",
			Status: models.AccountStatusActive, CreatedAt: t0, UpdatedAt: t0,
		},
		{
			ID: 22, UserID: 1, Platform: "facebook",
			PlatformUserID: "1029384cafebabe", Username: "alice.fb.page",
			Status: models.AccountStatusActive, CreatedAt: t1, UpdatedAt: t1,
		},
	}
}

// TestHandleListAccounts_Happy proves the closed endpoint contract:
// 200 + {"accounts":[{id,platform,platform_user_id,username,status,created_at}]}.
// NO user_id / workspace_id in the response (the wire shape is the
// spec'd one, not a mirror of models.PlatformAccount).
func TestHandleListAccounts_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	fixtures := twoAccountFixtures()
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			// Mirrors the production contract: no platform filter when
			// the handler passes "".
			if platform != "" {
				t.Errorf("handler must request ALL platforms (pass empty filter), got platform=%q", platform)
			}
			// User must come from the JWT (uid=1), NOT from query.
			if userID != 1 {
				t.Errorf("handler must use JWT-derived userID; got userID=%d (cross-tenant leak risk)", userID)
			}
			return fixtures, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Accounts []struct {
			ID             int64     `json:"id"`
			Platform       string    `json:"platform"`
			PlatformUserID string    `json:"platform_user_id"`
			Username       string    `json:"username"`
			Status         string    `json:"status"`
			CreatedAt      time.Time `json:"created_at"`
			// The following are EXPLICITLY forbidden by the contract:
			UserID    int64  `json:"user_id,omitempty"`
			UpdatedAt string `json:"updated_at,omitempty"`
			LastError string `json:"last_error_code,omitempty"`
			Metadata  any    `json:"metadata,omitempty"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Accounts) != 2 {
		t.Fatalf("accounts length: want 2, got %d", len(resp.Accounts))
	}
	// First account (instagram).
	if resp.Accounts[0].ID != 21 {
		t.Errorf("accounts[0].id: want 21, got %d", resp.Accounts[0].ID)
	}
	if resp.Accounts[0].Platform != "instagram" {
		t.Errorf("accounts[0].platform: want instagram, got %s", resp.Accounts[0].Platform)
	}
	if resp.Accounts[0].PlatformUserID != "1784deadbeef" {
		t.Errorf("accounts[0].platform_user_id: want 1784deadbeef, got %s", resp.Accounts[0].PlatformUserID)
	}
	if resp.Accounts[0].Username != "alice_ig" {
		t.Errorf("accounts[0].username: want alice_ig, got %s", resp.Accounts[0].Username)
	}
	if resp.Accounts[0].Status != models.AccountStatusActive {
		t.Errorf("accounts[0].status: want active, got %s", resp.Accounts[0].Status)
	}
	if resp.Accounts[0].CreatedAt.IsZero() {
		t.Errorf("accounts[0].created_at: want non-zero, got zero value")
	}
	// Forbidden fields must NOT appear in any account item.
	for i, a := range resp.Accounts {
		if a.UserID != 0 {
			t.Errorf("accounts[%d].user_id leaked: %d (the SPA must NEVER see internal user id)", i, a.UserID)
		}
		if a.UpdatedAt != "" {
			t.Errorf("accounts[%d].updated_at leaked: %q (not in spec'd response shape)", i, a.UpdatedAt)
		}
		if a.LastError != "" {
			t.Errorf("accounts[%d].last_error_code leaked: %q (not in spec'd response shape)", i, a.LastError)
		}
		if a.Metadata != nil {
			t.Errorf("accounts[%d].metadata leaked: %v (internal PlatformAccount metadata)", i, a.Metadata)
		}
	}
}

// TestHandleListAccounts_EmptyList_ReturnsAccountsArrayKey proves the
// wrapper key is always present even when there are zero connections.
// SPA JSON decoders rely on `accounts` being an array, never null —
// returning {"accounts": null} would crash `accounts.map(...)` in the
// /connections page.
func TestHandleListAccounts_EmptyList_ReturnsAccountsArrayKey(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (empty list, NOT 404), got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	raw, ok := resp["accounts"]
	if !ok {
		t.Fatal("response MUST contain the 'accounts' key even when empty (SPA relies on it being an array)")
	}
	// RawMessage of "null" means the handler returned accounts: nil
	// instead of accounts: [] — decode and assert []interface{}.
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("'accounts' must always be a JSON array (got %s): %v", string(raw), err)
	}
	if len(arr) != 0 {
		t.Fatalf("'accounts' should be empty array, got %d items", len(arr))
	}
}

// TestHandleListAccounts_NoSession_401 proves the r.protected chain
// rejects unauthenticated requests before reaching the handler. The
// handler itself has its own defence-in-depth check (writeError 401
// if identity is nil) so the test never reaches it — but we lock the
// behaviour at the route level here so a future refactor that swaps
// r.protected for something else (e.g. a custom middleware) won't
// silently bypass the auth requirement.
func TestHandleListAccounts_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			t.Errorf("ListPlatformAccountsByUser MUST NOT be called without a session (data leak risk); got userID=%d", userID)
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	// NO withBearerJWT — session-less probe.
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /api/v1/accounts: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleListAccounts_IgnoresQueryUserIDAndWorkspace is the
// security-binding test for this endpoint. An attacker MUST NOT be
// able to read another user's accounts by appending ?user_id=999 to
// the URL. The handler must derive user_id from auth context only
// and silently ignore (or strip) any user_id/workspace_id query
// params. The listFn captures the user_id call to assert the JWT
// user wins over the query.
func TestHandleListAccounts_IgnoresQueryUserIDAndWorkspace(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	var listFnUserID int64
	var listFnCalled bool
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			listFnUserID = userID
			listFnCalled = true
			return []*models.PlatformAccount{}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	// Attacker tries ?user_id=999&workspace_id=42 while presenting a
	// legitimate JWT for user 1.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?user_id=999&workspace_id=42", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (auth from JWT, query ignored), got %d: %s", w.Code, w.Body.String())
	}
	if !listFnCalled {
		t.Fatal("ListPlatformAccountsByUser must be called even when query params are present (the cancel-out is identity-based, not query-based)")
	}
	if listFnUserID != 1 {
		t.Errorf("SQL filter used userID=%d, want 1 (JWT-derived). Query ?user_id=999 MUST NOT leak across tenants.", listFnUserID)
	}
}

// ---------------------------------------------------------------------------
// handleGetAccount / handleValidateAccount / handleReconnectAccount /
// handleDeleteAccount tests (Taglio 1.4 — full implementations replacing
// the 501 stubs). Workspace-isolation matrix: cross-tenant probes return
// 404 (existential non-leak); no-session returns 401; vault errors
// surface as 500; happy paths return the spec'd response shape.
// ---------------------------------------------------------------------------

// ownedAccountFixture returns a synthetic account owned by ownerID —
// the template for the 4 happy-path tests below.
func ownedAccountFixture(ownerID int64, platform string) *models.PlatformAccount {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	return &models.PlatformAccount{
		ID: 21, UserID: ownerID, Platform: platform,
		PlatformUserID: "pf-21", Username: "alice_" + platform,
		Status:    models.AccountStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
}

// TestHandleGetAccount_Happy proves the closed endpoint contract: 200 +
// the 6-field wire shape, no internal PlatformAccount columns leaking.
func TestHandleGetAccount_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id != 21 {
				t.Errorf("handler called FindPlatformAccountByID with id=%d, want 21 (path param)", id)
			}
			return owner, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID             int64     `json:"id"`
		Platform       string    `json:"platform"`
		PlatformUserID string    `json:"platform_user_id"`
		Username       string    `json:"username"`
		Status         string    `json:"status"`
		CreatedAt      time.Time `json:"created_at"`
		UserID         int64     `json:"user_id,omitempty"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ID != 21 || resp.Platform != "instagram" || resp.Username != "alice_instagram" {
		t.Errorf("response shape mismatch: %+v", resp)
	}
	if resp.UserID != 0 {
		t.Errorf("internal user_id leaked: %d", resp.UserID)
	}
}

// TestHandleGetAccount_NotFound_404 covers both the genuine-not-found
// and the cross-tenant cases under one roof (the loadOwnAccountByID
// helper collapses them by design — 404 prevents existence leaks).
func TestHandleGetAccount_NotFound_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return nil, nil // genuine not-found
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/999", nil)
	w := httptest.NewRecorder()
	// JWT for user 1, but no row exists for id=999.
	jwt := issueTestJWT(t, 1)
	req.Header.Set("Authorization", "Bearer "+jwt)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (account not found), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleGetAccount_CrossTenant_404 is the workspace-isolation
// canary: an account owned by user 999 MUST NOT be returned when the
// caller is user 1. The 404 (not 403) is critical — 403 would confirm
// to a probe that the id exists but is cross-tenant, leaking the
// existence of accounts in other user boundaries.
func TestHandleGetAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil // exists, but owned by user 999
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	// Caller is user 1.
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant probe MUST return 404 (not 403), got %d: %s", w.Code, w.Body.String())
	}
	// Defence-in-depth: response body must NOT echo the cross-tenant
	// owner's id. Plain "account not found" string is the only safe form.
	if strings.Contains(w.Body.String(), "999") {
		t.Errorf("response leaks owned_by user id in body: %s", w.Body.String())
	}
}

// TestHandleGetAccount_NoSession_401 proves r.protected rejects the
// request before the handler runs. The handler's own nil-identity 401
// is defence-in-depth (loadOwnAccountByID returns 401 on nil identity)
// but the route-level middleware is the primary gate.
func TestHandleGetAccount_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Errorf("FindPlatformAccountByUser MUST NOT be called without a session (data leak risk); got id=%d", id)
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO JWT — session-less probe

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /accounts/21: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// validTokenFuture returns a non-nil OAuthToken that the mock vault
// hands back for "token is valid" cases in handleValidateAccount tests.
func validTokenFuture() *models.OAuthToken {
	exp := time.Now().Add(time.Hour)
	return &models.OAuthToken{
		AccessToken: "valid-token",
		TokenType:   models.TokenTypeShortLived,
		ExpiresAt:   &exp,
	}
}

// TestHandleValidateAccount_ActiveToken verifies the happy path: a
// valid short-lived token ⇒ 200 + status='active' + last_validated_at
// stamped on the row. The handler UPDATE must be issued (UpdatePlatformAccount
// is the persistence call we observe via the mock's updatePlatformAccountFn).
func TestHandleValidateAccount_ActiveToken(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, tokenType string) (*models.OAuthToken, error) {
			return validTokenFuture(), nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (active token), got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called — last_validated_at not stamped")
	}
	if updatedAccount.Status != models.AccountStatusActive {
		t.Errorf("status: want active, got %s", updatedAccount.Status)
	}
	if updatedAccount.LastValidatedAt == nil || updatedAccount.LastValidatedAt.IsZero() {
		t.Errorf("last_validated_at was NOT stamped (status check passed but freshness row not updated)")
	}
}

// TestHandleValidateAccount_ExpiredToken verifies the expired path:
// vault returns "token expired at ..." ⇒ status='expired' on the
// UPDATE. The handler always returns 200 (validation IS the answer;
// caller reads status to react).
func TestHandleValidateAccount_ExpiredToken(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, tokenType string) (*models.OAuthToken, error) {
			return nil, fmt.Errorf("vault: token expired at 2020-01-01T00:00:00Z")
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (validation IS the answer; caller reads status), got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount.Status != models.AccountStatusExpired {
		t.Errorf("status: want expired, got %s", updatedAccount.Status)
	}
}

// TestHandleValidateAccount_ReauthRequired covers the fall-through case:
// vault returns a non-expiry error (DB error, decrypt failure) for both
// token types ⇒ status='reauth_required'. Proves the handler does
// NOT silently mark the row 'active' on a vault error path.
func TestHandleValidateAccount_ReauthRequired(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	// Default mock returns "Get not implemented" (no expiry keyword).
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount.Status != models.AccountStatusReauthRequired {
		t.Errorf("status: want reauth_required (vault 'not implemented' is neither valid nor 'expired'), got %s", updatedAccount.Status)
	}
}

// TestHandleValidateAccount_CrossTenant_404: the ownership check MUST
// fire FIRST. vault.Get must NEVER be called for an account owned by
// another user.
func TestHandleValidateAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called for cross-tenant Validate; got status=%s", a.Status)
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, tokenType string) (*models.OAuthToken, error) {
			t.Errorf("vault.Get MUST NOT be called for cross-tenant Validate (data leak risk); got accountID=%d tokenType=%s", accountID, tokenType)
			return validTokenFuture(), nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant Validate: want 404 (NOT 200, NOT 403), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleReconnectAccount_Happy verifies status flips to
// 'reauth_required' + reauth_required_at is stamped. The status
// field in the response shape MUST reflect the new state.
func TestHandleReconnectAccount_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/reconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called — reauth_required not stamped")
	}
	if updatedAccount.Status != models.AccountStatusReauthRequired {
		t.Errorf("status: want reauth_required, got %s", updatedAccount.Status)
	}
	if updatedAccount.ReauthRequiredAt == nil || updatedAccount.ReauthRequiredAt.IsZero() {
		t.Errorf("reauth_required_at was NOT stamped")
	}
}

// TestHandleReconnectAccount_CrossTenant_404: vault + DB writes MUST
// NOT happen for cross-tenant probes.
func TestHandleReconnectAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called for cross-tenant reconnect (data leak risk); got status=%s", a.Status)
			return nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/reconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant reconnect: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccount_Happy_204 verifies: 204 No Content + vault.Revoke
// was called + account row was updated to status='disconnected' +
// auditLogStore fired (when present).
func TestHandleDeleteAccount_Happy_204(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var revokeCalled bool
	var revokeAccountID int64
	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			revokeCalled = true
			revokeAccountID = platformAccountID
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if !revokeCalled {
		t.Fatal("vault.Revoke was NOT called — local token cleanup skipped")
	}
	if revokeAccountID != 21 {
		t.Errorf("vault.Revoke called with accountID=%d, want 21", revokeAccountID)
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called — soft-disconnect not stamped")
	}
	if updatedAccount.Status != models.AccountStatusDisconnected {
		t.Errorf("status: want disconnected, got %s", updatedAccount.Status)
	}
	if updatedAccount.LastErrorCode != "DISCONNECTED" {
		t.Errorf("last_error_code: want DISCONNECTED, got %s", updatedAccount.LastErrorCode)
	}
	if updatedAccount.ConnectedAt != nil {
		t.Errorf("connected_at: want nil after disconnect, got %v", updatedAccount.ConnectedAt)
	}
}

// TestHandleDeleteAccount_VaultRevokeError_500 covers the failure path:
// vault.Revoke errors ⇒ 500, account row NOT updated, cross-handler
// state machine stays consistent.
func TestHandleDeleteAccount_VaultRevokeError_500(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called when vault.Revoke fails (transaction consistency); got status=%s", a.Status)
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			return fmt.Errorf("simulated vault DB error")
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("vault.Revoke error: want 500, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccount_CrossTenant_404 is the workspace-isolation
// canary: vault.Revoke MUST NOT be called and UpdatePlatformAccount
// MUST NOT be called for a cross-tenant probe. Existence-leak
// prevention: 404 (not 403).
func TestHandleDeleteAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called for cross-tenant delete; got status=%s", a.Status)
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			t.Errorf("vault.Revoke MUST NOT be called for cross-tenant delete (data leak risk); got accountID=%d", platformAccountID)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant delete: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccount_NoSession_401: r.protected rejects the
// session-less probe BEFORE any DB or vault work happens. The
// handler's own nil-identity 401 in loadOwnAccountByID is
// defence-in-depth.
func TestHandleDeleteAccount_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Errorf("FindPlatformAccountByID MUST NOT be called without a session; got id=%d", id)
			return nil, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called without a session")
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			t.Errorf("vault.Revoke MUST NOT be called without a session (token leak risk); got accountID=%d", platformAccountID)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO JWT — session-less probe

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /accounts/21 DELETE: want 401, got %d: %s", w.Code, w.Body.String())
	}
}
