package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func newGroupCoversRouter(
	t *testing.T,
	workspace *models.Workspace,
	group *models.Group,
	accountIDs []int64,
	editStore *mockYouTubeVideoEditStore,
	userStore *mockUserStore,
	opts ...RouterOption,
) *Router {
	t.Helper()
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if group != nil && id == group.ID {
				return group, nil
			}
			return nil, nil
		},
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
			if group != nil && groupID == group.ID {
				return accountIDs, nil
			}
			return nil, nil
		},
	}
	return newGroupVideosRouter(
		t,
		workspace,
		groupStore,
		userStore,
		editStore,
		&mockYouTubeOAuthServiceForEditor{},
		&mockCredentialVault{},
		opts...,
	)
}

func coversRequest(t *testing.T, groupID string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/"+groupID+"/covers", nil)
	withBearerJWT(t, req, 1)
	return req
}

func testGroupCover(t *testing.T, projectID, veloxProjectID string, status models.ThumbnailProjectStatus) *models.GroupCover {
	t.Helper()
	now := time.Now().UTC()
	media := "00000000-0000-0000-0000-000000000042"
	title := "Cover per video test"
	return &models.GroupCover{
		ProjectID:         projectID,
		WorkspaceID:       7,
		ProjectName:       "YouTube cover",
		ProjectStatus:     status,
		PreviewMediaID:    &media,
		ProjectVersion:    2,
		ProjectCreatedAt:  now.Add(-time.Hour),
		ProjectUpdatedAt:  now,
		SessionID:         projectID,
		PlatformAccountID: 42,
		YouTubeVideoID:    "fwFGQglE9c0",
		VeloxProjectID:    veloxProjectID,
		CategoryID:        "24",
		DesiredPrivacy:    "private",
		EditStatus:        "editing",
		DraftTitle:        &title,
		SessionCreatedAt:  now.Add(-time.Hour),
		SessionUpdatedAt:  now,
	}
}

func TestHandleListGroupCovers_HappyPath(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	group := &models.Group{ID: 3, WorkspaceID: 7, Name: "Amish"}
	cover := testGroupCover(t, "ytes_cover_1", "ve_cover_1", models.ThumbnailProjectStatusReady)
	archived := testGroupCover(t, "ytes_cover_2", "ve_cover_2", models.ThumbnailProjectStatusArchived)
	editStore := &mockYouTubeVideoEditStore{
		listCoversFn: func(_ context.Context, workspaceID int64, accountIDs []int64) ([]*models.GroupCover, error) {
			if workspaceID != 7 {
				t.Errorf("workspaceID: want 7, got %d", workspaceID)
			}
			if len(accountIDs) != 2 || accountIDs[0] != 42 || accountIDs[1] != 43 {
				t.Errorf("accountIDs: want [42 43], got %v", accountIDs)
			}
			return []*models.GroupCover{cover, archived}, nil
		},
	}
	r := newGroupCoversRouter(t, workspace, group, []int64{42, 43}, editStore, &mockUserStore{})

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "3"))

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp groupCoversResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Covers) != 2 {
		t.Fatalf("covers: want 2, got %d", len(resp.Covers))
	}
	first := resp.Covers[0]
	if first.ProjectID != "ytes_cover_1" {
		t.Errorf("project_id: want ytes_cover_1, got %q", first.ProjectID)
	}
	if first.ProjectStatus != string(models.ThumbnailProjectStatusReady) {
		t.Errorf("project_status: want ready, got %q", first.ProjectStatus)
	}
	if first.EditorURL != "https://editor.instaedit.test/editor/ve_cover_1" {
		t.Errorf("editor_url: want https://editor.instaedit.test/editor/ve_cover_1, got %q", first.EditorURL)
	}
	if first.PreviewMediaID == nil || *first.PreviewMediaID != "00000000-0000-0000-0000-000000000042" {
		t.Errorf("preview_media_id: want the seeded media id, got %v", first.PreviewMediaID)
	}
	if first.DraftTitle == nil || *first.DraftTitle != "Cover per video test" {
		t.Errorf("draft_title: want seeded title, got %v", first.DraftTitle)
	}
	if first.CategoryID != "24" {
		t.Errorf("category_id: want 24, got %q", first.CategoryID)
	}
	// No actual read-back yet → privacy_status falls back to desired.
	if first.PrivacyStatus != "private" {
		t.Errorf("privacy_status: want private (desired fallback), got %q", first.PrivacyStatus)
	}
	// Archived covers must stay visible (the hub shows full history).
	second := resp.Covers[1]
	if second.ProjectStatus != string(models.ThumbnailProjectStatusArchived) {
		t.Errorf("second project_status: want archived, got %q", second.ProjectStatus)
	}
}

func TestHandleListGroupCovers_PrivacyStatusPrefersActualReadBack(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	group := &models.Group{ID: 3, WorkspaceID: 7, Name: "Amish"}
	cover := testGroupCover(t, "ytes_cover_1", "ve_cover_1", models.ThumbnailProjectStatusReady)
	// Operator scheduled private, YouTube read back public after the
	// schedule fired — actual must win over desired in the projection.
	cover.DesiredPrivacy = "private"
	actual := "public"
	cover.ActualPrivacy = &actual
	editStore := &mockYouTubeVideoEditStore{
		listCoversFn: func(_ context.Context, _ int64, _ []int64) ([]*models.GroupCover, error) {
			return []*models.GroupCover{cover}, nil
		},
	}
	r := newGroupCoversRouter(t, workspace, group, []int64{42}, editStore, &mockUserStore{})

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "3"))

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp groupCoversResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Covers) != 1 {
		t.Fatalf("covers: want 1, got %d", len(resp.Covers))
	}
	if resp.Covers[0].PrivacyStatus != "public" {
		t.Errorf("privacy_status: want public (actual read-back wins), got %q", resp.Covers[0].PrivacyStatus)
	}
}

func TestHandleListGroupCovers_ChannelMetadataResolved(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	group := &models.Group{ID: 3, WorkspaceID: 7, Name: "Amish"}
	cover := testGroupCover(t, "ytes_cover_1", "ve_cover_1", models.ThumbnailProjectStatusDraft)
	cover.SourceThumbnailURL = "https://i.ytimg.com/vi/fwFGQglE9c0/hqdefault.jpg"
	editStore := &mockYouTubeVideoEditStore{
		listCoversFn: func(_ context.Context, _ int64, _ []int64) ([]*models.GroupCover, error) {
			return []*models.GroupCover{cover}, nil
		},
	}
	// The covers handler resolves channel display metadata through
	// userRepo.FindPlatformAccountByID — wire the account so the
	// response carries channel_name + language.
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id != 42 {
				return nil, nil
			}
			return &models.PlatformAccount{
				ID:             42,
				Platform:       models.PlatformYouTube,
				Username:       "Wrestling Insider RU",
				PlatformUserID: "UC_RU",
				Status:         models.AccountStatusActive,
				Metadata:       models.Metadata{"language": "ru"},
			}, nil
		},
	}
	r := newGroupCoversRouter(t, workspace, group, []int64{42}, editStore, userStore)

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "3"))

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp groupCoversResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Covers) != 1 {
		t.Fatalf("covers: want 1, got %d", len(resp.Covers))
	}
	if resp.Covers[0].ChannelName != "Wrestling Insider RU" {
		t.Errorf("channel_name: want Wrestling Insider RU, got %q", resp.Covers[0].ChannelName)
	}
	if resp.Covers[0].Language != "ru" {
		t.Errorf("language: want ru, got %q", resp.Covers[0].Language)
	}
}

func TestHandleListGroupCovers_EmptyGroup(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	group := &models.Group{ID: 3, WorkspaceID: 7, Name: "empty"}
	r := newGroupCoversRouter(t, workspace, group, nil, &mockYouTubeVideoEditStore{}, &mockUserStore{})

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "3"))

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp groupCoversResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Covers) != 0 {
		t.Errorf("covers: want empty slice, got %d", len(resp.Covers))
	}
}

func TestHandleListGroupCovers_Unauthorized(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	group := &models.Group{ID: 3, WorkspaceID: 7, Name: "Amish"}
	r := newGroupCoversRouter(t, workspace, group, []int64{42}, &mockYouTubeVideoEditStore{}, &mockUserStore{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/3/covers", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleListGroupCovers_ForeignGroupCollapsesTo404(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	// Group belongs to workspace 99 — caller (user 1) does not own it.
	group := &models.Group{ID: 3, WorkspaceID: 99, Name: "foreign"}
	r := newGroupCoversRouter(t, workspace, group, []int64{42}, &mockYouTubeVideoEditStore{}, &mockUserStore{})

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "3"))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleListGroupCovers_BadGroupID(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	r := newGroupCoversRouter(t, workspace, nil, nil, &mockYouTubeVideoEditStore{}, &mockUserStore{})

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "abc"))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHandleListGroupCovers_StoreError(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "ws"}
	group := &models.Group{ID: 3, WorkspaceID: 7, Name: "Amish"}
	editStore := &mockYouTubeVideoEditStore{
		listCoversFn: func(_ context.Context, _ int64, _ []int64) ([]*models.GroupCover, error) {
			return nil, fmt.Errorf("boom")
		},
	}
	r := newGroupCoversRouter(t, workspace, group, []int64{42}, editStore, &mockUserStore{})

	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, coversRequest(t, "3"))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d body=%s", w.Code, w.Body.String())
	}
}
