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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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

// Middleware returns a handler that enforces JWT auth.
//
// Two modes are supported, selected by the `strict` flag (wired at startup
// from the STRICT_JWT_AUTH env var; default true since the SPA ships JWT-aware):
//
//	STRICT MODE (default; STRICT_JWT_AUTH=true):
//	  - Missing Authorization header           → 401 missing authorization header
//	  - Authorization header without Bearer  → 401 invalid authorization header
//	  - Bearer with invalid/expired JWT       → 401 invalid or expired token
//	  - Bearer with valid JWT                  → user_id placed in ctx, calls next
//
//	LEGACY FALLBACK (STRICT_JWT_AUTH=false):
//	  Reserved for the AUTH MIGRATION ROLLBACK WINDOW. When the JWT-aware
//	  frontend is being rolled out, the old SPA clients don't send
//	  Authorization and would otherwise be locked out. In legacy mode the
//	  middleware accepts the request and emits a slog.Warn line so ops can
//	  measure how many legacy callers are still hitting the API.
//
//	  SECURITY: in legacy mode `resolveUserID` falls back to the
//	  `user_id` field on the request body/query, which means anyone who
//	  knows an integer id can publish as that user. NEVER run legacy mode
//	  in production once the new frontend is fully rolled out.
//
// Use UserIDFromContext to retrieve the authenticated user id downstream.
// The boolean is false when the request reached the handler without the
// middleware having run (or having accepted a legacy request).
func (m *Manager) Middleware(strict bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			if strict {
				http.Error(w, "missing authorization header", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			if strict {
				http.Error(w, "invalid authorization header", http.StatusUnauthorized)
				return
			}
			slog.Warn("auth: invalid Authorization header but STRICT_JWT_AUTH is off; allowing legacy request")
			next.ServeHTTP(w, r)
			return
		}
		raw := strings.TrimSpace(header[len(prefix):])
		userID, err := m.Verify(raw)
		if err != nil {
			if strict {
				slog.Info("auth: rejecting request with invalid token", "error", err)
				http.Error(w, "invalid or expired token", http.StatusUnauthorized)
				return
			}
			slog.Warn("auth: invalid JWT but STRICT_JWT_AUTH is off; allowing legacy request", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFromContext returns the authenticated user id placed by Middleware.
// The boolean is false when the request reached the handler without the
// middleware having run (or having accepted a legacy request).
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
// header) so the JWT is verified. Only call WithUserID from *_test.go
// files (e.g. to test resolveUserID / requireUserOrDefault without
// standing up a full JWT round-trip).
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
