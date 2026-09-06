package api

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestIsGoogleTokenInfoRejectionTypedSentinel pins the typed-sentinel
// contract replacing the historical `strings.Contains(err.Error(),
// "400")` heuristic, which misclassified any transport or storage error
// whose message happened to contain "400" (byte counts, row ids, URLs)
// as "Google rejected this token" — flipping healthy accounts to
// reauth_required.
func TestIsGoogleTokenInfoRejectionTypedSentinel(t *testing.T) {
	rejected := fmt.Errorf("youtube tokeninfo returned %d: %w", 400, services.ErrGoogleTokenInfoInvalid)
	if !isGoogleTokenInfoRejection(rejected) {
		t.Fatalf("typed hard rejection must classify as tokeninfo rejection: %v", rejected)
	}
	if !isGoogleTokenInfoRejection(services.ErrGoogleTokenInfoInvalid) {
		t.Fatal("bare sentinel must classify as tokeninfo rejection")
	}

	incidental := errors.New("unexpected EOF after 400 bytes of response body")
	if isGoogleTokenInfoRejection(incidental) {
		t.Fatalf("incidental '400' substring must NOT classify as tokeninfo rejection: %v", incidental)
	}

	transport := fmt.Errorf("youtube tokeninfo: request: %w", errors.New("connection reset by peer"))
	if isGoogleTokenInfoRejection(transport) {
		t.Fatalf("transport failure must NOT classify as tokeninfo rejection: %v", transport)
	}

	if isGoogleTokenInfoRejection(nil) {
		t.Fatal("nil error must not classify as tokeninfo rejection")
	}
}
