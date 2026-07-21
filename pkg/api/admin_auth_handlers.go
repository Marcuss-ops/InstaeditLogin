package api

import (
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// adminAuthMiddleware is the /admin/* authz gate. Mounted AFTER
// Manager.Middleware on each /admin/* route so the request context
// already carries the Identity. The handler enforces:
//
//  1. Identity must be present (Manager.Middleware either
//     deposited a UserIdentity or rejected with 401 — a missing
//     Identity here is a routing bug, not a user error, and the
//     401 fallback keeps the failure mode consistent).
//  2. Identity.IsAdmin() must be true. API-key identities never
//     satisfy this (ApiKeyIdentity.IsAdmin returns false) —
//     operator endpoints are JWT-user only. The dashboard SPA's
//     bootstrap operator mints a JWT with claims.Admin=true after
//     cmd/grant-admin --email <email> flips users.is_admin.
//
// Status codes:
//   - 401 unauthorized: no Identity (no JWT, no API key, or a
//     malformed token that passed Manager.Middleware's permissive
//     route — defensive only).
//   - 403 forbidden: Identity present but IsAdmin()==false. A
//     regular user attempting to read /admin/* is a non-2xx
//     response they can't act on; the body explicitly says "admin
//     only" so a misconfigured SPA surfaces the right error.
func adminAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := auth.IdentityFromContext(r.Context())
		if id == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !id.IsAdmin() {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminAuth is the same as adminAuthMiddleware but adapted to the
// Router.protected-style pattern (handler wrapper instead of
// middleware). Used by the http.HandlerFunc-style handlers in this
// file (adminChannels / adminQueue / adminHealth) so they don't
// need to repeat the authz check inline.
func (r *Router) adminAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := auth.IdentityFromContext(req.Context())
		if id == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !id.IsAdmin() {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		h(w, req)
	}
}
