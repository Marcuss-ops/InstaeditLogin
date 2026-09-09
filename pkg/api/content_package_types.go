package api

import (
	"encoding/json"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type contentPackageCreateRequest struct {
	WorkspaceID                   int64                       `json:"workspace_id"`
	SourceType                    string                      `json:"source_type"`
	DriveAccountID                *int64                      `json:"drive_account_id,omitempty"`
	DriveFileID                   string                      `json:"drive_file_id"`
	SourceFilename                string                      `json:"source_filename"`
	SourceFingerprint             string                      `json:"source_fingerprint"`
	VeloxProjectID                *string                     `json:"velox_project_id,omitempty"`
	SourceLanguage                string                      `json:"source_language"`
	CurrentCoverMediaID           *string                     `json:"current_cover_media_id,omitempty"`
	CurrentCoverTemplateVersionID *int64                      `json:"current_cover_template_version_id,omitempty"`
	Title                         string                      `json:"title"`
	Description                   string                      `json:"description"`
	Tags                          json.RawMessage             `json:"tags"`
	Targets                       []contentPackageTargetInput `json:"targets"`
}

type contentPackageTargetInput struct {
	PlatformAccountID      int64   `json:"platform_account_id"`
	Language               string  `json:"language"`
	PrivacyStatus          string  `json:"privacy_status"`
	PlaylistID             *string `json:"playlist_id,omitempty"`
	Enabled                *bool   `json:"enabled,omitempty"`
	CoverMediaID           *string `json:"cover_media_id,omitempty"`
	CoverTemplateVersionID *int64  `json:"cover_template_version_id,omitempty"`
}

type contentPackagePatchRequest struct {
	ExpectedPackageVersion        int64   `json:"expected_package_version"`
	SourceFilename                *string `json:"source_filename,omitempty"`
	SourceFingerprint             *string `json:"source_fingerprint,omitempty"`
	SourceLanguage                *string `json:"source_language,omitempty"`
	CurrentCoverMediaID           *string `json:"current_cover_media_id,omitempty"`
	CurrentCoverTemplateVersionID *int64  `json:"current_cover_template_version_id,omitempty"`
	State                         *string `json:"state,omitempty"`
}

type contentMetadataRequest struct {
	ExpectedPackageVersion int64           `json:"expected_package_version"`
	SourceLanguage         string          `json:"source_language"`
	Title                  string          `json:"title"`
	Description            string          `json:"description"`
	Tags                   json.RawMessage `json:"tags"`
}

type contentTranslationRequest struct {
	ExpectedPackageVersion int64                     `json:"expected_package_version"`
	Generate               bool                      `json:"generate,omitempty"`
	Entries                []contentTranslationInput `json:"entries"`
}

type contentTranslationInput struct {
	Language    string          `json:"language"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Tags        json.RawMessage `json:"tags"`
}

type contentScheduleRequest struct {
	ExpectedPackageVersion int64  `json:"expected_package_version"`
	ScheduledAt            string `json:"scheduled_at"`
	Timezone               string `json:"timezone"`
}

type contentPackageResponse struct {
	Package      *models.ContentPackage                    `json:"package"`
	Targets      []*models.ContentPackageTarget            `json:"targets"`
	Metadata     *models.ContentMetadataRevision           `json:"metadata,omitempty"`
	Schedule     *models.ContentSchedule                   `json:"schedule,omitempty"`
	Publications []*models.ContentPackagePublicationStatus `json:"publications,omitempty"`
}

type contentPreviewTarget struct {
	PlatformAccountID      int64                         `json:"platform_account_id"`
	ChannelName            string                        `json:"channel_name,omitempty"`
	Language               string                        `json:"language"`
	Title                  string                        `json:"title"`
	Description            string                        `json:"description"`
	Tags                   json.RawMessage               `json:"tags"`
	ThumbnailMediaID       *string                       `json:"thumbnail_media_id,omitempty"`
	CoverTemplateVersionID *int64                        `json:"cover_template_version_id,omitempty"`
	PrivacyStatus          string                        `json:"privacy_status"`
	ScheduledAt            *time.Time                    `json:"scheduled_at,omitempty"`
	Ready                  bool                          `json:"ready"`
	Blockers               []services.PublicationBlocker `json:"blockers"`
	Warnings               []string                      `json:"warnings,omitempty"`
}

type contentPreviewResponse struct {
	PackageID      int64                         `json:"package_id"`
	PackageVersion int64                         `json:"package_version"`
	Ready          bool                          `json:"ready"`
	Blockers       []services.PublicationBlocker `json:"blockers"`
	Targets        []contentPreviewTarget        `json:"targets"`
	Schedule       *models.ContentSchedule       `json:"schedule,omitempty"`
}
