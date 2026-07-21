package veloxclient

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ExpectedIssuer/Audience mirror the constants in VeloxEditiingg's
// internal/instaeditauth/verifier.go. They are duplicated here (not
// imported) because InstaeditLogin and VeloxEditiingg are separate
// repositories with no shared Go module. A mismatch between these
// constants and the Velox verifier's would surface as a 401 at the
// first BFF call — a deliberate fail-fast rather than a silent
// cross-service drift.
const (
	expectedIssuer   = "instaedit"
	expectedAudience = "velox"
	// tokenTTL is the JWT lifetime. The spec recommends 2-5 minutes;
	// 3 minutes gives enough margin for a single BFF request round-trip
	// without keeping the token valid long enough to be replayed.
	tokenTTL = 3 * time.Minute
)

// defaultScopes is the full scope set the InstaEdit BFF requests. The
// Velox verifier checks that every endpoint's required scope is
// present; issuing the full set up-front avoids per-call scope
// negotiation while still being narrow (no admin scopes).
var defaultScopes = []string{
	"velox:jobs:read",
	"velox:jobs:write",
	"velox:workers:read",
	"velox:assets:read",
}

// signControlToken issues a short-lived HS256 JWT for the InstaEdit→
// Velox internal control plane. The secret is the
// VELOX_CONTROL_JWT_SECRET shared between the two services (distinct
// from the reverse-direction VELOX_API_TOKEN). userID becomes the
// JWT subject (sub); workspaceID becomes the workspace_id claim.
//
// The token is fresh per call (random jti, exp = now + tokenTTL) so
// a replay would require intercepting and reusing within the 3-minute
// window. Velox does not implement jti replay protection in this
// phase; callers that need it should layer a jti blacklist on top.
//
// IMPLEMENTATION NOTE: we use jwt.MapClaims (not a custom struct
// embedding jwt.RegisteredClaims) because the Velox verifier's Claims
// struct expects `aud` as a plain string and `exp` as an int64.
// jwt.RegisteredClaims marshals `aud` as a JSON array (ClaimStrings)
// and `exp` as a NumericDate — both incompatible with the Velox
// verifier's manual JSON unmarshal. MapClaims marshals string values
// as strings and int64 values as integers, matching the verifier
// exactly. MapClaims also already implements jwt.Claims, so
// jwt.NewWithClaims and jwt.ParseWithClaims work without custom
// getter methods.
func signControlToken(secret []byte, userID, workspaceID int64) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("veloxclient: control JWT secret is empty (VELOX_CONTROL_JWT_SECRET not configured)")
	}
	if userID <= 0 || workspaceID <= 0 {
		return "", fmt.Errorf("veloxclient: invalid identity (user=%d workspace=%d)", userID, workspaceID)
	}
	jti, err := randomJTI()
	if err != nil {
		return "", fmt.Errorf("veloxclient: jti generation: %w", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":          expectedIssuer,
		"aud":          expectedAudience, // plain string, NOT ClaimStrings
		"sub":          fmt.Sprintf("%d", userID),
		"workspace_id": workspaceID,
		"scopes":       defaultScopes,
		"exp":          now.Add(tokenTTL).Unix(), // int64, NOT NumericDate
		"jti":          jti,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("veloxclient: sign control token: %w", err)
	}
	return signed, nil
}

// randomJTI returns a 16-byte hex-encoded unique token id. Uses
// crypto/rand so the jti is unpredictable (a predictable jti would
// let an attacker pre-compute a replay before the legitimate call).
func randomJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
