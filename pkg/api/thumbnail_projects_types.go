package api

import (
	"context"
	"encoding/json"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ThumbnailProjectStore is deliberately independent from YouTube stores.
// Production wires *repository.ThumbnailProjectRepository; tests can provide
// a fake without any OAuth/provider dependency.
type ThumbnailProjectStore interface {
	Create(ctx context.Context, project *models.ThumbnailProject) error
	FindByID(ctx context.Context, workspaceID int64, id string) (*models.ThumbnailProject, error)
	ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.ThumbnailProject, error)
	UpdateCAS(ctx context.Context, project *models.ThumbnailProject, expectedVersion int64) error
	UpdateStatusCAS(ctx context.Context, workspaceID int64, id string, status models.ThumbnailProjectStatus, expectedVersion int64) error
	SaveSnapshot(ctx context.Context, workspaceID int64, projectID string, snapshot models.ThumbnailProjectSnapshot, createdBy int64) (*models.ThumbnailProjectSnapshotResult, error)
	ListRevisions(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailProjectRevision, error)
	FindRevision(ctx context.Context, workspaceID int64, projectID, revisionID string) (*models.ThumbnailProjectRevision, error)
	RestoreRevision(ctx context.Context, workspaceID int64, projectID, revisionID string, baseVersion, createdBy int64, rendererVersion string) (*models.ThumbnailProjectSnapshotResult, error)
}

type createThumbnailProjectRequest struct {
	WorkspaceID  int64  `json:"workspace_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	CanvasWidth  int    `json:"canvas_width"`
	CanvasHeight int    `json:"canvas_height"`
}

type updateThumbnailProjectRequest struct {
	Name         *string `json:"name,omitempty"`
	Description  *string `json:"description,omitempty"`
	CanvasWidth  *int    `json:"canvas_width,omitempty"`
	CanvasHeight *int    `json:"canvas_height,omitempty"`
	Status       *string `json:"status,omitempty"`
	Version      int64   `json:"version"`
}

type thumbnailProjectSnapshotRequest struct {
	SchemaVersion   int             `json:"schema_version"`
	Snapshot        json.RawMessage `json:"snapshot"`
	RendererVersion string          `json:"renderer_version"`
	BaseVersion     int64           `json:"base_version"`
}

type thumbnailProjectRestoreRequest struct {
	BaseVersion     int64  `json:"base_version"`
	RendererVersion string `json:"renderer_version,omitempty"`
}

type thumbnailProjectListResponse struct {
	Items []models.ThumbnailProject `json:"items"`
}

type thumbnailProjectRevisionListResponse struct {
	Items []models.ThumbnailProjectRevision `json:"items"`
}

type thumbnailProjectRevisionDetailResponse struct {
	Revision models.ThumbnailProjectRevision `json:"revision"`
}
