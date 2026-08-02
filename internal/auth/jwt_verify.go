// Package auth / jwt_verify.go — token verification.
//
// Verify / VerifyWithAdmin + the cross-env sentinel. Extracted from
// jwt.go (split per responsabilità, 2026-08). Issuance lives in
// jwt_issue.go, HTTP middleware in jwt_middleware.go.
package auth

import (
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Verify parses + validates a JWT, returning (userID, workspaceID,
// sessionID, err). Tokens with a missing/zero sessionID are rejected
// — this is a forced re-auth for pre-SPRINT-2.1 tokens. The 4-tuple
// is preserved across the P2 admin-claim addition: callers that don't
// care about the admin claim continue binding 4 vars. To surface the
// P2 admin claim, use VerifyWithAdmin (returns a 5-tuple). A single
// underlying parse means both Verify and VerifyWithAdmin apply the
// same env / sig / expiry checks before the bool diff lands.
func (m *Manager) Verify(raw string) (int64, int64, int64, error) {
	uid, wsID, sid, _, err := m.VerifyWithAdmin(raw)
	return uid, wsID, sid, err
}

// VerifyWithAdmin (P2) is the 5-tuple counterpart to Verify. Same
// parse + sig + env + expiry checks; additionally returns the admin
// claim so /admin/* middleware can branch on it without a re-parse.
// Manager.Middleware calls this; non-admin paths use plain Verify
// to keep the call-site shape unchanged.
func (m *Manager) VerifyWithAdmin(raw string) (int64, int64, int64, bool, error) {
	if raw == "" {
		return 0, 0, 0, false, errors.New("empty token")
	}
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return 0, 0, 0, false, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return 0, 0, 0, false, errors.New("invalid token")
	}
	if claims.UserID <= 0 {
		return 0, 0, 0, false, errors.New("missing user id in claims")
	}
	if claims.WorkspaceID <= 0 {
		return 0, 0, 0, false, errors.New("missing workspace id in claims")
	}
	if claims.SessionID <= 0 {
		return 0, 0, 0, false, errors.New("missing session id in claims (pre-SPRINT-2.1 or invalid)")
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
		return 0, 0, 0, false, errCrossEnvMismatch
	}
	return claims.UserID, claims.WorkspaceID, claims.SessionID, claims.Admin, nil
}

// errCrossEnvMismatch (Blocco #5.2) is the canonical sentinel
// returned by Manager.Verify when a token's env claim does not
// match the Manager's configured env. Middleware inspects this
// error with errors.Is to emit an explicit 401 body; other
// failure modes (sig mismatch, expiry, malformed) keep the
// generic 401 message so the explicit rejection remains
// distinguishable to operators reading the Sentry / log feed.
var errCrossEnvMismatch = errors.New("token environment mismatch: explicit cross-env rejection")
