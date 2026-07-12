package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// PostStatus is the lifecycle of a Post (or PostTarget). Mirrors the
// Postgres enum `post_status` introduced by migration 003_posts_workspaces.sql
// and extended by migration 010_publish_jobs_and_queued_status.sql.
//
// Lifecycle:
//
//	draft → queued → publishing → published
//	                           → partially_published
//	                           → waiting_provider
//	                           → failed
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
		PostStatusWaitingProvider:
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
type PostTarget struct {
	ID                int64      `json:"id"`
	PostID            int64      `json:"post_id"`
	PlatformAccountID int64      `json:"platform_account_id"`
	Status            PostStatus `json:"status"`
	PlatformPostID    string     `json:"platform_post_id,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	PublishedAt       *time.Time `json:"published_at,omitempty"`
	ProviderState     string     `json:"provider_state,omitempty"`
	ContainerID       string     `json:"container_id,omitempty"`
	Version           int64      `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
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

// IdempotencyRecord stores the fingerprint of an idempotent POST request.
// original_post_id is NULL at insert time and set after the post is created.
type IdempotencyRecord struct {
	ID             int64     `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	WorkspaceID    int64     `json:"workspace_id"`
	RequestHash    string    `json:"request_hash"`
	ResponseStatus int       `json:"response_status"`
	OriginalPostID *int64    `json:"original_post_id,omitempty"`
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
