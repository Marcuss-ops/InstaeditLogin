package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockGroupStore is the test seam for GroupStore. It mirrors the
// router.go interface (Create / FindByID / Update / Delete /
// ListByWorkspace / ListAccountsInGroup / ValidateAccountOwnership /
// SetAccounts) using function-field indirection so a test injects
// only the methods its scenario needs; the rest fall back to a
// "not configured" zero-value behaviour.
//
// Scoped to this test file because production wiring (groupStore)
// is owned by `internal/bootstrap/app.go` and the production wiring
// code uses *repository.GroupRepository. The interface itself
// lives in pkg/api/router.go.
type mockGroupStore struct {
	findByIDFn                 func(id int64) (*models.Group, error)
	listByWorkspaceFn          func(workspaceID int64) ([]models.Group, error)
	listAccountsInGroupFn      func(groupID int64) ([]int64, error)
	validateAccountOwnershipFn func(userID, workspaceID int64, accountIDs []int64) ([]int64, error)
	createFn                   func(g *models.Group) error
	updateFn                   func(g *models.Group) error
	deleteFn                   func(id int64) error
	setAccountsFn              func(groupID int64, accountIDs []int64) error
}

func (m *mockGroupStore) FindByID(id int64) (*models.Group, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return nil, nil
}
func (m *mockGroupStore) ListByWorkspace(workspaceID int64) ([]models.Group, error) {
	if m.listByWorkspaceFn != nil {
		return m.listByWorkspaceFn(workspaceID)
	}
	return nil, nil
}
func (m *mockGroupStore) ListAccountsInGroup(groupID int64) ([]int64, error) {
	if m.listAccountsInGroupFn != nil {
		return m.listAccountsInGroupFn(groupID)
	}
	return nil, nil
}
func (m *mockGroupStore) ValidateAccountOwnership(userID, workspaceID int64, accountIDs []int64) ([]int64, error) {
	if m.validateAccountOwnershipFn != nil {
		return m.validateAccountOwnershipFn(userID, workspaceID, accountIDs)
	}
	return accountIDs, nil
}
func (m *mockGroupStore) Create(g *models.Group) error {
	if m.createFn != nil {
		return m.createFn(g)
	}
	return nil
}
func (m *mockGroupStore) Update(g *models.Group) error {
	if m.updateFn != nil {
		return m.updateFn(g)
	}
	return nil
}
func (m *mockGroupStore) Delete(id int64) error {
	if m.deleteFn != nil {
		return m.deleteFn(id)
	}
	return nil
}
func (m *mockGroupStore) SetAccounts(groupID int64, accountIDs []int64) error {
	if m.setAccountsFn != nil {
		return m.setAccountsFn(groupID, accountIDs)
	}
	return nil
}

// newGroupVideosRouter builds the minimal router required by the
// GET /api/v1/groups/{group_id}/youtube/videos handler. The defaults
// pin workspace ownership so cross-tenant probes fail with 404 in
// the dedicated test (TestListGroupYouTubeVideos_CrossTenant404).
//
// IMPORTANT: the userStore is wired via the 2nd positional
// argument to mustNewRouterWithDefaults (the UserStore is a
// constructor dep, not an option). Pass a non-nil userStore to
// inject FindPlatformAccountByID behaviour; pass nil for default
// identity-only behaviour.
func newGroupVideosRouter(
	t *testing.T,
	workspace *models.Workspace,
	groupStore *mockGroupStore,
	userStore *mockUserStore,
	editStore *mockYouTubeVideoEditStore,
	ytSvc *mockYouTubeOAuthServiceForEditor,
	vault *mockCredentialVault,
	opts ...RouterOption,
) *Router {
	t.Helper()
	workspaceStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		},
	}
	baseOpts := []RouterOption{
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithYouTubeService(ytSvc),
		WithCredentialVault(vault),
	}
	if groupStore != nil {
		baseOpts = append(baseOpts, WithGroupStore(groupStore))
	}
	baseOpts = append(baseOpts, opts...)
	resolvedUserStore := UserStore(userStoreIfNil(userStore))
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		resolvedUserStore,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		baseOpts...,
	)
}

// userStoreIfNil keeps newGroupVideosRouter ergonomic: callers can
// pass nil for userStore and we substitute the empty mock so the
// router never has a nil UserStore dep at the constructor boundary.
func userStoreIfNil(s *mockUserStore) UserStore {
	if s != nil {
		return s
	}
	return &mockUserStore{}
}

// TestListGroupYouTubeVideos_HappyPath is the canonical end-to-end
// success case: a workspace with one YouTube account in the group
// surfaces a single private/unlisted/processed YouTube video whose
// youtube_video_edits row already exists. The handler must:
//  1. resolve the account + vault token + YouTube.ListEditableVideos;
//  2. join with the existing session row (via
//     ListByWorkspaceAccountIDs);
//  3. fill editor_session_id / velox_project_id / editor_url /
//     editor_status / desired_privacy on the response;
//  4. derive actual_privacy = desired_privacy placeholder;
//  5. set editor_status = "editing" (mirroring the row state);
//  6. channel_name = account.Username.
func TestListGroupYouTubeVideos_HappyPath(t *testing.T) {
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
	if got.ActualPrivacy == nil || *got.ActualPrivacy != "unlisted" {
		val := ""
		if got.ActualPrivacy != nil {
			val = *got.ActualPrivacy
		}
		t.Errorf("actual_privacy placeholder must mirror desired until reconciler lands, want unlisted, got %q", val)
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

// TestListGroupYouTubeVideos_JoinMissesReady is the cross-condition
// variant: same shape as HappyPath but the YouTube listing returns a
// video with NO matching youtube_video_edits row. The handler must
// still surface the video with editor_status="ready" and omitempty
// on the editor_* fields — the SPA can then offer "Apri Dark
// Editor" which (behind the scenes) POSTs /editor-sessions to
// create a session lazily.
func TestListGroupYouTubeVideos_JoinMissesReady(t *testing.T) {
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

// TestListGroupYouTubeVideos_CrossTenant404 asserts the 404 path
// for a workspace the caller does not own. The handler MUST not
// distinguish "group not found" from "group exists but workspace
// not yours" — both collapse to the same response so a probe
// cannot enumerate tenant boundaries.
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

// TestListGroupYouTubeVideos_EmptyGroupReturns200EmptyArray asserts
// the empty-state contract: a group with NO accounts in it returns
// 200 + {"videos": []}, NOT 404. The SPA renders an "empty state"
// banner; 404 would message "group not found" which differs from
// "group found but currently empty".
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

// TestListGroupYouTubeVideos_AuthRequired asserts the auth gate: no
// JWT identity → 401, before any DB or YouTube work happens.
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

// TestListGroupYouTubeVideos_InvalidGroupIDIs400 asserts the input
// validation path: {group_id} must be a positive int. Negative or
// non-numeric values fail with 400 BEFORE any DB lookup.
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

// TestListGroupYouTubeVideos_AllAccountsFailReturns502 asserts the
// graceful-degradation contract: when YouTube fails for EVERY
// account in the group (e.g. global quota outage), the handler
// surfaces a hard 502 so the SPA renders a clear "couldn't reach
// YouTube" toast instead of an empty grid (which an operator would
// otherwise mis-read as "no videos left").
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
}

// TestListGroupYouTubeVideos_IncludeSubgroups aggregates accounts
// from a sub-group into the response when ?include_subgroups=true.
// The group_origin hint for sub-group rows must be "subgroup:<id>".
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

// (No compile-time production-repository assertion here — the
// interface conformance is already verified by the existing
// `var _ GroupStore = (*repository.GroupRepository)(nil)` line in
// pkg/api/router.go so this file does not need to repeat it.)
