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
	// CreateExport persists a rendered export bound to a revision of the
	// same project and a ready media asset owned by the workspace.
	CreateExport(ctx context.Context, workspaceID int64, export *models.ThumbnailExport) error
	// FindExport returns a workspace-scoped export by id.
	FindExport(ctx context.Context, workspaceID int64, exportID string) (*models.ThumbnailExport, error)
	// UpdateExportStatus transitions a 'rendering' export to 'ready' or
	// 'failed' and, on ready, advances the project's latest_export_id and
	// preview_media_id pointers in the same transaction.
	UpdateExportStatus(ctx context.Context, workspaceID int64, exportID, status, lastError string, sha256 []byte, fileSize int64, rendererVersion string) error
	// CreateAsset links a ready media asset owned by (or shared with) the
	// workspace to a project with a typed role (background/foreground/logo/
	// overlay/reference/font). Duplicate (project, media_id, role) links
	// surface ErrThumbnailDomainConflict.
	CreateAsset(ctx context.Context, workspaceID int64, asset *models.ThumbnailProjectAsset) error
	// ListAssets returns the workspace-scoped project's media links ordered
	// by creation.
	ListAssets(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailProjectAsset, error)
	// DeleteAsset removes one (project, media_id, role) link; a missing row
	// surfaces ErrThumbnailProjectAssetNotFound.
	DeleteAsset(ctx context.Context, workspaceID int64, projectID, mediaID, role string) error
	// CreateAssignment links a ready export of the workspace to a YouTube
	// video whose account is a workspace channel. Duplicate
	// (export, platform_account, video) tuples surface
	// ErrThumbnailAssignmentConflict.
	CreateAssignment(ctx context.Context, assignment *models.ThumbnailAssignment) error
	// ListAssignments returns the workspace-scoped project's destination
	// assignments, newest-first. Powers the library's "Collegate" filter:
	// a project with zero rows is unlinked, one or more means it is
	// assigned to at least one video.
	ListAssignments(ctx context.Context, workspaceID int64, projectID string) ([]models.ThumbnailAssignment, error)
	CreateVeloxProjectBridge(ctx context.Context, bridge *models.VeloxProjectBridge) error
	FindVeloxProjectBridge(ctx context.Context, workspaceID int64, projectID string) (*models.VeloxProjectBridge, error)
	DeleteVeloxProjectBridge(ctx context.Context, workspaceID int64, projectID string) error
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

type createThumbnailProjectAssetRequest struct {
	MediaID  string  `json:"media_id"`
	Role     string  `json:"role"`
	ObjectID *string `json:"object_id,omitempty"`
}

type thumbnailProjectAssetListResponse struct {
	Items []models.ThumbnailProjectAsset `json:"items"`
}

type createVeloxProjectBridgeRequest struct {
	// Required bridge discriminator. It must match the sole supported
	// contract; no request field may carry a group, channel list,
	// membership snapshot, or workspace copy.
	ContractVersion   string  `json:"contract_version"`
	WorkspaceID       int64   `json:"workspace_id"`
	Platform          string  `json:"platform,omitempty"`
	PlatformAccountID *int64  `json:"platform_account_id,omitempty"`
	ChannelID         *string `json:"channel_id,omitempty"`
	VideoID           *string `json:"video_id,omitempty"`
	Language          *string `json:"language,omitempty"`
	VeloxProjectID    string  `json:"velox_project_id"`
}

type veloxProjectBridgeResponse struct {
	ContractVersion string                    `json:"contract_version"`
	Bridge          models.VeloxProjectBridge `json:"bridge"`
	EditorURL       string                    `json:"editor_url"`
}
