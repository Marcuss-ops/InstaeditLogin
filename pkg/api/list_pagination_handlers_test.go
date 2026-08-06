package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestPostsWorkspaceListCursorHandler(t *testing.T) {
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	calls := 0
	r := newPostsTestRouter(&mockPostStore{
		listByWorkspacePageFn: func(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.Post, bool, error) {
			calls++
			if workspaceID != 1 || limit != 1 {
				t.Fatalf("page args = (%d, %d)", workspaceID, limit)
			}
			if calls == 1 {
				return []models.Post{{ID: 9, WorkspaceID: 1, CreatedAt: when, Status: models.PostStatusDraft}}, true, nil
			}
			if afterTime == nil || afterID != 9 {
				t.Fatalf("continuation cursor = (%v, %d)", afterTime, afterID)
			}
			return []models.Post{{ID: 8, WorkspaceID: 1, CreatedAt: when.Add(-time.Hour), Status: models.PostStatusDraft}}, false, nil
		},
	})
	first := httptest.NewRequest(http.MethodGet, "/api/v1/posts/workspace/1?limit=1", nil)
	withBearerJWT(t, first, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, first)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"has_more":true`) {
		t.Fatalf("first page = %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil || envelope.NextCursor == "" {
		t.Fatalf("next cursor missing: %v %s", err, w.Body.String())
	}
	second := httptest.NewRequest(http.MethodGet, "/api/v1/posts/workspace/1?limit=1&cursor="+envelope.NextCursor, nil)
	withBearerJWT(t, second, 1)
	w = httptest.NewRecorder()
	r.Setup().ServeHTTP(w, second)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"has_more":true`) || calls != 2 {
		t.Fatalf("second page = %d calls=%d %s", w.Code, calls, w.Body.String())
	}
}

func TestGroupsListRejectsCursorFromAnotherScope(t *testing.T) {
	r := groupsTestRouter(t, &mockGroupStore{}, userOwnedWorkspaceStore(1, 1, 99))
	bad := encodeListCursorForContext("posts", "workspace_id=1", time.Now(), "7")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups?workspace_id=1&cursor="+bad, nil)
	withBearerJWTForWorkspace(t, req, 1, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong-scope cursor: want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGroupsAggregateCursorHandler(t *testing.T) {
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	calls := 0
	r := groupsTestRouter(t, &mockGroupStore{
		listByWorkspaceWithAccountsPageFn: func(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.GroupWithAccounts, bool, error) {
			calls++
			if calls == 1 {
				return []models.GroupWithAccounts{{Group: models.Group{ID: 4, WorkspaceID: workspaceID, CreatedAt: when, Name: "A"}, AccountIDs: []int64{10}}}, true, nil
			}
			if afterID != 4 || afterTime == nil || limit != 1 {
				t.Fatalf("aggregate cursor args = (%v, %d, %d)", afterTime, afterID, limit)
			}
			return []models.GroupWithAccounts{{Group: models.Group{ID: 3, WorkspaceID: workspaceID, CreatedAt: when.Add(-time.Hour), Name: "B"}}}, false, nil
		},
	}, userOwnedWorkspaceStore(1, 1, 99))
	first := httptest.NewRequest(http.MethodGet, "/api/v1/groups/aggregate?workspace_id=1&limit=1", nil)
	withBearerJWTForWorkspace(t, first, 1, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, first)
	if w.Code != http.StatusOK {
		t.Fatalf("first aggregate page: %d %s", w.Code, w.Body.String())
	}
	var envelope struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil || envelope.NextCursor == "" {
		t.Fatalf("aggregate cursor missing: %v %s", err, w.Body.String())
	}
	second := httptest.NewRequest(http.MethodGet, "/api/v1/groups/aggregate?workspace_id=1&limit=1&cursor="+envelope.NextCursor, nil)
	withBearerJWTForWorkspace(t, second, 1, 1)
	w = httptest.NewRecorder()
	r.Setup().ServeHTTP(w, second)
	if w.Code != http.StatusOK || calls != 2 {
		t.Fatalf("second aggregate page: %d calls=%d %s", w.Code, calls, w.Body.String())
	}
}
