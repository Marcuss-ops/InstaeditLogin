// Identity interface and helpers.
//
// Identity represents the authenticated principal making a request.
// It unifies the two authentication paths used by the project today:
//
//   * JWT (via auth.Manager.Middleware) — dashboard SPA + browser
//     flows authenticated by HttpOnly cookie or Authorization header.
//     The underlying "user" is the dashboard user; OrgID is a
//     fallback that defaults to DefaultFallbackOrgID until the
//     organization_members table lands.
//
//   * API key (via internal/auth.Authenticator, defined alongside
//     this file in apikey_middleware.go) — machine-to-machine
//     clients authenticated by Authorization: Bearer sk_test_…/sk_live_….
//     The underlying principal IS the key itself; OrgID is the row's
//     organization_id, ProjectID is the row's project_id, Permissions
//     is the row's permissions array.
//
// Both paths deposit an Identity in the request context via
// WithIdentity. Downstream code reads it with IdentityFromContext.
// Existing handlers that only need the user id can fall back to
// UserIDFromContext (a thin wrapper that bridges to the new
// identity context key) — see jwt.go for the migration path.
//
// Design rationale (Taglio 4.6): separate the two interfaces
// (IsAPIKey / UserID / KeyID) so a handler can dispatch on the
// principal type. E.g. /api/v1/accounts/{id} would refuse API-key
// requests for some handlers and accept them for others; the
// dispatch is explicit at the call site rather than buried in
// middleware composition.

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
	// JWT-authenticated user). Most handlers serve both; a few —
	// e.g. /api/v1/auth/exchange which mints user sessions — must
	// only accept JWT (no "treat an API key as a user").
	IsAPIKey() bool

	// UserID returns the underlying user id. For a JWT identity
	// this is the dashboard user. For an ApiKeyIdentity this is
	// the row's created_by (the human who minted the key) — useful
	// for audit-logging "who acted" even on machine requests. 0
	// only when neither side has a real user (theoretically
	// possible for org-scoped service keys, never today).
	UserID() int64

	// KeyID returns the api_key row id. Always 0 for JWT identities.
	KeyID() int64

	// OrgID returns the tenant scope. The repository layer enforces
	// every query against this value (Taglio 4.5+ tenant filter
	// contract). For JWT users today this is DefaultFallbackOrgID
	// until organization_members lands; for ApiKeyIdentity it is
	// the row's organization_id.
	OrgID() int64

	// ProjectID returns the optional project scope. nil for JWT
	// identities and for org-wide API keys.
	ProjectID() *int64

	// Permissions returns the raw permission set for API keys
	// (nil for JWT users — they have the implicit "owner" of
	// their own resources). Use HasPermission for the wildcard
	// check.
	Permissions() []string

	// HasPermission reports whether this identity grants the named
	// permission. Implementations treat "admin" as the wildcard.
	HasPermission(p string) bool
}

// identityCtxKey is the unexported context-key type whose value can
// only be set by WithIdentity in this package — same package-tying
// pattern as the existing userIDKey in jwt.go. Handlers outside the
// auth package can read via IdentityFromContext but cannot collide.
type identityCtxKey struct{}

// IdentityFromContext returns the Identity placed by the JWT or
// API-key middleware. Returns nil if no middleware ran.
func IdentityFromContext(ctx context.Context) Identity {
	v, _ := ctx.Value(identityCtxKey{}).(Identity)
	return v
}

// WithIdentity returns a derived context carrying the given Identity.
// SECURITY: equivalent to auth.WithUserID — never call this from
// production handlers; the JWT/API-key middleware is the only
// legitimate producer of an authenticated context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// DefaultFallbackOrgID is the org id stamped onto JWT-derived
// identities today. Tomorrow's organization_members table will
// remove this fallback — once it exists, JWT identities will look
// up the user's actual membership row at login / refresh and stamp
// the right OrgID. Until then, every request through a JWT cookie
// or Authorization: Bearer JWT is treated as belonging to the
// "default" org. Seed data (db/migrations/seeds/001_seed_dev.sql
// — directory listing varies by compose layout) creates a row at
// id=1 named "Default Tenant"; this constant MUST stay in sync
// with that seed. If the seed renumbers the default tenant, this
// constant MUST be updated in the same change.
const DefaultFallbackOrgID int64 = 1

// --- UserIdentity -----------------------------------------------------------

// UserIdentity is the Identity implementation for a JWT-authenticated
// dashboard user. Created by auth.Manager.Middleware after a
// successful JWT verify (or HttpOnly cookie parse) and deposited in
// the context.
//
// OrgID is set to DefaultFallbackOrgID. A future Taglio will replace
// this constant with a repository lookup against organization_members
// (when that table lands); the Identity contract stays unchanged so
// downstream handlers do not need to refactor at that point.
//
// Fields are lowercase so they don't shadow the exported accessor
// methods that implement the Identity interface (Go disallows a
// field and a same-named method on the same struct because they
// share a name-resolution namespace).
type UserIdentity struct {
	uid int64
	org int64
}

// NewUserIdentity constructs a UserIdentity with explicit fields.
// Constructor-style (instead of struct literal) so call sites use
// stable names; matches the ApiKeyIdentity constructor below.
func NewUserIdentity(uid, org int64) UserIdentity {
	return UserIdentity{uid: uid, org: org}
}

// IsAPIKey implements Identity.
func (u UserIdentity) IsAPIKey() bool { return false }

// UserID implements Identity.
func (u UserIdentity) UserID() int64 { return u.uid }

// KeyID implements Identity.
func (u UserIdentity) KeyID() int64 { return 0 }

// OrgID implements Identity.
func (u UserIdentity) OrgID() int64 { return u.org }

// ProjectID implements Identity.
func (u UserIdentity) ProjectID() *int64 { return nil }

// Permissions implements Identity. JWT users have no permission array
// — they get the implicit "owner of resources they created" via the
// existing OwnerID/created_by checks (workspace, post, etc.). The
// HasPermission method returns false here so a future "deny by
// permission flag on user" can be added without re-plumbing handlers.
func (u UserIdentity) Permissions() []string { return nil }

// HasPermission implements Identity.
func (u UserIdentity) HasPermission(_ string) bool { return false }

// --- ApiKeyIdentity ---------------------------------------------------------

// ApiKeyIdentity is the Identity implementation for a machine-to-
// machine API key. Created by auth.Authenticator.Middleware after
// a successful hash lookup against the api_keys table.
//
// Field names are LOWERCASE so they don't shadow the exported
// accessor methods that implement the Identity interface (Go
// disallows a field and a same-named method on the same struct
// because they share a name-resolution namespace). The middleware
// populates the struct via NewApiKeyIdentity (constructor below);
// downstream code reads via the exported methods.
type ApiKeyIdentity struct {
	keyID       int64
	createdBy   int64
	orgID       int64
	projectID   *int64
	permissions []string
}

// NewApiKeyIdentity constructs an ApiKeyIdentity from the canonical
// row fields. Constructor-style (instead of struct literal) so call
// sites use stable names; matches the UserIdentity constructor above.
func NewApiKeyIdentity(keyID, createdBy, orgID int64, projectID *int64, permissions []string) ApiKeyIdentity {
	return ApiKeyIdentity{
		keyID:       keyID,
		createdBy:   createdBy,
		orgID:       orgID,
		projectID:   projectID,
		permissions: permissions,
	}
}

// IsAPIKey implements Identity.
func (a ApiKeyIdentity) IsAPIKey() bool { return true }

// UserID implements Identity. Returns the human owner (created_by)
// so "who acted" reports work the same for machine and human calls.
func (a ApiKeyIdentity) UserID() int64 { return a.createdBy }

// KeyID implements Identity.
func (a ApiKeyIdentity) KeyID() int64 { return a.keyID }

// OrgID implements Identity.
func (a ApiKeyIdentity) OrgID() int64 { return a.orgID }

// ProjectID implements Identity.
func (a ApiKeyIdentity) ProjectID() *int64 { return a.projectID }

// Permissions implements Identity.
func (a ApiKeyIdentity) Permissions() []string { return a.permissions }

// HasPermission implements Identity. The "admin" wildcard behaviour
// mirrors models.ApiKey.HasPermission — admin keys access every route
// regardless of the per-route required permission. References
// models.PermissionAdmin directly because internal/models is already
// a dependency of this package (see apikey.go).
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
