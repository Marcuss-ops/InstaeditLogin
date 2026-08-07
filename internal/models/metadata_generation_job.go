package models

import (
	"encoding/json"
	"time"
)

// MetadataGenerationJobStatus constants for the async NVIDIA metadata flow.
const (
	MetadataGenJobQueued     = "queued"
	MetadataGenJobProcessing = "processing"
	MetadataGenJobCompleted  = "completed"
	MetadataGenJobFailed     = "failed"
)

// MetadataGenerationJob is a single async NVIDIA metadata generation
// request. Created by POST /generate-metadata, consumed by the
// MetadataGenerationWorker, polled by the caller via GET.
type MetadataGenerationJob struct {
	ID             int64           `json:"id"`
	WorkspaceID    int64           `json:"workspace_id"`
	VeloxProjectID string          `json:"velox_project_id"`
	Prompt         string          `json:"prompt"`
	Status         string          `json:"status"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	AttemptCount   int             `json:"attempt_count"`
	MaxAttempts    int             `json:"max_attempts"`
	NextAttemptAt  *time.Time      `json:"next_attempt_at,omitempty"`
	LockedBy       string          `json:"-"`
	LockedAt       *time.Time      `json:"-"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
}

// IsTerminal returns true when the job has reached a terminal state.
func (j *MetadataGenerationJob) IsTerminal() bool {
	return j.Status == MetadataGenJobCompleted || j.Status == MetadataGenJobFailed
}
