package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestListGroupYouTubeVideos_CursorHandlerContinuation(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID}
	account := &models.PlatformAccount{ID: 42, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Username: "channel", Status: models.AccountStatusActive}
	groupStore := &mockGroupStore{
		findByIDFn:            func(int64) (*models.Group, error) { return group, nil },
		listAccountsInGroupFn: func(int64) ([]int64, error) { return []int64{account.ID}, nil },
	}
	userStore := &mockUserStore{findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil }}
	editStore := &mockYouTubeVideoEditStore{}
	vault := &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "token"}, nil
	}}
	ytSvc := &mockYouTubeOAuthServiceForEditor{listEditableVideosFn: func(context.Context, string, string, string) (*services.YouTubeVideoPage, error) {
		return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{
			{ID: "video-a", Title: "A", Privacy: "private", UploadStatus: "processed"},
			{ID: "video-b", Title: "B", Privacy: "private", UploadStatus: "processed"},
		}}, nil
	}}
	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	first := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos?limit=1", group.ID), nil)
	withBearerJWT(t, first, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, first)
	if w.Code != http.StatusOK {
		t.Fatalf("first page: %d %s", w.Code, w.Body.String())
	}
	var firstResp groupYouTubeVideosResponse
	if err := json.Unmarshal(w.Body.Bytes(), &firstResp); err != nil {
		t.Fatalf("decode first: %v", err)
	}
	if len(firstResp.Videos) != 1 || !firstResp.HasMore || firstResp.NextCursor == "" {
		t.Fatalf("first page metadata: %+v", firstResp)
	}

	second := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos?limit=1&cursor=%s", group.ID, firstResp.NextCursor), nil)
	withBearerJWT(t, second, 1)
	w = httptest.NewRecorder()
	r.Setup().ServeHTTP(w, second)
	if w.Code != http.StatusOK {
		t.Fatalf("second page: %d %s", w.Code, w.Body.String())
	}
	var secondResp groupYouTubeVideosResponse
	if err := json.Unmarshal(w.Body.Bytes(), &secondResp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if len(secondResp.Videos) != 1 || secondResp.HasMore || secondResp.NextCursor != "" {
		t.Fatalf("second page metadata: %+v", secondResp)
	}
	if secondResp.Videos[0].YouTubeVideoID == firstResp.Videos[0].YouTubeVideoID {
		t.Fatal("cursor continuation repeated the first video")
	}
}

func TestListGroupYouTubeVideos_CursorRejectsOffset(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID}
	r := newGroupVideosRouter(t, workspace, &mockGroupStore{}, nil, &mockYouTubeVideoEditStore{}, &mockYouTubeOAuthServiceForEditor{}, &mockCredentialVault{})
	cursor := encodeGroupVideosCursor("group_id=3&include_subgroups=false&days=90", 42, "video-a")
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/groups/%d/youtube/videos?cursor=%s&offset=1", group.ID, cursor), nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cursor+offset: got %d %s", w.Code, w.Body.String())
	}
}
