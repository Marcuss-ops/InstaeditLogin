package models

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// PostStatus is the lifecycle of a Post (or PostTarget). Mirrors the
// Postgres enum `post_status` introduced by migration 003_posts_workspaces.sql.
//
// String-based enum pattern lets us flow PostStatus values through
// encoding/json (default string serialization) and through database/sql
// (Scan/Value implementations) without bespoke marshalers.
type PostStatus string

// Lifecycle values. Persisted as-is by Value/Scan.
const (
	PostStatusDraft     PostStatus = "draft"
	PostStatusScheduled PostStatus = "scheduled"
	PostStatusPublishing PostStatus = "publishing"
	PostStatusPublished PostStatus = "published"
	PostStatusFailed    PostStatus = "failed"
)

// IsValid reports whether s is one of the defined PostStatus values.
func (s PostStatus) IsValid() bool {
	switch s {
	case PostStatusDraft,
		PostStatusScheduled,
		PostStatusPublishing,
		PostStatusPublished,
		PostStatusFailed:
		return true
	default:
		return false
	}
}

// String returns the underlying string value.
func (s PostStatus) String() string { return string(s) }

// Value implements driver.Valuer so PostStatus can flow as a parameter into
// a Postgres query (lib/pq expects driver.Value types: string, int64,
// float64, bool, []byte, time.Time, or nil).
func (s PostStatus) Value() (driver.Value, error) {
	return string(s), nil
}

// Scan implements sql.Scanner so PostStatus can be parsed from a Postgres
// column. lib/pq returns text columns as string or []byte depending on
// driver/database settings; we accept both.
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
//
// Nullable fields (ScheduledAt) use `*time.Time` matching the existing
// Token.ExpiresAt convention. Title/Caption/MediaURL are omitted from JSON
// when empty to keep API responses compact.
type Post struct {
	ID          int64      `json:"id"`
	WorkspaceID int64      `json:"workspace_id"`
	Title       string     `json:"title,omitempty"`
	Caption     string     `json:"caption,omitempty"`
	MediaURL    string     `json:"media_url,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	Status      PostStatus `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
}

// PostTarget represents the fan-out of a Post to a specific platform account.
// A single Post has 1..N PostTargets, each with its own lifecycle — this
// allows partial success (some platforms published, others failed) without
// losing per-platform error context.
type PostTarget struct {
	ID                int64      `json:"id"`
	PostID            int64      `json:"post_id"`
	PlatformAccountID int64      `json:"platform_account_id"`
	Status            PostStatus `json:"status"`
	PlatformPostID    string     `json:"platform_post_id,omitempty"`
	ErrorMessage      string     `json:"error_message,omitempty"`
	PublishedAt       *time.Time `json:"published_at,omitempty"`
}
