// Package auth / jwt_connectlink.go — connect-link OAuth state JWT.
//
// The P2 admin connect-link flow signs a short-lived JWT carrying
// the expected YouTube channel id inside the OAuth state. Extracted
// from jwt.go (split per responsabilità, 2026-08).
package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ConnectLinkStateClaims (P2 — admin connect-link) carries the
// expected YouTube channel id inside the OAuth state JWT.
// The full token is signed HS256 with the same secret as auth
// tokens, so the callback can verify via Manager without needing
// a browser cookie (the manager's browser is not involved).
//
// The state_type claim keyword makes the JWT self-identifying in
// the callback's verifyOAuthState path: a cookie-backed CSRF state
// nonce never has this keyword, so the callback cleanly branches
// on `if strings.Contains(state, ".")` (JWT shape) vs the
// legacy cookie path.
type ConnectLinkStateClaims struct {
	StateType         string `json:"stp"`
	ExpectedChannelID string `json:"ech"`
	jwt.RegisteredClaims
}

// IssueConnectLinkState signs a short-lived HS256 JWT carrying the
// expected_channel_id. TTL is 30 minutes — long enough for the
// manager to complete the OAuth flow on their browser, short enough
// that an intercepted URL has a tight replay window.
//
// Returns the signed JWT, the jti embedded inside it, and the exact
// JWT expiry time. The caller must persist the jti in a store that
// supports atomic single-use consumption so the link cannot be
// replayed, and it should use the returned expiry for that record so
// the database expiry cannot drift from the JWT expiry.
func (m *Manager) IssueConnectLinkState(expectedChannelID string) (string, string, time.Time, error) {
	if expectedChannelID == "" {
		return "", "", time.Time{}, errors.New("connect-link state: expected_channel_id is required")
	}
	jti, err := randomHex(16)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("connect-link state: jti generation: %w", err)
	}
	now := time.Now()
	expiresAt := now.Add(30 * time.Minute)
	claims := ConnectLinkStateClaims{
		StateType:         "connect_link",
		ExpectedChannelID: expectedChannelID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        jti,
		},
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(m.secret)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("connect-link state: sign: %w", err)
	}
	return signed, jti, expiresAt, nil
}

// VerifyConnectLinkState validates a state JWT and returns the
// parsed claims. Returns ErrMalformedConnectLinkState when the
// token isn't a JWT, doesn't carry the connect-link state_type,
// is expired, or has a signature mismatch.
//
// The returned claims contain the authoritative expected_channel_id
// and the jti (RegisteredClaims.ID). The callback MUST use the
// expected_channel_id for the expected_channel_id argument to
// attachDiscoveredAccounts so the channels.list(mine=true) result is
// filtered against the operator's intent. The caller must also
// consume the jti in its persistence store so the link can only be
// used once.
func (m *Manager) VerifyConnectLinkState(raw string) (*ConnectLinkStateClaims, error) {
	if raw == "" {
		return nil, ErrMalformedConnectLinkState
	}
	// Cheap shape check: a JWT has exactly 2 dots (header.payload.sig).
	// The cookie-backed state nonce has none. Skip the parse path
	// when the shape is wrong so callers don't get a JWT parse error
	// for a non-JWT state nonce.
	if strings.Count(raw, ".") != 2 {
		return nil, ErrMalformedConnectLinkState
	}
	claims := &ConnectLinkStateClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedConnectLinkState, err)
	}
	if !tok.Valid {
		return nil, ErrMalformedConnectLinkState
	}
	if claims.StateType != "connect_link" {
		return nil, fmt.Errorf("%w: state_type=%q", ErrMalformedConnectLinkState, claims.StateType)
	}
	if claims.ExpectedChannelID == "" {
		return nil, fmt.Errorf("%w: missing expected_channel_id", ErrMalformedConnectLinkState)
	}
	if claims.ID == "" {
		return nil, fmt.Errorf("%w: missing jti", ErrMalformedConnectLinkState)
	}
	return claims, nil
}

// ErrMalformedConnectLinkState is the canonical sentinel returned
// by VerifyConnectLinkState when the state param is not a JWT, not
// expirable, expired, doesn't carry state_type=connect_link, or
// signature mismatch. Wrapped errors.Is is what the callback uses
// to decide on a 4xx response (vs a 500 for unrelated parse failures).
var ErrMalformedConnectLinkState = errors.New("malformed connect-link state")
