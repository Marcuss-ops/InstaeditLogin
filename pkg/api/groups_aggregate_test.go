package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleListGroupsWithAccounts_ReturnsMembersInOneAggregateRead(t *testing.T) {
	const (
		userID      = int64(1)
		workspaceID = int64(7)
	)
	var aggregateCalls, legacyCalls int
	gStore := &mockGroupStore{
		listByWorkspaceWithAccountsFn: func(gotWorkspaceID int64) ([]models.GroupWithAccounts, error) {
			aggregateCalls++
			if gotWorkspaceID != workspaceID {
				t.Fatalf("workspace id: got %d, want %d", gotWorkspaceID, workspaceID)
			}
			return []models.GroupWithAccounts{
				{Group: models.Group{ID: 10, WorkspaceID: workspaceID, Name: "Editorial"}, AccountIDs: []int64{101, 102}},
				{Group: models.Group{ID: 11, WorkspaceID: workspaceID, Name: "Empty"}, AccountIDs: []int64{}},
			}, nil
		},
		listByWorkspaceFn: func(int64) ([]models.Group, error) {
			legacyCalls++
			return nil, nil
		},
		listAccountsInGroupFn: func(int64) ([]int64, error) {
			legacyCalls++
			return nil, nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(workspaceID, userID, 999))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/aggregate", nil)
	withBearerJWTForWorkspace(t, req, userID, workspaceID)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("aggregate endpoint: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Groups []models.GroupWithAccounts `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Groups) != 2 || len(response.Groups[0].AccountIDs) != 2 || len(response.Groups[1].AccountIDs) != 0 {
		t.Fatalf("unexpected aggregate response: %+v", response.Groups)
	}
	if aggregateCalls != 1 {
		t.Fatalf("aggregate store calls: got %d, want 1", aggregateCalls)
	}
	if legacyCalls != 0 {
		t.Fatalf("legacy fan-out store calls: got %d, want 0", legacyCalls)
	}
}

func TestHandleListGroupsWithAccounts_CrossOwnerIsNotRead(t *testing.T) {
	const (
		userID      = int64(1)
		workspaceID = int64(99)
	)
	gStore := &mockGroupStore{
		listByWorkspaceWithAccountsFn: func(int64) ([]models.GroupWithAccounts, error) {
			t.Fatal("aggregate store must not be called for a foreign workspace")
			return nil, nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(1, userID, 999))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups/aggregate?workspace_id=99", nil)
	withBearerJWTForWorkspace(t, req, userID, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace: want 404, got %d: %s", w.Code, w.Body.String())
	}
}
