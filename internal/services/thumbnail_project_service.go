package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ThumbnailProjectStore is the application-facing persistence contract for
// the autonomous thumbnail domain. It intentionally contains no YouTube
// editor-session methods or provider credentials.
type ThumbnailProjectStore interface {
	Create(context.Context, *models.ThumbnailProject) error
	FindByID(context.Context, int64, string) (*models.ThumbnailProject, error)
	ListByWorkspace(context.Context, int64) ([]models.ThumbnailProject, error)
	UpdateCAS(context.Context, *models.ThumbnailProject, int64) error
	UpdateStatusCAS(context.Context, int64, string, models.ThumbnailProjectStatus, int64) error
	SaveSnapshot(context.Context, int64, string, models.ThumbnailProjectSnapshot, int64) (*models.ThumbnailProjectSnapshotResult, error)
	ListRevisions(context.Context, int64, string) ([]models.ThumbnailProjectRevision, error)
	FindRevision(context.Context, int64, string, string) (*models.ThumbnailProjectRevision, error)
	RestoreRevision(context.Context, int64, string, string, int64, int64, string) (*models.ThumbnailProjectSnapshotResult, error)
	CreateAsset(context.Context, int64, *models.ThumbnailProjectAsset) error
	ListAssets(context.Context, int64, string) ([]models.ThumbnailProjectAsset, error)
	DeleteAsset(context.Context, int64, string, string, string) error
	CreateExport(context.Context, int64, *models.ThumbnailExport) error
	FindExport(context.Context, int64, string) (*models.ThumbnailExport, error)
	ListExports(context.Context, int64, string) ([]models.ThumbnailExport, error)
	CreateAssignment(context.Context, *models.ThumbnailAssignment) error
	ListAssignments(context.Context, int64, string) ([]models.ThumbnailAssignment, error)
	UpdateAssignmentStatus(context.Context, int64, string, string) error
	UpdateExportStatus(context.Context, int64, string, string, string, []byte, int64, string) error
	CreateVeloxProjectBridge(context.Context, *models.VeloxProjectBridge) error
	FindVeloxProjectBridge(context.Context, int64, string) (*models.VeloxProjectBridge, error)
	DeleteVeloxProjectBridge(context.Context, int64, string) error
	// EnsureThumbnailProjectForEditorSession idempotently mints the
	// application project row (id = editor session id) that the
	// velox_project_bridges foreign key requires when the "Modifica" flow
	// uses the session row as its temporary application project.
	EnsureThumbnailProjectForEditorSession(context.Context, int64, string, int64) error
}

var _ ThumbnailProjectStore = (*repository.ThumbnailProjectRepository)(nil)

// ThumbnailProjectService owns domain-level validation and delegates durable
// writes to the workspace-scoped repository. It is safe to use from HTTP,
// workers, and future render jobs without importing YouTube editor state.
type ThumbnailProjectService struct {
	store ThumbnailProjectStore
}

func NewThumbnailProjectService(store ThumbnailProjectStore) *ThumbnailProjectService {
	return &ThumbnailProjectService{store: store}
}

func (s *ThumbnailProjectService) validateWorkspace(workspaceID int64) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("thumbnail project service is not configured")
	}
	if workspaceID <= 0 {
		return fmt.Errorf("%w: workspace_id must be positive", repository.ErrThumbnailProjectInvalid)
	}
	return nil
}

func (s *ThumbnailProjectService) Create(ctx context.Context, project *models.ThumbnailProject) error {
	if project == nil {
		return fmt.Errorf("%w: project is required", repository.ErrThumbnailProjectInvalid)
	}
	if err := s.validateWorkspace(project.WorkspaceID); err != nil {
		return err
	}
	return s.store.Create(ctx, project)
}

func (s *ThumbnailProjectService) FindByID(ctx context.Context, workspaceID int64, id string) (*models.ThumbnailProject, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.FindByID(ctx, workspaceID, strings.TrimSpace(id))
}

func (s *ThumbnailProjectService) ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.ThumbnailProject, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.ListByWorkspace(ctx, workspaceID)
}

func (s *ThumbnailProjectService) UpdateCAS(ctx context.Context, project *models.ThumbnailProject, expectedVersion int64) error {
	if project == nil {
		return fmt.Errorf("%w: project is required", repository.ErrThumbnailProjectInvalid)
	}
	if err := s.validateWorkspace(project.WorkspaceID); err != nil {
		return err
	}
	return s.store.UpdateCAS(ctx, project, expectedVersion)
}

func (s *ThumbnailProjectService) UpdateStatusCAS(ctx context.Context, workspaceID int64, id string, status models.ThumbnailProjectStatus, expectedVersion int64) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	return s.store.UpdateStatusCAS(ctx, workspaceID, strings.TrimSpace(id), status, expectedVersion)
}

func (s *ThumbnailProjectService) SaveSnapshot(ctx context.Context, workspaceID int64, projectID string, snapshot models.ThumbnailProjectSnapshot, createdBy int64) (*models.ThumbnailProjectSnapshotResult, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.SaveSnapshot(ctx, workspaceID, strings.TrimSpace(projectID), snapshot, createdBy)
}

func (s *ThumbnailProjectService) ListRevisions(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailProjectRevision, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.ListRevisions(ctx, workspaceID, strings.TrimSpace(projectID))
}

func (s *ThumbnailProjectService) FindRevision(ctx context.Context, workspaceID int64, projectID, revisionID string) (*models.ThumbnailProjectRevision, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.FindRevision(ctx, workspaceID, strings.TrimSpace(projectID), strings.TrimSpace(revisionID))
}

func (s *ThumbnailProjectService) RestoreRevision(ctx context.Context, workspaceID int64, projectID, revisionID string, baseVersion, createdBy int64, rendererVersion string) (*models.ThumbnailProjectSnapshotResult, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.RestoreRevision(ctx, workspaceID, strings.TrimSpace(projectID), strings.TrimSpace(revisionID), baseVersion, createdBy, rendererVersion)
}

func (s *ThumbnailProjectService) CreateAsset(ctx context.Context, workspaceID int64, asset *models.ThumbnailProjectAsset) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	if asset == nil {
		return fmt.Errorf("%w: asset is required", repository.ErrThumbnailProjectInvalid)
	}
	return s.store.CreateAsset(ctx, workspaceID, asset)
}

func (s *ThumbnailProjectService) CreateExport(ctx context.Context, workspaceID int64, export *models.ThumbnailExport) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	if export == nil {
		return fmt.Errorf("%w: export is required", repository.ErrThumbnailProjectInvalid)
	}
	return s.store.CreateExport(ctx, workspaceID, export)
}

func (s *ThumbnailProjectService) UpdateExportStatus(ctx context.Context, workspaceID int64, exportID, status, lastError string, sha256 []byte, fileSize int64, rendererVersion string) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	return s.store.UpdateExportStatus(ctx, workspaceID, strings.TrimSpace(exportID), status, lastError, sha256, fileSize, rendererVersion)
}

func (s *ThumbnailProjectService) CreateAssignment(ctx context.Context, assignment *models.ThumbnailAssignment) error {
	if assignment == nil {
		return fmt.Errorf("%w: assignment is required", repository.ErrThumbnailProjectInvalid)
	}
	if assignment.WorkspaceID <= 0 || assignment.PlatformAccountID <= 0 {
		return fmt.Errorf("%w: workspace_id and platform_account_id must be positive", repository.ErrThumbnailProjectInvalid)
	}
	if strings.TrimSpace(assignment.YouTubeVideoID) == "" {
		return fmt.Errorf("%w: youtube_video_id is required", repository.ErrThumbnailProjectInvalid)
	}
	if err := assignment.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", repository.ErrThumbnailProjectInvalid, err)
	}
	return s.store.CreateAssignment(ctx, assignment)
}

func (s *ThumbnailProjectService) ListAssets(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailProjectAsset, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.ListAssets(ctx, workspaceID, strings.TrimSpace(projectID))
}

func (s *ThumbnailProjectService) FindExport(ctx context.Context, workspaceID int64, exportID string) (*models.ThumbnailExport, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.FindExport(ctx, workspaceID, strings.TrimSpace(exportID))
}

func (s *ThumbnailProjectService) ListExports(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailExport, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.ListExports(ctx, workspaceID, strings.TrimSpace(projectID))
}

func (s *ThumbnailProjectService) ListAssignments(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailAssignment, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.ListAssignments(ctx, workspaceID, strings.TrimSpace(projectID))
}

func (s *ThumbnailProjectService) DeleteAsset(ctx context.Context, workspaceID int64, projectID, mediaID, role string) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	return s.store.DeleteAsset(ctx, workspaceID, projectID, mediaID, role)
}

func (s *ThumbnailProjectService) CreateVeloxProjectBridge(ctx context.Context, bridge *models.VeloxProjectBridge) error {
	if bridge == nil {
		return fmt.Errorf("%w: bridge is required", repository.ErrVeloxProjectBridgeInvalid)
	}
	if err := s.validateWorkspace(bridge.WorkspaceID); err != nil {
		return err
	}
	if err := bridge.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", repository.ErrVeloxProjectBridgeInvalid, err)
	}
	return s.store.CreateVeloxProjectBridge(ctx, bridge)
}

func (s *ThumbnailProjectService) FindVeloxProjectBridge(ctx context.Context, workspaceID int64, projectID string) (*models.VeloxProjectBridge, error) {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return nil, err
	}
	return s.store.FindVeloxProjectBridge(ctx, workspaceID, strings.TrimSpace(projectID))
}

func (s *ThumbnailProjectService) DeleteVeloxProjectBridge(ctx context.Context, workspaceID int64, projectID string) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	return s.store.DeleteVeloxProjectBridge(ctx, workspaceID, strings.TrimSpace(projectID))
}

func (s *ThumbnailProjectService) EnsureThumbnailProjectForEditorSession(ctx context.Context, workspaceID int64, projectID string, createdBy int64) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(projectID) == "" || createdBy <= 0 {
		return fmt.Errorf("%w: project id and creator are required", repository.ErrThumbnailProjectInvalid)
	}
	return s.store.EnsureThumbnailProjectForEditorSession(ctx, workspaceID, strings.TrimSpace(projectID), createdBy)
}

func (s *ThumbnailProjectService) UpdateAssignmentStatus(ctx context.Context, workspaceID int64, assignmentID, status string) error {
	if err := s.validateWorkspace(workspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(assignmentID) == "" {
		return fmt.Errorf("%w: assignment_id is required", repository.ErrThumbnailProjectInvalid)
	}
	return s.store.UpdateAssignmentStatus(ctx, workspaceID, assignmentID, status)
}
