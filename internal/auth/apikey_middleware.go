// API-key authentication middleware.
//
// This file extends internal/auth/apikey.go (the helpers — Generate,
// Hash, ParseFullKey, IsApiKeyBearer) with the HTTP-side wiring:
// Authenticator.Middleware. When the request has Authorization:
// Bearer sk_test_… or sk_live_…, the middleware hashes the secret,
// looks up the row, enforces active state + tenant scope, and
// deposits an ApiKeyIdentity in the request context.
//
// For all OTHER requests (no Authorization header, Cookie-only
// requests, non-sk_ Bearer tokens), the middleware passes through
// unchanged — the existing JWT middleware (Manager.Middleware)
// picks up the request and authenticates against JWT / cookie.
// The two middlewares compose: Authenticator first (or after,
// they're commutative), then Manager.Middleware, then the handler.
//
// SECURITY invariants enforced here:
//
//   - Constant-time-like refusal for unknown hashes: a missing row
//     or bad prefix returns the SAME "invalid api key" message.
//     An attacker can't enumerate by status code.
//   - Active-state gate BEFORE identity injection: revoked_at set
//     OR expires_at past → 401. The lookup is wasted but the
//     "activeness check" is the security-relevant step.
//   - Tenant scope is implicit (the row IS the tenant — the OrgID
//     dropped into the context is the row's organization_id).
//     Cross-tenant access is impossible at this layer because
//     there is no org filter to forget.
//   - MarkUsed is best-effort: an error logging the bump does NOT
//     abort the request. The auth decision is already made; the
//     last_used_at column is operator UX.

package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ApiKeyLookup is the storage contract for the Authenticator
// middleware. Decoupled from the concrete *repository.ApiKeyRepository
// so tests can pass a fake.
type ApiKeyLookup interface {
	FindByHash(hash []byte) (*models.ApiKey, error)
	MarkUsed(orgID, id int64) error
}

// Authenticator wires the API-key path of the dual auth chain.
// Constructable from a real repository or an in-memory test fake.
type Authenticator struct {
	Repo ApiKeyLookup
	Now  func() time.Time // injectable for tests; default = time.Now
}

// NewApiKeyAuthenticator constructs an Authenticator against the
// given lookup. Now defaults to time.Now if nil.
func NewApiKeyAuthenticator(repo ApiKeyLookup) *Authenticator {
	return &Authenticator{Repo: repo, Now: time.Now}
}

// Middleware returns the http.Handler that performs API-key
// authentication. Confirmation of order in Router.Setup:
// Authenticator first → Manager.Middleware → handler.
//
// Requests where the Authorization header either is missing or
// doesn't match `Bearer sk_test_…/sk_live_…` are passed through
// unchanged. The wrapping Manager.Middleware then runs the JWT
// verify, and if THAT can't authenticate either (no Bearer JWT,
// no cookie, both fail), the request is rejected with 401.
//
// On success, the middleware deposits an ApiKeyIdentity in the
// request context via WithIdentity. Best-effort last_used_at bump
// via Repo.MarkUsed runs synchronously — no goroutine spawn; the
// query is fast (single-row UPDATE keyed by PK + org_id) and the
// table is small enough that synchronous blocking is acceptable
// for the eventual-consistency win operators get on the dashboard.
//
// The Now injection point is hoisted once per Middleware call:
// the nil-default is resolved before the closure is built, so the
// per-request cost is one indirect call (no per-request nil-check
// branch).
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	now := a.Now
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			next.ServeHTTP(w, r)
			return
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		raw := strings.TrimSpace(header[len(prefix):])
		if !IsApiKeyBearer(raw) {
			next.ServeHTTP(w, r)
			return
		}
		_, secret, err := ParseFullKey(raw)
		if err != nil {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}
		hash := Hash(secret)
		if hash == nil {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}
		key, err := a.Repo.FindByHash(hash)
		if err != nil {
			http.Error(w, "authentication failed", http.StatusInternalServerError)
			return
		}
		if key == nil {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}
		if !key.IsActive(now()) {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}
		id := NewApiKeyIdentity(key.ID, key.CreatedBy, key.OrganizationID, key.ProjectID, key.Permissions)
		ctx := WithIdentity(r.Context(), id)
		_ = a.Repo.MarkUsed(id.OrgID(), id.KeyID())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
