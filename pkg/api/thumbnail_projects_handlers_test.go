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
	// Asset surfaces for the asset endpoints.
	assets          []models.ThumbnailProjectAsset
	assetErr        error
	deleteAssetErr  error
	createdAsset    *models.ThumbnailProjectAsset
	lastDeleteMedia string
	lastDeleteRole  string
	// Assignment surfaces for the assignment endpoint.
	assignmentErr      error
	createdAssignments []models.ThumbnailAssignment
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
func (s *thumbnailProjectTestStore) CreateAsset(_ context.Context, _ int64, asset *models.ThumbnailProjectAsset) error {
	if s.assetErr != nil {
		return s.assetErr
	}
	if asset.CreatedAt.IsZero() {
		asset.CreatedAt = time.Now().UTC()
	}
	s.createdAsset = asset
	return nil
}
func (s *thumbnailProjectTestStore) ListAssets(_ context.Context, _ int64, _ string) ([]models.ThumbnailProjectAsset, error) {
	return s.assets, nil
}
func (s *thumbnailProjectTestStore) DeleteAsset(_ context.Context, _ int64, _ string, mediaID, role string) error {
	if s.deleteAssetErr != nil {
		return s.deleteAssetErr
	}
	s.lastDeleteMedia, s.lastDeleteRole = mediaID, role
	return nil
}
func (s *thumbnailProjectTestStore) CreateAssignment(_ context.Context, assignment *models.ThumbnailAssignment) error {
	if s.assignmentErr != nil {
		return s.assignmentErr
	}
	if assignment.ID == "" {
		assignment.ID = "thumbassign_test"
	}
	s.createdAssignments = append(s.createdAssignments, *assignment)
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

func TestThumbnailProjects_AddAssetRequiresWorkspaceQuery(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/assets", bytes.NewBufferString(`{"media_id":"00000000-0000-4000-8000-000000000001","role":"background"}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing workspace_id, got %d", w.Code)
	}
}

func TestThumbnailProjects_AddAssetLinksMediaToProject(t *testing.T) {
	store := &thumbnailProjectTestStore{}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	body := `{"media_id":"00000000-0000-4000-8000-000000000001","role":"logo","object_id":"text-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/assets?workspace_id=7", bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if store.createdAsset == nil || store.createdAsset.ProjectID != "thumbproj_test" || store.createdAsset.Role != "logo" || store.createdAsset.MediaID != "00000000-0000-4000-8000-000000000001" || store.createdAsset.ObjectID == nil || *store.createdAsset.ObjectID != "text-1" {
		t.Fatalf("unexpected created asset: %+v", store.createdAsset)
	}
}

func TestThumbnailProjects_AddAssetDuplicateIs409(t *testing.T) {
	store := &thumbnailProjectTestStore{assetErr: repository.ErrThumbnailDomainConflict}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/assets?workspace_id=7", bytes.NewBufferString(`{"media_id":"00000000-0000-4000-8000-000000000001","role":"background"}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["code"] != "ASSET_ALREADY_LINKED" {
		t.Fatalf("unexpected conflict body: %s", w.Body.String())
	}
}

func TestThumbnailProjects_AddAssetInvalidRoleIs422(t *testing.T) {
	store := &thumbnailProjectTestStore{assetErr: fmt.Errorf("%w: unsupported role", repository.ErrThumbnailProjectInvalid)}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/assets?workspace_id=7", bytes.NewBufferString(`{"media_id":"00000000-0000-4000-8000-000000000001","role":"bogus"}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailProjects_AddAssetCrossWorkspaceIs404(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 99}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/assets?workspace_id=7", bytes.NewBufferString(`{"media_id":"00000000-0000-4000-8000-000000000001","role":"background"}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for cross-workspace asset add, got %d", w.Code)
	}
}

func TestThumbnailProjects_ListAssetsReturnsItems(t *testing.T) {
	asset := models.ThumbnailProjectAsset{ProjectID: "thumbproj_test", MediaID: "00000000-0000-4000-8000-000000000001", Role: "background"}
	store := &thumbnailProjectTestStore{assets: []models.ThumbnailProjectAsset{asset}}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/thumbnail-projects/thumbproj_test/assets?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response thumbnailProjectAssetListResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].MediaID != asset.MediaID {
		t.Fatalf("unexpected list: %+v", response.Items)
	}
}

func TestThumbnailProjects_ListAssetsEmptyIsEmptyArray(t *testing.T) {
	store := &thumbnailProjectTestStore{}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/thumbnail-projects/thumbproj_test/assets?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response thumbnailProjectAssetListResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Items == nil || len(response.Items) != 0 {
		t.Fatalf("want empty items array, got %#v", response.Items)
	}
}

func TestThumbnailProjects_DeleteAssetRequiresRole(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/thumbnail-projects/thumbproj_test/assets/00000000-0000-4000-8000-000000000001?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing role, got %d", w.Code)
	}
}

func TestThumbnailProjects_DeleteAssetRemovesLink(t *testing.T) {
	store := &thumbnailProjectTestStore{}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/thumbnail-projects/thumbproj_test/assets/00000000-0000-4000-8000-000000000001?workspace_id=7&role=background", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
	if store.lastDeleteMedia != "00000000-0000-4000-8000-000000000001" || store.lastDeleteRole != "background" {
		t.Fatalf("delete scope mismatch: media=%q role=%q", store.lastDeleteMedia, store.lastDeleteRole)
	}
}

func TestThumbnailProjects_DeleteAssetMissingIs404(t *testing.T) {
	store := &thumbnailProjectTestStore{deleteAssetErr: repository.ErrThumbnailProjectAssetNotFound}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/thumbnail-projects/thumbproj_test/assets/00000000-0000-4000-8000-000000000001?workspace_id=7&role=background", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func readyAssignmentExport() *models.ThumbnailExport {
	return &models.ThumbnailExport{
		ID: "thumbexp_1", ProjectID: "thumbproj_test", RevisionID: "thumbrev_1",
		MediaID: "00000000-0000-4000-8000-000000000001", ContentType: "image/png",
		Width: 1920, Height: 1080, FileSize: 10, SHA256: make([]byte, 32),
		RendererVersion: "go-canvas-v1", Status: models.ThumbnailProjectExportStatusReady,
	}
}

func TestThumbnailProjects_AddAssignmentRequiresWorkspaceQuery(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments", bytes.NewBufferString(`{"targets":[{"platform_account_id":381,"youtube_video_id":"abc123"}]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing workspace_id, got %d", w.Code)
	}
}

func TestThumbnailProjects_AddAssignmentLinksReadyExport(t *testing.T) {
	store := &thumbnailProjectTestStore{export: readyAssignmentExport()}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	body := `{"targets":[{"platform_account_id":381,"youtube_video_id":"abc123"},{"platform_account_id":382,"youtube_video_id":"def456","target_language":"it"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7", bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var response thumbnailAssignmentListResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 || len(store.createdAssignments) != 2 {
		t.Fatalf("want 2 assignments, got %d (store %d)", len(response.Items), len(store.createdAssignments))
	}
	first := store.createdAssignments[0]
	if first.WorkspaceID != 7 || first.ProjectID != "thumbproj_test" || first.ExportID != "thumbexp_1" || first.PlatformAccountID != 381 || first.YouTubeVideoID != "abc123" || first.Platform != "youtube" {
		t.Fatalf("unexpected assignment: %+v", first)
	}
	if store.createdAssignments[1].TargetLanguage == nil || *store.createdAssignments[1].TargetLanguage != "it" {
		t.Fatalf("target_language not propagated: %+v", store.createdAssignments[1])
	}
}

func TestThumbnailProjects_AddAssignmentEmptyTargetsIs400(t *testing.T) {
	store := &thumbnailProjectTestStore{export: readyAssignmentExport()}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7", bytes.NewBufferString(`{"targets":[]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for empty targets, got %d", w.Code)
	}
}

func TestThumbnailProjects_AddAssignmentNonReadyExportIs422(t *testing.T) {
	export := readyAssignmentExport()
	export.Status = models.ThumbnailProjectExportStatusRendering
	store := &thumbnailProjectTestStore{export: export}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7", bytes.NewBufferString(`{"targets":[{"platform_account_id":381,"youtube_video_id":"abc123"}]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for non-ready export, got %d: %s", w.Code, w.Body.String())
	}
	if len(store.createdAssignments) != 0 {
		t.Fatalf("no assignment should be created for non-ready export")
	}
}

func TestThumbnailProjects_AddAssignmentCrossWorkspaceIs404(t *testing.T) {
	r := thumbnailProjectRouter(t, &thumbnailProjectTestStore{export: readyAssignmentExport()}, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 99}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7", bytes.NewBufferString(`{"targets":[{"platform_account_id":381,"youtube_video_id":"abc123"}]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for cross-workspace assignment, got %d", w.Code)
	}
}

func TestThumbnailProjects_AddAssignmentDuplicateIs409(t *testing.T) {
	store := &thumbnailProjectTestStore{export: readyAssignmentExport(), assignmentErr: repository.ErrThumbnailAssignmentConflict}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7", bytes.NewBufferString(`{"targets":[{"platform_account_id":381,"youtube_video_id":"abc123"}]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["code"] != "ASSIGNMENT_ALREADY_EXISTS" {
		t.Fatalf("unexpected conflict body: %s", w.Body.String())
	}
}

func TestThumbnailProjects_AddAssignmentExportNotFoundIs404(t *testing.T) {
	store := &thumbnailProjectTestStore{findExportErr: repository.ErrThumbnailExportNotFound}
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) { return &models.Workspace{ID: id, OwnerID: 1}, nil }})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-exports/thumbexp_1/assignments?workspace_id=7", bytes.NewBufferString(`{"targets":[{"platform_account_id":381,"youtube_video_id":"abc123"}]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
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
