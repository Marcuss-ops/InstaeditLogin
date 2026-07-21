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

// ---------------------------------------------------------------------------
// handleCreateWorkspace tests
// ---------------------------------------------------------------------------

func TestHandleCreateWorkspace_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		createFn: func(w *models.Workspace) error {
			w.ID = 42
			w.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	body := `{"name":"My Workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp models.Workspace
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Name != "My Workspace" {
		t.Fatalf("name: want My Workspace, got %s", resp.Name)
	}
	if resp.ID != 42 {
		t.Fatalf("id: want 42, got %d", resp.ID)
	}
}

func TestHandleCreateWorkspace_MissingName_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreateWorkspace_MalformedJSON_400(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleCreateWorkspace_MissingJWT_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "") // no WithWorkspaceStore

	body := `{"name":"My Workspace"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestHandleGetWorkspace_CrossOwner_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithWorkspaceStore(wsStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/42", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}
