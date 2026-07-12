// Identity interface and helpers.
//
// Identity represents the authenticated principal making a request.
// It unifies the two authentication paths used by the project today:
//
//   * JWT (via auth.Manager.Middleware) — dashboard SPA + browser
//     flows authenticated by HttpOnly cookie or Authorization header.
//
//   * API key (via internal/auth.Authenticator) — machine-to-machine
//     clients authenticated by Authorization: Bearer sk_test_…/sk_live_….
//
// Both paths deposit an Identity in the request context via
// WithIdentity. Downstream code reads it with IdentityFromContext.
//
// Taglio 5c: tenant anchor is WorkspaceID (was OrgID). ProjectID removed
// — projects are not part of the minimum tenant model (users → workspaces →
// workspace_members + api_keys + platform_accounts).

package auth

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Identity hides the difference between a JWT-authenticated user and an
// API-key-authenticated machine client. Implementations: UserIdentity
// (JWT path) and ApiKeyIdentity (machine path).
type Identity interface {
	// IsAPIKey reports whether the principal IS an API key (vs a
	// JWT-authenticated user).
	IsAPIKey() bool

	// UserID returns the underlying user id. For a JWT identity
	// this is the dashboard user. For an ApiKeyIdentity this is
	// the row's created_by (the human who minted the key).
	UserID() int64

	// KeyID returns the api_key row id. Always 0 for JWT identities.
	KeyID() int64

	// WorkspaceID returns the tenant scope. For JWT-authenticated
	// dashboard users this is the workspace_id claim stamped on the JWT
	// by the Manager.IssueAccess path; for ApiKeyIdentity it is the
	// api_keys row's workspace_id. NEVER a hard-coded fallback — the
	// caller is expected to derive this from a real membership lookup
	// or the JWT claim before stamping the identity into the context.
	WorkspaceID() int64

	// SessionID returns the active sessions row id (JWT path only).
	// Zero for API-key identities. SPRINT 2.1+: every JWT carries
	// a positive session_id and the middleware refuses to stamp
	// a session-less identity, so a zero here is unambiguous.
	SessionID() int64

	// Permissions returns the raw permission set for API keys
	// (nil for JWT users).
	Permissions() []string

	// HasPermission reports whether this identity grants the named
	// permission. Implementations treat "admin" as the wildcard.
	HasPermission(p string) bool
}

// identityCtxKey is the unexported context-key type.
type identityCtxKey struct{}

// IdentityFromContext returns the Identity placed by the JWT or
// API-key middleware. Returns nil if no middleware ran.
func IdentityFromContext(ctx context.Context) Identity {
	v, _ := ctx.Value(identityCtxKey{}).(Identity)
	return v
}

// WithIdentity returns a derived context carrying the given Identity.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// No fallback workspace id is defined: JWT-derived identities MUST carry
// a workspace_id claim that came out of Manager.Issue(userID, wsID). The
// middleware refuses to stamp an identity without a valid workspace claim
// (a request with a JWT issued before SPRINT 1.1 will be rejected with 401
// and the user must re-authenticate to receive a workspace-bearing JWT).

// --- UserIdentity -----------------------------------------------------------

// UserIdentity is the Identity implementation for a JWT-authenticated
// dashboard user. SPRINT 2.1+ adds the SessionID field; tokens minted
// before that sprint carried no session_id claim and are rejected by
// Manager.Verify, so the zero default is unreachable in production.
type UserIdentity struct {
	uid int64
	ws  int64
	sid int64
}

// NewUserIdentity constructs a UserIdentity with explicit fields.
func NewUserIdentity(uid, ws, sid int64) UserIdentity {
	return UserIdentity{uid: uid, ws: ws, sid: sid}
}

// IsAPIKey implements Identity.
func (u UserIdentity) IsAPIKey() bool { return false }

// UserID implements Identity.
func (u UserIdentity) UserID() int64 { return u.uid }

// KeyID implements Identity.
func (u UserIdentity) KeyID() int64 { return 0 }

// WorkspaceID implements Identity.
func (u UserIdentity) WorkspaceID() int64 { return u.ws }

// SessionID implements Identity.
func (u UserIdentity) SessionID() int64 { return u.sid }

// Permissions implements Identity. JWT users have no permission array.
func (u UserIdentity) Permissions() []string { return nil }

// HasPermission implements Identity.
func (u UserIdentity) HasPermission(_ string) bool { return false }

// --- ApiKeyIdentity ---------------------------------------------------------

// ApiKeyIdentity is the Identity implementation for a machine-to-
// machine API key.
type ApiKeyIdentity struct {
	keyID       int64
	createdBy   int64
	wsID        int64
	permissions []string
}

// NewApiKeyIdentity constructs an ApiKeyIdentity from the canonical
// row fields.
func NewApiKeyIdentity(keyID, createdBy, wsID int64, permissions []string) ApiKeyIdentity {
	return ApiKeyIdentity{
		keyID:       keyID,
		createdBy:   createdBy,
		wsID:        wsID,
		permissions: permissions,
	}
}

// IsAPIKey implements Identity.
func (a ApiKeyIdentity) IsAPIKey() bool { return true }

// UserID implements Identity. Returns the human owner (created_by).
func (a ApiKeyIdentity) UserID() int64 { return a.createdBy }

// KeyID implements Identity.
func (a ApiKeyIdentity) KeyID() int64 { return a.keyID }

// WorkspaceID implements Identity.
func (a ApiKeyIdentity) WorkspaceID() int64 { return a.wsID }

// SessionID implements Identity. API keys are not associated with a
// session row, so this always returns 0. Required by the Identity
// interface as of SPRINT 2.1 — handlers may branch on whether the
// principal is JWT-authenticated (has a session) or an API key
// (does not).
func (a ApiKeyIdentity) SessionID() int64 { return 0 }

// Permissions implements Identity.
func (a ApiKeyIdentity) Permissions() []string { return a.permissions }

// HasPermission implements Identity. The "admin" wildcard behaviour
// mirrors models.ApiKey.HasPermission.
func (a ApiKeyIdentity) HasPermission(p string) bool {
	for _, perm := range a.permissions {
		if perm == models.PermissionAdmin {
			return true
		}
		if perm == p {
			return true
		}
	}
	return false
}
