// Package auth provides JWT issuance, verification, and HTTP middleware
// for InstaEditLogin. The Manager signs and verifies HS256 tokens using
// a symmetric secret loaded from the JWT_SECRET environment variable.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SessionCookieName is the HttpOnly cookie name that the dashboard SPA
// receives from POST /api/v1/auth/exchange (Taglio 1.2). The cookie value
// is a JWT signed by Manager.Issue, identical in format to the Bearer token
// used by API-key clients. The middleware (below) accepts either form.
const SessionCookieName = "session"

// contextKey is unexported so external packages cannot collide with our keys.
type contextKey string

const userIDKey contextKey = "user_id"

// Claims carries the user identity inside a signed JWT.
//
// SPRINT 1.1: WorkspaceID (json:"ws") is mandatory. Tokens issued by
// pre-SPRINT-1.1 builds did NOT carry this claim; the middleware
// rejects any token missing it with 401. Every Issuer call must pass
// a positive wsID (Manager.Issue enforces this).
type Claims struct {
	UserID      int64 `json:"uid"`
	WorkspaceID int64 `json:"ws"`
	jwt.RegisteredClaims
}

// Manager issues and verifies session tokens for users.
type Manager struct {
	secret []byte
	ttl    time.Duration
}

// NewManager constructs a Manager. ttlHours <= 0 falls back to a 7-day default.
func NewManager(secret string, ttlHours int) *Manager {
	if ttlHours <= 0 {
		ttlHours = 168
	}
	return &Manager{
		secret: []byte(secret),
		ttl:    time.Duration(ttlHours) * time.Hour,
	}
}

// Issue signs a JWT for (userID, workspaceID). It returns the encoded
// token, the JTI, the expiry timestamp, and any signing error.
//
// SPRINT 1.1: workspaceID is REQUIRED. A zero workspaceID is rejected.
// The middleware will not stamp any UserIdentity whose workspace claim
// is missing/zero — every authenticated request must have an active
// workspace resolved from real workspace_members. Issuers include
// AuthService (register auto-creates a personal workspace; login picks
// the user's first membership) and handleSwitchWorkspace (re-issues
// after a successful /workspaces/{id}/switch).
func (m *Manager) Issue(userID, workspaceID int64) (string, string, time.Time, error) {
	if userID <= 0 {
		return "", "", time.Time{}, fmt.Errorf("invalid user id: %d", userID)
	}
	if workspaceID <= 0 {
		return "", "", time.Time{}, fmt.Errorf("invalid workspace id: %d", workspaceID)
	}
	jti, err := randomJTI()
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("jti generation failed: %w", err)
	}
	now := time.Now()
	exp := now.Add(m.ttl)
	claims := Claims{
		UserID:      userID,
		WorkspaceID: workspaceID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        jti,
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("sign failed: %w", err)
	}
	return signed, jti, exp, nil
}

// Verify parses and validates a JWT, returning (userID, workspaceID)
// on success. A token missing WorkspaceID (issued by pre-SPRINT-1.1
// builds) is rejected — no implicit fallback to any default workspace.
//
// The two-value return replaces the pre-SPRINT-1.1 single uint64
// return; every caller that consumed the old Verify(raw) (int64, err)
// must update to the new shape.
func (m *Manager) Verify(raw string) (int64, int64, error) {
	if raw == "" {
		return 0, 0, errors.New("empty token")
	}
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return 0, 0, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return 0, 0, errors.New("invalid token")
	}
	if claims.UserID <= 0 {
		return 0, 0, errors.New("missing user id in claims")
	}
	if claims.WorkspaceID <= 0 {
		return 0, 0, errors.New("missing workspace id in claims (token pre-SPRINT-1.1 or invalid)")
	}
	return claims.UserID, claims.WorkspaceID, nil
}

// Middleware returns a handler that enforces auth. The contract is
// strictly: every request MUST carry a valid session — either an
// `Authorization: Bearer <jwt>` header (API-key / machine clients) OR a
// `Cookie: session=<jwt>` cookie (dashboard SPA, set by
// /api/v1/auth/exchange after Taglio 1.2).
//
// Anything else (missing both, wrong scheme, expired/invalid token) is
// rejected with 401 before the handler runs.
//
// Identity never comes from the request body, never from the query string,
// and never from a synthetic fallback user id. When API-key auth is added
// (Taglio 1.2+), API keys live in their own database table and their
// resolved user id is dropped into the same context key by a separate
// middleware sitting in front of this one.
//
// Use UserIDFromContext to retrieve the authenticated user id downstream.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1) Authorization: Bearer wins when present (API-key / non-browser
		//    clients; preserves the existing test suite which sets this
		//    header via withBearerJWT).
		//
		// Taglio 4.6 dual-auth: if the Bearer token looks like an API key
		// (sk_test_/sk_live_ prefix), the JWT middleware does NOT reject
		// it. The upstream auth.Authenticator.Middleware (mounted first
		// in Router.Setup) handles API-key authentication; if THAT chain
		// fails to authenticate (skipped or no key row), the JWT middleware
		// cannot recover — the request continues without an identity,
		// the handler reads IdentityFromContext and gets nil, fails
		// with 401 in the requireIdentity helper. We pass through here
		// so the JWT path's eager-rejection doesn't 401 valid API-key
		// requests.
		if header := r.Header.Get("Authorization"); header != "" {
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				http.Error(w, "invalid authorization header", http.StatusUnauthorized)
				return
			}
			raw := strings.TrimSpace(header[len(prefix):])
		if IsApiKeyBearer(raw) {
			// Defer to API-key middleware (mounted earlier) or fall
			// through to the handler with no identity. Never reject
			// here — the absence of a valid API-key row is not the
			// JWT chain's call.
			next.ServeHTTP(w, r)
			return
		}
		userID, wsID, err := m.Verify(raw)
		if err != nil {
			http.Error(w, "invalid or expired token", http.StatusUnauthorized)
			return
		}
		m.putIdentity(r, w, next, NewUserIdentity(userID, wsID))
		return
	}
	// 2) Fallback: HttpOnly session cookie set by /api/v1/auth/exchange
	//    (Taglio 1.2). The browser attaches it via credentials:'include'.
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		userID, wsID, err := m.Verify(c.Value)
		if err == nil && userID > 0 && wsID > 0 {
			m.putIdentity(r, w, next, NewUserIdentity(userID, wsID))
			return
		}
	}
		http.Error(w, "missing or invalid session", http.StatusUnauthorized)
	})
}

// putIdentity deposits an authenticated Identity into the context.
// Dual-writes: sets BOTH the userIDKey (preserving UserIDFromContext
// for legacy handler code) AND the identityCtxKey (consumed by the
// new IdentityFromContext). The dual-write lets this commit ship
// without touching every package that calls UserIDFromContext
// today; a future cleanup pass can drop the userIDKey once all
// callers have migrated to IdentityFromContext.
func (m *Manager) putIdentity(r *http.Request, w http.ResponseWriter, next http.Handler, id Identity) {
	ctx := WithIdentity(r.Context(), id)
	ctx = context.WithValue(ctx, userIDKey, id.UserID())
	next.ServeHTTP(w, r.WithContext(ctx))
}

// UserIDFromContext returns the authenticated user id placed by Middleware.
// The boolean is false when the request reached the handler without the
// middleware having authenticated a valid JWT.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(userIDKey).(int64)
	return v, ok
}

// WithUserID is preserved for legacy tests. SPRINT 1.1: callers should
// prefer WithIdentity(ctx, NewUserIdentity(uid, wsID)) and provide an
// explicit workspace_id — WithUserID alone leaves IdentityFromContext
// returning nil, which is rejected (401) by handlers that read the
// identity via IdentityFromContext. Tests using WithUserID for handlers
// that read IdentityFromContext must migrate to WithIdentity.
// Returning context.WithValue(userIDKey, ...) keeps UserIDFromContext
// callers (requireUserID-based handlers) working as before.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

func randomJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
