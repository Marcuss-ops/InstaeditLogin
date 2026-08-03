package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type thumbnailProjectTestStore struct {
	created   *models.ThumbnailProject
	items     []models.ThumbnailProject
	project   *models.ThumbnailProject
	createErr error
	updateErr error
	statusErr error
}

func (s *thumbnailProjectTestStore) Create(_ context.Context, project *models.ThumbnailProject) error {
	if s.createErr != nil {
		return s.createErr
	}
	project.ID = "thumbproj_test"
	project.Version = 1
	project.CreatedAt = time.Now().UTC()
	project.UpdatedAt = project.CreatedAt
	s.created = project
	return nil
}
func (s *thumbnailProjectTestStore) FindByID(_ context.Context, _ int64, _ string) (*models.ThumbnailProject, error) {
	return s.project, nil
}
func (s *thumbnailProjectTestStore) ListByWorkspace(_ context.Context, _ int64) ([]models.ThumbnailProject, error) {
	return s.items, nil
}
func (s *thumbnailProjectTestStore) UpdateCAS(_ context.Context, _ *models.ThumbnailProject, _ int64) error {
	return s.updateErr
}
func (s *thumbnailProjectTestStore) UpdateStatusCAS(_ context.Context, _ int64, _ string, _ models.ThumbnailProjectStatus, _ int64) error {
	return s.statusErr
}

func thumbnailProjectRouter(t *testing.T, store *thumbnailProjectTestStore, ws *mockWorkspaceStore) *Router {
	t.Helper()
	return newTestRouter(&mockProvider{platform: models.PlatformYouTube}, &mockUserStore{}, "",
		WithWorkspaceStore(ws), WithThumbnailProjectStore(store))
}

func TestThumbnailProjects_CreateWithoutYouTubeRequirement(t *testing.T) {
	store := &thumbnailProjectTestStore{}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		return &models.Workspace{ID: id, OwnerID: 1}, nil
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects", bytes.NewBufferString(`{"workspace_id":7,"name":"Cover","canvas_width":1920,"canvas_height":1080}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if store.created == nil || store.created.WorkspaceID != 7 || store.created.CreatedBy != 1 {
		t.Fatalf("created project: %+v", store.created)
	}
	var response models.ThumbnailProject
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "thumbproj_test" {
		t.Fatalf("response id: %q", response.ID)
	}
}

func TestThumbnailProjects_CrossWorkspaceCreateIsNotFound(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		return &models.Workspace{ID: id, OwnerID: 99}, nil
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects", bytes.NewBufferString(`{"workspace_id":7,"name":"Cover","canvas_width":1920,"canvas_height":1080}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestThumbnailProjects_UpdateVersionConflictIs409(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project:   &models.ThumbnailProject{ID: "thumbproj_test", WorkspaceID: 7, CreatedBy: 1, Name: "Old", CanvasWidth: 1920, CanvasHeight: 1080, Status: models.ThumbnailProjectStatusDraft, Version: 4},
		updateErr: repository.ErrThumbnailProjectConflict,
	}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/thumbnail-projects/thumbproj_test?workspace_id=7", bytes.NewBufferString(`{"name":"New","version":3}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailProjects_DeleteRequiresVersion(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/thumbnail-projects/thumbproj_test?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}
