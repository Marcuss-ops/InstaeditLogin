package api

// Shared fixtures for the youtube publish pipeline test suite —
// extracted from youtube_publish_pipeline_test.go so the sub-flow
// files (thumbnail, asset-size, localization, dedup/CAS) only carry
// behavior. `apiError` is also referenced by
// nvidia_metadata_publish_e2e_test.go, so it lives here in the
// shared set rather than inside a single sub-flow file.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// byProjectPublishPayload is the minimal body accepted by
// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish.
// Kept tiny so the structural assertions stay focused on what each
// test cares about (status field, idempotency, byte identity, etc).
func byProjectPublishPayload(t *testing.T, additional map[string]any) []byte {
	t.Helper()
	base := map[string]any{"privacy_status": "public"}
	for k, v := range additional {
		base[k] = v
	}
	body, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal publish payload: %v", err)
	}
	return body
}

// apiError is a minimal error string carrier so the
// TestPublishPipeline_ThumbnailSetError_LeadsToFailed test injects
// a recognisable YouTube error message into the orchestrator's
// last_error column. type name `apiError` doesn't collide with
// anything in pkg/api.
type apiError struct{ msg string }

func (e *apiError) Error() string { return e.msg }

// backbonePlusCustomMocks mirrors commonPublishBackbone's wiring but
// leaves the publishThumbnailFn / update callback slots untouched
// (commonPublishBackbone's defensive `if youTubeSvc.publishThumbnailFn
// == nil` only fills the default; the editStore.update override
// fields are NOT auto-filled, but constructing inline here keeps the
// helper's responsibilities localised to the shared-feel tests).
func backbonePlusCustomMocks(t *testing.T, youTubeSvc *mockYouTubeOAuthServiceForEditor, editStore *mockYouTubeVideoEditStore) *Router {
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
	editStore.findFn = func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
		if id == "session-123" {
			media := "asset-uuid-123"
			return &models.YouTubeVideoEdit{ID: id, WorkspaceID: workspace.ID, PlatformAccountID: account.ID, YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123", ThumbnailMediaID: &media, DesiredPrivacy: "public", Status: "editing"}, nil
		}
		return nil, nil
	}
	editStore.findByProjectFn = func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
		if projectID == "ve-project-123" {
			media := "asset-uuid-123"
			return &models.YouTubeVideoEdit{ID: "session-123", WorkspaceID: workspace.ID, PlatformAccountID: account.ID, YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123", ThumbnailMediaID: &media, DesiredPrivacy: "public", Status: "editing"}, nil
		}
		return nil, nil
	}
	if youTubeSvc.publishThumbnailFn == nil {
		youTubeSvc.publishThumbnailFn = func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		}
	}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	t.Cleanup(srv.Close)

	media := newMockMediaStore()
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID: "asset-uuid-123", UserID: 1, UploadKey: "uploads/1/thumb.jpg", ContentType: "image/jpeg", SizeBytes: int64(len(thumbnailBytes)), Status: models.MediaAssetStatusReady,
	}
	storage := newMockStorageProvider()
	storage.assetURLFn = func(_ string) string { return srv.URL }

	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == account.ID {
					return account, nil
				}
				return nil, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		}}),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, nil
			},
		}),
	)
}
