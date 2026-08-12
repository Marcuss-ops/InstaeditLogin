package models

import (
	"encoding/json"
	"time"
)

// ThumbnailProjectStatus is the lifecycle of an autonomous graphic project.
// It intentionally has no YouTube/provider state; destinations are modeled by
// thumbnail_assignments after an export exists.
type ThumbnailProjectStatus string

const (
	ThumbnailProjectStatusDraft    ThumbnailProjectStatus = "draft"
	ThumbnailProjectStatusReady    ThumbnailProjectStatus = "ready"
	ThumbnailProjectStatusArchived ThumbnailProjectStatus = "archived"
	ThumbnailProjectStatusDeleted  ThumbnailProjectStatus = "deleted"
)

func (s ThumbnailProjectStatus) IsValid() bool {
	switch s {
	case ThumbnailProjectStatusDraft, ThumbnailProjectStatusReady,
		ThumbnailProjectStatusArchived, ThumbnailProjectStatusDeleted:
		return true
	default:
		return false
	}
}

// ThumbnailProject is the workspace-scoped editable project aggregate.
// Canvas snapshots and rendered files live in child tables; no channel,
// video, OAuth connection, or platform account is required to create it.
type ThumbnailProject struct {
	ID                string                 `json:"id"`
	WorkspaceID       int64                  `json:"workspace_id"`
	CreatedBy         int64                  `json:"created_by"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	CanvasWidth       int                    `json:"canvas_width"`
	CanvasHeight      int                    `json:"canvas_height"`
	Status            ThumbnailProjectStatus `json:"status"`
	CurrentRevisionID *string                `json:"current_revision_id,omitempty"`
	PreviewMediaID    *string                `json:"preview_media_id,omitempty"`
	LatestExportID    *string                `json:"latest_export_id,omitempty"`
	Version           int64                  `json:"version"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// ThumbnailProjectRevision is an immutable canvas snapshot.
type ThumbnailProjectRevision struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	RevisionNumber  int64           `json:"revision_number"`
	SchemaVersion   int             `json:"schema_version"`
	SnapshotJSON    json.RawMessage `json:"snapshot_json"`
	SnapshotSHA256  []byte          `json:"snapshot_sha256"`
	RendererVersion string          `json:"renderer_version"`
	CreatedBy       int64           `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
}

// ThumbnailProjectSnapshot is the immutable payload accepted by the snapshot
// endpoint. SnapshotJSON must be a JSON object; the repository canonicalizes
// it before hashing and persisting it.
type ThumbnailProjectSnapshot struct {
	SchemaVersion   int             `json:"schema_version"`
	SnapshotJSON    json.RawMessage `json:"snapshot"`
	RendererVersion string          `json:"renderer_version"`
	BaseVersion     int64           `json:"base_version"`
}

// ThumbnailProjectSnapshotResult is returned after a save or restore.
type ThumbnailProjectSnapshotResult struct {
	ProjectID      string                    `json:"project_id"`
	RevisionID     string                    `json:"revision_id"`
	RevisionNumber int64                     `json:"revision_number"`
	Version        int64                     `json:"version"`
	SavedAt        time.Time                 `json:"saved_at"`
	SnapshotSHA256 string                    `json:"snapshot_sha256"`
	Revision       *ThumbnailProjectRevision `json:"revision,omitempty"`
	Deduplicated   bool                      `json:"deduplicated,omitempty"`
}

// ThumbnailProjectAsset links a project object to a persistent media asset.
type ThumbnailProjectAsset struct {
	ProjectID string    `json:"project_id"`
	MediaID   string    `json:"media_id"`
	Role      string    `json:"role"`
	ObjectID  *string   `json:"object_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ThumbnailExport describes a rendered file persisted in media storage.
type ThumbnailExport struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	RevisionID      string `json:"revision_id"`
	MediaID         string `json:"media_id"`
	ContentType     string `json:"content_type"`
	Width           int    `json:"width"`
	Height          int    `json:"height"`
	FileSize        int64  `json:"file_size"`
	SHA256          []byte `json:"sha256"`
	RendererVersion string `json:"renderer_version"`
	// RenderProfile is the canonical output profile (format, dimensions,
	// renderer lineage). Non-empty profiles are unique per project/revision.
	RenderProfile string    `json:"render_profile,omitempty"`
	Status        string    `json:"status"`
	LastError     string    `json:"last_error"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ThumbnailAssignment is an optional YouTube destination for an export.
type ThumbnailAssignment struct {
	ID                string    `json:"id"`
	WorkspaceID       int64     `json:"workspace_id"`
	ProjectID         string    `json:"project_id"`
	ExportID          string    `json:"export_id"`
	PlatformAccountID int64     `json:"platform_account_id"`
	Platform          string    `json:"platform"`
	YouTubeVideoID    string    `json:"youtube_video_id"`
	TargetLanguage    *string   `json:"target_language,omitempty"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
