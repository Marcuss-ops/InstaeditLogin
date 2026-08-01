package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleUpdateGroupSettings_UsesOneAtomicStoreCall(t *testing.T) {
	var calls int
	var gotGroupID, gotWorkspaceID, gotUserID int64
	var gotUpdates []models.GroupAccountLanguageUpdate
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 7, Name: "Editorial"}, nil
		},
		updateSettingsFn: func(ctx context.Context, groupID, workspaceID, userID int64, updates []models.GroupAccountLanguageUpdate) error {
			calls++
			gotGroupID, gotWorkspaceID, gotUserID = groupID, workspaceID, userID
			gotUpdates = updates
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	body := map[string]any{"accounts": []map[string]any{
		{"account_id": 101, "language": "it"},
		{"account_id": 102, "language": "en"},
	}}
	reqBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/42/settings", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWTForWorkspace(t, req, 1, 7)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatalf("UpdateSettings calls: got %d, want 1", calls)
	}
	if gotGroupID != 42 || gotWorkspaceID != 7 || gotUserID != 1 {
		t.Fatalf("scope args: got group=%d workspace=%d user=%d", gotGroupID, gotWorkspaceID, gotUserID)
	}
	if len(gotUpdates) != 2 || gotUpdates[0].AccountID != 101 || gotUpdates[0].Language != "it" || gotUpdates[1].Language != "en" {
		t.Fatalf("updates: got %+v", gotUpdates)
	}
}

func TestHandleUpdateGroupSettings_ForeignWorkspaceDoesNotWrite(t *testing.T) {
	called := false
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 99, Name: "Foreign"}, nil
		},
		updateSettingsFn: func(context.Context, int64, int64, int64, []models.GroupAccountLanguageUpdate) error {
			called = true
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/42/settings", bytes.NewBufferString(`{"accounts":[]}`))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWTForWorkspace(t, req, 1, 7)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace: want 404, got %d: %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("UpdateSettings must not run for a foreign workspace")
	}
}

func TestHandleUpdateGroupSettings_RejectsDuplicateAccountIDs(t *testing.T) {
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 7}, nil
		},
		updateSettingsFn: func(context.Context, int64, int64, int64, []models.GroupAccountLanguageUpdate) error {
			t.Fatal("UpdateSettings must not run for duplicate account IDs")
			return errors.New("unexpected call")
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/42/settings", bytes.NewBufferString(`{"accounts":[{"account_id":101,"language":"it"},{"account_id":101,"language":"en"}]}`))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWTForWorkspace(t, req, 1, 7)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate account IDs: want 400, got %d", w.Code)
	}
}
