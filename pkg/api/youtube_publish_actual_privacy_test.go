package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// commonPublishBackbone builds the minimal router + dependencies
// shared by the three P0#7 actual_privacy read-back tests:
//   - one workspace owned by user 1
//   - one editor session row in 'editing' state
//   - one media asset ready for download
//   - one vault returning a valid access token
//
// Each test injects:
//   - the publishThumbnailFn override on youTubeSvc
//   - the getVideoFn override on youTubeSvc
//   - the markPublishedWithActualPrivacyFn override on editStore
//
// then POSTs the publish payload and asserts the captured CAS args
// + the response body / status code.
func commonPublishBackbone(t *testing.T, youTubeSvc *mockYouTubeOAuthServiceForEditor, editStore *mockYouTubeVideoEditStore) (*Router, *models.YouTubeVideoEdit) {
	t.Helper()
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

	edit := &models.YouTubeVideoEdit{
		ID:                "session-123",
		WorkspaceID:       workspace.ID,
		PlatformAccountID: account.ID,
		YouTubeVideoID:    "ytvideo123",
		VeloxProjectID:    "ve-project-123",
		ThumbnailMediaID:  strPtr("asset-uuid-123"),
		DesiredPrivacy:    "public",
		Status:            "editing",
	}
	// Inject findFn so the mockYouTubeVideoEditStore.MarkPublishing
	// fallback (first-call-wins simulator) returns a 'publishing'
	// row from the same edit.
	editStore.findFn = func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
		if id == edit.ID {
			return edit, nil
		}
		return nil, nil
	}
	// Inject findByProjectFn so the by-project handler's
	// FindByVeloxProjectID lookup resolves the edit row to the
	// test's row — without this the handler returns 404 BEFORE
	// MarkPublishing / PublishThumbnail / GetYouTubeVideo /
	// MarkPublishedWithActualPrivacy are exercised.
	editStore.findByProjectFn = func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
		if projectID == edit.VeloxProjectID {
			return edit, nil
		}
		return nil, nil
	}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	t.Cleanup(server.Close)

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	// Default PublishThumbnail success — tests may override.
	if youTubeSvc.publishThumbnailFn == nil {
		youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		}
	}

	return mustNewRouterWithDefaults(
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
	), edit
}

// TestPublishByProject_ReadBackConfirmed: the videos.list read-back
// returns the SAME privacy the orchestrator requested. The CAS must
// stamp actual_privacy=public + youtube_sync_status='confirmed'.
// The SPA colours the privacy badge green for `confirmed`.
func TestPublishByProject_ReadBackConfirmed(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
	}
	var capturedActualPrivacy, capturedSyncStatus string
	var capturedID string
	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			capturedID = id
			capturedActualPrivacy = actualPrivacy
			capturedSyncStatus = syncStatus
			row := &models.YouTubeVideoEdit{
				ID:                id,
				WorkspaceID:       7,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				Status:            "published",
				DesiredPrivacy:    "public",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
			}
			return row, nil
		},
	}
	r, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	payload := []byte(`{"privacy_status":"public"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedID != "session-123" {
		t.Errorf("expected CAS on session-123, got %s", capturedID)
	}
	if capturedActualPrivacy != "public" {
		t.Errorf("expected actual_privacy=public, got %q", capturedActualPrivacy)
	}
	if capturedSyncStatus != "confirmed" {
		t.Errorf("expected sync_status=confirmed, got %q", capturedSyncStatus)
	}

	// Verify the response body carries the same projection.
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ActualPrivacy != "public" {
		t.Errorf("expected response actual_privacy=public, got %q", resp.ActualPrivacy)
	}
	if resp.YouTubeSyncStatus != "confirmed" {
		t.Errorf("expected response sync_status=confirmed, got %q", resp.YouTubeSyncStatus)
	}
}

// TestPublishByProject_ReadBackDrift: videos.list returns a DIFFERENT
// privacy than requested. The CAS must stamp actual_privacy=<youtube's
// value> + youtube_sync_status='drift'. The publish is still
// terminal-published; the drift_reconciler sweeps the row on its
// next tick and attempts convergence.
func TestPublishByProject_ReadBackDrift(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			// YouTube reports the video as 'private' even though
			// the operator requested 'public'. This can happen
			// during a publish_at fluke or a scheduled_publish
			// race window — the orchestrator stamps drift + lets
			// the reconciler converge.
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "private",
			}, nil
		},
	}
	var capturedActualPrivacy, capturedSyncStatus string
	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			capturedActualPrivacy = actualPrivacy
			capturedSyncStatus = syncStatus
			row := &models.YouTubeVideoEdit{
				ID:                id,
				Status:            "published",
				DesiredPrivacy:    "public",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
			}
			return row, nil
		},
	}
	r, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	payload := []byte(`{"privacy_status":"public"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (publish still terminal), got %d: %s", w.Code, w.Body.String())
	}
	if capturedActualPrivacy != "private" {
		t.Errorf("expected actual_privacy=private (the YouTube-confirmed value), got %q", capturedActualPrivacy)
	}
	if capturedSyncStatus != "drift" {
		t.Errorf("expected sync_status=drift, got %q", capturedSyncStatus)
	}
}

// TestPublishByProject_ReadBackError: videos.list read-back errors.
// The publish is still terminal-published (publish itself
// succeeded); the orchestrator stamps actual_privacy=NULL +
// youtube_sync_status='pending' and the drift_reconciler's
// partial-index sweep retries later. The response surfaces the
// pending state so the SPA can show the "syncing with YouTube…"
// copy.
func TestPublishByProject_ReadBackError(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return nil, errors.New("transient videos.list 503")
		},
	}
	var capturedActualPrivacy, capturedSyncStatus string
	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			capturedActualPrivacy = actualPrivacy
			capturedSyncStatus = syncStatus
			// Mirror the production CAS: actual_privacy is NULL
			// (empty string is the dereference-of-nil-pointer
			// projection); sync_status is 'pending'.
			empty := ""
			row := &models.YouTubeVideoEdit{
				ID:                id,
				Status:            "published",
				DesiredPrivacy:    "public",
				ActualPrivacy:     nil,
				YouTubeSyncStatus: &syncStatus,
			}
			_ = empty // ActualPrivacy stays nil in the row on the read-back-failure path
			return row, nil
		},
	}
	r, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	payload := []byte(`{"privacy_status":"public"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (publish terminal despite read-back error), got %d: %s", w.Code, w.Body.String())
	}
	if capturedActualPrivacy != "" {
		t.Errorf("expected actual_privacy='' (read-back failed), got %q", capturedActualPrivacy)
	}
	if capturedSyncStatus != "pending" {
		t.Errorf("expected sync_status=pending (defer to reconciler), got %q", capturedSyncStatus)
	}

	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ActualPrivacy != "" {
		t.Errorf("expected response actual_privacy empty, got %q", resp.ActualPrivacy)
	}
	if resp.YouTubeSyncStatus != "pending" {
		t.Errorf("expected response sync_status=pending, got %q", resp.YouTubeSyncStatus)
	}
}

// TestPublishByProject_MarkPublishedCASLoss: when the
// MarkPublishedWithActualPrivacy CAS returns
// ErrYouTubeVideoEditNotFound, the orchestrator must map to 409 —
// not 500. This is the runtime guard for a concurrent reconciler
// stealing the row between PublishThumbnail success and the final
// CAS stamp.
func TestPublishByProject_MarkPublishedCASLoss(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{ID: videoID, ChannelID: "UC123", Privacy: "public", UploadStatus: "processed"}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			return nil, repository.ErrYouTubeVideoEditNotFound
		},
	}
	r, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	payload := []byte(`{"privacy_status":"public"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on MarkPublished CAS-loss, got %d: %s", w.Code, w.Body.String())
	}
}
