package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleRemoveGroupAccount_RemovesMembershipAndSyncsWorkspace(t *testing.T) {
	var gotGroupID, gotWorkspaceID, gotAccountID int64
	var repoCalls int
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 7, Name: "Editorial"}, nil
		},
		removeAccountFromGroupTxFn: func(_ context.Context, groupID, workspaceID, accountID int64) error {
			repoCalls++
			gotGroupID, gotWorkspaceID, gotAccountID = groupID, workspaceID, accountID
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/groups/42/accounts/101", nil)
	withBearerJWTForWorkspace(t, req, 1, 7)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
	if repoCalls != 1 {
		t.Fatalf("RemoveAccountFromGroupTx calls: got %d, want 1", repoCalls)
	}
	if gotGroupID != 42 || gotWorkspaceID != 7 || gotAccountID != 101 {
		t.Fatalf("scope args: got group=%d workspace=%d account=%d", gotGroupID, gotWorkspaceID, gotAccountID)
	}
}

func TestHandleRemoveGroupAccount_ForeignWorkspaceDoesNotRemove(t *testing.T) {
	called := false
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 99, Name: "Foreign"}, nil
		},
		removeAccountFromGroupTxFn: func(context.Context, int64, int64, int64) error {
			called = true
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/groups/42/accounts/101", nil)
	withBearerJWTForWorkspace(t, req, 1, 7)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace: want 404, got %d: %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("RemoveAccountFromGroupTx must not run for a foreign workspace")
	}
}

func TestHandleRemoveGroupAccount_MissingGroupIs404(t *testing.T) {
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return nil, nil
		},
		removeAccountFromGroupTxFn: func(context.Context, int64, int64, int64) error {
			t.Fatal("RemoveAccountFromGroupTx must not run for a missing group")
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/groups/42/accounts/101", nil)
	withBearerJWTForWorkspace(t, req, 1, 7)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("missing group: want 404, got %d", w.Code)
	}
}

func TestHandleRemoveGroupAccount_NonNumericIDsDoNotMatchRoute(t *testing.T) {
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 7}, nil
		},
		removeAccountFromGroupTxFn: func(context.Context, int64, int64, int64) error {
			t.Fatal("RemoveAccountFromGroupTx must not run for non-numeric ids")
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	// The group routes pin {id:[0-9]+} / {accountId:[0-9]+} in chi, so a
	// non-numeric segment never reaches the handler — it 404s at the
	// router (same contract as every other /groups/{id} route).
	for _, path := range []string{
		"/api/v1/groups/abc/accounts/101",
		"/api/v1/groups/42/accounts/abc",
	} {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		withBearerJWTForWorkspace(t, req, 1, 7)
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404 (route regex), got %d", path, w.Code)
		}
	}
}

func TestHandleRemoveGroupAccount_ZeroIDsAreRejected(t *testing.T) {
	gStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 7}, nil
		},
		removeAccountFromGroupTxFn: func(context.Context, int64, int64, int64) error {
			t.Fatal("RemoveAccountFromGroupTx must not run for zero ids")
			return nil
		},
	}
	r := groupsTestRouter(t, gStore, userOwnedWorkspaceStore(7, 1, 99))
	for _, path := range []string{
		"/api/v1/groups/0/accounts/101",
		"/api/v1/groups/42/accounts/0",
	} {
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		withBearerJWTForWorkspace(t, req, 1, 7)
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", path, w.Code)
		}
	}
}
