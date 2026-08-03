package services

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type thumbnailProjectServiceStore struct {
	createCalled      bool
	findWorkspaceID   int64
	findProjectID     string
	updateCalled      bool
	statusWorkspaceID int64
	statusProjectID   string
	status            models.ThumbnailProjectStatus
	statusVersion     int64
	createErr         error
	findResult        *models.ThumbnailProject
	findErr           error
	updateErr         error
	statusErr         error
}

func (s *thumbnailProjectServiceStore) Create(_ context.Context, project *models.ThumbnailProject) error {
	s.createCalled = true
	return s.createErr
}
func (s *thumbnailProjectServiceStore) FindByID(_ context.Context, workspaceID int64, projectID string) (*models.ThumbnailProject, error) {
	s.findWorkspaceID, s.findProjectID = workspaceID, projectID
	return s.findResult, s.findErr
}
func (s *thumbnailProjectServiceStore) ListByWorkspace(context.Context, int64) ([]models.ThumbnailProject, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) UpdateCAS(_ context.Context, _ *models.ThumbnailProject, _ int64) error {
	s.updateCalled = true
	return s.updateErr
}
func (s *thumbnailProjectServiceStore) UpdateStatusCAS(_ context.Context, workspaceID int64, projectID string, status models.ThumbnailProjectStatus, version int64) error {
	s.statusWorkspaceID, s.statusProjectID, s.status, s.statusVersion = workspaceID, projectID, status, version
	return s.statusErr
}
func (s *thumbnailProjectServiceStore) SaveSnapshot(context.Context, int64, string, models.ThumbnailProjectSnapshot, int64) (*models.ThumbnailProjectSnapshotResult, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) ListRevisions(context.Context, int64, string) ([]models.ThumbnailProjectRevision, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) FindRevision(context.Context, int64, string, string) (*models.ThumbnailProjectRevision, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) RestoreRevision(context.Context, int64, string, string, int64, int64, string) (*models.ThumbnailProjectSnapshotResult, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) CreateAsset(context.Context, int64, *models.ThumbnailProjectAsset) error {
	return nil
}
func (s *thumbnailProjectServiceStore) ListAssets(context.Context, int64, string) ([]models.ThumbnailProjectAsset, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) DeleteAsset(context.Context, int64, string, string, string) error {
	return nil
}
func (s *thumbnailProjectServiceStore) CreateExport(context.Context, int64, *models.ThumbnailExport) error {
	return nil
}
func (s *thumbnailProjectServiceStore) FindExport(context.Context, int64, string) (*models.ThumbnailExport, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) ListExports(context.Context, int64, string) ([]models.ThumbnailExport, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) CreateAssignment(context.Context, *models.ThumbnailAssignment) error {
	return nil
}
func (s *thumbnailProjectServiceStore) ListAssignments(context.Context, int64, string) ([]models.ThumbnailAssignment, error) {
	return nil, nil
}
func (s *thumbnailProjectServiceStore) UpdateAssignmentStatus(context.Context, int64, string, string) error {
	return nil
}
func (s *thumbnailProjectServiceStore) UpdateExportStatus(context.Context, int64, string, string, string, []byte, int64, string) error {
	return nil
}

func TestThumbnailProjectServiceUpdateExportStatusRequiresWorkspace(t *testing.T) {
	service := NewThumbnailProjectService(&thumbnailProjectServiceStore{})
	if err := service.UpdateExportStatus(context.Background(), 0, "export", models.ThumbnailProjectExportStatusReady, "", make([]byte, 32), 10, "renderer-1"); !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid workspace error, got %v", err)
	}
}

func TestThumbnailProjectServiceCreateRequiresWorkspace(t *testing.T) {
	store := &thumbnailProjectServiceStore{}
	service := NewThumbnailProjectService(store)
	err := service.Create(context.Background(), &models.ThumbnailProject{Name: "Cover"})
	if !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid workspace error, got %v", err)
	}
	if store.createCalled {
		t.Fatal("Create reached store with invalid workspace")
	}
}

func TestThumbnailProjectServiceFindTrimsProjectIDAndPreservesWorkspace(t *testing.T) {
	store := &thumbnailProjectServiceStore{findResult: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7}}
	service := NewThumbnailProjectService(store)
	if _, err := service.FindByID(context.Background(), 7, " thumbproj_1 "); err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if store.findWorkspaceID != 7 || store.findProjectID != "thumbproj_1" {
		t.Fatalf("scope/trim mismatch: workspace=%d project=%q", store.findWorkspaceID, store.findProjectID)
	}
}

func TestThumbnailProjectServiceUpdateCASRequiresWorkspaceAndDelegates(t *testing.T) {
	store := &thumbnailProjectServiceStore{}
	service := NewThumbnailProjectService(store)
	project := &models.ThumbnailProject{WorkspaceID: 7, ID: "p", Name: "Cover"}
	if err := service.UpdateCAS(context.Background(), project, 3); err != nil {
		t.Fatalf("UpdateCAS: %v", err)
	}
	if !store.updateCalled {
		t.Fatal("UpdateCAS did not reach store")
	}
}

func TestThumbnailProjectServiceStatusDelegatesScopedVersion(t *testing.T) {
	store := &thumbnailProjectServiceStore{}
	service := NewThumbnailProjectService(store)
	if err := service.UpdateStatusCAS(context.Background(), 7, " p ", models.ThumbnailProjectStatusArchived, 4); err != nil {
		t.Fatalf("UpdateStatusCAS: %v", err)
	}
	if store.statusWorkspaceID != 7 || store.statusProjectID != "p" || store.status != models.ThumbnailProjectStatusArchived || store.statusVersion != 4 {
		t.Fatalf("status delegation mismatch: %+v", store)
	}
}

func TestThumbnailProjectServicePropagatesStoreConflict(t *testing.T) {
	store := &thumbnailProjectServiceStore{updateErr: repository.ErrThumbnailProjectConflict}
	service := NewThumbnailProjectService(store)
	err := service.UpdateCAS(context.Background(), &models.ThumbnailProject{WorkspaceID: 7, ID: "p", Name: "Cover"}, 2)
	if !errors.Is(err, repository.ErrThumbnailProjectConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}
