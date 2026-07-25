package models

import "time"

// YouTubeTargetPublication is the per-target view of the YouTube publishing
// pipeline (Blocco #1, migration 066). It connects the generic ingest queue
// (upload_jobs.id) to the per-channel fan-out (post_targets.id +
// platform_accounts.id) and tracks the YouTube-specific lifecycle that no
// other model carries:
//
//   * youtube_video_id         — what YouTube returned after the resumable
//                                upload (NULL until the upload phase lands).
//   * youtube_upload_status    — Drive→Storage→YouTube upload state machine.
//   * youtube_processing_status — raw YouTube API processingStatus echo,
//                                polled after the upload lands.
//   * editor_session_id + velox_project_id — Velox dark-editor session
//                                opened per (per-target, video) once
//                                processing reaches 'processed'.
//   * thumbnail_media_id       — media_assets.id (TEXT, no FK constraint by
//                                design — same convention youtube_video_edits
//                                uses for thumbnail_media_id).
//   * thumbnail_status         — pending→thumbnail_editing→thumbnail_ready
//                                (or failed after Velox rejects the crop).
//   * desired_privacy          — snapshot of the publish cascade at
//                                row-creation time; defaults to 'public'.
//   * publish_at + published_at — publish-worker cursors (NULL = publish
//                                immediately; published_at sentinel = the
//                                publish phase fired).
//   * last_error + attempt_count — retry/dead-letter accounting mirroring
//                                post_targets.
//   * created_at + updated_at   — audit timestamps.
//
// This model is the single source of truth for "where is this video on its
// specific channel today" — used by:
//   * the worker to Claim+transition rows atomically,
//   * the unified pipeline view endpoint (content/pipeline),
//   * the YouTube-webhook callback to map a YouTube video_id back to the
//     per-target publication row, and
//   * the reconciliation worker (compare DB row vs YouTube API echo).
type YouTubeTargetPublication struct {
	ID                      int64     `json:"id"`
	UploadJobID             int64     `json:"upload_job_id"`
	PostTargetID            int64     `json:"post_target_id"`
	PlatformAccountID       int64     `json:"platform_account_id"`
	YouTubeVideoID          *string   `json:"youtube_video_id,omitempty"`
	YouTubeUploadStatus     string    `json:"youtube_upload_status"`
	YouTubeProcessingStatus *string   `json:"youtube_processing_status,omitempty"`
	EditorSessionID         *string   `json:"editor_session_id,omitempty"`
	VeloxProjectID          *string   `json:"velox_project_id,omitempty"`
	ThumbnailMediaID        *string   `json:"thumbnail_media_id,omitempty"`
	ThumbnailStatus         *string   `json:"thumbnail_status,omitempty"`
	DesiredPrivacy          string    `json:"desired_privacy"`
	PublishAt               *time.Time `json:"publish_at,omitempty"`
	PublishedAt             *time.Time `json:"published_at,omitempty"`
	LastError               string    `json:"last_error,omitempty"`
	AttemptCount            int       `json:"attempt_count"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}
