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
type Claims struct {
	UserID int64 `json:"uid"`
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

// Issue signs a JWT for userID. It returns the encoded token, the JTI, the
// expiry timestamp, and any signing error.
func (m *Manager) Issue(userID int64) (string, string, time.Time, error) {
	if userID <= 0 {
		return "", "", time.Time{}, fmt.Errorf("invalid user id: %d", userID)
	}
	jti, err := randomJTI()
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("jti generation failed: %w", err)
	}
	now := time.Now()
	exp := now.Add(m.ttl)
	claims := Claims{
		UserID: userID,
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

// Verify parses and validates a JWT, returning the user id on success.
func (m *Manager) Verify(raw string) (int64, error) {
	if raw == "" {
		return 0, errors.New("empty token")
	}
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return 0, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return 0, errors.New("invalid token")
	}
	if claims.UserID <= 0 {
		return 0, errors.New("missing user id in claims")
	}
	return claims.UserID, nil
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
		if header := r.Header.Get("Authorization"); header != "" {
			const prefix = "Bearer "
			if !strings.HasPrefix(header, prefix) {
				http.Error(w, "invalid authorization header", http.StatusUnauthorized)
				return
			}
			raw := strings.TrimSpace(header[len(prefix):])
			userID, err := m.Verify(raw)
			if err != nil {
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}
			m.putUser(r, w, next, userID)
			return
		}
		// 2) Fallback: HttpOnly session cookie set by /api/v1/auth/exchange
		//    (Taglio 1.2). The browser attaches it via credentials:'include'.
		if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
			userID, err := m.Verify(c.Value)
			if err == nil && userID > 0 {
				m.putUser(r, w, next, userID)
				return
			}
		}
		http.Error(w, "missing or invalid session", http.StatusUnauthorized)
	})
}

func (m *Manager) putUser(r *http.Request, w http.ResponseWriter, next http.Handler, userID int64) {
	ctx := context.WithValue(r.Context(), userIDKey, userID)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// UserIDFromContext returns the authenticated user id placed by Middleware.
// The boolean is false when the request reached the handler without the
// middleware having authenticated a valid JWT.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(userIDKey).(int64)
	return v, ok
}

// WithUserID returns a derived context carrying the given user id. The
// inverse of UserIDFromContext.
//
// SECURITY: test-only. Calling this from a production handler silently
// bypasses JWT auth — the request reaches the handler with a
// context-asserted user id but no real authentication. Production
// handlers MUST obtain the user id from Middleware (via the Authorization
// header OR the session cookie) so the JWT is verified. Only call
// WithUserID from *_test.go files (e.g. to test requireUserID /
// handleCreatePost without standing up a full JWT round-trip).
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
