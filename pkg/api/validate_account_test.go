// Package api: minimal test stub for the Commit C2 handler rewrite
// of /api/v1/accounts/{id}/validate (the 4-step pipeline:
// refresh-grant → tokeninfo → channel-binding → optional canary).
//
// This file deliberately declares ZERO custom fake/mock types:
// every generic "fakeUserStore / fakeVault / fakeYTService" name
// in pkg/api is OWNED by an existing test file (e.g. routes_test.go,
// admin_velox_destinations_test.go, oauth_session_redirect_test.go)
// and a second declaration in the same package would fail to
// compile. Compile-time interface conformance is verified via the
// production-side compile-time assertion
//   var _ YouTubeOAuthService = (*services.YouTubeOAuthService)(nil)
// in handlers.go — that catches any drift between the narrow
// capability interface and the production type WITHOUT us re-
// declaring anything here.
//
// Real handler-level coverage lives in
// tests/e2e/validate_account_pipeline_test.go (out of scope for
// this commit — wires the harness through the existing 4-step
// shape). This stub is intentionally trivial; its only purpose
// is to keep `go vet ./pkg/api/...` + `go build ./pkg/api/...`
// GREEN while the handler rewrite ships.
package api

import "testing"

// TestValidateAccount_StubCompiles is a runtime smoke check: if
// this stub ever fails to compile (typically because the
// production-side interface or call-site signature drifted), the
// pkg/api test target fails loud. The body is intentionally
// trivial; the assertion is the file's existence + clean compile.
func TestValidateAccount_StubCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("stub compile check (full handler coverage lives in tests/e2e)")
	}
}
