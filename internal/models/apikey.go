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
// The Bearer middleware (commit 3) reads the api_key row, sees the
// permissions []string, and refuses the request if the route's
// required permission is missing. The set is intentionally small
// and grows by additive commits only — never remove a value once
// a client starts using it (clients cached dashboards might still
// reference it).
const (
	// PermissionRead — safe GET-only audit (accounts list, post list,
	// metrics, …). Default for first-time API key creation.
	PermissionRead = "read"

	// PermissionWrite — create/update posts, schedule, cancel. Does
	// NOT include publish — keys with write but no publish can stage
	// content but not push it live.
	PermissionWrite = "write"

	// PermissionPublish — full publish path (single-post or
	// publish-all). Always issued alongside write in practice; kept
	// distinct so a "staging"-only key can have write without
	// publish.
	PermissionPublish = "publish"

// PermissionMedia — presigned uploads via /api/v1/media/presign.
// Bare name (no resource:action) matches the rest of the permission
// set; finer-grained actions (media.list, media.delete) can be
// added as separate constants when needed. The AllKnownApiKeyPermissionValues
// map (and the SQL GIN index on permissions) supports any of them
// transparently — once they are added to the known set here, the
// validators accept them and the index covers the lookup.
PermissionMedia = "media"

// PermissionAccountsManage — connect/disconnect platform accounts,
// refresh OAuth tokens, run account validations. Bare name for
// consistency with the rest of the permission set; finer-grained
// actions like account.connect / account.disconnect land in a future
// commit if and when the surface needs to split.
PermissionAccountsManage = "accounts"

	// PermissionAdmin — every route, including key minting and
	// revoking. Org owners only; never issued to third-party
	// integrations.
	PermissionAdmin = "admin"
)

// DefaultApiKeyPermissions is the permission set assigned to a freshly
// minted API key when the request body omits permissions. Read-only
// is intentionally conservative — operators must consciously opt in
// to publish/accounts-manage/admin by listing them in the request.
var DefaultApiKeyPermissions = []string{PermissionRead}

// AllKnownApiKeyPermissionValues is the validation set for incoming
// Create requests AND for the rotate endpoint's incoming permissions
// list. Unknown values are rejected with HTTP 422 before persistence
// so an intentional typo doesn't silently grant no permission after
// normalisation (which would be a footgun).
var AllKnownApiKeyPermissionValues = map[string]struct{}{
	PermissionRead:            {},
	PermissionWrite:           {},
	PermissionPublish:         {},
	PermissionMedia:           {},
	PermissionAccountsManage:  {},
	PermissionAdmin:           {},
}

// ValidateApiKeyPermissions reports whether every perm is in the
// known set. Returns the first unknown value as a hint for the
// error message; empty slice is treated as valid (defaults will
// apply at the repository layer).
func ValidateApiKeyPermissions(perms []string) (ok bool, unknown string) {
	for _, p := range perms {
		if _, known := AllKnownApiKeyPermissionValues[p]; !known {
			return false, p
		}
	}
	return true, ""
}

// ApiKey is the persistent record for a tenant API key. Mirrors the
// api_keys table introduced by migration 017_api_keys.sql.
//
// IMPORTANT: this struct deliberately does NOT carry a plaintext
// field. The plaintext is shown to the request originator exactly
// once (POST /api/v1/api-keys) and is never persisted or returned
// in GET. All server-side operations work with the SHA-256 hash
// (key_hash) for lookups and the visible prefix (key_prefix) for
// display.
//
// CreatedAt/UpdatedAt use the same convention as the other models
// (time.Time, JSON-encoded as ISO 8601). RevokedAt/ExpiresAt are
// nullable so an unset revocation or no-expiry policy both surface
// as JSON `null` (not omitted) — the dashboard distinguishes
// "active forever" from "metadata not yet loaded".
type ApiKey struct {
	ID             int64      `json:"id"`
	OrganizationID int64      `json:"organization_id"`
	ProjectID      *int64     `json:"project_id"`
	CreatedBy      int64      `json:"created_by"`
	Name           string     `json:"name"`
	Environment    string     `json:"environment"`
	KeyPrefix      string     `json:"key_prefix"`
	Permissions    []string   `json:"permissions"`
	ExpiresAt      *time.Time `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// HasPermission reports whether the API key grants the named permission.
// Wildcard check: "admin" satisfies every per-route requirement;
// otherwise the named permission must be present literally.
//
// Called from the Bearer middleware (commit 3) when the route is
// wrapped with requireApiKeyPermission(...) and from the GET
// /api-keys response handler when emitting a hint to the dashboard
// about what the key can do.
func (k *ApiKey) HasPermission(p string) bool {
	if k == nil {
		return false
	}
	for _, perm := range k.Permissions {
		if perm == PermissionAdmin {
			return true // admin is the explicit wildcard
		}
		if perm == p {
			return true
		}
	}
	return false
}

// IsActive reports whether an API key row is currently usable for
// authentication. A key is inactive when revoked_at is set OR
// expires_at is in the past. Used both by the repository layer
// (FindActiveByHash) and by tests asserting state transitions.
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
