package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
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
	// P1 — worker pool (migration 045). New lifecycle states that
	// ClaimBatch / Heartbeat / MarkRetry / MarkDeadLetter use. See
	// the migration header for the full transition diagram.
	UploadJobStatusLeased         UploadJobStatus = "leased"          // claimed; lease_owner + heartbeat alive
	UploadJobStatusRetryWait      UploadJobStatus = "retry_wait"      // transient failure; backoff not yet elapsed
	UploadJobStatusDeadLetter     UploadJobStatus = "dead_letter"     // retry budget exhausted; operator triage
	UploadJobStatusCancelled      UploadJobStatus = "cancelled"       // user cancelled before claim
	// P1 #4 — ingest/publish split (migrations 049a + 049c).
	// Migrated the existing 'ready_to_publish' / 'completed' values
	// to canonical names whose semantics map 1:1 to the user's
	// mental model:
	//   ingest_completed = Drive→S3 done, asset_id stamped, but the
	//                      publish call to provider has NOT happened
	//                      yet. The publish pool's ClaimBatch CTE
	//                      selects this state and waits on
	//                      (publish_at <= NOW()) WITHOUT holding a
	//                      lease.
	//   publish_completed = BOTH ingest AND publish are done; this
	//                      is the row's final at-rest state.
	// The old 'ready_to_publish' / 'completed' values stay on the
	// PostgreSQL enum (PG < 18 cannot DROP VALUE) but are never
	// written by Go code paths after this commit — they're listed
	// below as deprecated aliases so existing tests that string-
	// match them compile without immediate churn (TODO followups
	// migrate those tests to the new names).
	UploadJobStatusIngestCompleted UploadJobStatus = "ingest_completed"
	UploadJobStatusPublishCompleted UploadJobStatus = "publish_completed"
	// Deprecated alias — kept as a Go const so legacy test fixtures
	// that still match the old value compile. New code MUST use
	// UploadJobStatusIngestCompleted. Migration 049c UPDATE'd any
	// existing rows out of this state; the SQL value remains on the
	// enum because PostgreSQL < 18 cannot DROP VALUE (049a header).
	UploadJobStatusReadyToPublish UploadJobStatus = "ready_to_publish"
)

// IsIngestTerminal reports whether the row's ingest phase (Drive →
// S3 + asset_id stamp) is finished. Distinct from IsPublishTerminal:
// a row can be ingest-terminal while still waiting for its publish_at
// cursor. Replaces the historical "is ready_to_publish" check.
func (s UploadJobStatus) IsIngestTerminal() bool {
	return s == UploadJobStatusIngestCompleted ||
		s == UploadJobStatusPublishCompleted ||
		// Legacy aliases — same intent, kept for any reader that
		// hasn't migrated to the new names yet.
		s == UploadJobStatusReadyToPublish ||
		s == UploadJobStatusCompleted
}

// IsPublishTerminal reports whether BOTH phases (ingest + publish)
// are done. A row is publish-terminal only when at-rest completed:
// every post_target reached terminal success (or — for legacy
// single-file flows — the public Post.Publish() returned success).
func (s UploadJobStatus) IsPublishTerminal() bool {
	return s == UploadJobStatusPublishCompleted ||
		// Legacy alias for backward read.
		s == UploadJobStatusCompleted
}

// IsTerminal preserves the historical meaning — the row will not
// transition again without an operator action. Used by dashboard
// counters and the operator-triage bucket.
func (s UploadJobStatus) IsTerminal() bool {
	return s == UploadJobStatusPublishCompleted ||
		s == UploadJobStatusCompleted ||
		s == UploadJobStatusDeadLetter ||
		s == UploadJobStatusCancelled ||
		s == UploadJobStatusFailed
}

// UploadJob is a background job that downloads a video from a source
// (public or authenticated Google Drive), uploads it to S3, creates a
// post, and queues it for publishing to the requested platform accounts.
// The row survives server restarts so pending imports are not lost.
//
// IngestAfter (P1#4, migration 049c) is the earliest time the ingest
// pool may claim this row. Server-side DEFAULT NOW() lands the column
// on insert; ClaimBatch's CTE adds
// `AND (ingest_after IS NULL OR ingest_after <= NOW())` so the ingest
// pool never blocks on the user-supplied publish window — assets are
// staged in S3 well in advance. NOT NULL, stored as time.Time
// (never nil). Replaces migration-037's scheduled_at column on
// upload_jobs.
//
// PublishAt (P1#4, migration 049c) is the user-facing "what time
// should this post go live" cursor propagated to the created
// post.publish_at. NULL = publish immediately (existing single-file
// behaviour). The publish pool's ClaimBatchForPublish CTE adds
// `AND (publish_at IS NULL OR publish_at <= NOW())` so the publish
// pool waits for the cursor WITHOUT holding a lease on the row —
// the row sits at-rest in 'ingest_completed' until publish_at
// elapses. Pointer-typed so NULL ↔ omitempty maps cleanly through
// the API JSON contract.
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
	// P1#4 — split of the old scheduled_at field.
	// IngestAfter: when the asset can be fetched + copied to S3 (server
	//             defaults to NOW() at insert time, so the ingest pool
	//             claims the row immediately for the upcoming publish).
	// PublishAt:  when the platform publish should fire (NULL = now).
	// JSON tags: include `scheduled_at` alias so legacy SPA clients
	// continue to render the calendar until they migrate to the new
	// canonical key. The alias is populated server-side from PublishAt
	// in the API response layer (pkg/api/uploads.go).
	IngestAfter time.Time  `json:"ingest_after"`
	PublishAt   *time.Time `json:"publish_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	// P1 — worker pool (migration 046). Server-side DEFAULTs keep
	// legacy Insert/CreateIfSourceAbsent callers compatible: they
	// can write rows without mentioning these fields and ClaimBatch
	// / MarkRetry / MarkDeadLetter pick the rows up with sensible
	// values.
	AttemptCount   int        `json:"attempt_count"`
	MaxAttempts    int        `json:"max_attempts"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	LeaseOwner     *string    `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	ProgressBytes  int64      `json:"progress_bytes"`
	TotalBytes     *int64     `json:"total_bytes,omitempty"`
	ErrorCode      *string    `json:"error_code,omitempty"`
	Priority       int        `json:"priority"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	// P1#5 — YouTube resumable-upload session state. A worker that
	// crashes mid-upload can pick up where it left off instead of
	// restreaming the whole file from byte 0. TreatYouTubeSessionURI
	// as credential-adjacent — the `json:"-"` tag prevents it from
	// ever leaving the backend via any API response; workers
	// must redact it (first-8-chars+…) when logging.
	//
	// YouTubeSessionOffset is the bytes the server has
	// acknowledged (NULL = no progress / fresh session).
	// YouTubeSessionExpiresAt is YouTube-side expiry. The worker
	// checks this before trusting the URI.
	// YouTubeChunkSize is the chunk size the worker used to
	// stamp YouTubeSessionOffset; persisted so a restart uses
	// the same value.
	// YouTubeLastChunkAt is the wall-clock of the last successful
	// chunk PUT (dashboard + future per-session-idleness reaper).
	YouTubeSessionURI        *string    `json:"-"`
	YouTubeSessionOffset     *int64     `json:"youtube_session_offset,omitempty"`
	YouTubeSessionExpiresAt  *time.Time `json:"youtube_session_expires_at,omitempty"`
	YouTubeChunkSize         *int64     `json:"youtube_chunk_size,omitempty"`
	YouTubeLastChunkAt       *time.Time `json:"youtube_last_chunk_at,omitempty"`
	// P1#7 — import_batches FK. NULL for single-file imports (the
	// historical POST /media/import/drive shape) and for the
	// synchronous v1 Drive folder endpoint; non-NULL when the
	// async producer-side handler (POST /media/import/drive/
	// folder/async) created this row. Pointer + omitempty so the
	// JSON shape stays identical for legacy clients.
	BatchID *uuid.UUID `json:"batch_id,omitempty"`
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
	// P1#4 — UploadJobStatus::completed was renamed to
	// publish_completed; the JSON key stays 'completed_count' so the
	// SPA's existing badge renders the merged ingest+publish
	// terminal-state count. The new ingest_completed state (Drive →
	// S3 done, awaiting publish) surfaces as ReadyToPublishCount
	// below; the SPA can show both independently.
	CompletedCount  int        `json:"completed_count"`
	FailedCount     int        `json:"failed_count"`
	TotalCount      int        `json:"total_count"`
	FirstPublishAt  *time.Time `json:"first_publish_at,omitempty"`
	LastPublishAt   *time.Time `json:"last_publish_at,omitempty"`
	// P1 — worker pool states (migration 045). New columns appear in
	// the dashboard JSON so the SPA can show 'leased' / 'retry_wait'
	// / 'dead_letter' badges without re-fetching; legacy clients
	// ignore the unknown fields.
	LeasedCount     int `json:"leased_count,omitempty"`
	RetryWaitCount  int `json:"retry_wait_count,omitempty"`
	DeadLetterCount int `json:"dead_letter_count,omitempty"`
	CancelledCount  int `json:"cancelled_count,omitempty"`
	// P1#4 — new ingest_completed (asset staged, awaiting publish_at)
	// counts. Surfaced separately from CompletedCount so the SPA's
	// "ready to publish" widget renders without conflating it with
	// the at-rest publish_completed bucket.
	ReadyToPublishCount int `json:"ready_to_publish_count,omitempty"`
}
