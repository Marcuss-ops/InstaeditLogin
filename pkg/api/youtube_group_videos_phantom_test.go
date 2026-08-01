package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListGroupYouTubeVideos_PhantomEmissionForPublishedPublic(t *testing.T) {
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
	// YouTube returns EMPTY — the phantom pass MUST still emit one
	// entry derived solely from the session row.
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{}, nil
		},
	}
	draftTitle := "Phantom Title"
	actualPrivacy := "public"
	syncStatus := "confirmed"
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			if wsID != workspace.ID {
				return nil, nil
			}
			return []*models.YouTubeVideoEdit{{
				ID:                "session-1",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: ytAccount.ID,
				YouTubeVideoID:    "yt-phantom",
				VeloxProjectID:    "vp-1",
				DraftTitle:        &draftTitle,
				Status:            "published",
				DesiredPrivacy:    "public",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
				UpdatedAt:         time.Now(),
			}}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok"}, nil
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
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
		t.Fatalf("expected 1 phantom entry, got %d (response=%+v)", len(resp.Videos), resp.Videos)
	}
	v := resp.Videos[0]
	if !v.Phantom {
		t.Errorf("Phantom flag must be true on synthesized entry, got false (entry=%+v)", v)
	}
	if v.YouTubeVideoID != "yt-phantom" {
		t.Errorf("YouTubeVideoID mismatch: got %q want yt-phantom", v.YouTubeVideoID)
	}
	if v.Title != "Phantom Title" {
		t.Errorf("Title should mirror DraftTitle, got %q", v.Title)
	}
	if v.PrivacyStatus != "public" {
		t.Errorf("PrivacyStatus should resolve from ActualPrivacy, got %q", v.PrivacyStatus)
	}
	if v.EditorStatus != "published" {
		t.Errorf("EditorStatus mismatch: got %q want published", v.EditorStatus)
	}
	if v.EditorSessionID == nil || *v.EditorSessionID != "session-1" {
		t.Errorf("EditorSessionID must surface, got %v", v.EditorSessionID)
	}
	if v.VeloxProjectID == nil || *v.VeloxProjectID != "vp-1" {
		t.Errorf("VeloxProjectID must surface, got %v", v.VeloxProjectID)
	}
	if v.EditorURL == nil || *v.EditorURL == "" {
		t.Errorf("EditorURL must surface for phantom entries too, got %v", v.EditorURL)
	}
	wantThumb := "https://i.ytimg.com/vi/yt-phantom/hqdefault.jpg"
	if v.ThumbnailURL != wantThumb {
		t.Errorf("ThumbnailURL should point to YouTube CDN (%s), got %q", wantThumb, v.ThumbnailURL)
	}
	if v.ActualPrivacy == nil || *v.ActualPrivacy != "public" {
		t.Errorf("ActualPrivacy must propagate from session, got %v", v.ActualPrivacy)
	}
	if v.YouTubeSyncStatus == nil || *v.YouTubeSyncStatus != "confirmed" {
		t.Errorf("YouTubeSyncStatus must propagate from session, got %v", v.YouTubeSyncStatus)
	}
	if v.ProcessingStatus != "processed" {
		t.Errorf("ProcessingStatus must be 'processed' for phantom entries, got %q", v.ProcessingStatus)
	}
}

func TestListGroupYouTubeVideos_PhantomDedupsAgainstFanOut(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID: 42, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel",
		Status: models.AccountStatusActive,
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
	// YouTube returns the video (race window: privacy=public but
	// the fan-out captured it before the flip took effect).
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{
				ID: "yt-phantom", Title: "Live Title", ChannelID: "UC123",
				ThumbnailURL: "https://ytimg.com/live.jpg",
				Privacy:      "public", UploadStatus: "processed",
			}}}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			if wsID != workspace.ID {
				return nil, nil
			}
			actualPrivacy := "public"
			return []*models.YouTubeVideoEdit{{
				ID: "session-1", WorkspaceID: workspace.ID,
				PlatformAccountID: ytAccount.ID, YouTubeVideoID: "yt-phantom",
				VeloxProjectID: "vp-1", Status: "published",
				DesiredPrivacy: "public", ActualPrivacy: &actualPrivacy,
				UpdatedAt: time.Now(),
			}}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok"}, nil
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
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
		t.Fatalf("expected exactly 1 entry (no double emission), got %d: %+v", len(resp.Videos), resp.Videos)
	}
	v := resp.Videos[0]
	if v.Phantom {
		t.Errorf("regular fan-out entry must have Phantom=false; phantom pass must skip already-emitted tuples")
	}
	if v.Title != "Live Title" {
		t.Errorf("regular entry must use the LIVE YouTube title, got %q (phantom's fallback title would be wrong here)", v.Title)
	}
	if v.ThumbnailURL != "https://ytimg.com/live.jpg" {
		t.Errorf("regular entry must use the LIVE YouTube thumbnail, got %q", v.ThumbnailURL)
	}
}

func TestListGroupYouTubeVideos_PhantomSkipsEditingSession(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID: 42, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel",
		Status: models.AccountStatusActive,
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
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			return []*models.YouTubeVideoEdit{{
				ID: "session-1", WorkspaceID: workspace.ID,
				PlatformAccountID: ytAccount.ID, YouTubeVideoID: "yt-editing",
				VeloxProjectID: "vp-1", Status: "editing",
				DesiredPrivacy: "public", UpdatedAt: time.Now(),
			}}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok"}, nil
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
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
		t.Errorf("editing sessions must NOT synthesize phantoms; got %d entries: %+v", len(resp.Videos), resp.Videos)
	}
}

func TestListGroupYouTubeVideos_PhantomRespectsRecencyFilter(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID: 42, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel",
		Status: models.AccountStatusActive,
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
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{}, nil
		},
	}
	// UpdatedAt 91 days ago: just past the recency cap.
	staleUpdatedAt := time.Now().Add(-91 * 24 * time.Hour)
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			actualPrivacy := "public"
			return []*models.YouTubeVideoEdit{{
				ID: "session-stale", WorkspaceID: workspace.ID,
				PlatformAccountID: ytAccount.ID, YouTubeVideoID: "yt-stale",
				VeloxProjectID: "vp-1", Status: "published",
				DesiredPrivacy: "public", ActualPrivacy: &actualPrivacy,
				UpdatedAt: staleUpdatedAt,
			}}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok"}, nil
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
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
		t.Errorf("sessions older than %s must NOT emit phantoms; got %d entries: %+v",
			groupYouTubeVideosPhantomMaxAge, len(resp.Videos), resp.Videos)
	}
}

func TestListGroupYouTubeVideos_PhantomRespectsAccountGrouping(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccountInGroup := &models.PlatformAccount{
		ID: 42, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC-in-group", Username: "in_group",
		Status: models.AccountStatusActive,
	}
	ytAccountOutOfGroup := &models.PlatformAccount{
		ID: 99, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC-out-of-group", Username: "out_of_group",
		Status: models.AccountStatusActive,
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
				return []int64{ytAccountInGroup.ID}, nil
			}
			return nil, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			switch id {
			case ytAccountInGroup.ID:
				return ytAccountInGroup, nil
			case ytAccountOutOfGroup.ID:
				return ytAccountOutOfGroup, nil
			}
			return nil, nil
		},
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			// The in-group account has no editable videos.
			return &services.YouTubeVideoPage{}, nil
		},
	}
	// The out-of-group session belongs to account 99, which is NOT
	// in the group's account set. The phantom pass MUST skip it.
	actualPrivacy := "public"
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			return []*models.YouTubeVideoEdit{{
				ID: "session-cross", WorkspaceID: workspace.ID,
				PlatformAccountID: ytAccountOutOfGroup.ID, YouTubeVideoID: "yt-cross",
				VeloxProjectID: "vp-1", Status: "published",
				DesiredPrivacy: "public", ActualPrivacy: &actualPrivacy,
				UpdatedAt: time.Now(),
			}}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == ytAccountInGroup.ID {
				return &models.OAuthToken{AccessToken: "tok"}, nil
			}
			// Out-of-group accounts: still returns a token (the
			// session exists in the DB), but the phantom pass
			// must skip because the account is not in the group.
			return &models.OAuthToken{AccessToken: "tok"}, nil
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
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
	for _, v := range resp.Videos {
		if v.PlatformAccountID == ytAccountOutOfGroup.ID {
			t.Errorf("cross-group session leaked: phantom for out-of-group account %d surfaced in group %d response",
				ytAccountOutOfGroup.ID, group.ID)
		}
	}
	if len(resp.Videos) != 0 {
		t.Errorf("expected 0 entries (in-group has no videos + cross-group must not leak), got %d: %+v",
			len(resp.Videos), resp.Videos)
	}
}

func TestListGroupYouTubeVideos_JoinSessionMapKeepsNewest(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID: 42, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: "UC123", Username: "testchannel",
		Status: models.AccountStatusActive,
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
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
			return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{
				ID: "yt-x", Title: "Live Title", ChannelID: "UC123",
				ThumbnailURL: "https://ytimg.com/live.jpg",
				Privacy:      "private", UploadStatus: "processed",
			}}}, nil
		},
	}
	// Two sessions for the SAME (account, video) tuple. The SQL
	// returns them ORDER BY updated_at DESC, so the newer row is
	// index 0. A naive `map[key] = s` would end up with the OLDER
	// row (index 1); the fix's `if !exists { map[key] = s }` keeps
	// the NEWER row (index 0).
	olderUpdatedAt := time.Now().Add(-1 * time.Hour)
	editStore := &mockYouTubeVideoEditStore{
		listByAccountsFn: func(ctx context.Context, wsID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
			if wsID != workspace.ID {
				return nil, nil
			}
			return []*models.YouTubeVideoEdit{
				{ // NEWER first (ORDER BY updated_at DESC)
					ID: "session-new", WorkspaceID: workspace.ID,
					PlatformAccountID: ytAccount.ID, YouTubeVideoID: "yt-x",
					VeloxProjectID: "vp-new", Status: "published",
					DesiredPrivacy: "public",
					ActualPrivacy:  stringPtr("public"),
					UpdatedAt:      time.Now(),
				},
				{ // OLDER second
					ID: "session-old", WorkspaceID: workspace.ID,
					PlatformAccountID: ytAccount.ID, YouTubeVideoID: "yt-x",
					VeloxProjectID: "vp-old", Status: "editing",
					DesiredPrivacy: "unlisted",
					ActualPrivacy:  nil,
					UpdatedAt:      olderUpdatedAt,
				},
			}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "tok"}, nil
		},
	}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/v1/groups/%d/youtube/videos", group.ID), nil)
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
		t.Fatalf("expected 1 joined entry, got %d: %+v", len(resp.Videos), resp.Videos)
	}
	v := resp.Videos[0]
	// The newer session's fields MUST surface; if a regression
	// reverts the map-build to `map[key] = s`, the entry would
	// carry the older session's status='editing' + vp-old instead.
	if v.EditorSessionID == nil || *v.EditorSessionID != "session-new" {
		t.Errorf("EditorSessionID must be from the NEWER session, got %v (want session-new)", v.EditorSessionID)
	}
	if v.VeloxProjectID == nil || *v.VeloxProjectID != "vp-new" {
		t.Errorf("VeloxProjectID must be from the NEWER session, got %v (want vp-new)", v.VeloxProjectID)
	}
	if v.EditorStatus != "published" {
		t.Errorf("EditorStatus must be 'published' (the newer session's), got %q", v.EditorStatus)
	}
	if v.DesiredPrivacy != "public" {
		t.Errorf("DesiredPrivacy must be 'public' (the newer session's), got %q", v.DesiredPrivacy)
	}
	if v.ActualPrivacy == nil || *v.ActualPrivacy != "public" {
		t.Errorf("ActualPrivacy must be 'public' (the newer session's), got %v", v.ActualPrivacy)
	}
}
