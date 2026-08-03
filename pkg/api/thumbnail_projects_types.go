package api

import (
	"context"

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

type thumbnailProjectListResponse struct {
	Items []models.ThumbnailProject `json:"items"`
}
