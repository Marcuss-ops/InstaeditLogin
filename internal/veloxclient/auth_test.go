package veloxclient

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// makeSecret returns a 32-byte secret suitable for HS256 (matching the
// Velox MinimumSecretBytes gate on the verifier side). Tests that need
// a "valid secret" reuse this; tests that need a "bad secret" prepend
// a different prefix but keep the byte-length.
func makeSecret() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// decodeSegment parses one of the three base64url-encoded segments of
// a JWS compact serialization.
func decodeSegment(t *testing.T, token, label string) []byte {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("[%s] expected 3 segments, got %d", label, len(parts))
	}
	padded := parts[1]
	if rem := len(padded) % 4; rem != 0 {
		padded += strings.Repeat("=", 4-rem)
	}
	out, err := base64.URLEncoding.DecodeString(padded)
	if err != nil {
		t.Fatalf("[%s] base64url decode: %v", label, err)
	}
	return out
}

// TestSignControlToken_RejectsEmptyScopes — the new BLOCKER
// requirement: scopes must be non-empty at the mint side so a JWT
// with no scopes never reaches Velox to trigger a 403 on every
// protected route.
func TestSignControlToken_RejectsEmptyScopes(t *testing.T) {
	secret := makeSecret()
	_, err := signControlToken(secret, 1, 2, nil)
	if err == nil {
		t.Fatal("signControlToken(nil) = nil err; want non-nil")
	}
	_, err = signControlToken(secret, 1, 2, []string{})
	if err == nil {
		t.Fatal("signControlToken([]) = nil err; want non-nil")
	}
	_, err = signControlToken(secret, 1, 2, []string{""})
	if err == nil {
		t.Fatal("signControlToken([\"\"]) = nil err; want non-nil (empty string scope is programmer error)")
	}
	_, err = signControlToken(secret, 1, 2, []string{ScopeVeloxJobsRead, ""})
	if err == nil {
		t.Fatal("signControlToken with one empty-string element should reject")
	}
}

// TestSignControlToken_RejectsInvalidIdentity — defence against a
// regression in the original 3-arg signature losing its userID /
// workspaceID validation.
func TestSignControlToken_RejectsInvalidIdentity(t *testing.T) {
	secret := makeSecret()
	_, err := signControlToken(secret, 0, 2, []string{ScopeVeloxJobsRead})
	if err == nil {
		t.Fatal("signControlToken(0, _, _) should reject zero userID")
	}
	_, err = signControlToken(secret, 1, 0, []string{ScopeVeloxJobsRead})
	if err == nil {
		t.Fatal("signControlToken(_, 0, _) should reject zero workspaceID")
	}
	_, err = signControlToken(nil, 1, 2, []string{ScopeVeloxJobsRead})
	if err == nil {
		t.Fatal("signControlToken(nil secret) should reject empty secret")
	}
}

// TestSignControlToken_ClaimsCarryScopes — round-trip: mint a token,
// decode the payload, and assert the 4 fields the Velox verifier
// cares about are populated correctly.
func TestSignControlToken_ClaimsCarryScopes(t *testing.T) {
	secret := makeSecret()
	userID := int64(123)
	workspaceID := int64(45)
	scopes := []string{ScopeVeloxJobsRead}

	tok, err := signControlToken(secret, userID, workspaceID, scopes)
	if err != nil {
		t.Fatalf("signControlToken: %v", err)
	}
	payload := decodeSegment(t, tok, "claims")
	var got map[string]interface{}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// iss
	if got["iss"] != expectedIssuer {
		t.Fatalf("iss: got %v, want %q", got["iss"], expectedIssuer)
	}
	// aud (plain string, not ClaimStrings array)
	if got["aud"] != expectedAudience {
		t.Fatalf("aud: got %v, want %q", got["aud"], expectedAudience)
	}
	// sub
	if got["sub"] != "123" {
		t.Fatalf("sub: got %v, want %q", got["sub"], "123")
	}
	// workspace_id
	if v, ok := got["workspace_id"].(float64); !ok || int64(v) != workspaceID {
		t.Fatalf("workspace_id: got %v, want %d", got["workspace_id"], workspaceID)
	}
	// scopes — Round-trip serialization is either []interface{}{...}
	// or []string{...}; we accept either.
	scopesOut, ok := got["scopes"].([]interface{})
	if !ok {
		t.Fatalf("scopes type: got %T, want array", got["scopes"])
	}
	if len(scopesOut) != 1 || scopesOut[0] != ScopeVeloxJobsRead {
		t.Fatalf("scopes: got %v, want [%q]", scopesOut, ScopeVeloxJobsRead)
	}
	// exp — int64 within tokenTTL of now.
	if v, ok := got["exp"].(float64); !ok {
		t.Fatalf("exp type: got %T", got["exp"])
	} else {
		now := time.Now().Unix()
		if int64(v) < now || int64(v) > now+int64(tokenTTL/time.Second)+5 {
			t.Fatalf("exp out of range: got %v, now=%d, ttl=%s", v, now, tokenTTL)
		}
	}
	// jti — non-empty
	if s, _ := got["jti"].(string); s == "" {
		t.Fatalf("jti empty: got %v", got["jti"])
	}
}

// TestSignControlToken_AllScopesRoundTrip — exercise the scope
// values in the taxonomy so a future rename of any of them fails
// fast.
func TestSignControlToken_AllScopesRoundTrip(t *testing.T) {
	secret := makeSecret()
	cases := [][]string{
		{ScopeVeloxJobsRead},
		{ScopeVeloxJobsWrite},
		{ScopeVeloxWorkersRead},
		{ScopeVeloxAssetsRead},
		{ScopeVeloxAssetsWrite},
		{ScopeVeloxJobsRead, ScopeVeloxJobsWrite},
		{ScopeVeloxJobsRead, ScopeVeloxJobsWrite, ScopeVeloxWorkersRead, ScopeVeloxAssetsRead, ScopeVeloxAssetsWrite},
	}
	for _, scopes := range cases {
		t.Run(strings.Join(scopes, ","), func(t *testing.T) {
			tok, err := signControlToken(secret, 1, 2, scopes)
			if err != nil {
				t.Fatalf("signControlToken: %v", err)
			}
			payload := decodeSegment(t, tok, "claims")
			var got map[string]interface{}
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			scopesOut, ok := got["scopes"].([]interface{})
			if !ok {
				t.Fatalf("scopes: got %T, want array", got["scopes"])
			}
			if len(scopesOut) != len(scopes) {
				t.Fatalf("len(scopes): got %d, want %d", len(scopesOut), len(scopes))
			}
			for i, want := range scopes {
				if scopesOut[i] != want {
					t.Fatalf("scopes[%d]: got %v, want %q", i, scopesOut[i], want)
				}
			}
		})
	}
}
