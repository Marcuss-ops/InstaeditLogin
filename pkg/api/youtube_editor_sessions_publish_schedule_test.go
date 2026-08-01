package api

// Scheduled-publish handler tests.
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
	"testing"
	"time"
)

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

	publishAt := time.Now().UTC().Add(24 * time.Hour)
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, gotPublishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
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
	// Bug-fix Blocco #5 P0 #1: privacy + publish_at validation moved AFTER
	// the session load, so this test now supplies a resolvable session.
	// Payload privacy="public" wins (over edit.DesiredPrivacy="public"),
	// so resolved privacy is "public"; future publish_at + privacy !=
	// "private" → 400.
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
	r := newPublishRouter(t, workspace, editStore)

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
	// Bug-fix Blocco #5 P0 #1: privacy + publish_at validation moved AFTER
	// the session load, so this test now supplies a resolvable session.
	// Payload privacy="private" wins (over edit.DesiredPrivacy="private"),
	// so resolved privacy is "private"; past publish_at → 400.
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "private",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}
	r := newPublishRouter(t, workspace, editStore)

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

// TestPublishYouTubeEditorSession_ScheduledFromSessionPrivacy is the
// regression test for Bug #1 in the original assessment ("Validazione
// anticipata della privacy"). Pre-fix, this exact payload returned 400
// because the early body-only privacyStatus defaulted missing → "public",
// and then the early "future publish_at requires private" rule fired (400)
// — even though the session itself was already private. Post-fix, the
// late resolver falls back to edit.DesiredPrivacy when the payload omits
// privacy_status, so the request is correctly accepted with a 200.
//
// This test is the proof that moving the privacy resolution + validation
// AFTER the session load actually fixes the bug. Without this test the
// fix would be silent (no behaviour change in any other test case).
func TestPublishYouTubeEditorSession_ScheduledFromSessionPrivacy(t *testing.T) {
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

	var capturedPrivacy string
	var publishCalled bool
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			// The key field — session DesiredPrivacy is "private".
			// The payload below omits privacy_status; the late
			// resolver must fall through to this value for the
			// future publish_at to be accepted.
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "private",
				Status:            "editing",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
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

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, gotPublishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		publishCalled = true
		capturedPrivacy = privacyStatus
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

	// Payload DELIBERATELY omits privacy_status — the regression point.
	publishAt := time.Now().UTC().Add(24 * time.Hour)
	payload := map[string]any{
		"publish_at": publishAt.Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for scheduled publish inferred from session.DesiredPrivacy, got %d: %s", w.Code, w.Body.String())
	}
	if !publishCalled {
		t.Fatalf("expected PublishThumbnail to be called")
	}
	if capturedPrivacy != "private" {
		t.Errorf("resolved privacy must be 'private' (from session.DesiredPrivacy), got %q", capturedPrivacy)
	}
}
