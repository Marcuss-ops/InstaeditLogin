package credentials

import (
	"errors"
	"strings"
	"testing"
)

func TestParseOAuthTokenError_InvalidGrantIsTypedAndRedacted(t *testing.T) {
	err := ParseOAuthTokenError(400, []byte(`{"error":"invalid_grant","error_description":"secret provider detail"}`))
	if err.StatusCode != 400 || err.Code != "invalid_grant" {
		t.Fatalf("parsed error metadata: got status=%d code=%q", err.StatusCode, err.Code)
	}
	if err.Description != "secret provider detail" {
		t.Fatalf("description should remain available in memory for diagnostics; got %q", err.Description)
	}
	if !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("typed invalid_grant must unwrap to ErrInvalidGrant: %v", err)
	}
	if strings.Contains(err.Error(), "secret provider detail") {
		t.Fatalf("Error() leaked provider description: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "status 400") || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("Error() lost stable metadata: %q", err.Error())
	}
}

func TestParseOAuthTokenError_OtherCodeDoesNotRequestReauthorization(t *testing.T) {
	err := ParseOAuthTokenError(400, []byte(`{"error":"invalid_client","error_description":"secret"}`))
	if errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("invalid_client must not unwrap to ErrInvalidGrant: %v", err)
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("Error() leaked provider description: %q", err.Error())
	}
}

func TestParseOAuthTokenError_MalformedBodyRemainsTyped(t *testing.T) {
	err := ParseOAuthTokenError(502, []byte("upstream html"))
	if err.StatusCode != 502 || err.Code != "" {
		t.Fatalf("malformed body metadata: got status=%d code=%q", err.StatusCode, err.Code)
	}
	if errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("malformed body must not request reauthorization: %v", err)
	}
}
