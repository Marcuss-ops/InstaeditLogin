package models

import "time"

// ApiKeyEnvironment values — mirror the environment column accepted
// by migration 017_api_keys.sql. TEXT column (not Postgres enum) so
// adding new environments (e.g. "staging") doesn't require an
// ALTER TYPE migration; the application is the source of truth.
const (
	ApiKeyEnvironmentTest = "test"
	ApiKeyEnvironmentLive = "live"
)

// IsApiKeyEnvironment reports whether env is one of the recognised
// environment values. Validation lives in the application (not in
// a SQL CHECK constraint) so callers can extend the set without a
// DB migration; pkg/api/apikeys.go and the Create endpoint refuse
// unknown values before they reach the database.
func IsApiKeyEnvironment(env string) bool {
	return env == ApiKeyEnvironmentTest || env == ApiKeyEnvironmentLive
}

// Permission constants — the capability strings stored on api_keys.
const (
	// PermissionRead — safe GET-only audit (accounts list, post list).
	// Default for first-time API key creation.
	PermissionRead = "read"

	// PermissionWrite — create/update posts, schedule, cancel.
	PermissionWrite = "write"

	// PermissionPublish — full publish path.
	PermissionPublish = "publish"

	// PermissionMedia — presigned uploads via /api/v1/media/presign.
	PermissionMedia = "media"

	// PermissionAccountsManage — connect/disconnect platform accounts.
	PermissionAccountsManage = "accounts"

	// PermissionAdmin — every route, including key minting and revoking.
	PermissionAdmin = "admin"
)

// DefaultApiKeyPermissions is the permission set assigned to a freshly
// minted API key when the request body omits permissions.
var DefaultApiKeyPermissions = []string{PermissionRead}

// AllKnownApiKeyPermissionValues is the validation set for incoming
// Create requests AND for the rotate endpoint's incoming permissions list.
var AllKnownApiKeyPermissionValues = map[string]struct{}{
	PermissionRead:           {},
	PermissionWrite:          {},
	PermissionPublish:        {},
	PermissionMedia:          {},
	PermissionAccountsManage: {},
	PermissionAdmin:          {},
}

// ValidateApiKeyPermissions reports whether every perm is in the known set.
func ValidateApiKeyPermissions(perms []string) (ok bool, unknown string) {
	for _, p := range perms {
		if _, known := AllKnownApiKeyPermissionValues[p]; !known {
			return false, p
		}
	}
	return true, ""
}

// ApiKey is the persistent record for a workspace-scoped API key.
// Mirrors the api_keys table introduced by migration 017_api_keys.sql.
//
// IMPORTANT: this struct deliberately does NOT carry a plaintext
// field. The plaintext is shown to the request originator exactly
// once (POST /api/v1/api-keys) and is never persisted or returned
// in GET. All server-side operations work with the SHA-256 hash
// (key_hash) for lookups and the visible prefix (key_prefix) for display.
//
// Taglio 5c: tenant anchor is WorkspaceID (was OrganizationID).
// ProjectID removed — projects are not part of the minimum tenant model.
type ApiKey struct {
	ID          int64      `json:"id"`
	WorkspaceID int64      `json:"workspace_id"`
	CreatedBy   int64      `json:"created_by"`
	Name        string     `json:"name"`
	Environment string     `json:"environment"`
	KeyPrefix   string     `json:"key_prefix"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// HasPermission reports whether the API key grants the named permission.
// Wildcard check: "admin" satisfies every per-route requirement;
// otherwise the named permission must be present literally.
func (k *ApiKey) HasPermission(p string) bool {
	if k == nil {
		return false
	}
	for _, perm := range k.Permissions {
		if perm == PermissionAdmin {
			return true
		}
		if perm == p {
			return true
		}
	}
	return false
}

// IsActive reports whether an API key row is currently usable for authentication.
func (k *ApiKey) IsActive(now time.Time) bool {
	if k == nil {
		return false
	}
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !k.ExpiresAt.IsZero() && now.After(*k.ExpiresAt) {
		return false
	}
	return true
}
