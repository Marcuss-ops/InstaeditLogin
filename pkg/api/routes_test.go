package api

import (
	"context"
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
	platform       string
	loginURL       string
	handleCallback func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)
	publishFn      func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	saveTokenFn    func(platformAccountID int64, tokenData *models.TokenData) error
	ensureFreshFn  func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error)
	refreshFn      func(ctx context.Context, refreshToken string) (*models.TokenData, error)
}

func (m *mockPlatformService) GetPlatform() string                            { return m.platform }
func (m *mockPlatformService) GetLoginURL(state string) string                { return m.loginURL + "?state=" + state }
func (m *mockPlatformService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
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
	findOrCreateFn func(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error)
	listFn         func(userID int64, platform string) ([]*models.PlatformAccount, error)
}

func (m *mockUserStore) FindOrCreateUserByPlatform(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
	return m.findOrCreateFn(profile, platform)
}
func (m *mockUserStore) ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error) {
	return m.listFn(userID, platform)
}

// mockWorkspaceStore implements WorkspaceStore with configurable function fields.
type mockWorkspaceStore struct {
	createFn    func(*models.Workspace) error
	findByIDFn  func(id int64) (*models.Workspace, error)
	listByOwner func(ownerID int64) ([]models.Workspace, error)
}

func (m *mockWorkspaceStore) Create(w *models.Workspace) error {
	if m.createFn == nil {
		return nil
	}
	return m.createFn(w)
}
func (m *mockWorkspaceStore) FindByID(id int64) (*models.Workspace, error) {
	if m.findByIDFn == nil {
		return nil, nil
	}
	return m.findByIDFn(id)
}
func (m *mockWorkspaceStore) ListByOwner(ownerID int64) ([]models.Workspace, error) {
	if m.listByOwner == nil {
		return nil, nil
	}
	return m.listByOwner(ownerID)
}

// mockPostStore implements PostStore with configurable function fields.
type mockPostStore struct {
	createFn     func(*models.Post, []*models.PostTarget) error
	findByIDFn   func(id int64) (*models.Post, error)
	listByWsFn   func(workspaceID int64) ([]models.Post, error)
	listByPostFn func(postID int64) ([]models.PostTarget, error)
}

func (m *mockPostStore) Create(post *models.Post, targets []*models.PostTarget) error {
	if m.createFn == nil {
		return nil
	}
	return m.createFn(post, targets)
}
func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	if m.findByIDFn == nil {
		return nil, nil
	}
	return m.findByIDFn(id)
}
func (m *mockPostStore) ListByWorkspace(workspaceID int64) ([]models.Post, error) {
	if m.listByWsFn == nil {
		return nil, nil
	}
	return m.listByWsFn(workspaceID)
}
func (m *mockPostStore) ListByPost(postID int64) ([]models.PostTarget, error) {
	if m.listByPostFn == nil {
		return nil, nil
	}
	return m.listByPostFn(postID)
}

// mockStorageProvider implements StorageProvider with configurable function
// fields. Captures SignUpload args so tests can assert key construction
// (user_id scoping, UUID4 uniqueness, name sanitization).
type mockStorageProvider struct {
	grant              *services.UploadGrant
	err                error
	capturedUserID     int64
	capturedKey        string
	capturedContentType string
	capturedSize       int64
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

// newTestRouter builds a Router wired with a mock platform and store.
// By default strictAuth=false (so publish is reachable without Bearer).
func newTestRouter(
	platformSvc *mockPlatformService,
	store *mockUserStore,
	strictAuth bool,
	frontendURL string,
	opts ...RouterOption,
) *Router {
	platforms := map[string]services.PlatformService{
		"meta":    platformSvc,
		"tiktok":  platformSvc,
		"twitter": platformSvc,
	}
	return NewRouter(
		platforms,
		store,
		auth.NewManager(testJWTSecret, 24),
		strictAuth,
		frontendURL,
		nil,
		opts...,
	)
}

// issueTestJWT issues a JWT for the given userID using the test secret.
// Mirrors the pattern in TestHandlePublishPost_WithJWT_StrictMode and is
// used by the protected-endpoint tests (workspaces + posts) so that
// `requireUserID` can read back the authenticated user from context.
func issueTestJWT(t *testing.T, userID int64) string {
	t.Helper()
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.Issue(userID)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	return tok
}

// successCallback returns canned HandleCallback results.
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

// successFindOrCreate returns a canned user+account pair.
var successFindOrCreate = func(profile *models.PlatformProfile, platform string) (*models.User, *models.PlatformAccount, error) {
	return &models.User{ID: 1, Name: profile.Name, Email: profile.Email},
		&models.PlatformAccount{ID: 10, UserID: 1, Platform: platform, PlatformUserID: profile.PlatformUserID, Username: profile.Username},
		nil
}

// ---------------------------------------------------------------------------
// handleLogin tests
// ---------------------------------------------------------------------------

func TestHandleLogin_RedirectsToProviderURL(t *testing.T) {
	svc := &mockPlatformService{platform: "meta", loginURL: "https://auth.example.com/oauth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/login", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://auth.example.com/oauth?state=") {
		t.Fatalf("unexpected redirect: %s", loc)
	}
	// Default state should be provider + "_default".
	if !strings.Contains(loc, "state=meta_default") {
		t.Fatalf("expected default state meta_default in redirect: %s", loc)
	}
}

func TestHandleLogin_UnsupportedProvider(t *testing.T) {
	svc := &mockPlatformService{platform: "meta", loginURL: "https://auth.example.com"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown/login", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestHandleLogin_UsesCustomState(t *testing.T) {
	svc := &mockPlatformService{platform: "twitter", loginURL: "https://auth.twitter.com/auth"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/twitter/login?state=my-custom-state", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "state=my-custom-state") {
		t.Fatalf("expected custom state in redirect, got: %s", loc)
	}
}

// ---------------------------------------------------------------------------
// handleCallback tests
// ---------------------------------------------------------------------------

func TestHandleCallback_MissingCode(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleCallback_UnsupportedProvider(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown/callback?code=abc", nil)
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/twitter/callback?code=bad&state=s", nil)
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=s", nil)
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=s", nil)
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "") // empty frontendURL → JSON

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=s", nil)
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/meta/callback?code=abc&state=s", nil)
	w := httptest.NewRecorder()
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

func TestHandlePublishPost_InvalidJSON(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandlePublishPost_MissingUserID_Strict(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, true, "") // strictAuth=true, no Bearer

	body := `{"platform":"meta","content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	// JWT middleware rejects with 401 before handler runs.
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandlePublishPost_MissingUserID_Lenient(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "") // strictAuth=false, no user_id in body

	body := `{"platform":"meta","content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	// No JWT, no fallback user_id → 400.
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_UnsupportedPlatform(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"platform":"unknown","content_type":"text","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_NoAccountLinked(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return nil, nil // no accounts
		},
	}
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"platform":"meta","content_type":"text","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"platform":"twitter","content_type":"text","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"platform":"meta","content_type":"unknown","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"platform":"meta","content_type":"video","media_url":"https://cdn.example.com/video.mp4","caption":"Check this out"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"platform":"meta","content_type":"text","caption":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	// Publish errors are not publishError, so they default to 500.
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishPost_WithJWT_StrictMode(t *testing.T) {
	// Issue a real JWT and inject it. The handler should use the JWT uid, not the body user_id.
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.Issue(42)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

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
	r := newTestRouter(svc, store, true, "")

	// Send body with user_id=999, but JWT says 42. Strict mode should use JWT.
	body := `{"user_id":999,"platform":"meta","content_type":"text","caption":"jwt test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	// Verify the JWT userID (42) was used, not the body's user_id (999).
	if capturedUserID != 42 {
		t.Fatalf("userID from context: want 42, got %d", capturedUserID)
	}
}

// ---------------------------------------------------------------------------
// handlePublishAll tests
// ---------------------------------------------------------------------------

func TestHandlePublishAll_NoAccounts(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlePublishAll_InvalidContentType(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"content_type":"bogus","caption":"hi"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
			// handlePublishAll calls with platform="" to get all accounts.
			// publishToAccount does NOT call ListPlatformAccountsByUser again.
			return []*models.PlatformAccount{
				{ID: 10, UserID: 1, Platform: "meta", PlatformUserID: "fb-123", Username: "fbuser"},
				{ID: 11, UserID: 1, Platform: "twitter", PlatformUserID: "tw-456", Username: "twuser"},
			}, nil
		},
	}
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"content_type":"text","caption":"hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, false, "")

	body := `{"user_id":1,"content_type":"text","caption":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts/publish-all", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "", WithWorkspaceStore(wsStore))

	body := `{"name":"My Workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "", WithWorkspaceStore(wsStore))

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreateWorkspace_MalformedJSON_400(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	r := newTestRouter(svc, store, true, "", WithWorkspaceStore(wsStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// NotConfigured_501 fires BEFORE requireUserID, so no JWT context needed.
func TestHandleCreateWorkspace_NotConfigured_501(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "") // no WithWorkspaceStore

	body := `{"name":"My Workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}

func TestHandleGetWorkspace_CrossOwner_404(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil // not caller (1)
		},
	}
	r := newTestRouter(svc, store, true, "", WithWorkspaceStore(wsStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/42", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"title":"hello","caption":"world","targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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

// TestHandleCreatePost_HappyWithScheduledAt verifies the happy path of
// the scheduling feature: when scheduled_at is provided the auto-status
// transition `draft -> scheduled` happens, AND the response echoes back
// scheduled_at so the client can confirm what was stored.
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"title":"future post","media_url":"https://cdn/img.png","scheduled_at":"2030-01-01T00:00:00Z","targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"targets":[{"platform_account_id":0}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// handleCreateUploadURL tests
// ---------------------------------------------------------------------------

// NotConfigured_501 fires BEFORE requireUserID, so no JWT context needed.
func TestHandleCreateUploadURL_NotConfigured_501(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, false, "") // no WithStorageProvider

	body := `{"filename":"test.mp4","content_type":"video/mp4","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}

func TestHandleCreateUploadURL_MissingJWT_401(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	r := newTestRouter(svc, store, true, "", WithStorageProvider(storage))

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
	r := newTestRouter(svc, store, true, "", WithStorageProvider(storage))

	// text/html is in no allowlist (imagine XSS surface area).
	body := `{"filename":"xss.html","content_type":"text/html","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUploadURL_TooLarge_422(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	// Cap at 1000 bytes so the test is deterministic without MB numbers.
	r := newTestRouter(svc, store, true, "",
		WithStorageProvider(storage),
		WithMaxUploadBytes(1000),
	)

	body := `{"filename":"huge.mp4","content_type":"video/mp4","size_bytes":99999999}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCreateUploadURL_MissingFilename_422(t *testing.T) {
	svc := &mockPlatformService{platform: "meta"}
	store := &mockUserStore{}
	storage := &mockStorageProvider{}
	r := newTestRouter(svc, store, true, "", WithStorageProvider(storage))

	body := `{"content_type":"video/mp4","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	r := newTestRouter(svc, store, true, "", WithStorageProvider(storage))

	body := `{"filename":"test.mp4","content_type":"video/mp4","size_bytes":1024000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/storage/upload-url", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, 1))
	w := httptest.NewRecorder()
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
	// Key must be scoped under user_id=1 (the JWT uid).
	if storage.capturedUserID != 1 {
		t.Errorf("user_id capture: want 1, got %d", storage.capturedUserID)
	}
	if !strings.HasPrefix(storage.capturedKey, "uploads/1/") {
		t.Errorf("key prefix: want uploads/1/, got %q", storage.capturedKey)
	}
	// content_type forwarded verbatim.
	if storage.capturedContentType != "video/mp4" {
		t.Errorf("content_type capture: want video/mp4, got %q", storage.capturedContentType)
	}
	if storage.capturedSize != 1024000 {
		t.Errorf("size_bytes capture: want 1024000, got %d", storage.capturedSize)
	}
}
