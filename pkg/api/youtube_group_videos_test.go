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
	"time"

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

// stringPtr is a tiny helper used by TestListGroupYouTubeVideos_
// JoinSessionMapKeepsNewest to construct *string literals inline.
// Lives in this test file only (no other test in pkg/api uses it);
// placed in the helpers block alongside newGroupVideosRouter +
// userStoreIfNil for consistency.
func stringPtr(s string) *string {
	return &s
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
//
// =========================================================================
// PHANTOM EMISSION (handleListGroupYouTubeVideos step 7.5)
// =========================================================================
//
// A video published as `public` disappears from YouTube's
// ListEditableVideos (which filters privacy=public). Without the
// phantom-emission pass the operator's card would vanish from the
// group's video grid the moment they click "Pubblica" with
// privacy=public. These tests pin the five guarantees:
//
//  1. status='published' + no matching YouTube row → phantom entry
//     emitted with YouTube CDN thumbnail + correct privacy/sync;
//  2. status='published' + matching YouTube row → no double emission
//     (fan-out wins, regular entry has Phantom=false);
//  3. status='editing' + no matching YouTube row → NOT emitted
//     (only terminal-published sessions synthesize phantoms);
//  4. status='published' + UpdatedAt older than
//     groupYouTubeVideosPhantomMaxAge → NOT emitted (recency cap);
//  5. status='published' + account not in the group's account set
//     → NOT emitted (cross-group leak guard).
//
// All five tests share the newGroupVideosRouter seam already used
// by the existing suite; no new helper is introduced.

// TestListGroupYouTubeVideos_PhantomEmissionForPublishedPublic is
// the canonical happy path for the phantom pass: a status='published'
// session whose YouTube row was filtered out (because privacy=public
// is filtered by ListEditableVideos) MUST surface as a phantom entry
// in the response so the operator still sees their freshly-published
// card in the group's video grid.
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

// TestListGroupYouTubeVideos_PhantomDedupsAgainstFanOut: when the
// fan-out ALREADY emitted a regular entry for the same
// (account, video) tuple (the rare race where YouTube briefly
// included a public video before the privacy flip took effect),
// the phantom pass MUST NOT double-emit. Result: exactly one entry
// with Phantom=false (the fan-out's regular entry wins).
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

// TestListGroupYouTubeVideos_PhantomSkipsEditingSession: only
// status='published' sessions synthesize phantoms. status='editing'
// (and any other non-terminal-published status) MUST be skipped.
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

// TestListGroupYouTubeVideos_PhantomRespectsRecencyFilter: a
// published session older than groupYouTubeVideosPhantomMaxAge
// (90 days) MUST NOT be emitted as a phantom. Guards the response
// from saturating with year-old publishes.
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

// TestListGroupYouTubeVideos_PhantomRespectsAccountGrouping: a
// published session whose PlatformAccountID is NOT in the group's
// account set MUST NOT emit a phantom entry in this group's
// response (cross-group leak guard).
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

// TestListGroupYouTubeVideos_JoinSessionMapKeepsNewest is the
// regression test for the drive-by fix in step 5 of
// handleListGroupYouTubeVideos: the sessionMap build loop was
// silently keeping the OLDEST session per (account, video) tuple
// because ListByWorkspaceAccountIDs returns rows ORDER BY updated_at
// DESC and a naive `sessionMap[key] = s` map-build keeps the LAST
// iteration = the OLDEST row.
//
// The fix flips the map-build to `if !exists { ... }` so the
// FIRST occurrence in the loop (= the newest) wins. Without this
// test, a future refactor that reverts the map-build would silently
// pin stale data over fresh data and the fan-out would emit the
// OLD session's velox_project_id, editor_status, etc.
//
// Setup: TWO sessions for the same (account, video) tuple with
// distinct updated_at; newer first (matches the SQL ORDER BY). The
// YouTube fan-out returns ONE matching video so the join runs.
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
func TestFetchAccountEditableVideos_PaginatesUntilConfiguredLimit(t *testing.T) {
	account := &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123"}
	var pageTokens []string
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(_ context.Context, _, _ string, pageToken string) (*services.YouTubeVideoPage, error) {
			pageTokens = append(pageTokens, pageToken)
			switch pageToken {
			case "":
				return &services.YouTubeVideoPage{
					Items: []models.YouTubeVideoDetails{{ID: "v1"}, {ID: "v2"}}, NextPageToken: "page-2",
				}, nil
			case "page-2":
				return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{ID: "v3"}, {ID: "v4"}}}, nil
			default:
				return nil, fmt.Errorf("unexpected page token %q", pageToken)
			}
		},
	}
	r := &Router{
		vault: &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		}},
		youTubeSvc: ytSvc,
	}

	items, err := r.fetchAccountEditableVideos(context.Background(), account, 3)
	if err != nil {
		t.Fatalf("fetchAccountEditableVideos: %v", err)
	}
	if got, want := len(items), 3; got != want {
		t.Fatalf("items: got %d, want %d", got, want)
	}
	if got := []string{items[0].ID, items[1].ID, items[2].ID}; fmt.Sprint(got) != "[v1 v2 v3]" {
		t.Errorf("item order: got %v, want [v1 v2 v3]", got)
	}
	if got, want := fmt.Sprint(pageTokens), "[ page-2]"; got != want {
		t.Errorf("page tokens: got %s, want %s", got, want)
	}
}

func TestFetchCachedAccountEditableVideos_UsesShortLivedCache(t *testing.T) {
	account := &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123"}
	var listCalls int
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(context.Context, string, string, string) (*services.YouTubeVideoPage, error) {
			listCalls++
			return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{ID: "cached-video"}}}, nil
		},
	}
	r := &Router{
		vault: &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		}},
		youTubeSvc: ytSvc,
	}
	cfg := YouTubeGroupVideosConfig{MaxVideos: 10, CacheTTL: time.Minute}.normalized()

	first, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	second, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("YouTube list calls: got %d, want 1", listCalls)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("cached results differ: first=%+v second=%+v", first, second)
	}
}

func TestParseGroupVideosPagination(t *testing.T) {
	cfg := YouTubeGroupVideosConfig{MaxVideos: 100, DefaultPageSize: 25}.normalized()
	tests := []struct {
		name       string
		query      string
		wantOffset int
		wantLimit  int
		wantErr    bool
	}{
		{name: "defaults", query: "", wantLimit: 25},
		{name: "offset and limit", query: "offset=10&limit=7", wantOffset: 10, wantLimit: 7},
		{name: "limit capped", query: "limit=1000", wantLimit: 100},
		{name: "negative offset", query: "offset=-1", wantErr: true},
		{name: "zero limit", query: "limit=0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			offset, limit, err := parseGroupVideosPagination(req, cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error: got %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if offset != tt.wantOffset || limit != tt.wantLimit {
				t.Errorf("pagination: got offset=%d limit=%d, want offset=%d limit=%d", offset, limit, tt.wantOffset, tt.wantLimit)
			}
		})
	}
}

func TestGroupYouTubeVideos_InvalidTokenClassification(t *testing.T) {
	for _, test := range []struct {
		message string
		want    bool
	}{
		{message: "oauth: invalid_grant", want: true},
		{message: "youtube list: status 401", want: true},
		{message: "youtube list: status 500", want: false},
		{message: "context deadline exceeded", want: false},
	} {
		t.Run(test.message, func(t *testing.T) {
			if got := isInvalidYouTubeTokenError(errors.New(test.message)); got != test.want {
				t.Errorf("isInvalidYouTubeTokenError(%q) = %v, want %v", test.message, got, test.want)
			}
		})
	}
}
