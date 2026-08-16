package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// thumbnailPublishRig wires the group router with a media store, a
// storage provider whose signed URLs resolve to a local download server
// (the rendered thumbnail bytes), and the mock YouTube service.
type thumbnailPublishRig struct {
	r             *Router
	ytSvc         *mockYouTubeOAuthServiceForEditor
	media         *mockMediaStore
	storage       *mockStorageProvider
	thumbnailData []byte
}

func newThumbnailPublishRig(
	t *testing.T,
	setThumbnailFn func(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error,
	getVideoFn func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error),
	vaultGetFn func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error),
) *thumbnailPublishRig {
	t.Helper()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Marketing"}
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == group.ID {
				return group, nil
			}
			return nil, nil
		},
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
			if groupID == group.ID {
				return []int64{ytAccount.ID}, nil
			}
			return nil, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == ytAccount.ID {
				return ytAccount, nil
			}
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		setThumbnailFn: setThumbnailFn,
		getVideoFn:     getVideoFn,
	}
	vault := &mockCredentialVault{getFn: vaultGetFn}

	media := newMockMediaStore()
	data := []byte("fake-thumbnail-bytes")
	media.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.png",
		ContentType: "image/png",
		SizeBytes:   int64(len(data)),
		Status:      models.MediaAssetStatusReady,
	}
	downloadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(downloadSrv.Close)
	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return downloadSrv.URL + "/" + key }

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault,
		WithMediaStore(media),
		WithStorageProvider(storage),
	)
	return &thumbnailPublishRig{r: r, ytSvc: ytSvc, media: media, storage: storage, thumbnailData: data}
}

func publishThumbnailRequest(t *testing.T, r *Router, groupID int, videoID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/groups/%d/youtube/videos/%s/thumbnail", groupID, videoID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

func okVaultToken(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
	return &models.OAuthToken{AccessToken: "valid-token"}, nil
}

func TestPublishGroupVideoThumbnail_HappyPath(t *testing.T) {
	var gotToken, gotVideoID, gotMime string
	var gotSize int64
	var gotBody []byte
	setThumbnailFn := func(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error {
		gotToken, gotVideoID, gotMime, gotSize = accessToken, videoID, mimeType, size
		gotBody, _ = io.ReadAll(body)
		return nil
	}
	rig := newThumbnailPublishRig(t, setThumbnailFn, nil, okVaultToken)

	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// The video ownership probe ran under the renewed token.
	if gotToken != "valid-token" {
		t.Errorf("token: want valid-token, got %q", gotToken)
	}
	if gotVideoID != "VID123" {
		t.Errorf("video id: want VID123, got %q", gotVideoID)
	}
	if gotMime != "image/png" {
		t.Errorf("mime: want image/png, got %q", gotMime)
	}
	if gotSize != int64(len(rig.thumbnailData)) {
		t.Errorf("size: want %d, got %d", len(rig.thumbnailData), gotSize)
	}
	if string(gotBody) != string(rig.thumbnailData) {
		t.Errorf("body: want %q, got %q", rig.thumbnailData, gotBody)
	}
	if rig.storage.getObjectCalls.Load() != 1 {
		t.Errorf("expected one thumbnail download, got %d", rig.storage.getObjectCalls.Load())
	}

	var resp publishGroupVideoThumbnailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "published" || resp.YouTubeVideoID != "VID123" ||
		resp.WatchURL != "https://www.youtube.com/watch?v=VID123" ||
		resp.ThumbnailMediaID != "asset-uuid-123" ||
		resp.ContentType != "image/png" || resp.Bytes != int64(len(rig.thumbnailData)) {
		t.Errorf("response: got %+v", resp)
	}
}

func TestPublishGroupVideoThumbnail_InvalidatesAccountCache(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error { return nil }, nil, okVaultToken)
	// Seed the account's cached editable videos as if a list had just run.
	rig.r.youtubeGroupVideosCacheMu.Lock()
	rig.r.youtubeGroupVideosCache = map[string]youtubeGroupVideosCacheEntry{
		"42:UC123:50": {
			items:     []models.YouTubeVideoDetails{{ID: "VID123", Title: "Stale title"}},
			expiresAt: time.Now().Add(time.Hour),
		},
	}
	rig.r.youtubeGroupVideosCacheMu.Unlock()

	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	rig.r.youtubeGroupVideosCacheMu.Lock()
	defer rig.r.youtubeGroupVideosCacheMu.Unlock()
	if len(rig.r.youtubeGroupVideosCache) != 0 {
		t.Errorf("expected the account cache to be invalidated, still has %d entries", len(rig.r.youtubeGroupVideosCache))
	}
}

func TestPublishGroupVideoThumbnail_Validation(t *testing.T) {
	cases := []struct {
		name string
		body string
		url  string
		want string
	}{
		{"invalid group id", `{"platform_account_id": 42, "thumbnail_media_id": "a"}`, "/api/v1/groups/abc/youtube/videos/VID123/thumbnail", "group_id path parameter"},
		{"missing video id", `{"platform_account_id": 42, "thumbnail_media_id": "a"}`, "/api/v1/groups/3/youtube/videos//thumbnail", "video_id"},
		{"missing platform_account_id", `{"thumbnail_media_id": "a"}`, "", "platform_account_id"},
		{"missing thumbnail_media_id", `{"platform_account_id": 42}`, "", "thumbnail_media_id"},
		{"malformed json", `{`, "", "invalid JSON body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
				t.Errorf("SetThumbnail must not be reached on %s", tc.name)
				return nil
			}, nil, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				t.Errorf("vault.Renew must not be reached on %s", tc.name)
				return nil, errors.New("unexpected")
			})
			urlPath := tc.url
			if urlPath == "" {
				urlPath = "/api/v1/groups/3/youtube/videos/VID123/thumbnail"
			}
			req := httptest.NewRequest(http.MethodPost, urlPath, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			rig.r.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("body %q does not mention %q", w.Body.String(), tc.want)
			}
		})
	}
}

func TestPublishGroupVideoThumbnail_AuthRequired(t *testing.T) {
	rig := newThumbnailPublishRig(t, nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/groups/3/youtube/videos/VID123/thumbnail", strings.NewReader(`{"platform_account_id": 42, "thumbnail_media_id": "a"}`))
	w := httptest.NewRecorder()
	rig.r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_AccountNotInGroup404(t *testing.T) {
	rig := newThumbnailPublishRig(t, nil, nil, nil)
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 999, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_VideoNotFound404(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
		t.Errorf("SetThumbnail must not be reached when the video is missing")
		return nil
	}, func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
		return nil, fmt.Errorf("%w: video_id=%s", services.ErrYouTubeVideoNotFound, videoID)
	}, okVaultToken)
	w := publishThumbnailRequest(t, rig.r, 3, "VID-MISSING", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_WrongChannel404(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
		t.Errorf("SetThumbnail must not be reached for a cross-channel video")
		return nil
	}, func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
		return &models.YouTubeVideoDetails{ID: videoID, ChannelID: "UC-OTHER-CHANNEL"}, nil
	}, okVaultToken)
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (collapsed cross-channel), got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_MediaAssetRejected(t *testing.T) {
	missing := func(t *testing.T, rig *thumbnailPublishRig) {
		w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "unknown-asset"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
	}
	t.Run("missing asset", func(t *testing.T) {
		rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
			t.Errorf("SetThumbnail must not be reached")
			return nil
		}, nil, okVaultToken)
		missing(t, rig)
	})
	t.Run("not owned by caller", func(t *testing.T) {
		rig := newThumbnailPublishRig(t, nil, nil, okVaultToken)
		rig.media.assets["asset-uuid-123"].UserID = 99
		missing(t, rig)
	})
	t.Run("not ready", func(t *testing.T) {
		rig := newThumbnailPublishRig(t, nil, nil, okVaultToken)
		rig.media.assets["asset-uuid-123"].Status = models.MediaAssetStatusPending
		missing(t, rig)
	})
	t.Run("unsupported content type", func(t *testing.T) {
		rig := newThumbnailPublishRig(t, nil, nil, okVaultToken)
		rig.media.assets["asset-uuid-123"].ContentType = "image/webp"
		w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "image/jpeg") {
			t.Errorf("body %q does not mention the allowed types", w.Body.String())
		}
	})
}

func TestPublishGroupVideoThumbnail_Forbidden403(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
		return &services.YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "not owned"}
	}, nil, okVaultToken)
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_RateLimit429(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
		return &services.YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "slow down"}
	}, nil, okVaultToken)
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_TokenError502(t *testing.T) {
	rig := newThumbnailPublishRig(t, nil, nil, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return nil, errors.New("no valid token")
	})
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_Upstream502(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
		return &services.YouTubeAPIError{StatusCode: 0, Category: "network", Message: "boom"}
	}, nil, okVaultToken)
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_DownloadFailure500(t *testing.T) {
	rig := newThumbnailPublishRig(t, func(context.Context, string, string, string, io.Reader, int64) error {
		t.Errorf("SetThumbnail must not be reached when the download fails")
		return nil
	}, nil, okVaultToken)
	rig.storage.assetURLFn = func(key string) string { return "http://127.0.0.1:1/does-not-exist" }
	w := publishThumbnailRequest(t, rig.r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishGroupVideoThumbnail_CrossTenant404(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 99}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Marketing"}
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == group.ID {
				return group, nil
			}
			return nil, nil
		},
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		setThumbnailFn: func(context.Context, string, string, string, io.Reader, int64) error {
			t.Errorf("SetThumbnail must NOT be called on cross-tenant 404")
			return nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	vault := &mockCredentialVault{}
	media := newMockMediaStore()
	storage := newMockStorageProvider()
	r := newGroupVideosRouter(t, workspace, groupStore, nil, editStore, ytSvc, vault,
		WithMediaStore(media), WithStorageProvider(storage))

	w := publishThumbnailRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "thumbnail_media_id": "asset-uuid-123"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
