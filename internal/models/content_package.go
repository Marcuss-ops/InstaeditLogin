package models

import (
	"encoding/json"
	"time"
)

// ContentPackageState is the product-level lifecycle. It deliberately does
// not contain provider-specific upload states; those remain on PostTarget and
// YouTubeTargetPublication.
type ContentPackageState string

const (
	ContentPackageStateDraft              ContentPackageState = "draft"
	ContentPackageStateReady              ContentPackageState = "ready"
	ContentPackageStateScheduled          ContentPackageState = "scheduled"
	ContentPackageStatePreparing          ContentPackageState = "preparing"
	ContentPackageStateReadyToPublish     ContentPackageState = "ready_to_publish"
	ContentPackageStatePublishing         ContentPackageState = "publishing"
	ContentPackageStatePartiallyPublished ContentPackageState = "partially_published"
	ContentPackageStatePublished          ContentPackageState = "published"
	ContentPackageStateBlocked            ContentPackageState = "blocked"
)

func (s ContentPackageState) IsValid() bool {
	switch s {
	case ContentPackageStateDraft, ContentPackageStateReady,
		ContentPackageStateScheduled, ContentPackageStatePreparing,
		ContentPackageStateReadyToPublish, ContentPackageStatePublishing,
		ContentPackageStatePartiallyPublished, ContentPackageStatePublished,
		ContentPackageStateBlocked:
		return true
	default:
		return false
	}
}

type ContentPackage struct {
	ID                        int64               `json:"id"`
	WorkspaceID               int64               `json:"workspace_id"`
	CreatedBy                 int64               `json:"created_by"`
	SourceType                string              `json:"source_type"`
	DriveAccountID            *int64              `json:"drive_account_id,omitempty"`
	DriveFileID               string              `json:"drive_file_id"`
	SourceFilename            string              `json:"source_filename,omitempty"`
	SourceFingerprint         string              `json:"source_fingerprint,omitempty"`
	VeloxProjectID            *string             `json:"velox_project_id,omitempty"`
	SourceLanguage            string              `json:"source_language"`
	CurrentMetadataRevisionID *int64              `json:"current_metadata_revision_id,omitempty"`
	CurrentCoverMediaID       *string             `json:"current_cover_media_id,omitempty"`
	State                     ContentPackageState `json:"state"`
	Version                   int64               `json:"version"`
	CreatedAt                 time.Time           `json:"created_at"`
	UpdatedAt                 time.Time           `json:"updated_at"`
}

type ContentPackageTarget struct {
	ID                int64     `json:"id"`
	ContentPackageID  int64     `json:"content_package_id"`
	PlatformAccountID int64     `json:"platform_account_id"`
	Language          string    `json:"language"`
	PrivacyStatus     string    `json:"privacy_status"`
	PlaylistID        *string   `json:"playlist_id,omitempty"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type ContentMetadataRevision struct {
	ID               int64           `json:"id"`
	ContentPackageID int64           `json:"content_package_id"`
	RevisionNumber   int64           `json:"revision_number"`
	SourceLanguage   string          `json:"source_language"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Tags             json.RawMessage `json:"tags"`
	CreatedBy        int64           `json:"created_by"`
	CreatedAt        time.Time       `json:"created_at"`
}

type TranslationBundle struct {
	ID                       int64      `json:"id"`
	ContentPackageID         int64      `json:"content_package_id"`
	SourceMetadataRevisionID int64      `json:"source_metadata_revision_id"`
	Provider                 string     `json:"provider"`
	Status                   string     `json:"status"`
	RequestedLanguages       []string   `json:"requested_languages"`
	CreatedAt                time.Time  `json:"created_at"`
	CompletedAt              *time.Time `json:"completed_at,omitempty"`
}

type TranslationEntry struct {
	ID          int64           `json:"id"`
	BundleID    int64           `json:"bundle_id"`
	Language    string          `json:"language"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Tags        json.RawMessage `json:"tags"`
	Origin      string          `json:"origin"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type ContentSchedule struct {
	ID               int64      `json:"id"`
	ContentPackageID int64      `json:"content_package_id"`
	ScheduledAt      time.Time  `json:"scheduled_at"`
	PrepareAt        time.Time  `json:"prepare_at"`
	Timezone         string     `json:"timezone"`
	Status           string     `json:"status"`
	PackageVersion   int64      `json:"package_version"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LeaseOwner       *string    `json:"-"`
	LeaseExpiresAt   *time.Time `json:"-"`
	HeartbeatAt      *time.Time `json:"-"`
	AttemptCount     int        `json:"attempt_count"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
}

type PublishSnapshot struct {
	ID                  int64           `json:"id"`
	ContentScheduleID   int64           `json:"content_schedule_id"`
	ContentPackageID    int64           `json:"content_package_id"`
	PackageVersion      int64           `json:"package_version"`
	TargetAccountID     int64           `json:"target_account_id"`
	Language            string          `json:"language"`
	MetadataRevisionID  int64           `json:"metadata_revision_id"`
	TranslationBundleID *int64          `json:"translation_bundle_id,omitempty"`
	CoverMediaID        *string         `json:"cover_media_id,omitempty"`
	SourceMediaAssetID  *string         `json:"source_media_asset_id,omitempty"`
	Title               string          `json:"title"`
	Description         string          `json:"description"`
	Tags                json.RawMessage `json:"tags"`
	PrivacyStatus       string          `json:"privacy_status"`
	PublishAt           time.Time       `json:"publish_at"`
	CreatedAt           time.Time       `json:"created_at"`
}

type PublicationEvent struct {
	ID                  int64     `json:"id"`
	ContentPackageID    int64     `json:"content_package_id"`
	ContentScheduleID   *int64    `json:"content_schedule_id,omitempty"`
	TargetPublicationID *int64    `json:"target_publication_id,omitempty"`
	Stage               string    `json:"stage"`
	EventType           string    `json:"event_type"`
	AttemptNo           int       `json:"attempt_no"`
	ErrorCode           *string   `json:"error_code,omitempty"`
	Message             *string   `json:"message,omitempty"`
	OccurredAt          time.Time `json:"occurred_at"`
}

type DriveInbox struct {
	ID             int64      `json:"id"`
	WorkspaceID    int64      `json:"workspace_id"`
	DriveAccountID int64      `json:"drive_account_id"`
	FolderID       string     `json:"folder_id"`
	Enabled        bool       `json:"enabled"`
	LastScanAt     *time.Time `json:"last_scan_at,omitempty"`
	Cursor         *string    `json:"cursor,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type DriveInboxItem struct {
	ID               int64      `json:"id"`
	InboxID          int64      `json:"inbox_id"`
	DriveFileID      string     `json:"drive_file_id"`
	Filename         string     `json:"filename"`
	MimeType         string     `json:"mime_type"`
	SizeBytes        *int64     `json:"size_bytes,omitempty"`
	ModifiedTime     *time.Time `json:"modified_time,omitempty"`
	Fingerprint      string     `json:"fingerprint,omitempty"`
	Status           string     `json:"status"`
	ContentPackageID *int64     `json:"content_package_id,omitempty"`
	FirstSeenAt      time.Time  `json:"first_seen_at"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
}
