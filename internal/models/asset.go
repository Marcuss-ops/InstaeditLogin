package models

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// MediaAssetStatus is the lifecycle of a media asset. Mirrors the
// `status` TEXT column on media_assets (Taglio 3.2 migration 006).
//
// Lifecycle:
//
//	pending → ready   (POST /complete succeeded; HEAD verified the S3 object)
//	       → failed   (HEAD failed, size mismatch, or content-type mismatch)
//	       → expired  (expires_at < NOW() OR cleanup job marked it)
//
// The status field is a TEXT column (not a Postgres ENUM) so future
// statuses can be added without a migration — the application code
// uses IsValid() for explicit validation, the DB stores any string.
type MediaAssetStatus string

const (
	MediaAssetStatusPending MediaAssetStatus = "pending"
	MediaAssetStatusReady   MediaAssetStatus = "ready"
	MediaAssetStatusFailed  MediaAssetStatus = "failed"
	MediaAssetStatusExpired MediaAssetStatus = "expired"
)

// IsValid reports whether s is one of the defined MediaAssetStatus values.
func (s MediaAssetStatus) IsValid() bool {
	switch s {
	case MediaAssetStatusPending,
		MediaAssetStatusReady,
		MediaAssetStatusFailed,
		MediaAssetStatusExpired:
		return true
	default:
		return false
	}
}

// Value implements driver.Valuer.
func (s MediaAssetStatus) Value() (driver.Value, error) {
	return string(s), nil
}

// Scan implements sql.Scanner.
func (s *MediaAssetStatus) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*s = ""
		return nil
	case string:
		*s = MediaAssetStatus(v)
		return nil
	case []byte:
		*s = MediaAssetStatus(string(v))
		return nil
	default:
		return fmt.Errorf("models: cannot scan MediaAssetStatus from %T", src)
	}
}

// MediaAsset is the server-side record of a presigned-uploaded media
// file. The actual bytes live in S3 (key = upload_key); this table
// tracks ownership, content metadata, and verification status.
//
// ID is a UUID (not a BIGSERIAL) because /presign exposes asset_id to
// the client before the asset is bound to a post — using a UUID
// prevents enumeration of pending assets.
//
// Security contract (Taglio 3.2): the only URL the publish flow ever
// passes to a per-platform provider is built from this row's
// upload_key, via the StorageProvider.AssetURL() method. The URL is
// always an internal S3 URL pointing at our own bucket — no
// user-controlled URL can ever reach the platform API.
type MediaAsset struct {
	ID           string           `json:"id"`
	UserID       int64            `json:"user_id"`
	UploadKey    string           `json:"upload_key"`
	ContentType  string           `json:"content_type"`
	SizeBytes    int64            `json:"size_bytes"`
	Status       MediaAssetStatus `json:"status"`
	SHA256       string           `json:"sha256,omitempty"`
	ErrorMessage string           `json:"error_message,omitempty"`
	ExpiresAt    time.Time        `json:"expires_at"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}
