package models

import (
	"time"

	"github.com/google/uuid"
)

// ImportBatchStatus tracks the lifecycle of an async folder-batch
// import (P1#7).
//
//   queued        — the producer handler inserted the row; the
//                   background folder crawler has not claimed it yet.
//   processing    — crawler claimed the row; cursor_page_token may be
//                   non-null mid-flight.
//   completed     — full folder listing processed; upload_jobs.batch_id
//                   collapses all per-file jobs.
//   failed        — terminal fail (Drive permission 403, source bad,
//                   etc); recoverable via operator retry (not DLQ).
//   dead_letter   — retry budget exhausted; operator triage.
//   cancelled     — user requested cancel before completion.
type ImportBatchStatus string

const (
	ImportBatchStatusQueued     ImportBatchStatus = "queued"
	ImportBatchStatusProcessing ImportBatchStatus = "processing"
	ImportBatchStatusCompleted  ImportBatchStatus = "completed"
	ImportBatchStatusFailed     ImportBatchStatus = "failed"
	ImportBatchStatusDeadLetter ImportBatchStatus = "dead_letter"
	ImportBatchStatusCancelled  ImportBatchStatus = "cancelled"
)

// IsTerminal reports whether s will not transition again without an
// operator action. Used by the dashboard's "stuck batches" filter.
func (s ImportBatchStatus) IsTerminal() bool {
	return s == ImportBatchStatusCompleted ||
		s == ImportBatchStatusFailed ||
		s == ImportBatchStatusDeadLetter ||
		s == ImportBatchStatusCancelled
}

// ImportBatch is the header row for an async folder-batch import
// (P1#7). The header is created by the API handler IMMEDIATELY and
// returned to the caller as {batch_id, status:"queued"}; the
// background folder crawler (internal/worker/drive_batch_crawler.go)
// later claims the row, paginates the Drive folder, and creates
// upload_jobs rows with batch_id FK stamping.
//
// P1#7 design choices (per thinker verdict):
//   * D1 — header lives in import_batches; upload_jobs.batch_id UUID FK
//     joins cleanly without cramming source/schedule metadata into
//     per-file ingest rows.
//   * D3 — XOR (handler validates either target_account_ids XOR
//     target_group_name, never both).
//   * D4 — workspace_channels.group_name is the ad-hoc string key
//     until a future migration adds a UUID column.
//   * D5.b+cursor — the crawler crash-recovery path requires the
//     cursor_page_token + cursor_indexed_count fields to checkpoint
//     per page; a reaper-restarted crawler resumes from there.
//   * D6 — schedule_clamped + schedule_clamp_reason are surfaced in the
//     handler response (best-effort from file_count estimate) AND in
//     the runtime result (exact horizon once file_count is known).
type ImportBatch struct {
	ID                     uuid.UUID         `json:"id"`
	UserID                 int64             `json:"user_id"`
	WorkspaceID            int64             `json:"workspace_id"`
	SourceProvider         string            `json:"source_provider"` // "google_drive" today
	SourceDriveAccountID   *int64            `json:"source_drive_account_id,omitempty"`
	SourceFolderID         string            `json:"source_folder_id"`
	TargetAccountIDs       []int64           `json:"target_account_ids"`
	TargetGroupName        *string           `json:"target_group_name,omitempty"`
	PublishScheduleStartAt time.Time         `json:"publish_schedule_start_at"`
	PublishScheduleMinGap  int               `json:"publish_schedule_min_gap_seconds"`
	PublishScheduleMaxGap  int               `json:"publish_schedule_max_gap_seconds"`
	Status                 ImportBatchStatus `json:"status"`
	CursorPageToken        *string           `json:"cursor_page_token,omitempty"`
	CursorIndexedCount     int               `json:"cursor_indexed_count"`
	ScheduleClamped        bool              `json:"schedule_clamped"`
	ScheduleClampReason    *string           `json:"schedule_clamp_reason,omitempty"`
	Warnings               []string          `json:"warnings,omitempty"`
	ErrorMessage           *string           `json:"error_message,omitempty"`
	CreatedCount           int               `json:"created_count"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
	CompletedAt            *time.Time        `json:"completed_at,omitempty"`
}

// DriveSourceRef is the triple {provider, drive_account_id, folder_id}
// the request DTO carries.
type DriveSourceRef struct {
	Provider       string `json:"provider"` // "google_drive" today
	DriveAccountID *int64 `json:"drive_account_id,omitempty"`
	FolderID       string `json:"folder_id"`
}

// PublishScheduleRef is the {start_at, min_gap, max_gap} envelope.
type PublishScheduleRef struct {
	StartAt       time.Time `json:"start_at"`
	MinGapSeconds int       `json:"min_gap_seconds"`
	MaxGapSeconds int       `json:"max_gap_seconds"`
}

// BatchTargetsRef carries either target_account_ids[] or
// target_group_name (XOR). The handler validates XOR (D3.a).
type BatchTargetsRef struct {
	AccountIDs []int64 `json:"account_ids,omitempty"`
	GroupName  *string `json:"group_name,omitempty"`
}

// DriveBatchImportRequest is the new P1#7 producer-side body.
// Replaces the historical FacebookAccountID-hardcoded shape with
// multi-platform semantics: source/provider discriminates today's
// google_drive (forward-compatible), target_account_ids[] OR
// target_group_name (XOR), publish_schedule envelope.
type DriveBatchImportRequest struct {
	Source          DriveSourceRef     `json:"source"`
	WorkspaceID     int64              `json:"workspace_id"`
	Targets         BatchTargetsRef    `json:"targets"`
	PublishSchedule PublishScheduleRef `json:"publish_schedule"`
	Title           string             `json:"title,omitempty"`
	CaptionPrefix   string             `json:"caption_prefix,omitempty"`
}

// DriveBatchImportResponse is what the producer-side handler
// returns IMMEDIATELY on success (202 Accepted). Field set is the
// producer's view; the full per-file rollup is available via
// GET /api/v1/media/import/drive/folder/async/{id} which returns
// the import_batches header + child upload_jobs.
type DriveBatchImportResponse struct {
	BatchID            uuid.UUID `json:"batch_id"`
	Status             string    `json:"status"` // "queued" on producer response
	ScheduleClamped    bool      `json:"schedule_clamped"`
	ScheduleClampReason *string  `json:"schedule_clamp_reason,omitempty"`
	EstimatedFileCount int       `json:"estimated_file_count"`
	Notice             string    `json:"notice,omitempty"`
}
