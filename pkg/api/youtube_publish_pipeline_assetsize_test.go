package api

// Asset-size sub-flow test — the 2 MB download cap. Extracted from
// youtube_publish_pipeline_test.go by sub-flow.

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestPublishPipeline_AssetLargerThan2MB_Rejected asserts that the
// orchestrator refuses a publish on an asset whose bytes exceed the
// 2 MB cap the downloadThumbnailBytes helper enforces. The orchestrator
// MUST NOT reach publishThumbnailFn for an oversize asset.
//
// We avoid stuffing 3 MB of zeros into a test binary: a synthetic
// Content-Length header well above 2 MB triggers the helper's
// pre-read size guard ("thumbnail download exceeded max size"). The
// httptest server never needs to stream the actual bytes.
func TestPublishPipeline_AssetLargerThan2MB_Rejected(t *testing.T) {
	const oversize int64 = 3 * 1024 * 1024

	var publishCalled bool
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			publishCalled = true
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
	}

	media := newMockMediaStore()
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   oversize,
		Status:      models.MediaAssetStatusReady,
	}

	storage := newMockStorageProvider()
	// (assetURLFn set below, after the oversize-rejection server
	// is constructed; the helper rejects the download pre-fetch so
	// the URL is exercised but never actually FETCHED.)
	_ = storage

	router := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == 42 {
					return &models.PlatformAccount{ID: 42, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Username: "testchannel", Status: models.AccountStatusActive}, nil
				}
				return nil, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == 7 {
					return &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(&mockYouTubeVideoEditStore{
			findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
				if projectID == "ve-project-123" {
					media := "asset-uuid-123"
					return &models.YouTubeVideoEdit{
						ID:                "session-123",
						WorkspaceID:       7,
						PlatformAccountID: 42,
						YouTubeVideoID:    "ytvideo123",
						VeloxProjectID:    "ve-project-123",
						ThumbnailMediaID:  &media,
						DesiredPrivacy:    "public",
						Status:            "editing",
					}, nil
				}
				return nil, nil
			},
		}),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
				if id == 42 {
					// Asset download would have already been rejected before
					// the token is consulted, but the vault is wired so the
					// router + orchestrator don't 503 on missing-dep.
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, nil
			},
		}),
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.FormatInt(oversize, 10))
		// The helper rejects before reading the body -> cheaper to
		// just send an empty body to satisfy net/http mechanics.
	}))
	defer srv.Close()
	storage.assetURLFn = func(_ string) string { return srv.URL }

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on oversize asset (cap=2MB), got %d (body=%s)", w.Code, w.Body.String())
	}
	if publishCalled {
		t.Fatalf("publishThumbnailFn MUST NOT be called when the asset exceeds the 2 MB cap")
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "exceeded max size") {
		t.Fatalf("expected body to mention the size-cap (`exceeded max size`), got %s", w.Body.String())
	}
}
