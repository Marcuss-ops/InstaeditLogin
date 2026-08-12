package models

import (
	"encoding/json"
	"time"
)

// CoverLibraryItem is the read model for a ready thumbnail export. The
// rendered export remains the canonical cover asset; this projection only
// adds the workspace/project metadata needed by the library UI.
type CoverLibraryItem struct {
	ExportID        string    `json:"export_id"`
	WorkspaceID     int64     `json:"workspace_id"`
	ProjectID       string    `json:"project_id"`
	ProjectName     string    `json:"project_name"`
	RevisionID      string    `json:"revision_id"`
	MediaID         string    `json:"media_id"`
	ContentType     string    `json:"content_type"`
	Width           int       `json:"width"`
	Height          int       `json:"height"`
	FileSize        int64     `json:"file_size"`
	SHA256          string    `json:"sha256"`
	RendererVersion string    `json:"renderer_version"`
	RenderProfile   string    `json:"render_profile,omitempty"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CoverTemplate is a workspace-owned template identity. Its editable
// definition is represented by immutable CoverTemplateVersion rows.
type CoverTemplate struct {
	ID                   int64     `json:"id"`
	WorkspaceID          int64     `json:"workspace_id"`
	CreatedBy            int64     `json:"created_by"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	Category             string    `json:"category,omitempty"`
	Language             string    `json:"language,omitempty"`
	Status               string    `json:"status"`
	CurrentVersionNumber int64     `json:"current_version_number"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// CoverTemplateVersion is immutable. InstaEdit stores only the editor
// project handle and the rendered preview; the editable canvas remains owned
// by InstaEditor.
type CoverTemplateVersion struct {
	ID              int64           `json:"id"`
	TemplateID      int64           `json:"template_id"`
	VersionNumber   int64           `json:"version_number"`
	EditorProjectID string          `json:"editor_project_id"`
	PreviewMediaID  *string         `json:"preview_media_id,omitempty"`
	Slots           json.RawMessage `json:"slots"`
	CreatedBy       int64           `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
}

const (
	CoverLibraryStatusReady     = "ready"
	CoverLibraryStatusArchived  = "archived"
	CoverTemplateStatusActive   = "active"
	CoverTemplateStatusArchived = "archived"
)
