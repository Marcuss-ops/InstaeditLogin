package credentials

import (
	"errors"
	"fmt"
	"testing"
)

// TestIsExpiryError_TypedOnly pins the sentinel-only classification
// contract: expiry is detected exclusively via ErrTokenExpired. Message
// text is never consulted — provider-controlled strings are unstable,
// and the historical "Token has been expired or revoked." invalid_grant
// body must classify as reauth (ErrInvalidGrant), not expiry.
func TestIsExpiryError_TypedOnly(t *testing.T) {
	if !isExpiryError(ErrTokenExpired) {
		t.Fatal("typed sentinel must classify as expiry")
	}
	if !isExpiryError(fmt.Errorf("vault: %w", ErrTokenExpired)) {
		t.Fatal("wrapped sentinel must classify as expiry")
	}
	incidental := errors.New("Token has been expired or revoked.")
	if isExpiryError(incidental) {
		t.Fatalf("provider-controlled string must NOT classify as expiry: %v", incidental)
	}
	if isExpiryError(ErrInvalidGrant) {
		t.Fatal("invalid_grant must NOT classify as expiry (distinct lifecycle: reauth, not refresh-lapsed)")
	}
	if isExpiryError(nil) {
		t.Fatal("nil must not classify as expiry")
	}
}
