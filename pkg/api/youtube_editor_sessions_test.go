package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockYouTubeVideoEditStore is an test seam for YouTubeVideoEditStore.
type mockYouTubeVideoEditStore struct {
	created         []*models.YouTubeVideoEdit
	findFn          func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error)
	findByProjectFn func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	update          func(ctx context.Context, edit *models.YouTubeVideoEdit) error
}

func (m *mockYouTubeVideoEditStore) Create(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	m.created = append(m.created, edit)
	return nil
}

func (m *mockYouTubeVideoEditStore) FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
	if m.findFn != nil {
		return m.findFn(ctx, id)
	}
	return nil, nil
}

func (m *mockYouTubeVideoEditStore) FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
	if m.findByProjectFn != nil {
		return m.findByProjectFn(ctx, projectID)
	}
	return nil, nil
}

func (m *mockYouTubeVideoEditStore) Update(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	if m.update != nil {
		return m.update(ctx, edit)
	}
	return nil
}

// mockYouTubeOAuthServiceForEditor implements the subset of
// YouTubeOAuthService needed by the editor session handler.
type mockYouTubeOAuthServiceForEditor struct {
	getVideoFn         func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error)
	publishThumbnailFn func(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, title, description string) (string, error)
}

func (m *mockYouTubeOAuthServiceForEditor) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) GetTokenInfo(ctx context.Context, accessToken string) (*services.YouTubeTokenInfo, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	return errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) FetchEarnings(ctx context.Context, accessToken, channelID string, days int) ([]repository.AccountMetricPoint, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) ClientID() string { return "test-client-id" }
func (m *mockYouTubeOAuthServiceForEditor) GetYouTubeVideo(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
	if m.getVideoFn != nil {
		return m.getVideoFn(ctx, accessToken, videoID)
	}
	return &models.YouTubeVideoDetails{
		ID:           videoID,
		Title:        "Test Video",
		ChannelID:    "UC123",
		ThumbnailURL: "https://i.ytimg.com/default.jpg",
		Privacy:      "private",
		UploadStatus: "processed",
	}, nil
}
func (m *mockYouTubeOAuthServiceForEditor) SetThumbnail(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error {
	return errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) UpdateVideoPrivacy(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, title, description string) error {
	return errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, title, description string) (string, error) {
	if m.publishThumbnailFn != nil {
		return m.publishThumbnailFn(ctx, accessToken, videoID, thumbnailData, mimeType, privacyStatus, publishAt, title, description)
	}
	return "", errors.New("not implemented")
}

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

	var updated *models.YouTubeVideoEdit
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
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
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
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, title, description string) (string, error) {
		publishCalled = true
		if string(data) != string(thumbnailBytes) {
			t.Errorf("expected thumbnail data %q, got %q", string(thumbnailBytes), string(data))
		}
		if privacyStatus != "public" {
			t.Errorf("expected privacyStatus public, got %s", privacyStatus)
		}
		if title != "Updated title" {
			t.Errorf("expected title \"Updated title\", got %q", title)
		}
		if description != "Updated description" {
			t.Errorf("expected description \"Updated description\", got %q", description)
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
	if updated == nil || updated.Status != "published" {
		t.Fatalf("expected session status published, got %v", updated)
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

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(editStore),
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

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, title, description string) (string, error) {
		if title != "" || description != "" {
			t.Errorf("expected empty title and description, got title=%q description=%q", title, description)
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

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(editStore),
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

func TestPublishYouTubeEditorSession_ScheduledPublishing(t *testing.T) {
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
				Status:              "editing",
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

	publishAt := time.Now().UTC().Add(24 * time.Hour)
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, gotPublishAt *time.Time, title, description string) (string, error) {
		if privacyStatus != "private" {
			t.Errorf("expected privacyStatus private for scheduled publishing, got %s", privacyStatus)
		}
		if gotPublishAt == nil {
			t.Fatalf("expected publishAt for scheduled publishing, got nil")
		}
		if gotPublishAt.IsZero() {
			t.Errorf("expected non-zero publishAt for scheduled publishing")
		}
		if gotPublishAt.Format(time.RFC3339) != publishAt.Format(time.RFC3339) {
			t.Errorf("publishAt mismatch: want %v, got %v", publishAt, gotPublishAt)
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
		"privacy_status": "private",
		"publish_at":     publishAt.Format(time.RFC3339),
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
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrivacyStatus != "private" {
		t.Errorf("expected privacy_status private, got %s", resp.PrivacyStatus)
	}
	if resp.PublishedAt == nil {
		t.Errorf("expected published_at in response")
	}
}

func TestPublishYouTubeEditorSession_ScheduledPublishingRequiresPrivate(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	// Validation happens before the session is looked up, so a minimal
	// router with just workspace/edit stores is sufficient.
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(&mockYouTubeVideoEditStore{}),
	)

	payload := map[string]any{
		"privacy_status": "public",
		"publish_at":     time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for scheduled publishing without private status, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_PastPublishAtRejected(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	// Validation happens before the session is looked up, so a minimal
	// router with just workspace/edit stores is sufficient.
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(&mockYouTubeVideoEditStore{}),
	)

	payload := map[string]any{
		"privacy_status": "private",
		"publish_at":     time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for past publish_at, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_PublishingInFlightReturnsConflict(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "publishing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
				UpdatedAt:        time.Now().UTC().Add(-30 * time.Second),
			}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(editStore),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for in-flight publishing session, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_RetryFromFailed(t *testing.T) {
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

	var updated *models.YouTubeVideoEdit
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
				Status:              "failed",
				LastError:         "previous failure",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
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

	var publishCalls int
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, title, description string) (string, error) {
		publishCalls++
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
	if publishCalls != 1 {
		t.Errorf("expected 1 publish call, got %d", publishCalls)
	}
	if updated == nil || updated.Status != "published" {
		t.Fatalf("expected session status published after retry, got %v", updated)
	}
	if updated.LastError != "" {
		t.Errorf("expected last_error to be cleared, got %q", updated.LastError)
	}
}

func strPtr(s string) *string { return &s }

func TestCreateYouTubeEditorSession_HappyPath(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{
		ID:      7,
		OwnerID: 1,
		Name:    "Test Workspace",
	}
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
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}
	editStore := &mockYouTubeVideoEditStore{}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithCredentialVault(vault),
		WithYouTubeService(youTubeSvc),
		WithYouTubeVideoEditStore(editStore),
		WithEditorURL("https://editor.instaedit.org"),
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
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp createYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SessionID == "" {
		t.Errorf("session_id should be present")
	}
	if resp.VeloxProjectID == "" {
		t.Errorf("velox_project_id should be present")
	}
	if resp.EditorURL == "" {
		t.Errorf("editor_url should be present")
	}
	if len(editStore.created) != 1 {
		t.Fatalf("expected one edit session to be created, got %d", len(editStore.created))
	}
	edit := editStore.created[0]
	if edit.WorkspaceID != workspace.ID {
		t.Errorf("workspace_id: want %d, got %d", workspace.ID, edit.WorkspaceID)
	}
	if edit.PlatformAccountID != account.ID {
		t.Errorf("platform_account_id: want %d, got %d", account.ID, edit.PlatformAccountID)
	}
	if edit.YouTubeVideoID != "abc123" {
		t.Errorf("youtube_video_id: want abc123, got %s", edit.YouTubeVideoID)
	}
	if edit.Status != "editing" {
		t.Errorf("status: want editing, got %s", edit.Status)
	}
}

func TestCreateYouTubeEditorSession_RequiresAuth(t *testing.T) {
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)
	payload := map[string]any{
		"workspace_id":        1,
		"platform_account_id": 1,
		"youtube_video_id":  "abc123",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateYouTubeEditorSession_MissingRequiredFields(t *testing.T) {
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)
	payload := map[string]any{
		"workspace_id":        1,
		"platform_account_id": 1,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateYouTubeEditorSession_PublicVideoRejected(t *testing.T) {
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
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				Title:        "Already Public",
				ChannelID:    account.PlatformUserID,
				ThumbnailURL: "https://i.ytimg.com/default.jpg",
				Privacy:      "public",
				UploadStatus: "processed",
			}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithCredentialVault(vault),
		WithYouTubeService(youTubeSvc),
		WithYouTubeVideoEditStore(editStore),
		WithEditorURL("https://editor.instaedit.org"),
	)

	payload := map[string]any{
		"workspace_id":        workspace.ID,
		"platform_account_id": account.ID,
		"youtube_video_id":    "public123",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if len(editStore.created) != 0 {
		t.Fatalf("expected no edit session to be created, got %d", len(editStore.created))
	}
}

func TestUpdateYouTubeEditorSession_StoresThumbnailMediaID(t *testing.T) {
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
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}

	var updated *models.YouTubeVideoEdit
	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "abc123",
				VeloxProjectID:    "ve-project-123",
				Status:            "editing",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
		},
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
	)

	payload := map[string]any{"thumbnail_media_id": "asset-uuid-123"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil {
		t.Fatalf("expected session to be updated")
	}
	if updated.ThumbnailMediaID == nil || *updated.ThumbnailMediaID != "asset-uuid-123" {
		t.Fatalf("expected thumbnail_media_id to be asset-uuid-123, got %v", updated.ThumbnailMediaID)
	}
}
