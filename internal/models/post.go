package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// PostStatus is the lifecycle of a Post (or PostTarget). Mirrors the
// Postgres enum `post_status` introduced by migration 003_posts_workspaces.sql
// and extended by:
//
//   - migration 010_publish_jobs_and_queued_status.sql (queued, waiting_provider)
//   - migration 012_async_threads_support.sql (waiting_provider, queued, partially_published)
//   - migration 018_publish_state_machine.sql (retrying)
//
// Lifecycle (post-commit-018):
//
//	draft → queued → publishing → published
//	                           → partially_published
//	                           → waiting_provider
//	                           → retrying ───→ (when ListPending picks
//	                                        ───  the row again after
//	                                        ───  next_attempt_at <= now,
//	                                        ───  ClaimQueuedTarget flips
//	                                        ───  it back to publishing) ─→ publishing
//	                           → failed
//
// The retrying → publishing step is INDIRECT: a worker tick re-checks
// post_targets rows where status='retrying' AND next_attempt_at <= now,
// claims them via ClaimQueuedTarget (which sets status='publishing'),
// and resumes the publish from there. There is no direct UPDATE
// 'retrying' → 'publishing' anywhere — the worker is the only legitimate
// writer of that transition.
//
// String-based enum pattern lets us flow PostStatus values through
// encoding/json (default string serialization) and through database/sql
// (Scan/Value implementations) without bespoke marshalers.
type PostStatus string

const (
	PostStatusDraft              PostStatus = "draft"
	PostStatusQueued             PostStatus = "queued"
	PostStatusPublishing         PostStatus = "publishing"
	PostStatusPublished          PostStatus = "published"
	PostStatusPartiallyPublished PostStatus = "partially_published"
	PostStatusFailed             PostStatus = "failed"
	PostStatusWaitingProvider    PostStatus = "waiting_provider"
	// PostStatusRetrying (migration 018) — the target failed
	// transiently (e.g. transient 5xx, rate limit) and is now sitting
	// in backoff. NOT terminal: when next_attempt_at <= now the
	// worker re-claims it (transitioning back to publishing via the
	// ListPending SELECT) and resumes the pipeline.
	PostStatusRetrying PostStatus = "retrying"

	// Deprecated: use PostStatusQueued instead.
	PostStatusScheduled = PostStatusQueued
)

// IsValid reports whether s is one of the defined PostStatus values.
func (s PostStatus) IsValid() bool {
	switch s {
	case PostStatusDraft,
		PostStatusQueued,
		PostStatusPublishing,
		PostStatusPublished,
		PostStatusPartiallyPublished,
		PostStatusFailed,
		PostStatusWaitingProvider,
		PostStatusRetrying:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether s is a terminal state (no further transitions).
func (s PostStatus) IsTerminal() bool {
	return s == PostStatusPublished || s == PostStatusPartiallyPublished || s == PostStatusFailed
}

// String returns the underlying string value.
func (s PostStatus) String() string { return string(s) }

// Value implements driver.Valuer.
func (s PostStatus) Value() (driver.Value, error) {
	return string(s), nil
}

// Scan implements sql.Scanner.
func (s *PostStatus) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = ""
		return nil
	case string:
		*s = PostStatus(v)
		return nil
	case []byte:
		*s = PostStatus(string(v))
		return nil
	default:
		return fmt.Errorf("models: cannot scan PostStatus from %T", src)
	}
}

// Post is a piece of content (idea → edit → publish pipeline) belonging to
// a Workspace. The fan-out to multiple platform accounts is captured by
// PostTarget rows that reference this Post.
type Post struct {
	ID             int64      `json:"id"`
	WorkspaceID    int64      `json:"workspace_id"`
	Title          string     `json:"title,omitempty"`
	Caption        string     `json:"caption,omitempty"`
	MediaURL       string     `json:"media_url,omitempty"`
	ScheduledAt    *time.Time `json:"scheduled_at,omitempty"`
	Status         PostStatus `json:"status"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// PostTarget represents the fan-out of a Post to a specific platform account.
// The retry-aware columns added by migration 018_publish_state_machine.sql
// (current_step, progress, attempt_count, next_attempt_at, remote_post_id,
// remote_post_url, last_error_code) are exposed here so the worker and
// API layer can read/write them through the same struct.
type PostTarget struct {
	ID                int64      `json:"id"`
	PostID            int64      `json:"post_id"`
	PlatformAccountID int64      `json:"platform_account_id"`
	Status            PostStatus `json:"status"`

	// Zernio publish-state-machine (migration 018):
	//   * current_step       — free-form pipeline-stage label, written
	//                          by the worker at every transition.
	//   * progress (0..100)  — percentage, bumped by async check status.
	//   * attempt_count      — retry counter, monotonically increasing.
	//   * next_attempt_at    — backoff target; NULL while not retrying.
	CurrentStep   string     `json:"current_step,omitempty"`
	Progress      int        `json:"progress"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`

	// Provider-facing ids (see migration 011_target_provider_state.sql
	// for the platform_post_id / provider_state / container_id trio).
	// platform_post_id is the INTERNAL id the provider exposes for the
	// publish (publish_id on async platforms; same as remote_post_id on
	// sync platforms). Set at claim time on async flows; set at
	// successful-publish time on sync flows.
	PlatformPostID string `json:"platform_post_id,omitempty"`
	// remote_post_id / remote_post_url (migration 018) carry the
	// PUBLIC-FACING identification of the published post. They are
	// the canonical "where did this end up" answer dashboards render.
	// For sync platforms they're populated together from
	// models.PublishResult; for async platforms they materialise once
	// the reconciler lands the provider's terminal-state response.
	RemotePostID  string `json:"remote_post_id,omitempty"`
	RemotePostURL string `json:"remote_post_url,omitempty"`

	// Diagnostic / observability columns.
	ErrorMessage  string     `json:"error_message,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	ProviderState string     `json:"provider_state,omitempty"`
	ContainerID   string     `json:"container_id,omitempty"`
	// last_error_code (migration 018) is the short stable code —
	// "RATE_LIMITED", "INVALID_TOKEN", "MEDIA_UNREACHABLE",
	// "CONTAINER_NOT_READY" — surfaces in dashboards/retry-logic
	// without the human prose of error_message.
	LastErrorCode string `json:"last_error_code,omitempty"`

	// Optimistic-concurrency + audit timestamps (migration 012).
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublishJob is an append-only audit log. One row per publish attempt.
// post_targets.status is the source of truth for current state;
// publish_jobs is the attempt history for retry/debugging.
type PublishJob struct {
	ID            int64      `json:"id"`
	PostTargetID  int64      `json:"post_target_id"`
	Status        string     `json:"status"`
	AttemptNumber int        `json:"attempt_number"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// PostMediaAsset is an attachment linked to a Post (legacy pre-Step-7 table).
// Not to be confused with the Taglio 3.2 MediaAsset in asset.go.
type PostMediaAsset struct {
	ID        int64     `json:"id"`
	PostID    int64     `json:"post_id"`
	URL       string    `json:"url"`
	MimeType  string    `json:"mime_type,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// IdempotencyRecord stores the fingerprint of an idempotent POST request
// (level 1 of the two-level idempotency design — migration
// 021_idempotency_records.sql). Backed by the api_keys-style lookup
// pattern of "same key + same hash → return original; same key +
// different hash → 409".
//
// WorkspaceID + IdempotencyKey together form the lookup key (UNIQUE
// composite on the SQL table). ResourceType + ResourceID let the
// handler re-fetch the resource on replay — we deliberately do NOT
// cache the response body, instead relying on a re-render from
// resource_id. That's simpler (no body buffer in middleware) and
// keeps the cached payload tiny.
//
// RequestHash is []byte (raw 32 bytes of SHA-256 output), not a hex
// string — JSON serialisation will base64-encode it automatically.
// Storing as the raw bytes halves the storage cost vs hex and the
// lookup comparison uses bytes.Equal which is constant-time.
type IdempotencyRecord struct {
	ID             int64     `json:"id"`
	WorkspaceID    int64     `json:"workspace_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	ResourceType   string    `json:"resource_type"`
	ResourceID     int64     `json:"resource_id"`
	RequestHash    []byte    `json:"request_hash"`
	ResponseStatus int       `json:"response_status"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// OutboxEvent is a row in the transactional-outbox table. Written atomically
// inside the same transaction that mutates the aggregate, so downstream
// consumers never miss an event (no dual-write problem).
type OutboxEvent struct {
	ID            int64           `json:"id"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   int64           `json:"aggregate_id"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     time.Time       `json:"created_at"`
}

// CreateResult is the return value of PostRepository.Create. It is either a
// newly created post+targets (Duplicate=false) or a cached idempotent
// response (Duplicate=true with CachedBody set).
type CreateResult struct {
	Post       *Post
	Targets    []*PostTarget
	Duplicate  bool
	CachedBody []byte
}
