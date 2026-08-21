package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeAPIKeyRepo is an in-memory auth.ApiKeyLookup for the API-key
// machine path tests.
type fakeAPIKeyRepo struct {
	keys map[string]*models.ApiKey // keyed by string(hash)
	used int
}

func newFakeAPIKeyRepo() *fakeAPIKeyRepo {
	return &fakeAPIKeyRepo{keys: map[string]*models.ApiKey{}}
}

func (f *fakeAPIKeyRepo) FindByHash(hash []byte) (*models.ApiKey, error) {
	return f.keys[string(hash)], nil
}

func (f *fakeAPIKeyRepo) MarkUsed(wsID, id int64) error {
	f.used++
	return nil
}

// mintAPIKey generates a real sk_test_ key, stores its hash in the
// fake repo and returns the plaintext the caller must send as Bearer.
func mintAPIKey(t *testing.T, repo *fakeAPIKeyRepo, createdBy, wsID int64, perms []string) string {
	t.Helper()
	full, _, err := auth.Generate(models.ApiKeyEnvironmentTest)
	if err != nil {
		t.Fatalf("generate api key: %v", err)
	}
	if _, _, err := auth.ParseFullKey(full); err != nil {
		t.Fatalf("parse api key: %v", err)
	}
	repo.keys[string(auth.Hash(full))] = &models.ApiKey{
		ID:          1,
		WorkspaceID: wsID,
		CreatedBy:   createdBy,
		Environment: models.ApiKeyEnvironmentTest,
		Permissions: perms,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	return full
}

// newAPIKeyMediaRouter builds the full router with the API-key
// authenticator wired, so the machine path (sk_test_ → apiKeyAuth →
// protectedWithAPIKeyPermission → requireUserID) is exercised end to
// end through the real media presign route (which mounts
// editorSessionProtectedUnscoped and falls through to the API-key
// permission gate when no editor bearer is present).
func newAPIKeyMediaRouter(repo *fakeAPIKeyRepo) *Router {
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithMaxUploadBytes(200*1024*1024),
		WithApiKeyAuthenticator(auth.NewApiKeyAuthenticator(repo)),
	)
}

func presignRequest(body, bearer string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

// TestAPIKeyAuth_Presign_WithMediaPermission_200 pins the machine
// path: an API key carrying the `media` permission can presign without
// any cookie-based session. requireUserID must fall back to the
// ApiKeyIdentity's UserID() (the key's created_by), so the minted
// asset is owned by user 42.
func TestAPIKeyAuth_Presign_WithMediaPermission_200(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	key := mintAPIKey(t, repo, 42, 7, []string{models.PermissionMedia})
	r := newAPIKeyMediaRouter(repo)

	body := `{"filename":"cover.png","content_type":"image/png","size_bytes":1024}`
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, presignRequest(body, key))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PresignMediaResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AssetID == "" {
		t.Error("asset_id should be set")
	}
	store := r.mediaStore.(*mockMediaStore)
	if asset, ok := store.assets[resp.AssetID]; ok && asset.UserID != 42 {
		t.Errorf("asset user_id = %d, want 42 (key created_by)", asset.UserID)
	}
}

// TestAPIKeyAuth_Presign_MissingMediaPermission_403 pins that a key
// without the required permission is rejected by the permission gate
// with a 403 naming the missing permission, before any handler runs.
func TestAPIKeyAuth_Presign_MissingMediaPermission_403(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	key := mintAPIKey(t, repo, 42, 7, []string{models.PermissionRead})
	r := newAPIKeyMediaRouter(repo)

	body := `{"filename":"cover.png","content_type":"image/png","size_bytes":1024}`
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, presignRequest(body, key))

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required permission: media") {
		t.Errorf("body should name the missing permission, got %s", w.Body.String())
	}
}

// TestAPIKeyAuth_Presign_UnknownKey_401 pins that a syntactically
// valid sk_test_ key that is not in the repo is rejected with 401 by
// the API-key authenticator.
func TestAPIKeyAuth_Presign_UnknownKey_401(t *testing.T) {
	repo := newFakeAPIKeyRepo() // empty: no key known
	r := newAPIKeyMediaRouter(repo)

	body := `{"filename":"cover.png","content_type":"image/png","size_bytes":1024}`
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, presignRequest(body, "sk_test_"+strings.Repeat("a", 52)))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAPIKeyAuth_JWTUserStillWorks pins that wiring the API-key
// authenticator does not change the browser path: a normal JWT user
// keeps passing protectedWithAPIKeyPermission's gate without carrying
// any API-key permission.
func TestAPIKeyAuth_JWTUserStillWorks(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	r := newAPIKeyMediaRouter(repo)

	body := `{"filename":"cover.png","content_type":"image/png","size_bytes":1024}`
	req := presignRequest(body, "")
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 for JWT user, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// YouTube editor-session routes (write / publish permission gates)
// ---------------------------------------------------------------------------

// TestAPIKeyAuth_CreateEditorSession_WithWritePermission_201 pins that
// POST /api/v1/youtube/editor-sessions (protectedWithAPIKeyPermission
// "write") accepts an API key carrying the write permission and mints
// a session anchored to the key's created_by — no cookie session.
func TestAPIKeyAuth_CreateEditorSession_WithWritePermission_201(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == account.ID {
				return account, nil
			}
			return nil, nil
		},
	}
	workspaceStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		},
		findChannelFn: func(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
			if workspaceID == workspace.ID && platformAccountID == account.ID {
				return &models.WorkspaceChannel{WorkspaceID: workspace.ID, PlatformAccountID: account.ID, Enabled: true}, nil
			}
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == account.ID {
				return &models.OAuthToken{AccessToken: "valid-token"}, nil
			}
			return nil, errors.New("token not found")
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	var capturedSession *models.YouTubeVideoEdit
	editStore.findOrCreateFn = func(ctx context.Context, workspaceID, platformAccountID int64, youtubeVideoID, sessionIDHint, projectIDHint string) (*models.YouTubeVideoEdit, error) {
		capturedSession = &models.YouTubeVideoEdit{
			ID:                sessionIDHint,
			WorkspaceID:       workspaceID,
			PlatformAccountID: platformAccountID,
			YouTubeVideoID:    youtubeVideoID,
			VeloxProjectID:    projectIDHint,
			Status:            "editing",
		}
		editStore.created = append(editStore.created, capturedSession)
		return capturedSession, nil
	}

	repo := newFakeAPIKeyRepo()
	key := mintAPIKey(t, repo, 1, workspace.ID, []string{models.PermissionWrite})
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithCredentialVault(vault),
		WithYouTubeService(&mockYouTubeOAuthServiceForEditor{}),
		WithYouTubeVideoEditStore(editStore),
		WithEditorURL("https://editor.instaedit.org"),
		WithApiKeyAuthenticator(auth.NewApiKeyAuthenticator(repo)),
	)

	payload := map[string]any{
		"workspace_id":         workspace.ID,
		"platform_account_id":  account.ID,
		"youtube_video_id":     "abc123",
		"source_thumbnail_url": "https://i.ytimg.com/original.jpg",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 for API key with write permission, got %d: %s", w.Code, w.Body.String())
	}
	if capturedSession == nil || capturedSession.WorkspaceID != workspace.ID {
		t.Fatalf("session not created for the key's workspace: %+v", capturedSession)
	}
	if capturedSession.PlatformAccountID != account.ID {
		t.Errorf("platform_account_id: want %d, got %d", account.ID, capturedSession.PlatformAccountID)
	}
}

// TestAPIKeyAuth_Publish_MissingPublishPermission_403 pins that
// POST /api/v1/youtube/editor-sessions/{id}/publish (gate "publish")
// rejects a key that only has write with 403 before any handler runs.
func TestAPIKeyAuth_Publish_MissingPublishPermission_403(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	key := mintAPIKey(t, repo, 42, 7, []string{models.PermissionWrite})
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithApiKeyAuthenticator(auth.NewApiKeyAuthenticator(repo)),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/ytedit_123/publish", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required permission: publish") {
		t.Errorf("body should name the missing permission, got %s", w.Body.String())
	}
}

// TestAPIKeyAuth_Publish_WithPublishPermission_PassesGate pins that a
// key carrying publish gets PAST the auth gate: the request reaches the
// handler (which 404s on the unknown session instead of 401/403).
func TestAPIKeyAuth_Publish_WithPublishPermission_PassesGate(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	key := mintAPIKey(t, repo, 42, 7, []string{models.PermissionWrite, models.PermissionPublish})
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithYouTubeVideoEditStore(&mockYouTubeVideoEditStore{}),
		WithApiKeyAuthenticator(auth.NewApiKeyAuthenticator(repo)),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/ytedit_123/publish", strings.NewReader(`{"privacy_status":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (gate passed, session unknown), got %d: %s", w.Code, w.Body.String())
	}
}

// TestAPIKeyAuth_Thumbnail_MissingWritePermission_403 pins that
// POST /api/v1/youtube/editor-sessions/{id}/thumbnail (gate "write")
// rejects a read-only key before the handler runs.
func TestAPIKeyAuth_Thumbnail_MissingWritePermission_403(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	key := mintAPIKey(t, repo, 42, 7, []string{models.PermissionRead})
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithApiKeyAuthenticator(auth.NewApiKeyAuthenticator(repo)),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/ytedit_123/thumbnail", strings.NewReader(`{"thumbnail_media_id":"media_1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing required permission: write") {
		t.Errorf("body should name the missing permission, got %s", w.Body.String())
	}
}
