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
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockPlatformService lets each test override only the methods it cares about.
type mockPlatformService struct {
	platform            string
	loginURL            string
	handleCallback      func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)
	publishFn           func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	saveTokenFn         func(platformAccountID int64, tokenData *models.TokenData) error
	ensureFreshFn       func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error)
	refreshFn           func(ctx context.Context, refreshToken string) (*models.TokenData, error)
	handleCallbackCalls int
}

func (m *mockPlatformService) Name() string                    { return m.platform }
func (m *mockPlatformService) GetLoginURL(state string) string { return m.loginURL + "?state=" + state }
func (m *mockPlatformService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	m.handleCallbackCalls++
	if m.handleCallback == nil {
		return nil, nil, fmt.Errorf("HandleCallback not implemented")
	}
	return m.handleCallback(ctx, state, code)
}
func (m *mockPlatformService) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	if m.refreshFn != nil {
		return m.refreshFn(ctx, refreshToken)
	}
	return nil, fmt.Errorf("refresh not implemented")
}
func (m *mockPlatformService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	if m.publishFn == nil {
		return nil, fmt.Errorf("Publish not implemented")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}
func (m *mockPlatformService) SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error {
	if m.saveTokenFn != nil {
		return m.saveTokenFn(platformAccountID, tokenData)
	}
	return nil
}
func (m *mockPlatformService) GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockPlatformService) EnsureFreshToken(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
	if m.ensureFreshFn == nil {
		return nil, fmt.Errorf("EnsureFreshToken not implemented")
	}
	return m.ensureFreshFn(ctx, accountID, tokenType, refresh)
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
	saveFn            func(*models.PostTarget) error
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
func (m *mockPostStore) Save(target *models.PostTarget) error {
	if m.saveFn == nil {
		return nil
	}
	return m.saveFn(target)
}

// mockStorageProvider implements StorageProvider with configurable function fields.
type mockStorageProvider struct {
	grant               *services.UploadGrant
	err                 error
	capturedUserID      int64
	capturedKey         string
	capturedContentType string
	capturedSize        int64
}

func (m *mockStorageProvider) Provider() string { return "mock" }

func (m *mockStorageProvider) SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error) {
	m.capturedUserID = userID
	m.capturedKey = key
	m.capturedContentType = contentType
	m.capturedSize = sizeBytes
	if m.err != nil {
		return nil, m.err
	}
	return m.grant, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const testJWTSecret = "test-jwt-secret-must-be-long-enough-for-hs256"

func withBearerJWT(t *testing.T, req *http.Request, userID int64) {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, userID))
}

// newTestRouter builds a Router wired with a mock platform and store.
func newTestRouter(
	platformSvc *mockPlatformService,
	store *mockUserStore,
	frontendURL string,
	opts ...RouterOption,
) *Router {
	registry := services.NewPlatformRegistry()
	registry.RegisterPlatformService(platformSvc.Name(), platformSvc)
	registry.RegisterPlatformService("meta", platformSvc)
	registry.RegisterPlatformService("tiktok", platformSvc)
	registry.RegisterPlatformService("twitter", platformSvc)
	return NewRouter(
		registry,
		store,
		auth.NewManager(testJWTSecret, 24),
		frontendURL,
		nil,
		opts...,
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
	svc := &mockPlatformService{platform: "meta", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/login", nil)
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
		if c.Name == OAuthStateCookieName("meta") {
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
	svc := &mockPlatformService{platform: "meta", loginURL: "https://auth.example.com"}
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
	svc := &mockPlatformService{platform: "twitter", loginURL: "https://auth.twitter.com/auth"}
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
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleCallback_UnsupportedProvider(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{
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
	svc := &mockPlatformService{
		platform:       "meta",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		findOrCreateFn: func(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
			return nil, nil, fmt.Errorf("db error")
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "meta", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
}

func TestHandleCallback_SaveTokenError(t *testing.T) {
	svc := &mockPlatformService{
		platform:       "meta",
		handleCallback: successCallback,
		saveTokenFn: func(platformAccountID int64, tokenData *models.TokenData) error {
			return fmt.Errorf("token save error")
		},
	}
	store := &mockUserStore{
		findOrCreateFn: successFindOrCreate,
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "meta", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
}

func TestHandleCallback_Success_JSONResponse(t *testing.T) {
	svc := &mockPlatformService{
		platform:       "meta",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		findOrCreateFn: successFindOrCreate,
	}
	r := newTestRouter(svc, store, "") // empty frontendURL → JSON

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "meta", "test-state")
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
	if body["status"] != "authenticated" {
		t.Fatalf("status: want authenticated, got %v", body["status"])
	}
	if body["provider"] != "meta" {
		t.Fatalf("provider: want meta, got %v", body["provider"])
	}
	if body["jwt_token"] == nil || body["jwt_token"] == "" {
		t.Fatal("expected jwt_token in response")
	}
}

func TestHandleCallback_Success_FrontendRedirect(t *testing.T) {
	svc := &mockPlatformService{
		platform:       "meta",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		findOrCreateFn: successFindOrCreate,
	}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "meta", "test-state")
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
	if !strings.Contains(loc, "jwt=") {
		t.Fatal("expected jwt in redirect params")
	}
}

// ---------------------------------------------------------------------------
// handlePublishPost tests
// ---------------------------------------------------------------------------

func TestHandlePublishPost_MissingJWT_401(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"meta","content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandlePublishPost_InvalidJSON(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandlePublishPost_UnsupportedPlatform(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"unknown","content_type":"text","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_NoAccountLinked(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"meta","content_type":"text","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_TokenRefreshFailed(t *testing.T) {
	svc := &mockPlatformService{
		platform: "twitter",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return nil, fmt.Errorf("refresh failed: token expired")
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "twitter", PlatformUserID: "tw-123", Username: "twuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"twitter","content_type":"text","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_BadContentType(t *testing.T) {
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok", TokenType: "bearer"}, nil
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "meta", PlatformUserID: "fb-123", Username: "fbuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"meta","content_type":"unknown","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_Success(t *testing.T) {
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-token", TokenType: "bearer"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			if accessToken != "fresh-token" {
				return nil, fmt.Errorf("unexpected token: %s", accessToken)
			}
			return &models.PublishResult{PlatformMediaID: "media-456", PlatformURL: "https://example.com/post/1"}, nil
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "meta", PlatformUserID: "fb-123", Username: "fbuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"meta","content_type":"video","media_url":"https://cdn.example.com/video.mp4","caption":"Check this out"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["status"] != "published" {
		t.Fatalf("status: want published, got %v", resp["status"])
	}
	if resp["platform_media_id"] != "media-456" {
		t.Fatalf("platform_media_id: want media-456, got %v", resp["platform_media_id"])
	}
}

func TestHandlePublishPost_PublishError(t *testing.T) {
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok", TokenType: "bearer"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return nil, fmt.Errorf("publish failed: 500 internal error")
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "meta", PlatformUserID: "fb-123", Username: "fbuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"platform":"meta","content_type":"text","caption":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_BodyUserIDIgnored_TrustsJWT(t *testing.T) {
	var capturedUserID int64
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok", TokenType: "bearer"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "jwt-ok"}, nil
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			capturedUserID = userID
			return []*models.PlatformAccount{
				{ID: 10, UserID: userID, Platform: "meta", PlatformUserID: "fb-42", Username: "jwtuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"user_id":999,"platform":"meta","content_type":"text","caption":"jwt test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedUserID != 42 {
		t.Fatalf("userID from context: want 42 (JWT), got %d (must NOT use body user_id)", capturedUserID)
	}
}

// ---------------------------------------------------------------------------
// handlePublishAll tests
// ---------------------------------------------------------------------------

func TestHandlePublishAll_MissingJWT_401(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	body := `{"content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandlePublishAll_NoAccounts(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishAll_InvalidContentType(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	body := `{"content_type":"bogus","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishAll_Success_AllPlatforms(t *testing.T) {
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok", TokenType: "bearer"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "id-" + platformUserID}, nil
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "meta", PlatformUserID: "fb-123", Username: "fbuser"},
				{ID: 11, UserID: 1, Platform: "twitter", PlatformUserID: "tw-456", Username: "twuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"content_type":"text","caption":"hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status  string `json:"status"`
		Results []struct {
			Platform string `json:"platform"`
			Status   string `json:"status"`
		} `json:"results"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("status: want completed, got %s", resp.Status)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results count: want 2, got %d", len(resp.Results))
	}
	for _, r := range resp.Results {
		if r.Status != "published" {
			t.Errorf("platform %s: want published, got %s", r.Platform, r.Status)
		}
	}
}

func TestHandlePublishAll_PartialFailures(t *testing.T) {
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok", TokenType: "bearer"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			if platformUserID == "tw-456" {
				return nil, fmt.Errorf("twitter api error")
			}
			return &models.PublishResult{PlatformMediaID: "ok"}, nil
		},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "meta", PlatformUserID: "fb-123", Username: "fbuser"},
				{ID: 11, UserID: 1, Platform: "twitter", PlatformUserID: "tw-456", Username: "twuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	body := `{"content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Status  string `json:"status"`
		Results []struct {
			Platform string `json:"platform"`
			Status   string `json:"status"`
			Error    string `json:"error,omitempty"`
		} `json:"results"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("status: want completed, got %s", resp.Status)
	}

	var metaOK, twitterFail bool
	for _, r := range resp.Results {
		switch r.Platform {
		case "meta":
			if r.Status == "published" {
				metaOK = true
			}
		case "twitter":
			if r.Status == "error" && r.Error != "" {
				twitterFail = true
			}
		}
	}
	if !metaOK {
		t.Error("meta should have published successfully")
	}
	if !twitterFail {
		t.Error("twitter should have failed")
	}
}

// ---------------------------------------------------------------------------
// handleCreateWorkspace tests
// ---------------------------------------------------------------------------

func TestHandleCreateWorkspace_Happy(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
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

	body := `{"workspace_id":1,"title":"hello","caption":"world","targets":[{"platform_account_id":10}]}`
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
	svc := &mockPlatformService{platform: "meta"}
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
	)

	body := `{"workspace_id":1,"title":"future post","media_url":"https://cdn/img.png","scheduled_at":"2030-01-01T00:00:00Z","targets":[{"platform_account_id":10}]}`
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
	if resp.Status != "scheduled" {
		t.Fatalf("status: want scheduled, got %s", resp.Status)
	}
	if resp.ScheduledAt != "2030-01-01T00:00:00Z" {
		t.Fatalf("scheduled_at: want 2030-01-01T00:00:00Z, got %s", resp.ScheduledAt)
	}
}

func TestHandleCreatePost_MissingWorkspaceID_422(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
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
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"targets":[{"platform_account_id":0}]}`
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
	svc := &mockPlatformService{platform: "meta"}
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

	body := `{"workspace_id":1,"targets":[{"platform_account_id":10}]}`
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
	svc := &mockPlatformService{platform: "meta"}
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
// handleCreateUploadURL tests
// ---------------------------------------------------------------------------

func TestHandleCreateUploadURL_NotConfigured_501(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	body := `{"filename":"test.mp4","content_type":"video/mp4","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}

func TestHandleCreateUploadURL_MissingJWT_401(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	r := newTestRouter(svc, store, "", WithStorageProvider(storage))

	body := `{"filename":"test.mp4","content_type":"video/mp4","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandleCreateUploadURL_InvalidContentType_422(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	r := newTestRouter(svc, store, "", WithStorageProvider(storage))

	body := `{"filename":"xss.html","content_type":"text/html","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUploadURL_TooLarge_422(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	r := newTestRouter(svc, store, "",
		WithStorageProvider(storage),
		WithMaxUploadBytes(1000),
	)

	body := `{"filename":"huge.mp4","content_type":"video/mp4","size_bytes":99999999}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUploadURL_MissingFilename_422(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	r := newTestRouter(svc, store, "", WithStorageProvider(storage))

	body := `{"content_type":"video/mp4","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreateUploadURL_Happy_200(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{
		grant: &services.UploadGrant{
			UploadURL: "https://example.supabase.co/storage/v1/upload/sign/bucket/key?token=xyz",
			MediaURL:  "https://example.supabase.co/storage/v1/object/public/bucket/key",
			ExpiresAt: time.Now().Add(15 * time.Minute),
		},
	}
	r := newTestRouter(svc, store, "", WithStorageProvider(storage))

	body := `{"filename":"test.mp4","content_type":"video/mp4","size_bytes":1024000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		UploadURL string    `json:"upload_url"`
		MediaURL  string    `json:"media_url"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.UploadURL == "" {
		t.Error("upload_url should be non-empty")
	}
	if resp.MediaURL == "" {
		t.Error("media_url should be non-empty")
	}
	if resp.ExpiresAt.IsZero() {
		t.Error("expires_at should be set")
	}
	if storage.capturedUserID != 1 {
		t.Errorf("user_id capture: want 1, got %d", storage.capturedUserID)
	}
	if !strings.HasPrefix(storage.capturedKey, "uploads/1/") {
		t.Errorf("key prefix: want uploads/1/, got %q", storage.capturedKey)
	}
	if storage.capturedContentType != "video/mp4" {
		t.Errorf("content_type capture: want video/mp4, got %q", storage.capturedContentType)
	}
	if storage.capturedSize != 1024000 {
		t.Errorf("size_bytes capture: want 1024000, got %d", storage.capturedSize)
	}
}

// ---------------------------------------------------------------------------
// CORS middleware tests
// ---------------------------------------------------------------------------

func newCORSTestRouter(allowedOrigins []string) *Router {
	return NewRouter(
		services.NewPlatformRegistry(),
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
	svc := &mockPlatformService{platform: "meta", loginURL: "https://auth.example.com/oauth"}
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
	r.Setup().ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/login", nil))
	w2 := httptest.NewRecorder()
	r.Setup().ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/login", nil))

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
	svc := &mockPlatformService{platform: "meta", handleCallback: successCallback}
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=anything", nil)
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
		if c.Name == OAuthStateCookieName("meta") && c.MaxAge < 0 {
			t.Errorf("state cookie was deleted on verification failure (should persist so the legitimate user can retry): %+v", c)
		}
	}
	if !strings.Contains(w.Body.String(), "invalid state") {
		t.Errorf("response body should explain the state failure; got %q", w.Body.String())
	}
}

func TestHandleCallback_RejectsMismatchedState_400(t *testing.T) {
	svc := &mockPlatformService{platform: "meta", handleCallback: successCallback}
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=different-state", nil)
	setOAuthStateCookieForTest(req, "meta", "cookie-state")
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
		if c.Name == OAuthStateCookieName("meta") && c.MaxAge < 0 {
			t.Errorf("state cookie was deleted on mismatch (should persist so the legitimate user can retry): %+v", c)
		}
	}
	if !strings.Contains(w.Body.String(), "invalid state") {
		t.Errorf("response body should explain the state mismatch; got %q", w.Body.String())
	}
}

func TestHandleCallback_RejectsMissingStateParam_400(t *testing.T) {
	svc := &mockPlatformService{platform: "meta", handleCallback: successCallback}
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc", nil)
	setOAuthStateCookieForTest(req, "meta", "any-state")
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
	svc := &mockPlatformService{platform: "meta", handleCallback: successCallback}
	store := &mockUserStore{findOrCreateFn: successFindOrCreate}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "meta", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var deletionCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == OAuthStateCookieName("meta") {
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
