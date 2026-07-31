package api

// Update handler tests.
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
	"strings"
	"testing"
	"time"
)

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
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			media := thumbnailMediaID
			edit := &models.YouTubeVideoEdit{
				ID:                sessionID,
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "abc123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  &media,
				Status:            "editing",
				UpdatedAt:         time.Now().UTC(),
			}
			updated = edit
			return edit, nil
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

// TestUpdateYouTubeEditorSession_AssetNotReady_ReturnsConflict verifies
// that the refactored PATCH-by-project endpoint uses the shared
// resolver and returns the same 409 the direct /thumbnail endpoint
// would return for a non-ready asset.
func TestUpdateYouTubeEditorSession_AssetNotReady_ReturnsConflict(t *testing.T) {
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
		Status: models.MediaAssetStatusPending,
	}

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

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for non-ready asset, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateYouTubeEditorSession_PublishingStatus_Returns409 verifies
// the shared-resolver contract guarantee: a PATCH that arrives while
// the publish orchestrator has already called MarkPublishing
// (pkg/api/youtube_editor_sessions_by_project.go::executePublishYouTubeEditorSession)
// MUST be rejected with 409, not silently overwrite thumbnail_media_id
// on a row the publish orchestrator is about to use.
//
// The AttachThumbnail repo call's WHERE clause is `status IN
// ('editing','failed')`; a row whose status moved to 'publishing'
// no longer matches, returns 0 rows, and the repository surfaces
// ErrYouTubeVideoEditNotFound. The shared resolver
// (attachThumbnailToSession, pkg/api/youtube_editor_sessions.go:413)
// maps that to errAttachSessionNotEditable and
// writeAttachThumbnailError returns HTTP 409.
//
// This test asserts the by-project path shares that behaviour -- a
// late PATCH must not be able to swap the thumbnail out from under
// an in-flight publish.
func TestUpdateYouTubeEditorSession_PublishingStatus_Returns409(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
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

	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "abc123",
				VeloxProjectID:    "ve-project-123",
				// The /publish handler has flipped this row to
				// 'publishing' via MarkPublishing CAS (5-minute
				// in-flight guard) and is mid-flight on the
				// thumbnail.set YouTube call. A late PATCH
				// arriving now must not be able to overwrite
				// the asset the orchestrator is about to ship.
				Status: "publishing",
			}, nil
		},
		// Simulate the production CAS-loss for a row whose status
		// is no longer in the allow-list ('editing','failed').
		// The shared resolver maps ErrYouTubeVideoEditNotFound
		// to errAttachSessionNotEditable -> 409.
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			return nil, repository.ErrYouTubeVideoEditNotFound
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

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on PATCH while session is publishing, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "editor session is not in an editable state") {
		t.Fatalf("expected 409 body to mention 'editor session is not in an editable state', got %s", w.Body.String())
	}
}

// TestUpdateYouTubeEditorSession_PublishedStatus_Returns409 verifies
// the terminal-state guard: once a session is 'published' the
// thumbnail IS the canonical YouTube-side thumbnail (the orchestrator
// uploaded it during publish). A late PATCH must NOT be able to
// re-stamp thumbnail_media_id on a row whose YouTube-side state is
// already terminal -- that would create a Go/YouTube divergence that
// the next operator click would either pick up or silently lose.
//
// Same shared-resolver path as the 'publishing' test: AttachThumbnail
// CAS predicates status IN ('editing','failed'), 'published' matches
// 0 rows, the shared resolver surfaces ErrYouTubeVideoEditNotFound -> 409.
func TestUpdateYouTubeEditorSession_PublishedStatus_Returns409(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
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

	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "abc123",
				VeloxProjectID:    "ve-project-123",
				// Terminal state -- the orchestrator has reversed to
				// status='published' (MarkPublishedWithActualPrivacy).
				// The YouTube-side thumbnail IS the asset referenced
				// on this row. Mutating it via a PATCH would silently
				// diverge the Go row from YouTube's stored thumbnail.
				Status: "published",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			return nil, repository.ErrYouTubeVideoEditNotFound
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

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on PATCH after session is published, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "editor session is not in an editable state") {
		t.Fatalf("expected 409 body to mention 'editor session is not in an editable state', got %s", w.Body.String())
	}
}

// TestUpdateYouTubeEditorSession_WorkspaceMismatch_ReturnsForbidden verifies
// that the shared resolver returns 403 when the caller does not own
// the session's workspace.
func TestUpdateYouTubeEditorSession_WorkspaceMismatch_ReturnsForbidden(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 2, Name: "Other Workspace"}
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

	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:             "session-123",
				WorkspaceID:    workspace.ID,
				YouTubeVideoID: "abc123",
				VeloxProjectID: "ve-project-123",
				Status:         "editing",
			}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
	)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for workspace mismatch, got %d: %s", w.Code, w.Body.String())
	}
}

// TestUpdateYouTubeEditorSession_AssetOwnershipMismatch_ReturnsForbidden
// verifies the shared resolver returns 403 when the asset belongs to
// another user.
func TestUpdateYouTubeEditorSession_AssetOwnershipMismatch_ReturnsForbidden(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
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
		UserID: 2,
		Status: models.MediaAssetStatusReady,
	}

	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:             "session-123",
				WorkspaceID:    workspace.ID,
				YouTubeVideoID: "abc123",
				VeloxProjectID: "ve-project-123",
				Status:         "editing",
			}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
	)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for asset ownership mismatch, got %d: %s", w.Code, w.Body.String())
	}
}
