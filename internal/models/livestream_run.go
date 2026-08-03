package models

import (
	"errors"
	"time"
)

// LivestreamRunStatus is the worker-observed lifecycle of one execution of
// a reusable livestream configuration. It is deliberately separate from
// Livestream's operator-owned desired state.
type LivestreamRunStatus string

const (
	LivestreamRunStatusDraft            LivestreamRunStatus = "draft"
	LivestreamRunStatusPreflighting     LivestreamRunStatus = "preflighting"
	LivestreamRunStatusPreparing        LivestreamRunStatus = "preparing"
	LivestreamRunStatusReady            LivestreamRunStatus = "ready"
	LivestreamRunStatusScheduled        LivestreamRunStatus = "scheduled"
	LivestreamRunStatusStarting         LivestreamRunStatus = "starting"
	LivestreamRunStatusWaitingForIngest LivestreamRunStatus = "waiting_for_ingest"
	LivestreamRunStatusTesting          LivestreamRunStatus = "testing"
	LivestreamRunStatusLive             LivestreamRunStatus = "live"
	LivestreamRunStatusDegraded         LivestreamRunStatus = "degraded"
	LivestreamRunStatusReconnecting     LivestreamRunStatus = "reconnecting"
	LivestreamRunStatusStopping         LivestreamRunStatus = "stopping"
	LivestreamRunStatusCompleted        LivestreamRunStatus = "completed"
	LivestreamRunStatusFailed           LivestreamRunStatus = "failed"
	LivestreamRunStatusCancelled        LivestreamRunStatus = "cancelled"
)

// validLivestreamRunStatuses mirrors migration 093 exactly.
var validLivestreamRunStatuses = map[LivestreamRunStatus]struct{}{
	LivestreamRunStatusDraft:            {},
	LivestreamRunStatusPreflighting:     {},
	LivestreamRunStatusPreparing:        {},
	LivestreamRunStatusReady:            {},
	LivestreamRunStatusScheduled:        {},
	LivestreamRunStatusStarting:         {},
	LivestreamRunStatusWaitingForIngest: {},
	LivestreamRunStatusTesting:          {},
	LivestreamRunStatusLive:             {},
	LivestreamRunStatusDegraded:         {},
	LivestreamRunStatusReconnecting:     {},
	LivestreamRunStatusStopping:         {},
	LivestreamRunStatusCompleted:        {},
	LivestreamRunStatusFailed:           {},
	LivestreamRunStatusCancelled:        {},
}

// ValidLivestreamRunStatus reports whether status is accepted by the run
// state machine and the migration-093 database constraint.
func ValidLivestreamRunStatus(status LivestreamRunStatus) bool {
	_, ok := validLivestreamRunStatuses[status]
	return ok
}

// Concurrency sentinels returned by the livestream run repository. Callers
// can use errors.Is even when the repository adds contextual details.
var (
	ErrLivestreamRunActiveConflict     = errors.New("livestream channel already has an active run")
	ErrLivestreamRunGenerationConflict = errors.New("livestream run generation conflict")
	ErrLivestreamRunLeaseLost          = errors.New("livestream run lease lost")
	ErrLivestreamRunVersionConflict    = errors.New("livestream run configuration version conflict")
)

// LivestreamRun is one durable execution of a Livestream configuration.
// Resource IDs and lifecycle checkpoints belong here rather than on the
// reusable configuration, allowing retries, history, and back-to-back runs.
type LivestreamRun struct {
	ID                   string              `json:"id"`
	LivestreamID         string              `json:"livestream_id"`
	PlatformAccountID    int64               `json:"platform_account_id"`
	Generation           int64               `json:"generation"`
	Status               LivestreamRunStatus `json:"status"`
	YouTubeBroadcastID   *string             `json:"youtube_broadcast_id,omitempty"`
	YouTubeStreamID      *string             `json:"youtube_stream_id,omitempty"`
	ConfigurationVersion int64               `json:"configuration_version"`
	WorkerID             *string             `json:"worker_id,omitempty"`
	LeaseExpiresAt       *time.Time          `json:"lease_expires_at,omitempty"`
	HeartbeatAt          *time.Time          `json:"heartbeat_at,omitempty"`
	LastFrameAt          *time.Time          `json:"last_frame_at,omitempty"`
	EncoderPID           string              `json:"encoder_pid,omitempty"`
	ReconnectCount       int                 `json:"reconnect_count"`
	AttemptCount         int                 `json:"attempt_count"`
	StartedAt            *time.Time          `json:"started_at,omitempty"`
	LiveAt               *time.Time          `json:"live_at,omitempty"`
	EndedAt              *time.Time          `json:"ended_at,omitempty"`
	ErrorCode            string              `json:"error_code,omitempty"`
	ErrorMessage         string              `json:"error_message,omitempty"`
	LastErrorCode        string              `json:"last_error_code,omitempty"`
	LastErrorMessage     string              `json:"last_error_message,omitempty"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
}
