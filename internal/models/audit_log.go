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
