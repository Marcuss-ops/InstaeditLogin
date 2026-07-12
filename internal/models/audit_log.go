package models

import "time"

// Audit log action constants.
const (
	AuditActionLogin                = "login"
	AuditActionLogout               = "logout"
	AuditActionRefreshFailed        = "refresh_failed"
	AuditActionPlatformConnected    = "platform_connected"
	AuditActionPlatformReconnected  = "platform_reconnected"
	AuditActionPlatformDisconnected = "platform_disconnected"
	AuditActionPlatformRevoked      = "platform_revoked"
	AuditActionTokenExpired         = "token_expired"
	AuditActionValidationFailed     = "validation_failed"
	AuditActionPublishRequested     = "publish_requested"
	AuditActionRetryManual          = "retry_manual"
	AuditActionSessionRevoked       = "session_revoked"
	AuditActionDataExport           = "data_export"
	AuditActionAccountDeleted       = "account_deleted"

	// Taglio 4.6 — API key lifecycle events. Emitted from the
	// apikeys handlers (pkg/api/apikeys.go) on create / revoke /
	// rotate. ResourceID is the affected api_key.id. Metadata
	// (JSON-shaped, see audit_log_repo.go) carries the org_id,
	// the key_prefix (the visible slice, NOT the full secret)
	// and the human-readable name; organisation-level grouping
	// on the audit table itself arrives with the platform
	// tenant-filter migration (016) in a future Taglio.
	AuditActionApiKeyCreated = "api_key.created"
	AuditActionApiKeyRevoked = "api_key.revoked"
	AuditActionApiKeyRotated = "api_key.rotated"
)

// Audit log result constants.
const (
	AuditResultSuccess = "success"
	AuditResultFailure = "failure"
	AuditResultSkipped = "skipped"
)

// AuditLog represents a single security-relevant event.
type AuditLog struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceID   int64     `json:"resource_id,omitempty"`
	Result       string    `json:"result,omitempty"`
	IPHash       string    `json:"ip_hash,omitempty"`
	Metadata     Metadata  `json:"metadata,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
