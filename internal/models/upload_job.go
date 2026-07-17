package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// UploadJobSource identifies where the video should be fetched from.
type UploadJobSource string

const (
	UploadJobSourcePublicDrive        UploadJobSource = "public_drive"
	UploadJobSourceAuthenticatedDrive UploadJobSource = "authenticated_drive"
)

// UploadJobStatus tracks the lifecycle of an upload job.
type UploadJobStatus string

const (
	UploadJobStatusPending    UploadJobStatus = "pending"
	UploadJobStatusProcessing UploadJobStatus = "processing"
	UploadJobStatusCompleted  UploadJobStatus = "completed"
	UploadJobStatusFailed     UploadJobStatus = "failed"
)

// UploadJob is a background job that downloads a video from a source
// (public or authenticated Google Drive), uploads it to S3, creates a
// post, and queues it for publishing to the requested platform accounts.
// The row survives server restarts so pending imports are not lost.
//
// ScheduledAt is migration-037's new optional field. NULL means "publish
// the resulting post immediately" (existing single-file behaviour). When
// set, the UploadWorker propagates it into the created post's
// scheduled_at column, gating publishing until that timestamp via the
// existing publish_worker `WHERE scheduled_at <= NOW()` predicate.
//
// FolderID is migration-038's new optional field. NULL for single-file
// imports (which have no "enclosing folder"). When set, the
// /api/v1/media/import/drive/batch/status endpoint can GROUP BY
// per-folder to report dashboard counters without scanning the whole
// upload_jobs table. Pointer-typed so NULL ↔ omitempty maps cleanly.
type UploadJob struct {
	ID             int64           `json:"id"`
	UserID         int64           `json:"user_id"`
	WorkspaceID    int64           `json:"workspace_id"`
	SourceType     UploadJobSource `json:"source_type"`
	SourceID       string          `json:"source_id"`
	DriveAccountID *int64          `json:"drive_account_id,omitempty"`
	FolderID       *string         `json:"folder_id,omitempty"`
	Title          string          `json:"title"`
	Caption        string          `json:"caption"`
	Targets        []int64         `json:"targets"`
	Status         UploadJobStatus `json:"status"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	PostID         *int64          `json:"post_id,omitempty"`
	AssetID        *string         `json:"asset_id,omitempty"`
	ScheduledAt    *time.Time      `json:"scheduled_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ScanTargets unmarshals the JSONB targets column into the Targets slice.
func (j *UploadJob) ScanTargets(src []byte) error {
	if len(src) == 0 {
		j.Targets = []int64{}
		return nil
	}
	return json.Unmarshal(src, &j.Targets)
}

// TargetsJSON returns the targets slice as JSON bytes for persistence.
func (j *UploadJob) TargetsJSON() ([]byte, error) {
	if j.Targets == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(j.Targets)
}

// Value implements driver.Valuer for UploadJobSource.
func (s UploadJobSource) Value() (driver.Value, error) {
	return string(s), nil
}

// Scan implements sql.Scanner for UploadJobSource.
func (s *UploadJobSource) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = ""
		return nil
	case string:
		*s = UploadJobSource(v)
		return nil
	case []byte:
		*s = UploadJobSource(string(v))
		return nil
	default:
		return fmt.Errorf("models: cannot scan UploadJobSource from %T", src)
	}
}

// Value implements driver.Valuer for UploadJobStatus.
func (s UploadJobStatus) Value() (driver.Value, error) {
	return string(s), nil
}

// Scan implements sql.Scanner for UploadJobStatus.
func (s *UploadJobStatus) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = ""
		return nil
	case string:
		*s = UploadJobStatus(v)
		return nil
	case []byte:
		*s = UploadJobStatus(string(v))
		return nil
	default:
		return fmt.Errorf("models: cannot scan UploadJobStatus from %T", src)
	}
}

// BatchStatusSummary is the per-folder aggregate returned by
// UploadJobRepository.AggregateByFolder. The fields map 1:1 to the
// JSON response of GET /api/v1/media/import/drive/batch/status.
// Pointer timestamps are nil when the folder has zero jobs OR when
// every job's scheduled_at is NULL (single-file legacy imports).
type BatchStatusSummary struct {
	PendingCount    int        `json:"pending_count"`
	ProcessingCount int        `json:"processing_count"`
	CompletedCount  int        `json:"completed_count"`
	FailedCount     int        `json:"failed_count"`
	TotalCount      int        `json:"total_count"`
	FirstPublishAt  *time.Time `json:"first_publish_at,omitempty"`
	LastPublishAt   *time.Time `json:"last_publish_at,omitempty"`
}
