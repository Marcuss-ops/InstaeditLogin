package api

// Thumbnail attachment tests.
import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAttachThumbnail_HappyPath — POST with valid payload on a
// session in 'editing' state, asset ready + owned by caller,
// workspace accessible. Expects 200 + response body with
// session_id + thumbnail_media_id.
func TestAttachThumbnail_HappyPath(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}
	var attachedSessionID, attachedMediaID string
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				YouTubeVideoID:    "yt-abc",
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachedSessionID = sessionID
			attachedMediaID = thumbnailMediaID
			return &models.YouTubeVideoEdit{
				ID:                sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				ThumbnailMediaID:  &thumbnailMediaID,
				Status:            "editing",
			}, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if attachedSessionID != rig.sessionID {
		t.Fatalf("expected AttachThumbnail called with session_id=%s, got %s", rig.sessionID, attachedSessionID)
	}
	if attachedMediaID != "asset-uuid-123" {
		t.Fatalf("expected AttachThumbnail called with media_id=asset-uuid-123, got %s", attachedMediaID)
	}
	var resp attachThumbnailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SessionID != rig.sessionID {
		t.Fatalf("expected response session_id=%s, got %s", rig.sessionID, resp.SessionID)
	}
	if resp.ThumbnailMediaID != "asset-uuid-123" {
		t.Fatalf("expected response thumbnail_media_id=asset-uuid-123, got %s", resp.ThumbnailMediaID)
	}
	if resp.ThumbnailStatus != "editing" {
		t.Fatalf("expected response thumbnail_status=editing, got %s", resp.ThumbnailStatus)
	}
}

// TestAttachThumbnail_SessionNotFound — the session_id does not
// resolve in the store. Expects 404 (without touching the asset).
func TestAttachThumbnail_SessionNotFound(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return nil, nil // session not found
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/missing-id/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when session lookup fails")
	}
}

// TestAttachThumbnail_WorkspaceMismatch — the session's workspace is
// owned by a different user. Expects 403 (explicit gate per user spec;
// deviates from the existing handlers which return 404 for the same
// scenario).
func TestAttachThumbnail_WorkspaceMismatch(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}
	attachCalled := false
	// Build a workspace owned by user 99 (caller is user 1).
	foreignWorkspace := &models.Workspace{ID: 7, OwnerID: 99, Name: "Foreign Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       foreignWorkspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
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
				if id == foreignWorkspace.ID {
					return foreignWorkspace, nil
				}
				return nil, nil
			},
		}),
		WithMediaStore(rig.mediaStore),
		WithYouTubeVideoEditStore(editStore),
	)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when workspace check fails")
	}
}

// TestAttachThumbnail_AssetNotFound — the supplied media_id does not
// resolve in the media store. Expects 404 (no asset exists).
func TestAttachThumbnail_AssetNotFound(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "missing-asset-id"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when asset lookup fails")
	}
}

// TestAttachThumbnail_AssetNotReady — the asset exists but its
// Status is not 'ready' (e.g. 'uploading', 'failed', 'deleted').
// Expects 409.
func TestAttachThumbnail_AssetNotReady(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatus("uploading"),
	}
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when asset is not ready")
	}
}

// TestAttachThumbnail_AssetOwnershipMismatch — asset exists but is
// owned by a different user. Expects 403 (anti-cross-tenant probe).
func TestAttachThumbnail_AssetOwnershipMismatch(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 99, // belongs to a different user
		Status: models.MediaAssetStatusReady,
	}
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when asset ownership check fails")
	}
}

// TestAttachThumbnail_CASLoss — the session is in a state that does
// not match the AttachThumbnail CAS predicate (status='publishing' or
// 'published'). Expects 409, no asset mutation.
func TestAttachThumbnail_CASLoss(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			// Pretend the session just transitioned to 'publishing' between
			// the read at the top of the handler and the AttachThumbnail
			// call. The mock faithfully reports the not-found sentinel.
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			return nil, repository.ErrYouTubeVideoEditNotFound
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAttachThumbnail_MissingPayload — body lacks thumbnail_media_id
// or is empty. Expects 400.
func TestAttachThumbnail_MissingPayload(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	editStore := &mockYouTubeVideoEditStore{
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			t.Fatalf("AttachThumbnail must NOT be called when payload is missing")
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
