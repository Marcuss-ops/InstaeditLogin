package veloxclient

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
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

// New scope taxonomy. Each BFF API call signs a JWT containing ONLY
// the scope(s) that the operation needs (per-call, not all-scopes):
// the Velox middleware MUST see exactly the scope it requires on the
// route being called; extra scopes are accepted but a missing scope
// is a hard 403 ("insufficient scope").
//
// Naming matches VeloxEditiingg/internal/instaeditauth/scopes.go
// (declared as the authoritative source on the Velox side). The
// values are duplicated here so a drift between the two repos
// surfaces as a 403 at the first call, not at deploy time.
//
//	editor.project.read      : read a dark-editor project (Velox
//	                           GET /internal/v1/editor/projects/*,
//	                           list-jobs, list-deliveries, list-workers,
//	                           get-asset)
//	editor.project.write     : mutate a dark-editor project (Velox
//	                           POST/PATCH/DELETE on projects/cancel)
//	editor.asset.upload      : upload a render asset (Velox
//	                           PUT/POST /internal/v1/editor/assets/*)
//	youtube.session.publish  : publish a thumbnail update to YouTube
//	                           (Velox POST
//	                           /internal/v1/editor/sessions/.../publish)
//
// The canonical definitions moved to internal/veloxcontract (the
// shared InstaEdit⇄Velox BFF contract package); the aliases below
// keep this package's existing references compiling and guarantee
// the client and the BFF handlers can never drift apart.
const (
	ScopeVeloxJobsRead    = veloxcontract.ScopeVeloxJobsRead
	ScopeVeloxJobsWrite   = veloxcontract.ScopeVeloxJobsWrite
	ScopeVeloxWorkersRead = veloxcontract.ScopeVeloxWorkersRead
	ScopeVeloxAssetsRead  = veloxcontract.ScopeVeloxAssetsRead

	ScopeEditorProjectRead     = veloxcontract.ScopeEditorProjectRead
	ScopeEditorProjectWrite    = veloxcontract.ScopeEditorProjectWrite
	ScopeEditorAssetUpload     = veloxcontract.ScopeEditorAssetUpload
	ScopeYouTubeSessionPublish = veloxcontract.ScopeYouTubeSessionPublish
)

// signControlToken issues a short-lived HS256 JWT for the InstaEdit→
// Velox internal control plane. The secret is the
// VELOX_CONTROL_JWT_SECRET shared between the two services (distinct
// from the reverse-direction VELOX_API_TOKEN). userID becomes the
// JWT subject (sub); workspaceID becomes the workspace_id claim.
// scopes is the list of authorization grants the caller needs
// (subslice of the 4 valid scopes above); the JWT carries ONLY
// these scopes — a Velox route demanding a scope not in this list
// will 403.
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
func signControlToken(secret []byte, userID, workspaceID int64, scopes []string) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("veloxclient: control JWT secret is empty (VELOX_CONTROL_JWT_SECRET not configured)")
	}
	if userID <= 0 || workspaceID <= 0 {
		return "", fmt.Errorf("veloxclient: invalid identity (user=%d workspace=%d)", userID, workspaceID)
	}
	if len(scopes) == 0 {
		// A JWT with no scopes would 403 on every protected route;
		// reject here at the source rather than at the Velox side
		// so the BFF handler surfaces a clearer error.
		return "", fmt.Errorf("veloxclient: sign control token: at least one scope is required")
	}
	for _, s := range scopes {
		if s == "" {
			return "", fmt.Errorf("veloxclient: empty scope string in claim (programmer error)")
		}
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
		"scopes":       scopes,
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
