package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type thumbnailProjectTestStore struct {
	created     *models.ThumbnailProject
	items       []models.ThumbnailProject
	project     *models.ThumbnailProject
	createErr   error
	updateErr   error
	statusErr   error
	snapshotErr error
	snapshot    *models.ThumbnailProjectSnapshotResult
	revisions   []models.ThumbnailProjectRevision
	revision    *models.ThumbnailProjectRevision
	// Export surfaces for the render/export endpoints.
	createExportErr   error
	updateExportErr   error
	createdExport     *models.ThumbnailExport
	export            *models.ThumbnailExport
	findExportErr     error
	lastExportStatus  string
	lastExportError   string
	lastExportSHA     []byte
	lastExportFileSz  int64
	lastExportVersion string
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
func (s *thumbnailProjectTestStore) SaveSnapshot(_ context.Context, _ int64, _ string, _ models.ThumbnailProjectSnapshot, _ int64) (*models.ThumbnailProjectSnapshotResult, error) {
	return s.snapshot, s.snapshotErr
}
func (s *thumbnailProjectTestStore) ListRevisions(_ context.Context, _ int64, _ string) ([]models.ThumbnailProjectRevision, error) {
	return s.revisions, nil
}
func (s *thumbnailProjectTestStore) FindRevision(_ context.Context, _ int64, _, _ string) (*models.ThumbnailProjectRevision, error) {
	return s.revision, nil
}
func (s *thumbnailProjectTestStore) RestoreRevision(_ context.Context, _ int64, _, _ string, _, _ int64, _ string) (*models.ThumbnailProjectSnapshotResult, error) {
	return s.snapshot, s.snapshotErr
}
func (s *thumbnailProjectTestStore) CreateExport(_ context.Context, _ int64, export *models.ThumbnailExport) error {
	if s.createExportErr != nil {
		return s.createExportErr
	}
	if export.ID == "" {
		export.ID = "thumbexp_test"
	}
	export.CreatedAt = time.Now().UTC()
	s.createdExport = export
	return nil
}
func (s *thumbnailProjectTestStore) FindExport(_ context.Context, _ int64, _ string) (*models.ThumbnailExport, error) {
	return s.export, s.findExportErr
}
func (s *thumbnailProjectTestStore) UpdateExportStatus(_ context.Context, _ int64, _ string, status, lastError string, sha256 []byte, fileSize int64, rendererVersion string) error {
	if s.updateExportErr != nil {
		return s.updateExportErr
	}
	s.lastExportStatus = status
	s.lastExportError = lastError
	s.lastExportSHA = sha256
	s.lastExportFileSz = fileSize
	s.lastExportVersion = rendererVersion
	if s.export != nil {
		s.export.Status = status
	}
	return nil
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

func TestThumbnailProjects_SnapshotConflictHonorsIfMatch(t *testing.T) {
	store := &thumbnailProjectTestStore{snapshotErr: repository.ErrThumbnailProjectConflict}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/thumbnail-projects/thumbproj_test/snapshot?workspace_id=7", bytes.NewBufferString(`{"schema_version":1,"snapshot":{"canvas":{},"objects":[]},"renderer_version":"r1","base_version":3}`))
	req.Header.Set("If-Match", `"version-3"`)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["code"] != "PROJECT_VERSION_CONFLICT" {
		t.Fatalf("unexpected conflict body: %s", w.Body.String())
	}
}

func TestThumbnailProjects_SnapshotConflictCarriesCurrentVersion(t *testing.T) {
	store := &thumbnailProjectTestStore{snapshotErr: fmt.Errorf("%w: expected=8 current=9", repository.ErrThumbnailProjectConflict)}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/thumbnail-projects/thumbproj_test/snapshot?workspace_id=7", bytes.NewBufferString(`{"schema_version":1,"snapshot":{"canvas":{},"objects":[]},"renderer_version":"r1","base_version":8}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "PROJECT_VERSION_CONFLICT" {
		t.Fatalf("unexpected conflict body: %s", w.Body.String())
	}
	if body["current_version"] != float64(9) {
		t.Fatalf("want current_version=9, got %v", body["current_version"])
	}
}

func TestThumbnailProjects_UpdateConflictOmitsUnknownCurrentVersion(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project:   &models.ThumbnailProject{ID: "thumbproj_test", WorkspaceID: 7, CreatedBy: 1, Name: "Old", CanvasWidth: 1920, CanvasHeight: 1080, Status: models.ThumbnailProjectStatusDraft, Version: 4},
		updateErr: fmt.Errorf("%w: project_id=thumbproj_test expected_version=3", repository.ErrThumbnailProjectConflict),
	}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/thumbnail-projects/thumbproj_test?workspace_id=7", bytes.NewBufferString(`{"name":"New","version":3}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "PROJECT_VERSION_CONFLICT" {
		t.Fatalf("unexpected conflict body: %s", w.Body.String())
	}
	if _, present := body["current_version"]; present {
		t.Fatalf("current_version must be omitted when unknown, got %v", body["current_version"])
	}
}

func TestThumbnailProjects_RevisionsCrossWorkspaceAreHidden(t *testing.T) {
	store := &thumbnailProjectTestStore{revisions: []models.ThumbnailProjectRevision{{ID: "rev-1", ProjectID: "thumbproj_test"}}}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 99}, nil }})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/thumbnail-projects/thumbproj_test/revisions?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for cross-workspace revision list, got %d", w.Code)
	}
}

func TestThumbnailProjects_RestoreReturnsNewRevision(t *testing.T) {
	store := &thumbnailProjectTestStore{snapshot: &models.ThumbnailProjectSnapshotResult{
		ProjectID: "thumbproj_test", RevisionID: "rev-new", RevisionNumber: 2, Version: 3,
	}}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/restore/rev-old?workspace_id=7", bytes.NewBufferString(`{"base_version":2}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var result models.ThumbnailProjectSnapshotResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.RevisionID != "rev-new" || result.RevisionNumber != 2 || result.Version != 3 {
		t.Fatalf("unexpected restore result: %+v", result)
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
