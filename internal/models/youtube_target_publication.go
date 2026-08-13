package models

import "time"

// YouTubeTargetPublication is the per-target view of the YouTube publishing
// pipeline (Blocco #1, migration 066). It connects the generic ingest queue
// (upload_jobs.id) to the per-channel fan-out (post_targets.id +
// platform_accounts.id) and tracks the YouTube-specific lifecycle that no
// other model carries:
//
//   - youtube_video_id         — what YouTube returned after the resumable
//     upload (NULL until the upload phase lands).
//   - youtube_upload_status    — Drive→Storage→YouTube upload state machine.
//   - youtube_processing_status — raw YouTube API processingStatus echo,
//     polled after the upload lands.
//   - editor_session_id + velox_project_id — Velox InstaEditor session
//     opened per (per-target, video) once
//     processing reaches 'processed'.
//   - thumbnail_media_id       — media_assets.id (TEXT, no FK constraint by
//     design — same convention youtube_video_edits
//     uses for thumbnail_media_id).
//   - thumbnail_status         — pending→thumbnail_editing→thumbnail_ready
//     (or failed after Velox rejects the crop).
//   - desired_privacy          — snapshot of the publish cascade at
//     row-creation time; defaults to 'public'.
//   - youtube_uploaded_at — TIMESTAMPTZ stamp when videos.insert
//     returned 200 + youtube_upload_status
//     transitioned to 'youtube_uploaded'. NULL
//     until upload completes; added in
//     migration 067. Sister field of the
//     youtube_upload_status enum — the
//     timestamp gives the operator-triage
//     dashboard SLA metrics; the status
//     gives the worker its next state.
//   - youtube_processed_at — TIMESTAMPTZ stamp when YouTube API
//     processingStatus='processed' (webhook
//     callback or reconcile worker poll).
//     NULL throughout the upload +
//     processing window. Sister of
//     youtube_processing_status.
//   - publish_at + published_at — publish-worker cursors (NULL = publish
//     immediately; published_at sentinel = the
//     publish phase fired).
//   - last_error + attempt_count — retry/dead-letter accounting mirroring
//     post_targets.
//   - created_at + updated_at   — audit timestamps.
//
// STATUS SEPARATION (Blocco #3 P0):
//
//   - cross-platform post_targets.status (queued/publishing/published/
//     blocked_auth) covers the publish pipeline for ALL platforms.
//     YouTube transitions it to 'publishing' ONLY when the videos.update
//     side-effect fires, NOT during the upload-as-private phase.
//
//   - youtube_target_publications.youtube_upload_status (and its sister
//     timestamps) covers the YouTube-specific lifecycle (upload, processing,
//     thumbnail editing) for THIS per-target row.
//
//     This separation is INTENTIONALLY not modelled as new enums on
//     post_targets — cross-platform state should not know about per-platform
//     sub-states (YouTube's "youtube_uploading" is meaningless to a
//     Facebook post). The verdict explicitly asks for this separation; we
//     achieve it via table shape, not enum layout.
//
// This model is the single source of truth for "where is this video on its
// specific channel today" — used by:
//   - the worker to Claim+transition rows atomically,
//   - the unified pipeline view endpoint (content/pipeline),
//   - the YouTube-webhook callback to map a YouTube video_id back to the
//     per-target publication row, and
//   - the reconciliation worker (compare DB row vs YouTube API echo).
type YouTubeTargetPublication struct {
	ID                  int64   `json:"id"`
	UploadJobID         int64   `json:"upload_job_id"`
	PostTargetID        int64   `json:"post_target_id"`
	PlatformAccountID   int64   `json:"platform_account_id"`
	YouTubeVideoID      *string `json:"youtube_video_id,omitempty"`
	YouTubeUploadStatus string  `json:"youtube_upload_status"`
	// YouTubeProcessingStatus TEXT — kept distinct from upload_status
	// because the two phases are asynchronous (uploading finishes
	// before YouTube even starts its own background processing).
	YouTubeProcessingStatus *string `json:"youtube_processing_status,omitempty"`
	// YouTubeUploadedAt (migration 067, Blocco #3 P0): stamp NOW()
	// when MarkYouTubeUploaded transitions the row. NULL pre-upload.
	YouTubeUploadedAt *time.Time `json:"youtube_uploaded_at,omitempty"`
	// YouTubeProcessedAt (migration 067): stamp NOW() when the
	// reconcile worker / webhook hears YouTube processingStatus='processed'.
	// NULL pre-processed.
	YouTubeProcessedAt *time.Time `json:"youtube_processed_at,omitempty"`
	EditorSessionID    *string    `json:"editor_session_id,omitempty"`
	VeloxProjectID     *string    `json:"velox_project_id,omitempty"`
	ThumbnailMediaID   *string    `json:"thumbnail_media_id,omitempty"`
	ThumbnailStatus    *string    `json:"thumbnail_status,omitempty"`
	DesiredPrivacy     string     `json:"desired_privacy"`
	PublishAt          *time.Time `json:"publish_at,omitempty"`
	// NativePublishAt (migration 126): the publish_at value baked into
	// the initial videos.insert status.publishAt. Non-nil means YouTube
	// owns the private→public transition at that time, so the publish
	// worker must NOT re-issue a videos.update for the same transition
	// (saves one ~50-unit call from the 2026 general quota bucket per
	// scheduled public video). NULL when the upload did not carry a
	// native schedule (immediate publish, or desired privacy != public).
	NativePublishAt *time.Time `json:"native_publish_at,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	AttemptCount       int        `json:"attempt_count"`
	// Delivery-queue operational fields (migration 125). `state` is the
	// canonical lifecycle cursor for the WHOLE delivery — the legacy
	// youtube_upload_status / youtube_processing_status / thumbnail_status
	// columns are kept and demoted to per-phase observations. The row is
	// the independently claimable unit of work (1 PostTarget YouTube = 1
	// publication = 1 delivery), so a single upload_job with N targets
	// fans out to N queue rows consumed by the global delivery pool.
	State string `json:"state"`
	// Priority SMALLINT, lower = higher priority (drives the claim ORDER BY).
	Priority int16 `json:"priority"`
	// PrepareAt is the earliest wall-clock time the delivery may be
	// claimed (NULL = eligible immediately).
	PrepareAt *time.Time `json:"prepare_at,omitempty"`
	// NextAttemptAt is the retry backoff cursor: retry_wait / quota_wait
	// rows are unclaimable until it elapses.
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	// MaxAttempts is the retry cap before dead_letter.
	MaxAttempts int `json:"max_attempts"`
	LeaseOwner      *string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt     *time.Time `json:"heartbeat_at,omitempty"`
	// ResumeState is where a side state (quota_wait / blocked_auth /
	// retry_wait) returns once its condition clears.
	ResumeState *string `json:"resume_state,omitempty"`
	// LastErrorCode is the stable machine-readable error class (sister of
	// last_error, which stays human-readable).
	LastErrorCode *string `json:"last_error_code,omitempty"`
	// LastTransitionAt is stamped on every state change (audit + stuck
	// detection).
	LastTransitionAt time.Time `json:"last_transition_at"`
	// VerifiedAt is the terminal success stamp (published → verified).
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// OriginalPublishAt is the user's originally requested publish time,
	// preserved across capacity spillover.
	OriginalPublishAt *time.Time `json:"original_publish_at,omitempty"`
	// SpilloverCount is how many days capacity planning moved this
	// delivery forward (audit of the planner).
	SpilloverCount int `json:"spillover_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
