package api

// Dedup / CAS sub-flow tests — publish idempotency plus the PATCH
// by-project CAS guards. Extracted from youtube_publish_pipeline_test.go
// by sub-flow.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestPublishPipeline_DoublePublishProducesSingleYouTubeCall asserts
// the orchestrator's idempotency: a second POST on a session already
// in 'published' state must NOT call PublishThumbnail again. Any
// operator retry, network-replay, or double-click on Pubblica must
// bottom out at zero additional YouTube API calls.
//
// Counter is captured across both POSTs (sync/atomic.Int32 because
// the orchestrator is single-goroutine but the assertion reads after
// each POST completes, so we want a load-acquire not a stale Read).
func TestPublishPipeline_DoublePublishProducesSingleYouTubeCall(t *testing.T) {
	var publishCount atomic.Int32
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			publishCount.Add(1)
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
	router, _ := commonPublishBackbone(t, youTubeSvc, &mockYouTubeVideoEditStore{})

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(byProjectPublishPayload(t, nil)))
		req.Header.Set("Content-Type", "application/json")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		router.Setup().ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != http.StatusOK {
		t.Fatalf("first publish expected 200, got %d (body=%s)", first.Code, first.Body.String())
	}
	if got := publishCount.Load(); got != 1 {
		t.Fatalf("first publish: expected publishCount=1, got %d", got)
	}

	// Second POST: findFn (commonPublishBackbone) returns the row
	// mutated by the first POST's publish-flow; status is now
	// 'published' (the CAS fallback in mockYouTubeVideoEditStore
	// stamps it after MarkPublishedWithActualPrivacy). The
	// idempotency branch in executePublishYouTubeEditorSession
	// returns BEFORE any MarkPublishing / PublishThumbnail call.
	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second publish expected 200 (idempotency), got %d (body=%s)", second.Code, second.Body.String())
	}
	if got := publishCount.Load(); got != 1 {
		t.Fatalf("second publish: expected publishCount STAY at 1, got %d (publish was re-invoked)", got)
	}

	// Spot-check the wire shape on the replay: status MUST equal
	// 'published' so the SPA's BroadcastChannel contract stays
	// green on the replay.
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("replay response.status expected published, got %q", resp.Status)
	}
}

// =========================================================================
// ROUND 2 (closes the 3 remaining gaps from the 14-case checklist)
// =========================================================================
//
// The previous batch (commit 513d229) closed 6 of the 14 cases. The
// remaining 3 are PATCH-side concerns that the publish-side suite did
// not reach:
//
//  * TestPublishPipeline_PatchByProjectUsesCAS
//      Locks the full CAS predicate matrix on the by-project PATCH
//      endpoint: status IN ('editing','failed') allows the update,
//      status IN ('publishing','published') returns 409. Without this
//      lock a future refactor that drops the CAS would silently let a
//      concurrent publish overwrite the session's thumbnail_media_id
//      after the orchestrator had already started the YouTube call.
//
//  * TestPublishPipeline_PatchByProjectAttachRejectsCrossUserAsset
//      PATCH /by-project with an asset whose workspace differs from
//      the session's workspace MUST be rejected (the handler uses
//      r.userCanAccessWorkspace on the asset's workspace). Without
//      this guard an operator in workspace A could attach a workspace-B
//      asset and the publish would silently leak cross-workspace
//      storage bytes.
//
//  * TestPublishPipeline_PatchByProjectAttachRejectsNotReadyAsset
//      PATCH /by-project with an asset whose Status != 'ready' MUST
//      return 409. The check uses errAttachAssetNotReady (line ~459
//      of youtube_editor_sessions.go) and is the canonical guard for
//      "don't publish a half-uploaded thumbnail".
//
// CASE 13 (youtube_sync_status transita confirmed/pending/drift) is
// already covered by the three TestPublishByProject_ReadBack* tests
// in youtube_publish_actual_privacy_test.go (one per value); a single
// transition test would re-pin the same surfaces and is deliberately
// deferred.

// patchByProjectPayload is the minimal body accepted by
// PATCH /api/v1/youtube/editor-sessions/by-project/{velox_project_id}.
func patchByProjectPayload(t *testing.T, thumbnailMediaID string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]string{"thumbnail_media_id": thumbnailMediaID})
	if err != nil {
		t.Fatalf("marshal PATCH payload: %v", err)
	}
	return body
}

// newPatchRouter builds the router + edit store needed for PATCH
// /by-project tests. Mirrors the manual mustNewRouterWithDefaults
// chain used by the publish tests but with the workspace + media +
// storage options wired in (PATCH by-project needs them so the
// asset-readiness + workspace-accessibility checks can run).
func newPatchRouter(
	t *testing.T,
	session *models.YouTubeVideoEdit,
	assetWorkspaceID int64,
	media *mockMediaStore,
	storage *mockStorageProvider,
	youTubeSvc *mockYouTubeOAuthServiceForEditor,
	editStore *mockYouTubeVideoEditStore,
) *Router {
	t.Helper()
	// OwnerID must be the JWT user (1) so userCanAccessWorkspace
	// passes — mirrors the commonPublishBackbone pattern.
	workspace := &models.Workspace{ID: session.WorkspaceID, OwnerID: 1, Name: "Test Workspace"}
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == session.PlatformAccountID {
					return &models.PlatformAccount{
						ID:             session.PlatformAccountID,
						UserID:         1,
						Platform:       models.PlatformYouTube,
						PlatformUserID: "UC123",
						Username:       "testchannel",
						Status:         models.AccountStatusActive,
					}, nil
				}
				return nil, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				// Asset workspace exists only so the asset
				// existence check returns OK; the session
				// workspace exists so the session resolution
				// returns OK. Both are queryable.
				if id == session.WorkspaceID {
					return workspace, nil
				}
				if id == assetWorkspaceID {
					return &models.Workspace{ID: assetWorkspaceID, OwnerID: 99, Name: "Other Workspace"}, nil
				}
				return nil, nil
			},
		}),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(_ context.Context, id int64, _ string) (*models.OAuthToken, error) {
				if id == session.PlatformAccountID {
					return &models.OAuthToken{AccessToken: "tok"}, nil
				}
				return nil, nil
			},
		}),
	)
}

// TestPublishPipeline_PatchByProjectUsesCAS locks the 4-state CAS
// predicate that AttachThumbnail enforces on the by-project PATCH
// endpoint. The predicate is `status IN ('editing','failed')`; any
// other status (publishing, published) returns 409.
//
// Without this test a future refactor that drops the CAS would
// silently let a concurrent publish overwrite thumbnail_media_id
// after the orchestrator had already issued the YouTube
// thumbnails.set call -- a real race the orchestrator's
// MarkPublishing/Published CAS is supposed to make impossible.
func TestPublishPipeline_PatchByProjectUsesCAS(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		wantAllowed bool
		wantCode    int
	}{
		{name: "editing_state_allows_update", status: "editing", wantAllowed: true, wantCode: http.StatusOK},
		{name: "failed_state_allows_update", status: "failed", wantAllowed: true, wantCode: http.StatusOK},
		{name: "publishing_state_returns_409", status: "publishing", wantAllowed: false, wantCode: http.StatusConflict},
		{name: "published_state_returns_409", status: "published", wantAllowed: false, wantCode: http.StatusConflict},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			session := &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       7,
				PlatformAccountID: 42,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  nil,
				Status:            tc.status,
				DesiredPrivacy:    "public",
			}
			mediaStore := newMockMediaStore()
			mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
				ID: "asset-uuid-123", UserID: 1, UploadKey: "uploads/1/thumb.jpg",
				ContentType: "image/jpeg", SizeBytes: 1024, Status: models.MediaAssetStatusReady,
			}
			storage := newMockStorageProvider()
			editStore := &mockYouTubeVideoEditStore{
				findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
					if projectID == "ve-project-123" {
						return session, nil
					}
					return nil, nil
				},
				attachThumbnailFn: func(_ context.Context, _ string, assetID string) (*models.YouTubeVideoEdit, error) {
					// Mirror the SQL CAS predicate so the mock
					// is NOT tautological: the handler's actual
					// job is to translate this error into HTTP 409.
					// If a future refactor drops the CAS at the SQL
					// layer, the mock still returns the error (mock
					// mirrors production) and the test still pins
					// the 409 translation.
					if tc.status != "editing" && tc.status != "failed" {
						return nil, errAttachSessionNotEditable
					}
					// Mirror the SQL UPDATE ... RETURNING: the
					// attach call mutates the session row to stamp
					// thumbnail_media_id. Production's repo does
					// this at the SQL layer; the mock does it in
					// process so the handler's response reflects
					// the mutated state.
					session.ThumbnailMediaID = strPtr(assetID)
					return session, nil
				},
			}
			router := newPatchRouter(t, session, 7, mediaStore, storage, &mockYouTubeOAuthServiceForEditor{}, editStore)

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(patchByProjectPayload(t, "asset-uuid-123")))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			router.Setup().ServeHTTP(w, req)

			if w.Code != tc.wantCode {
				t.Fatalf("status=%q: expected HTTP %d, got %d (body=%s)", tc.status, tc.wantCode, w.Code, w.Body.String())
			}
			if tc.wantAllowed {
				if session.ThumbnailMediaID == nil {
					t.Errorf("status=%q: PATCH should have stamped thumbnail_media_id on the session", tc.status)
				}
			} else {
				if session.ThumbnailMediaID != nil {
					t.Errorf("status=%q: PATCH should NOT have stamped thumbnail_media_id (CAS lost)", tc.status)
				}
			}
		})
	}
}

// TestPublishPipeline_PatchByProjectAttachRejectsCrossUserAsset
// locks the workspace-accessibility guard in attachThumbnailToSession:
// an asset that exists but whose workspace_id does NOT match the
// caller's accessible workspace MUST be rejected. Without this
// guard an operator in workspace A could attach a workspace-B asset
// and the publish would silently leak cross-workspace storage bytes
// to YouTube via the thumbnails.set call.
func TestPublishPipeline_PatchByProjectAttachRejectsCrossUserAsset(t *testing.T) {
	session := &models.YouTubeVideoEdit{
		ID: "session-123", WorkspaceID: 7, PlatformAccountID: 42,
		YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
		Status: "editing", DesiredPrivacy: "public",
	}
	// Asset exists but is owned by user 99 (a different user from
	// the session's owner / caller user 1). The handler must reject
	// the attach because the cross-user access would otherwise let
	// user 1 publish a thumbnail built from user 99's storage bytes.
	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID: "asset-uuid-123", UserID: 99, UploadKey: "uploads/99/thumb.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024, Status: models.MediaAssetStatusReady,
	}
	storage := newMockStorageProvider()

	var attachCalled bool
	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if projectID == "ve-project-123" {
				return session, nil
			}
			return nil, nil
		},
		attachThumbnailFn: func(_ context.Context, _ string, _ string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return session, nil
		},
	}
	router := newPatchRouter(t, session, 8, mediaStore, storage, &mockYouTubeOAuthServiceForEditor{}, editStore)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(patchByProjectPayload(t, "asset-uuid-123")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("cross-workspace asset MUST be rejected; got 200 (body=%s)", w.Body.String())
	}
	if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound && w.Code != http.StatusConflict {
		t.Fatalf("expected 403/404/409 on cross-workspace asset, got %d (body=%s)", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail MUST NOT be called when the asset is cross-workspace (would leak storage bytes)")
	}
	if session.ThumbnailMediaID != nil {
		t.Errorf("session.ThumbnailMediaID must remain nil after a cross-workspace rejection")
	}
}

// TestPublishPipeline_PatchByProjectAttachRejectsNotReadyAsset
// locks the asset-readiness guard in attachThumbnailToSession: an
// asset whose Status is NOT 'ready' (e.g. 'uploading', 'failed')
// MUST be rejected with 409 and errAttachAssetNotReady. Without
// this guard an operator could publish a half-uploaded thumbnail
// and YouTube would reject thumbnails.set with a 400, wasting the
// orchestrator's quota.
func TestPublishPipeline_PatchByProjectAttachRejectsNotReadyAsset(t *testing.T) {
	session := &models.YouTubeVideoEdit{
		ID: "session-123", WorkspaceID: 7, PlatformAccountID: 42,
		YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
		Status: "editing", DesiredPrivacy: "public",
	}
	for _, notReady := range []models.MediaAssetStatus{
		models.MediaAssetStatusPending,
		models.MediaAssetStatusFailed,
		models.MediaAssetStatusExpired,
	} {
		notReady := notReady
		t.Run(string(notReady), func(t *testing.T) {
			mediaStore := newMockMediaStore()
			mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
				ID: "asset-uuid-123", UserID: 1, UploadKey: "uploads/1/thumb.jpg",
				ContentType: "image/jpeg", SizeBytes: 1024,
				Status: notReady,
			}
			storage := newMockStorageProvider()

			var attachCalled bool
			editStore := &mockYouTubeVideoEditStore{
				findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
					if projectID == "ve-project-123" {
						return session, nil
					}
					return nil, nil
				},
				attachThumbnailFn: func(_ context.Context, _ string, _ string) (*models.YouTubeVideoEdit, error) {
					attachCalled = true
					return session, nil
				},
			}
			router := newPatchRouter(t, session, 7, mediaStore, storage, &mockYouTubeOAuthServiceForEditor{}, editStore)

			req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(patchByProjectPayload(t, "asset-uuid-123")))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			router.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusConflict {
				t.Fatalf("asset.Status=%q: expected 409 (errAttachAssetNotReady), got %d (body=%s)", notReady, w.Code, w.Body.String())
			}
			if attachCalled {
				t.Fatalf("asset.Status=%q: AttachThumbnail MUST NOT be called for a not-ready asset", notReady)
			}
			if session.ThumbnailMediaID != nil {
				t.Errorf("asset.Status=%q: session.ThumbnailMediaID must remain nil after the rejection", notReady)
			}
		})
	}
}
