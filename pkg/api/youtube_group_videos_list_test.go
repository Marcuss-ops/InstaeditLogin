package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestListGroupYouTubeVideos_HappyPath(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel",
		Status:   models.AccountStatusActive,
		Metadata: models.Metadata{"language": "pl"},
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
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			if wsID != workspace.ID {
				return nil, nil
			}
			return []*models.YouTubeVideoEdit{
				{
					ID:                "session-existing",
					WorkspaceID:       workspace.ID,
					PlatformAccountID: ytAccount.ID,
					YouTubeVideoID:    "ytvideo-existing",
					VeloxProjectID:    "ve-existing-001",
					DesiredPrivacy:    "unlisted",
					Status:            "editing",
				},
			}, nil
		},
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{
				Items: []models.YouTubeVideoDetails{
					{
						ID:           "ytvideo-existing",
						Title:        "Existing edit",
						ChannelID:    "UC123",
						ThumbnailURL: "https://i.ytimg.com/vi/ytvideo-existing/maxresdefault.jpg",
						Privacy:      "private",
						UploadStatus: "processed",
					},
				},
			}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == ytAccount.ID {
				return &models.OAuthToken{AccessToken: "valid-token"}, nil
			}
			return nil, errors.New("token not found")
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault,
		WithEditorURL("https://editor.instaedit.org"))

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp groupYouTubeVideosResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Videos) != 1 {
		t.Fatalf("expected 1 video, got %d", len(resp.Videos))
	}
	got := resp.Videos[0]
	if got.YouTubeVideoID != "ytvideo-existing" {
		t.Errorf("youtube_video_id: want ytvideo-existing, got %q", got.YouTubeVideoID)
	}
	if got.ChannelName != "testchannel" {
		t.Errorf("channel_name: want testchannel (Username), got %q", got.ChannelName)
	}
	if got.Language != "pl" {
		t.Errorf("language: want pl from account metadata, got %q", got.Language)
	}
	if got.Language != "pl" {
		t.Errorf("language: want pl from account metadata, got %q", got.Language)
	}
	if got.EditorSessionID == nil || *got.EditorSessionID != "session-existing" {
		t.Errorf("editor_session_id: want session-existing, got %v", got.EditorSessionID)
	}
	if got.VeloxProjectID == nil || *got.VeloxProjectID != "ve-existing-001" {
		t.Errorf("velox_project_id: want ve-existing-001, got %v", got.VeloxProjectID)
	}
	if got.EditorURL == nil || !strings.Contains(*got.EditorURL, "ve-existing-001") {
		t.Errorf("editor_url: want to contain ve-existing-001, got %v", got.EditorURL)
	}
	if got.EditorStatus != "editing" {
		t.Errorf("editor_status: want editing, got %q", got.EditorStatus)
	}
	if got.DesiredPrivacy != "unlisted" {
		t.Errorf("desired_privacy: want unlisted, got %q", got.DesiredPrivacy)
	}
	if got.ActualPrivacy != nil {
		t.Errorf("actual_privacy must remain nil until YouTube read-back, got %q", *got.ActualPrivacy)
	}
	if got.YouTubeSyncStatus == nil || *got.YouTubeSyncStatus != "unconfirmed" {
		val := ""
		if got.YouTubeSyncStatus != nil {
			val = *got.YouTubeSyncStatus
		}
		t.Errorf("youtube_sync_status: want unconfirmed, got %q", val)
	}
	if got.PrivacyStatus != "private" {
		t.Errorf("privacy_status: want private, got %q", got.PrivacyStatus)
	}
	if got.ProcessingStatus != "processed" {
		t.Errorf("processing_status: want processed, got %q", got.ProcessingStatus)
	}
}

func TestListGroupYouTubeVideos_JoinMissesReady(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel",
		Status:   models.AccountStatusActive,
		Metadata: models.Metadata{"language": "pl"},
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
	// Empty session list — the join misses.
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			return nil, nil
		},
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{
				Items: []models.YouTubeVideoDetails{
					{
						ID:           "ytvideo-fresh",
						Title:        "Unedited video",
						ChannelID:    "UC123",
						ThumbnailURL: "https://i.ytimg.com/vi/ytvideo-fresh/maxresdefault.jpg",
						Privacy:      "unlisted",
						UploadStatus: "processed",
					},
				},
			}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == ytAccount.ID {
				return &models.OAuthToken{AccessToken: "valid-token"}, nil
			}
			return nil, errors.New("token not found")
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc,
		vault, WithEditorURL("https://editor.instaedit.org"))

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp groupYouTubeVideosResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Videos) != 1 {
		t.Fatalf("expected 1 video, got %d", len(resp.Videos))
	}
	got := resp.Videos[0]
	if got.EditorSessionID != nil {
		t.Errorf("editor_session_id: want nil (no session yet), got %v", *got.EditorSessionID)
	}
	if got.VeloxProjectID != nil {
		t.Errorf("velox_project_id: want nil, got %v", *got.VeloxProjectID)
	}
	if got.EditorURL != nil {
		t.Errorf("editor_url: want nil, got %v", *got.EditorURL)
	}
	if got.EditorStatus != "ready" {
		t.Errorf("editor_status: want ready (no session yet), got %q", got.EditorStatus)
	}
	if got.DesiredPrivacy != "" {
		t.Errorf("desired_privacy: want empty, got %q", got.DesiredPrivacy)
	}
	if got.ActualPrivacy != nil {
		t.Errorf("actual_privacy: want nil when no session, got %q", *got.ActualPrivacy)
	}
	if got.PrivacyStatus != "unlisted" {
		t.Errorf("privacy_status: want unlisted, got %q", got.PrivacyStatus)
	}
}

func TestListGroupYouTubeVideos_CrossTenant404(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 99} // owned by user 99, not 1
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Marketing"}
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == group.ID {
				return group, nil
			}
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			t.Errorf("YouTube.ListEditableVideos must NOT be called on cross-tenant 404")
			return nil, nil
		},
	}
	vault := &mockCredentialVault{}

	r := newGroupVideosRouter(t, workspace, groupStore, nil, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
	withBearerJWT(t, req, 1) // JWT for user 1, but workspace is owned by 99
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGroupYouTubeVideos_EmptyGroupReturns200EmptyArray(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Empty"}
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == group.ID {
				return group, nil
			}
			return nil, nil
		},
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	ytSvc := &mockYouTubeOAuthServiceForEditor{}
	vault := &mockCredentialVault{}

	r := newGroupVideosRouter(t, workspace, groupStore, nil, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp groupYouTubeVideosResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Videos) != 0 {
		t.Errorf("expected empty videos array, got %d entries", len(resp.Videos))
	}
}

func TestListGroupYouTubeVideos_AuthRequired(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	groupStore := &mockGroupStore{}
	editStore := &mockYouTubeVideoEditStore{}
	ytSvc := &mockYouTubeOAuthServiceForEditor{}
	vault := &mockCredentialVault{}

	r := newGroupVideosRouter(t, workspace, groupStore, nil, editStore, ytSvc, vault)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/3/youtube/videos", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGroupYouTubeVideos_InvalidGroupIDIs400(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	groupStore := &mockGroupStore{}
	editStore := &mockYouTubeVideoEditStore{}
	ytSvc := &mockYouTubeOAuthServiceForEditor{}
	vault := &mockCredentialVault{}

	r := newGroupVideosRouter(t, workspace, groupStore, nil, editStore, ytSvc, vault)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/abc/youtube/videos", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListGroupYouTubeVideos_AllAccountsFailReturns502(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	acc1 := &models.PlatformAccount{ID: 11, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC-A", Username: "chA"}
	acc2 := &models.PlatformAccount{ID: 12, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC-B", Username: "chB"}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "All fail"}
	groupStore := &mockGroupStore{
		findByIDFn:            func(id int64) (*models.Group, error) { return group, nil },
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) { return []int64{acc1.ID, acc2.ID}, nil },
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			switch id {
			case acc1.ID:
				return acc1, nil
			case acc2.ID:
				return acc2, nil
			}
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			return nil, nil
		},
	}
	var listCalls int32
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			atomic.AddInt32(&listCalls, 1)
			return nil, errors.New("simulated youtube outage")
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == acc1.ID || id == acc2.ID {
				return &models.OAuthToken{AccessToken: "tok"}, nil
			}
			return nil, errors.New("no token")
		},
	}
	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	// The 502 path must emit a diagnostic log line with the per-account
	// warnings: the SPA swallows the response body into a generic
	// "YouTube non risponde temporaneamente" toast, so the log is the
	// only place where the upstream reasons remain observable.
	var logBuf bytes.Buffer
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(oldDefault)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when all accounts fail, got %d: %s", w.Code, w.Body.String())
	}
	if atomic.LoadInt32(&listCalls) == 0 {
		t.Errorf("expected at least one YouTube.ListEditableVideos call, got 0")
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "group youtube videos: every account failed (502)") {
		t.Errorf("expected 502 diagnostic log line, got:\n%s", logged)
	}
	if !strings.Contains(logged, "simulated youtube outage") {
		t.Errorf("expected per-account warnings in the 502 log, got:\n%s", logged)
	}
	if !strings.Contains(logged, "group_id=3") {
		t.Errorf("expected group_id in the 502 log, got:\n%s", logged)
	}
}

func TestListGroupYouTubeVideos_IncludeSubgroups(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	accDirect := &models.PlatformAccount{ID: 11, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC-D", Username: "chDirect"}
	accSub := &models.PlatformAccount{ID: 12, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC-S", Username: "chSub"}
	rootGroup := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Root"}
	subGroup := &models.Group{ID: 4, WorkspaceID: workspace.ID, Name: "Sub", ParentGroupID: &rootGroup.ID}

	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == rootGroup.ID {
				return rootGroup, nil
			}
			if id == subGroup.ID {
				return subGroup, nil
			}
			return nil, nil
		},
		listByWorkspaceFn: func(wsID int64) ([]models.Group, error) {
			return []models.Group{*rootGroup, *subGroup}, nil
		},
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
			switch groupID {
			case rootGroup.ID:
				return []int64{accDirect.ID}, nil
			case subGroup.ID:
				return []int64{accSub.ID}, nil
			}
			return nil, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			switch id {
			case accDirect.ID:
				return accDirect, nil
			case accSub.ID:
				return accSub, nil
			}
			return nil, nil
		},
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			switch channelID {
			case "UC-D":
				return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{
					ID: "yt-direct", Title: "Direct", ChannelID: "UC-D", ThumbnailURL: "https://x/d.jpg",
					Privacy: "private", UploadStatus: "processed",
				}}}, nil
			case "UC-S":
				return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{
					ID: "yt-sub", Title: "Sub", ChannelID: "UC-S", ThumbnailURL: "https://x/s.jpg",
					Privacy: "unlisted", UploadStatus: "processed",
				}}}, nil
			}
			return &services.YouTubeVideoPage{}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == accDirect.ID || id == accSub.ID {
				return &models.OAuthToken{AccessToken: "tok"}, nil
			}
			return nil, errors.New("no token")
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos?include_subgroups=true", rootGroup.ID), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp groupYouTubeVideosResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Videos) != 2 {
		t.Fatalf("expected 2 videos aggregated across root + sub-group, got %d", len(resp.Videos))
	}
	// Both IDs must surface; the verdict locked the response to
	// platform_account_id + youtube_video_id (not a per-group origin
	// tag), so we verify inclusion via those two fields alone.
	seenIDs := map[string]int64{}
	for _, v := range resp.Videos {
		seenIDs[v.YouTubeVideoID] = v.PlatformAccountID
	}
	if seenIDs["yt-direct"] != accDirect.ID {
		t.Errorf("direct group's ytvideo got platform_account_id=%d (want %d)", seenIDs["yt-direct"], accDirect.ID)
	}
	if seenIDs["yt-sub"] != accSub.ID {
		t.Errorf("sub-group's ytvideo got platform_account_id=%d (want %d)", seenIDs["yt-sub"], accSub.ID)
	}
}
