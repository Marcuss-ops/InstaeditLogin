// Package auth / jwt_middleware.go — HTTP middleware + context keys.
//
// Manager.Middleware, the error mapping, the context-identity
// stamping and the context accessors. Extracted from jwt.go (split
// per responsabilità, 2026-08). Note: contextKey + the two key
// constants also live here because they are only consumed by this
// file's helpers (putIdentity / UserIDFromContext / SessionIDFromContext /
// WithUserID).
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// contextKey is unexported so external packages cannot collide with our keys.
type contextKey string

const (
	userIDKey    contextKey = "user_id"
	sessionIDKey contextKey = "session_id"
)

// Middleware returns a handler that enforces auth. P2: this is the
// call site that resolves the admin claim and threads it into the
// context-stamped Identity so downstream handlers (requireAdmin,
// /admin/*) can branch on IsAdmin() without a re-parse.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header := r.Header.Get("Authorization"); header != "" {
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				http.Error(w, "invalid authorization header", http.StatusUnauthorized)
				return
			}
			raw := strings.TrimSpace(header[len(prefix):])
			if IsApiKeyBearer(raw) {
				next.ServeHTTP(w, r)
				return
			}
			uid, wsID, sid, isAdmin, err := m.VerifyWithAdmin(raw)
			if err != nil {
				writeVerifyError(w, err) // Blocco #5.2: differentiate explicit cross-env 401 from generic 401
				return
			}
			m.putIdentity(r, w, next, NewUserIdentityWithAdmin(uid, wsID, sid, isAdmin))
			return
		}
		if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
			uid, wsID, sid, isAdmin, err := m.VerifyWithAdmin(c.Value)
			if err == nil && uid > 0 && wsID > 0 && sid > 0 {
				m.putIdentity(r, w, next, NewUserIdentityWithAdmin(uid, wsID, sid, isAdmin))
				return
			}
		}
		http.Error(w, "missing or invalid session", http.StatusUnauthorized)
	})
}

// writeVerifyError (Blocco #5.2) maps a Verify-failure error to a
// 401 body. Cross-env mismatches use the explicit "environment
// mismatch" body so an operator watching the response can
// distinguish a token that arrived at the wrong deployment from
// a forged/expired/malformed token. All other failure modes keep
// the generic "invalid or expired token" body so the explicit
// rejection stays distinguishable in logs.
func writeVerifyError(w http.ResponseWriter, err error) {
	if errors.Is(err, errCrossEnvMismatch) {
		http.Error(w, errCrossEnvMismatch.Error(), http.StatusUnauthorized)
		return
	}
	http.Error(w, "invalid or expired token", http.StatusUnauthorized)
}

func (m *Manager) putIdentity(r *http.Request, w http.ResponseWriter, next http.Handler, id Identity) {
	ctx := WithIdentity(r.Context(), id)
	ctx = context.WithValue(ctx, userIDKey, id.UserID())
	ctx = context.WithValue(ctx, sessionIDKey, id.SessionID())
	next.ServeHTTP(w, r.WithContext(ctx))
}

// UserIDFromContext returns the authenticated user id placed by
// Middleware.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(userIDKey).(int64)
	return v, ok
}

// SessionIDFromContext returns the session id placed by Middleware.
// Returns (0, false) when the request was authenticated via API key
// or when no auth ran.
func SessionIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(sessionIDKey).(int64)
	return v, ok
}

// WithUserID test-only helper, see identity.go.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}
