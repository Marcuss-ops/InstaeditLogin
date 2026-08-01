package api

// In-flight / retry / concurrency publish handler tests.
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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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

	r := newPublishRouter(t, workspace, editStore, WithPublishingInFlightTimeout(1*time.Minute))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for in-flight publishing session, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_InFlightTimeoutExpiredRetries(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}

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
				Status:            "publishing",
				UpdatedAt:         time.Now().UTC().Add(-2 * time.Minute),
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

	r := newPublishRouter(t, workspace, editStore,
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
		WithPublishingInFlightTimeout(1*time.Minute),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after in-flight timeout expired, got %d: %s", w.Code, w.Body.String())
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.VideoID != "ytvideo123" {
		t.Fatalf("expected video_id ytvideo123, got %s", resp.VideoID)
	}
}

func TestPublishYouTubeEditorSession_DefaultInFlightTimeoutExpiredRetries(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}

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
				Status:            "publishing",
				UpdatedAt:         time.Now().UTC().Add(-DefaultPublishingInFlightTimeout - time.Minute),
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

	r := newPublishRouter(t, workspace, editStore,
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
		t.Fatalf("expected 200 after default in-flight timeout expired, got %d: %s", w.Code, w.Body.String())
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.VideoID != "ytvideo123" {
		t.Fatalf("expected video_id ytvideo123, got %s", resp.VideoID)
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
				Status:            "failed",
				LastError:         "previous failure",
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

	var publishCalls int
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
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
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.VideoID != "ytvideo123" {
		t.Fatalf("expected video_id ytvideo123, got %s", resp.VideoID)
	}
}

// TestPublishYouTubeEditorSession_ConcurrentPublishClaimsAtomically is the
// concurrency regression for Blocco #5 P0 #2 — the atomic CAS claim
// must guarantee that exactly ONE publish fires PublishThumbnail per
// N concurrent requests on the same session_id. Pre-fix the handler's
// read-then-update race would stamp status='publishing' on the same row
// from multiple goroutines, each dispatching a PublishThumbnail call.
// Post-fix, MarkPublishing's CAS returns the typed sentinel from N-1
// callers; the handler maps to 409.
func TestPublishYouTubeEditorSession_ConcurrentPublishClaimsAtomically(t *testing.T) {
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

	var publishCalls int32
	capturedPrivacyMu := sync.Mutex{}
	var capturedPrivacy string
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

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		atomic.AddInt32(&publishCalls, 1)
		capturedPrivacyMu.Lock()
		capturedPrivacy = privacyStatus
		capturedPrivacyMu.Unlock()
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

	const numGoroutines = 10
	var wg sync.WaitGroup
	type callResult struct {
		code int
		body string
	}
	results := make([]callResult, numGoroutines)
	payload := []byte("{}")

	// Sync barrier: release every goroutine at once so the HTTP
	// dispatch lands as close to "all at once" as the runtime
	// allows. Without this, goroutines that reach the publish handler
	// a few microseconds apart would still hit the CAS — with this,
	// any flaky ordering regressions surface under repeated runs.
	//
	// r.Setup() MUST be called exactly once here, NOT per-goroutine:
	// each Setup() rebuilds the chi.Mux mux + AuthModule/csrf module
	// route tables, neither of which is safe for concurrent
	// map writes. Capture the handler and call it from every
	// goroutine — http.Handler is safe to invoke concurrently.
	handler := r.Setup()
	start := make(chan struct{})
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			results[idx] = callResult{code: rec.Code, body: rec.Body.String()}
		}(i)
	}
	close(start)
	wg.Wait()

	var success, conflict, other int
	for _, res := range results {
		switch res.code {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		default:
			other++
			t.Errorf("unexpected status code %d (body=%s)", res.code, res.body)
		}
	}
	if success != 1 {
		t.Errorf("expected exactly 1 successful publish (200), got %d", success)
	}
	if conflict != numGoroutines-1 {
		t.Errorf("expected %d concurrent CAS-loss (409), got %d", numGoroutines-1, conflict)
	}
	if other != 0 {
		t.Errorf("expected 0 unexpected statuses, got %d", other)
	}
	if got := atomic.LoadInt32(&publishCalls); got != 1 {
		t.Errorf("expected exactly 1 PublishThumbnail YouTube API call, got %d", got)
	}
	capturedPrivacyMu.Lock()
	gotPrivacy := capturedPrivacy
	capturedPrivacyMu.Unlock()
	if gotPrivacy != "public" {
		t.Errorf("expected resolved privacy 'public' on the winning publish, got %q", gotPrivacy)
	}
}
