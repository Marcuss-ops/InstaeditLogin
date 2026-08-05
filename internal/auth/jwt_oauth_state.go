// Package auth / jwt_oauth_state.go — signed OAuth flow state.
//
// The YouTube OAuth Client Pool login flow (pkg/api/auth_handlers.go
// handleLogin) replaces the cookie-backed CSRF nonce with a short-lived
// HS256-signed JWT when a pool registry is configured. The state carries
// the pool client that MUST be used for the code→token exchange in the
// callback (oauth_client_key), the optional channel binding hint
// (expected_channel_id), the workspace the operator is connecting from
// (workspace_id) and a single-use nonce (jti) persisted in the same
// ConnectLinkNonceStore that guards admin connect-links.
//
// Keeping the state signed (HMAC, same secret as auth tokens) + short
// TTL + single-use jti means the callback never re-selects a pool
// client: the client that built the consent URL is the one that must
// exchange the code, and Google rejects an exchange against a different
// client_id. See jwt_connectlink.go for the sibling connect-link state.
package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OAuthFlowStateTTL is the validity window of a signed OAuth flow
// state. Mirrors the legacy cookie-backed oauthStateMaxAge (10 min):
// long enough for the operator to complete Google's consent dialog,
// short enough that an intercepted URL has a tight replay window.
const OAuthFlowStateTTL = 10 * time.Minute

// OAuthFlowStateClaims carries the login-time intent inside the OAuth
// state JWT for the YouTube OAuth Client Pool flow.
//
//	stp  "oauth_flow"           — self-identifying state type keyword
//	ock  oauth_client_key       — pool client that MUST exchange the code
//	ech  expected_channel_id    — optional channel binding hint (UC...)
//	ws   workspace_id           — workspace the operator is connecting from
//	jti  RegisteredClaims.ID    — single-use nonce (persisted + consumed)
//
// The state_type keyword makes the JWT self-identifying in the
// callback's resolveCallbackState path: connect-link states carry
// "connect_link", oauth-flow states "oauth_flow", and the legacy
// cookie-backed CSRF nonce has no dots at all.
type OAuthFlowStateClaims struct {
	StateType         string `json:"stp"`
	OAuthClientKey    string `json:"ock"`
	ExpectedChannelID string `json:"ech"`
	WorkspaceID       int64  `json:"ws"`
	jwt.RegisteredClaims
}

// IssueOAuthFlowState signs a short-lived HS256 JWT carrying the pool
// client key, optional expected_channel_id and the caller's
// workspace_id. TTL is OAuthFlowStateTTL (10 minutes).
//
// Returns the signed JWT, the jti embedded inside it, and the exact JWT
// expiry time. The caller must persist the jti in a single-use store
// (the ConnectLinkNonceStore already wired for admin connect-links) so
// the state cannot be replayed, using the returned expiry for that
// record so the database expiry cannot drift from the JWT expiry.
func (m *Manager) IssueOAuthFlowState(oauthClientKey, expectedChannelID string, workspaceID int64) (string, string, time.Time, error) {
	if oauthClientKey == "" {
		return "", "", time.Time{}, errors.New("oauth flow state: oauth_client_key is required")
	}
	if workspaceID < 0 {
		return "", "", time.Time{}, errors.New("oauth flow state: workspace_id must not be negative")
	}
	jti, err := randomHex(16)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("oauth flow state: jti generation: %w", err)
	}
	now := time.Now()
	expiresAt := now.Add(OAuthFlowStateTTL)
	claims := OAuthFlowStateClaims{
		StateType:         "oauth_flow",
		OAuthClientKey:    oauthClientKey,
		ExpectedChannelID: expectedChannelID,
		WorkspaceID:       workspaceID,
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
		return "", "", time.Time{}, fmt.Errorf("oauth flow state: sign: %w", err)
	}
	return signed, jti, expiresAt, nil
}

// VerifyOAuthFlowState validates an OAuth flow state JWT and returns
// the parsed claims. Returns ErrMalformedOAuthFlowState when the token
// isn't a JWT, doesn't carry state_type=oauth_flow, is expired, has a
// signature mismatch, lacks the oauth_client_key claim, or carries a
// negative workspace_id.
//
// The returned claims contain the authoritative oauth_client_key the
// callback MUST use for the code→token exchange, the expected_channel_id
// binding hint and the workspace_id. The caller must also consume the
// jti (RegisteredClaims.ID) in the single-use store so the state can
// only be used once.
func (m *Manager) VerifyOAuthFlowState(raw string) (*OAuthFlowStateClaims, error) {
	if raw == "" {
		return nil, ErrMalformedOAuthFlowState
	}
	// Cheap shape check: a JWT has exactly 2 dots (header.payload.sig).
	// The cookie-backed state nonce has none. Skip the parse path when
	// the shape is wrong so callers don't get a JWT parse error for a
	// non-JWT state nonce.
	if strings.Count(raw, ".") != 2 {
		return nil, ErrMalformedOAuthFlowState
	}
	claims := &OAuthFlowStateClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (interface{}, error) {
		if t.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return m.secret, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithAudience(m.audience), jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedOAuthFlowState, err)
	}
	if !tok.Valid {
		return nil, ErrMalformedOAuthFlowState
	}
	if claims.StateType != "oauth_flow" {
		return nil, fmt.Errorf("%w: state_type=%q", ErrMalformedOAuthFlowState, claims.StateType)
	}
	if claims.OAuthClientKey == "" {
		return nil, fmt.Errorf("%w: missing oauth_client_key", ErrMalformedOAuthFlowState)
	}
	if claims.WorkspaceID < 0 {
		return nil, fmt.Errorf("%w: negative workspace_id", ErrMalformedOAuthFlowState)
	}
	if claims.ID == "" {
		return nil, fmt.Errorf("%w: missing jti", ErrMalformedOAuthFlowState)
	}
	return claims, nil
}

// ErrMalformedOAuthFlowState is the canonical sentinel returned by
// VerifyOAuthFlowState when the state param is not a JWT, not expirable,
// expired, doesn't carry state_type=oauth_flow, has a signature
// mismatch, or is missing a required claim. Wrapped errors.Is is what
// the callback uses to decide on a 4xx response (vs a 500 for unrelated
// parse failures).
var ErrMalformedOAuthFlowState = errors.New("malformed oauth flow state")
