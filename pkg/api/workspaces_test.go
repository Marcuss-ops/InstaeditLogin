package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// newWorkspaceTestRouter builds a Router wired with the supplied
// workspace store. Authentication is JWT-only (Taglio 1.1).
//
// Taglio 2.1: the Router takes a CapabilityRouter (per-capability lookups)
// instead of the old PlatformRegistry. These tests don't exercise the
// publish path so a default no-op mockTokenService is enough.
func newWorkspaceTestRouter(
	workspaceStore *mockWorkspaceStore,
) *Router {
	return NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithPostStore(&mockPostStore{}),
		WithTokenService(&mockTokenService{}),
	)
}

func workspacesIssueJWT(t *testing.T, userID int64) string {
	t.Helper()
	tok, _, _, err := auth.NewManager(testJWTSecret, 24).Issue(userID)
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	return tok
}

func TestWorkspacesAPI_Create_Happy(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		createFn: func(w *models.Workspace) error {
			w.ID = 42
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", bytes.NewReader([]byte(`{"name":"Editorial"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var ws models.Workspace
	if err := json.NewDecoder(w.Body).Decode(&ws); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ws.ID != 42 || ws.Name != "Editorial" {
		t.Errorf("workspace mismatch: %+v", ws)
	}
}

func TestWorkspacesAPI_Create_MissingName_422(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestWorkspacesAPI_Create_StrictAuth_NoJWT_401(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces",
		bytes.NewReader([]byte(`{"name":"X"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestWorkspacesAPI_List_Happy(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		listByOwnerFn: func(ownerID int64) ([]models.Workspace, error) {
			return []models.Workspace{{ID: 7, Name: "Personal", OwnerID: ownerID}, {ID: 8, Name: "Work", OwnerID: ownerID}}, nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Workspaces []models.Workspace `json:"workspaces"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Workspaces) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(body.Workspaces))
	}
}

func TestWorkspacesAPI_Get_Found_200(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Personal", OwnerID: 1}, nil
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/77", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspacesAPI_Get_NotFound_404(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) { return nil, sql.ErrNoRows },
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/999", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestWorkspacesAPI_Get_RepoErrWorkspaceNotFound_404(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return nil, repository.ErrWorkspaceNotFound
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/999", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestWorkspacesAPI_Get_BadID_400(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/not-a-number", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestWorkspacesAPI_Delete_Happy_204(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Personal", OwnerID: 1}, nil
		},
		deleteFn: func(id int64) error { return nil },
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/77", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspacesAPI_Delete_WrongOwner_403(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
		deleteFn: func(id int64) error {
			t.Errorf("Delete must NOT be called when ownership check fails")
			return nil
		},
	})
	tok := workspacesIssueJWT(t, 1)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/77", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestWorkspacesAPI_Delete_NotFound_404(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) { return nil, sql.ErrNoRows },
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/999", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestWorkspacesAPI_Delete_RaceBetweenFindAndDelete_Returns404(t *testing.T) {
	r := newWorkspaceTestRouter(&mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "X", OwnerID: 1}, nil
		},
		deleteFn: func(id int64) error {
			return repository.ErrWorkspaceNotFound
		},
	})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/77", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}
