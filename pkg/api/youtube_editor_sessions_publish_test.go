package api

// Publish handler tests.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublishYouTubeEditorSession_HappyPath(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	store := &mockUserStore{
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
	}

	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "public",
				Status:            "editing",
			}, nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	// Serve the thumbnail bytes via an HTTP server so the signed download URL works.
	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	var publishCalled bool
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		publishCalled = true
		if string(data) != string(thumbnailBytes) {
			t.Errorf("expected thumbnail data %q, got %q", string(thumbnailBytes), string(data))
		}
		if privacyStatus != "public" {
			t.Errorf("expected privacyStatus public, got %s", privacyStatus)
		}
		if opts.Title != "Updated title" {
			t.Errorf("expected title \"Updated title\", got %q", opts.Title)
		}
		if opts.Description != "Updated description" {
			t.Errorf("expected description \"Updated description\", got %q", opts.Description)
		}
		return "https://www.youtube.com/watch?v=" + videoID, nil
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, errors.New("token not found")
			},
		}),
	)

	payload := map[string]any{
		"privacy_status": "public",
		"title":          "Updated title",
		"description":    "Updated description",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !publishCalled {
		t.Fatalf("expected PublishThumbnail to be called")
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.VideoID != "ytvideo123" {
		t.Errorf("expected video_id ytvideo123, got %s", resp.VideoID)
	}
	if resp.PrivacyStatus != "public" {
		t.Errorf("expected privacy_status public, got %s", resp.PrivacyStatus)
	}
}

// TestPublishYouTubeEditorSession_HappyPathResponseContainsStatus is the
// P0 contract lock for the by-id publish pathway
// (POST /api/v1/youtube/editor-sessions/{id}/publish). The dark editor's
// publish handler reads publishResult.status and broadcasts it on the
// BroadcastChannel -- a missing or wrong JSON key there breaks the live
// card update for every session published via this endpoint. The
// by-project pathway has its own equivalent test in
// youtube_editor_sessions_by_project_test.go; this test closes the
// by-id gap so a future refactor that bypasses the shared
// executePublishYouTubeEditorSession helper (which already populates
// Status) cannot silently drop the field on this route.
//
// Two assertion layers:
//  1. Decoded struct: publishYouTubeEditorSessionResponse.Status must
//     equal "published" -- catches handler-vs-DTO drift (e.g. writing
//     the response literal without the Status: edit.Status line).
//  2. Raw wire body: the response bytes must contain the literal
//     substring `"status":"published"` -- catches JSON-tag drift on
//     the struct (e.g. someone renaming `json:"status"` would still
//     leave the decoded struct populated but the wire key would no
//     longer be `"status"`).
func TestPublishYouTubeEditorSession_HappyPathResponseContainsStatus(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	store := &mockUserStore{
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
	}

	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "public",
				Status:            "editing",
			}, nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}
	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		return "https://www.youtube.com/watch?v=" + videoID, nil
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, errors.New("token not found")
			},
		}),
	)

	body := bytes.NewReader([]byte(`{"privacy_status":"public"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", body)
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	r.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	// Layer 1: decoded struct catches handler-vs-DTO drift.
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("expected response.status=published, got %q (full resp: %+v)", resp.Status, resp)
	}
	if resp.VideoID != "ytvideo123" {
		t.Fatalf("expected response.video_id=ytvideo123, got %q", resp.VideoID)
	}
	if resp.PrivacyStatus != "public" {
		t.Fatalf("expected response.privacy_status=public, got %q", resp.PrivacyStatus)
	}

	// Layer 2: raw-wire substring catches JSON-tag drift on the struct.
	if !strings.Contains(rec.Body.String(), `"status":"published"`) {
		t.Fatalf("expected raw wire body to contain literal `\"status\":\"published\"`, got %s", rec.Body.String())
	}
}

func TestPublishYouTubeEditorSession_TooLongTitle(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}

	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
	)

	payload := map[string]any{"title": strings.Repeat("a", 101)}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_WithoutTitleDescription(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	store := &mockUserStore{
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
	}

	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "public",
				Status:            "editing",
			}, nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		if opts.Title != "" || opts.Description != "" {
			t.Errorf("expected empty title and description, got title=%q description=%q", opts.Title, opts.Description)
		}
		return "https://www.youtube.com/watch?v=" + videoID, nil
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, errors.New("token not found")
			},
		}),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_IdempotentWhenPublished(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "published",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}

	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for published session, got %d: %s", w.Code, w.Body.String())
	}
}
