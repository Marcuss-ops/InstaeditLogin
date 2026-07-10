package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	)
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
