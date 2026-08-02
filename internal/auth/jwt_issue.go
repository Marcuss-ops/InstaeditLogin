// Package auth / jwt_issue.go — token issuance.
//
// Issue* methods on Manager + the refresh-token helpers. Extracted
// from jwt.go (split per responsabilità, 2026-08): issuance stays
// in this file, verification in jwt_verify.go, HTTP middleware in
// jwt_middleware.go, connect-link state in jwt_connectlink.go.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

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
	return m.IssueAccessWithJTI(userID, wsID, sessionID, jti)
}

// IssueAccessWithJTI signs a short-lived JWT using the caller-supplied
// JTI. This lets SessionsService persist the same access_jti that is
// embedded in the token, preserving the invariant
// sessions.access_jti == JWT claims.jti.
func (m *Manager) IssueAccessWithJTI(userID, wsID, sessionID int64, jti string) (string, string, time.Time, error) {
	if userID <= 0 || wsID <= 0 || sessionID <= 0 {
		return "", "", time.Time{}, fmt.Errorf("invalid ids: user=%d ws=%d session=%d", userID, wsID, sessionID)
	}
	if jti == "" {
		return "", "", time.Time{}, errors.New("jti required")
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

// IssueAccessAdmin (P2) signs a JWT with the admin claim set. Same
// semantics as IssueAccess + an extra isAdmin arg. Use this for the
// admin user's session tokens; non-admin users continue to use
// IssueAccess (admin=false). The 4th arg is the on-the-wire
// representation of users.is_admin (read by cmd/grant-admin or the
// followup POST /admin/users/{id}/grant-admin).
func (m *Manager) IssueAccessAdmin(userID, wsID, sessionID int64, isAdmin bool) (string, string, time.Time, error) {
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
		Env:         m.env,
		Admin:       isAdmin,
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
