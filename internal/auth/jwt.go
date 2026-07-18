// Package auth provides JWT issuance, verification, and HTTP middleware
// for InstaEditLogin. SPRINT 2.1 extends the Manager with two TTLs
// (access vs refresh) and a SessionID claim that ties a short-lived
// access JWT to a row in the `sessions` table.
//
// Issuers in this codebase (AuthService.Register/Login,
// handleExchangeCode, handleSwitchWorkspace) must create a session row
// BEFORE calling IssueAccess so the JWT carries
// a positive session_id. A token with a missing/zero session_id is
// rejected by Verify — this is a forced re-auth for all tokens minted
// pre-SPRINT-2.1.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SessionCookieName is the HttpOnly cookie name for the short-lived
// access JWT. The SPA's frontend uses credentials:'include' so the
// browser attaches it automatically; document.cookie cannot see it.
const SessionCookieName = "session"

// RefreshCookieName carries the opaque refresh token.
const RefreshCookieName = "refresh"

// CSRFTokenCookieName is intentionally NOT HttpOnly so the SPA can
// read the value via document.cookie and echo it on write requests.
const CSRFTokenCookieName = "csrf_token"

// CSRFHeader is the request header the SPA must echo CSRFTokenCookieName
// into on every non-safe request (POST/PUT/DELETE/PATCH).
const CSRFHeader = "X-CSRF-Token"

// contextKey is unexported so external packages cannot collide with our keys.
type contextKey string

const (
	userIDKey    contextKey = "user_id"
	sessionIDKey contextKey = "session_id"
)

// Claims carries the user identity inside a signed JWT.
//
// SPRINT 2.1 adds SessionID (json:"sid") to tie a token to a row in
// the sessions table. Tokens minted before SPRINT 2.1 do NOT have
// this claim and will be rejected by Verify — forcing all existing
// users to re-authenticate.
//
// Blocco #5.2 adds Env (json:"env") so a token minted under
// AppEnv=A is rejected (with explicit 401) by Manager running
// under AppEnv=B. Verified by Manager.Verify when the Manager
// has been configured with WithEnv() (production wiring); when
// the Manager has no env set (test fixtures, post-NewManager
// chainable call missing), the env check is skipped so the
// existing test suite keeps working without per-test env
// plumbing. Tokens minted before Blocco #5.2 do NOT have the
// env claim; their Verify call falls under the same skip path
// (manager.env == ""), so the rollout is silent — but skipping
// is the wrong long-term posture for production. Callers wiring
// the real binary MUST chain WithEnv(cfg.AppEnv) at construction
// time (see internal/bootstrap.Wire).
type Claims struct {
	UserID      int64  `json:"uid"`
	WorkspaceID int64  `json:"ws"`
	SessionID   int64  `json:"sid"`
	Env         string `json:"env,omitempty"`
	jwt.RegisteredClaims
}

// Manager issues and verifies session tokens.
type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	issuer     string
	audience   string
	// env (Blocco #5.2) is the AppEnv this process is running in.
	// When non-empty, every Issue stamps claims.Env with this value
	// and Verify rejects tokens whose env claim differs. When
	// empty (the test-default), the env check is skipped —
	// preserving backwards-compat for the existing 17+ .NewManager
	// call sites that don't care about cross-env partitions.
	env string
}

// NewManager constructs a Manager. Variadic for backward-compat:
//
//	NewManager(secret)                       // 15m access / 30d refresh defaults
//	NewManager(secret, ttlHours)             // legacy 2-arg: ttlHours for access
//	NewManager(secret, accessTTL, refreshTTL) // SPRINT 2.1+ form
//
// accessTTL <= 0 falls back to 15 min; refreshTTL <= 0 falls back to 30 days.
// issuer/audience are stamped on every JWT's RegisteredClaims (iss / aud).
func NewManager(secret string, ttls ...interface{}) *Manager {
	var accessTTL, refreshTTL time.Duration
	switch len(ttls) {
	case 0:
		accessTTL = 15 * time.Minute
		refreshTTL = 30 * 24 * time.Hour
	case 1:
		switch v := ttls[0].(type) {
		case int:
			accessTTL = time.Duration(v) * time.Hour
			refreshTTL = 30 * 24 * time.Hour
		case time.Duration:
			accessTTL = v
			refreshTTL = 30 * 24 * time.Hour
		default:
			accessTTL = 15 * time.Minute
			refreshTTL = 30 * 24 * time.Hour
		}
	default:
		if v, ok := ttls[0].(time.Duration); ok {
			accessTTL = v
		}
		if v, ok := ttls[1].(time.Duration); ok {
			refreshTTL = v
		}
	}
	if accessTTL <= 0 {
		accessTTL = 15 * time.Minute
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	return &Manager{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		issuer:     "instaeditlogin",
		audience:   "api",
	}
}

// WithEnv (Blocco #5.2) configures the Manager to stamp every
// issued token with `env` and to reject every verified token whose
// env claim differs. Builder form so existing callers (and the 17+
// test fixtures using NewManager directly) remain untouched;
// production bootstrap.Wire chains WithEnv(cfg.AppEnv) once at
// startup. Passing an empty env disables the check (equivalent to
// not calling WithEnv at all) — useful for tests that mint and
// verify tokens in the same env (or no env at all).
func (m *Manager) WithEnv(env string) *Manager {
	m.env = env
	return m
}

// Env returns the env the Manager was configured with ("" when
// WithEnv was not called). Exposed for tests that need to confirm
// a Manager's env-binding before asserting on Verify behaviour.
func (m *Manager) Env() string { return m.env }

// NewManagerWithHours keeps the pre-SPRINT-2.1 constructor usable
// at its original name. Maps ttlHours to the access TTL; refresh
// TTL stays at 30d.
func NewManagerWithHours(secret string, ttlHours int) *Manager {
	return NewManager(secret, ttlHours)
}

// AccessTTL returns the access-token TTL.
func (m *Manager) AccessTTL() time.Duration { return m.accessTTL }

// RefreshTTL returns the refresh-token TTL.
func (m *Manager) RefreshTTL() time.Duration { return m.refreshTTL }

// IssueAccess signs a short-lived JWT. Returns the encoded token, the
// JTI, the expiry timestamp, and any signing error. sessionID MUST
// be > 0; a zero sessionID is rejected so we never mint a token
// that the middleware would later 401.
func (m *Manager) IssueAccess(userID, wsID, sessionID int64) (string, string, time.Time, error) {
	if userID <= 0 || wsID <= 0 || sessionID <= 0 {
		return "", "", time.Time{}, fmt.Errorf("invalid ids: user=%d ws=%d session=%d", userID, wsID, sessionID)
	}
	jti, err := randomHex(16)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("jti generation failed: %w", err)
	}
	now := time.Now()
	exp := now.Add(m.accessTTL)
	claims := Claims{
		UserID:      userID,
		WorkspaceID: wsID,
		SessionID:   sessionID,
		Env:         m.env, // Blocco #5.2 — empty string is omitempty (legacy tokens minted before Blocco #5.2 carry no env claim)
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
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

func (m *Manager) Issue(userID int64, rest ...int64) (string, string, time.Time, error) {
	wsID, sessionID := int64(0), int64(0)
	switch len(rest) {
	case 1:
		wsID = rest[0]
	case 2:
		wsID, sessionID = rest[0], rest[1]
	}
	if userID <= 0 || wsID <= 0 || sessionID <= 0 {
		return "", "", time.Time{}, fmt.Errorf("auth: Issue requires all three IDs to be > 0 (got user=%d ws=%d session=%d); use IssueAccess after creating a sessions row via SessionsService.Start", userID, wsID, sessionID)
	}
	jti, err := randomHex(16)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("jti generation failed: %w", err)
	}
	now := time.Now()
	exp := now.Add(m.accessTTL)
	claims := Claims{
		UserID:      userID,
		WorkspaceID: wsID,
		SessionID:   sessionID, // guaranteed > 0 (early-return above)
		Env:         m.env,     // Blocco #5.2 — same as IssueAccess
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   fmt.Sprintf("%d", userID),
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
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

// IssueRefresh generates a cryptographically-secure opaque refresh
// token. Returns the plaintext (to put in the cookie) and its SHA-256
// hash (to persist on the sessions row). exp is now + refreshTTL.
func (m *Manager) IssueRefresh() (plain string, hash []byte, exp time.Time, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, time.Time{}, fmt.Errorf("rand: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	return plain, sum[:], time.Now().Add(m.refreshTTL), nil
}

// HashRefreshToken returns the SHA-256 of the plaintext refresh token
// for the cookie-path lookup. Exposed so callers can hash a cookie
// value without going through IssueRefresh.
func HashRefreshToken(plaintext string) []byte {
	if plaintext == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(plaintext))
	return sum[:]
}

// Verify parses + validates a JWT, returning (userID, workspaceID,
// sessionID, err). Tokens with a missing/zero sessionID are rejected
// — this is a forced re-auth for pre-SPRINT-2.1 tokens. The 4-tuple
// is a breaking change versus the pre-SPRINT-2.1 3-tuple; callers
// must update.
func (m *Manager) Verify(raw string) (int64, int64, int64, error) {
	if raw == "" {
		return 0, 0, 0, errors.New("empty token")
	}
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	})
	if err != nil {
		return 0, 0, 0, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return 0, 0, 0, errors.New("invalid token")
	}
	if claims.UserID <= 0 {
		return 0, 0, 0, errors.New("missing user id in claims")
	}
	if claims.WorkspaceID <= 0 {
		return 0, 0, 0, errors.New("missing workspace id in claims")
	}
	if claims.SessionID <= 0 {
		return 0, 0, 0, errors.New("missing session id in claims (pre-SPRINT-2.1 or invalid)")
	}
	// Blocco #5.2 — cross-environment rejection. When the
	// Manager was configured with an env (via WithEnv at
	// bootstrap time), every verified token must carry the same
	// env. Tokens minted under env A that arrive on a process
	// running env B are rejected with the canonical sentinel
	// error so the middleware can write an explicit 401 body
	// (separately distinguishable from generic signature /
	// expiry failures). Verifies where Manager.env == "" (the
	// test-default) skip this check.
	if m.env != "" && claims.Env != m.env {
		return 0, 0, 0, errCrossEnvMismatch
	}
	return claims.UserID, claims.WorkspaceID, claims.SessionID, nil
}

// errCrossEnvMismatch (Blocco #5.2) is the canonical sentinel
// returned by Manager.Verify when a token's env claim does not
// match the Manager's configured env. Middleware inspects this
// error with errors.Is to emit an explicit 401 body; other
// failure modes (sig mismatch, expiry, malformed) keep the
// generic 401 message so the explicit rejection remains
// distinguishable to operators reading the Sentry / log feed.
var errCrossEnvMismatch = errors.New("token environment mismatch: explicit cross-env rejection")

// Middleware returns a handler that enforces auth.
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
			uid, wsID, sid, err := m.Verify(raw)
			if err != nil {
				writeVerifyError(w, err) // Blocco #5.2: differentiate explicit cross-env 401 from generic 401
				return
			}
			m.putIdentity(r, w, next, NewUserIdentity(uid, wsID, sid))
			return
		}
		if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
			uid, wsID, sid, err := m.Verify(c.Value)
			if err == nil && uid > 0 && wsID > 0 && sid > 0 {
				m.putIdentity(r, w, next, NewUserIdentity(uid, wsID, sid))
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

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RandomHex is the exported alias used by callers (e.g. SessionsService)
// that need to generate opaque identifiers without importing crypto
// packages directly.
func RandomHex(n int) (string, error) { return randomHex(n) }
