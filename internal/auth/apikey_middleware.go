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
//
// Taglio 5c: tenant anchor is WorkspaceID (was OrgID). ProjectID removed.
//
// SECURITY invariants enforced here:
//
//   - Constant-time-like refusal for unknown hashes.
//   - Active-state gate BEFORE identity injection.
//   - Tenant scope is implicit (the row IS the tenant — the WorkspaceID
//     dropped into the context is the row's workspace_id).
//   - MarkUsed is best-effort: an error logging the bump does NOT
//     abort the request.

package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ApiKeyLookup is the storage contract for the Authenticator middleware.
type ApiKeyLookup interface {
	FindByHash(hash []byte) (*models.ApiKey, error)
	MarkUsed(wsID, id int64) error
}

// Authenticator wires the API-key path of the dual auth chain.
type Authenticator struct {
	Repo ApiKeyLookup
	Now  func() time.Time // injectable for tests; default = time.Now
	// ExpectedEnvironment optionally binds the authenticator to the
	// deployment environment. Empty preserves test/legacy wiring;
	// production wiring sets live for APP_ENV=production and test
	// otherwise, preventing cross-environment key replay.
	ExpectedEnvironment string
}

// NewApiKeyAuthenticator constructs an Authenticator against the given lookup.
func NewApiKeyAuthenticator(repo ApiKeyLookup) *Authenticator {
	return &Authenticator{Repo: repo, Now: time.Now}
}

// NewApiKeyAuthenticatorForEnvironment constructs an authenticator bound
// to one deployment environment (test or live).
func NewApiKeyAuthenticatorForEnvironment(repo ApiKeyLookup, environment string) *Authenticator {
	return &Authenticator{
		Repo:                repo,
		Now:                 time.Now,
		ExpectedEnvironment: environment,
	}
}

// Middleware returns the http.Handler that performs API-key authentication.
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
		environment, _, err := ParseFullKey(raw)
		if err != nil || (a.ExpectedEnvironment != "" && environment != a.ExpectedEnvironment) {
			http.Error(w, "invalid api key", http.StatusUnauthorized)
			return
		}
		// ApiKey creation persists the hash of the complete plaintext key
		// (including sk_test_/sk_live_). Hash the same canonical value at
		// lookup time; hashing only the suffix would make every valid key
		// fail authentication.
		hash := Hash(raw)
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
		id := NewApiKeyIdentity(key.ID, key.CreatedBy, key.WorkspaceID, key.Permissions)
		ctx := WithIdentity(r.Context(), id)
		_ = a.Repo.MarkUsed(id.WorkspaceID(), id.KeyID())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
