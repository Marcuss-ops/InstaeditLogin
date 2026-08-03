package models

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	ThumbnailProjectAssetRoleBackground = "background"
	ThumbnailProjectAssetRoleForeground = "foreground"
	ThumbnailProjectAssetRoleLogo       = "logo"
	ThumbnailProjectAssetRoleOverlay    = "overlay"
	ThumbnailProjectAssetRoleReference  = "reference"
	ThumbnailProjectAssetRoleFont       = "font"

	ThumbnailProjectExportContentTypePNG  = "image/png"
	ThumbnailProjectExportContentTypeJPEG = "image/jpeg"

	ThumbnailProjectExportStatusRendering = "rendering"
	ThumbnailProjectExportStatusReady     = "ready"
	ThumbnailProjectExportStatusFailed    = "failed"

	ThumbnailProjectAssignmentStatusDraft     = "draft"
	ThumbnailProjectAssignmentStatusPending   = "pending"
	ThumbnailProjectAssignmentStatusApplied   = "applied"
	ThumbnailProjectAssignmentStatusFailed    = "failed"
	ThumbnailProjectAssignmentStatusCancelled = "cancelled"
)

func validThumbnailProjectAssetRole(role string) bool {
	switch role {
	case ThumbnailProjectAssetRoleBackground, ThumbnailProjectAssetRoleForeground,
		ThumbnailProjectAssetRoleLogo, ThumbnailProjectAssetRoleOverlay,
		ThumbnailProjectAssetRoleReference, ThumbnailProjectAssetRoleFont:
		return true
	default:
		return false
	}
}

// NormalizeAndValidate prepares a project asset for persistence.
func (a *ThumbnailProjectAsset) NormalizeAndValidate() error {
	if a == nil {
		return fmt.Errorf("thumbnail project asset is required")
	}
	a.ProjectID = strings.TrimSpace(a.ProjectID)
	a.MediaID = strings.TrimSpace(a.MediaID)
	a.Role = strings.ToLower(strings.TrimSpace(a.Role))
	if a.ProjectID == "" || a.MediaID == "" {
		return fmt.Errorf("project_id and media_id are required")
	}
	if _, err := uuid.Parse(a.MediaID); err != nil {
		return fmt.Errorf("media_id must be a UUID")
	}
	if !validThumbnailProjectAssetRole(a.Role) {
		return fmt.Errorf("unsupported thumbnail project asset role %q", a.Role)
	}
	if a.ObjectID != nil {
		value := strings.TrimSpace(*a.ObjectID)
		if value == "" {
			a.ObjectID = nil
		} else {
			a.ObjectID = &value
		}
	}
	return nil
}

// NormalizeAndValidate prepares a rendered export for persistence.
func (e *ThumbnailExport) NormalizeAndValidate() error {
	if e == nil {
		return fmt.Errorf("thumbnail export is required")
	}
	e.ID = strings.TrimSpace(e.ID)
	e.ProjectID = strings.TrimSpace(e.ProjectID)
	e.RevisionID = strings.TrimSpace(e.RevisionID)
	e.MediaID = strings.TrimSpace(e.MediaID)
	e.ContentType = strings.ToLower(strings.TrimSpace(e.ContentType))
	e.RendererVersion = strings.TrimSpace(e.RendererVersion)
	e.Status = strings.ToLower(strings.TrimSpace(e.Status))
	if e.ProjectID == "" || e.RevisionID == "" || e.MediaID == "" {
		return fmt.Errorf("project_id, revision_id, and media_id are required")
	}
	if _, err := uuid.Parse(e.MediaID); err != nil {
		return fmt.Errorf("media_id must be a UUID")
	}
	if e.ContentType != ThumbnailProjectExportContentTypePNG && e.ContentType != ThumbnailProjectExportContentTypeJPEG {
		return fmt.Errorf("content_type must be image/png or image/jpeg")
	}
	if e.Width <= 0 || e.Width > 16384 || e.Height <= 0 || e.Height > 16384 {
		return fmt.Errorf("export dimensions must be between 1 and 16384")
	}
	if e.FileSize < 0 {
		return fmt.Errorf("file_size cannot be negative")
	}
	if len(e.SHA256) != 32 {
		return fmt.Errorf("sha256 must contain exactly 32 bytes")
	}
	if e.RendererVersion == "" {
		return fmt.Errorf("renderer_version is required")
	}
	if e.Status == "" {
		e.Status = ThumbnailProjectExportStatusRendering
	}
	switch e.Status {
	case ThumbnailProjectExportStatusRendering, ThumbnailProjectExportStatusReady, ThumbnailProjectExportStatusFailed:
	default:
		return fmt.Errorf("unsupported thumbnail export status %q", e.Status)
	}
	return nil
}

func validThumbnailProjectAssignmentStatus(status string) bool {
	switch status {
	case ThumbnailProjectAssignmentStatusDraft, ThumbnailProjectAssignmentStatusPending,
		ThumbnailProjectAssignmentStatusApplied, ThumbnailProjectAssignmentStatusFailed,
		ThumbnailProjectAssignmentStatusCancelled:
		return true
	default:
		return false
	}
}

// ValidateStatus validates an assignment lifecycle value independently of
// the assignment aggregate, for status-only updates.
func ValidateThumbnailProjectAssignmentStatus(status string) error {
	status = strings.ToLower(strings.TrimSpace(status))
	if !validThumbnailProjectAssignmentStatus(status) {
		return fmt.Errorf("unsupported thumbnail assignment status %q", status)
	}
	return nil
}

// NormalizeAndValidate prepares an optional YouTube destination assignment.
func (a *ThumbnailAssignment) NormalizeAndValidate() error {
	if a == nil {
		return fmt.Errorf("thumbnail assignment is required")
	}
	a.ID = strings.TrimSpace(a.ID)
	a.ProjectID = strings.TrimSpace(a.ProjectID)
	a.ExportID = strings.TrimSpace(a.ExportID)
	a.Platform = strings.ToLower(strings.TrimSpace(a.Platform))
	a.YouTubeVideoID = strings.TrimSpace(a.YouTubeVideoID)
	a.Status = strings.ToLower(strings.TrimSpace(a.Status))
	if a.ProjectID == "" || a.ExportID == "" || a.YouTubeVideoID == "" {
		return fmt.Errorf("project_id, export_id, and youtube_video_id are required")
	}
	if a.Platform == "" {
		a.Platform = "youtube"
	}
	if a.Platform != "youtube" {
		return fmt.Errorf("thumbnail assignments currently support only youtube")
	}
	if a.Status == "" {
		a.Status = ThumbnailProjectAssignmentStatusDraft
	}
	if !validThumbnailProjectAssignmentStatus(a.Status) {
		return fmt.Errorf("unsupported thumbnail assignment status %q", a.Status)
	}
	if a.TargetLanguage != nil {
		language := strings.TrimSpace(*a.TargetLanguage)
		if language == "" {
			a.TargetLanguage = nil
		} else {
			a.TargetLanguage = &language
		}
	}
	return nil
}
