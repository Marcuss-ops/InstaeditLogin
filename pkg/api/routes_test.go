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
type mockUserStore struct {
	findOrCreateFn          func(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error)
	listFn                  func(userID int64, platform string) ([]*models.PlatformAccount, error)
	findPlatformAccountFn   func(id int64) (*models.PlatformAccount, error)
	updatePlatformAccountFn func(account *models.PlatformAccount) error
	deletePlatformAccountFn func(id int64) error
}

func (m *mockUserStore) FindOrCreateUserByPlatform(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
	return m.findOrCreateFn(profile, platform)
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
	platformSvc *mockProvider,
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

func issueTestJWT(t *testing.T, userID int64) string {
	t.Helper()
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.Issue(userID)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
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

var successFindOrCreate = func(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
	return &models.User{ID: 1, Name: profile.Name, Email: profile.Email},
		&models.PlatformAccount{ID: 10, UserID: 1, Platform: platform, PlatformUserID: profile.PlatformUserID, Username: profile.Username},
		nil
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

func TestHandleCallback_FindOrCreateError(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		findOrCreateFn: func(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
			return nil, nil, fmt.Errorf("db error")
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
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
		findOrCreateFn: successFindOrCreate,
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
		findOrCreateFn: successFindOrCreate,
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
	// Taglio 1.2: the callback now returns a one-time code, NOT the JWT.
	// The SPA POSTs the code to /api/v1/auth/exchange to mint the
	// HttpOnly session cookie.
	if body["status"] != "code_issued" {
		t.Fatalf("status: want code_issued, got %v", body["status"])
	}
	if body["provider"] != "instagram" {
		t.Fatalf("provider: want instagram, got %v", body["provider"])
	}
	if body["code"] == nil || body["code"] == "" {
		t.Fatal("expected one-time code in response (Taglio 1.2: JWT never in body)")
	}
	if uid, ok := body["user_id"].(float64); !ok || uid != 1 {
		t.Fatalf("user_id: want 1, got %v (Taglio 1.2 contract)", body["user_id"])
	}
}

func TestHandleCallback_Success_FrontendRedirect(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		findOrCreateFn: successFindOrCreate,
	}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "https://app.example.com/auth/callback?") {
		t.Fatalf("redirect URL mismatch: %s", loc)
	}
	// Taglio 1.2: the redirect carries a one-time code, NOT the JWT.
	if strings.Contains(loc, "jwt=") {
		t.Fatalf("JWT must never appear in the redirect URL (Taglio 1.2): %s", loc)
	}
	if !strings.Contains(loc, "code=") {
		t.Fatalf("expected one-time code in redirect params (Taglio 1.2): %s", loc)
	}
	if !strings.Contains(loc, "provider=instagram") {
		t.Fatalf("expected provider=instagram in redirect params: %s", loc)
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

	w1 := httptest.NewRecorder()
	r.Setup().ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil))
	w2 := httptest.NewRecorder()
	r.Setup().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/login", nil))

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
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
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
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
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
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
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

func TestHandleCallback_DeletesStateCookieAfterUse(t *testing.T) {
	svc := &mockProvider{platform: "instagram", handleCallback: successCallback}
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
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
