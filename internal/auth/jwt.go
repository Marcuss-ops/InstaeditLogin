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
//
// File layout (split per responsabilità, 2026-08):
//
//	jwt.go            — types + constructors + TTL accessors (this file)
//	jwt_issue.go      — IssueAccess / IssueAccessWithJTI / Issue /
//	                    IssueRefresh / HashRefreshToken / IssueAccessAdmin
//	jwt_verify.go     — Verify / VerifyWithAdmin + errCrossEnvMismatch
//	jwt_middleware.go — Middleware + writeVerifyError + context helpers
//	jwt_random.go     — randomHex / RandomHex
//	jwt_connectlink.go— ConnectLinkStateClaims issue/verify + sentinel
package auth

import (
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
// the real binary MUST chain WithEnv(cfg.HTTP.AppEnv) at construction
// time (see internal/bootstrap.Wire).
type Claims struct {
	UserID      int64  `json:"uid"`
	WorkspaceID int64  `json:"ws"`
	SessionID   int64  `json:"sid"`
	Env         string `json:"env,omitempty"`
	// Admin (P2 — ops dashboard) gates /admin/* endpoints via
	// requireAdmin() middleware. Stamped at Issue* time from the
	// caller-supplied isAdmin bool. omitempty so legacy tokens
	// minted before P2 carry admin=false (the safe default).
	// Operators bootstrap via cmd/grant-admin --email <email>; the
	// next Issue starts minting admin=true tokens for that user.
	Admin bool `json:"adm,omitempty"`
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
// production bootstrap.Wire chains WithEnv(cfg.HTTP.AppEnv) once at
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
