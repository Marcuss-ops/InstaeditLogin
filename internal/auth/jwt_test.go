package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-jwt-secret-must-be-long-enough-for-hs256"

func TestIssueAndVerify(t *testing.T) {
	m := NewManager(testSecret, 24)
	// SPRINT 7.2 fix: Manager.Issue refuses to sign without a
	// positive sessionID (post-SPRINT-2.1 contract — Verify would
	// reject a sessionID=0 JWT). Use IssueAccess to mint a JWT
	// carrying all three IDs.
	tok, jti, exp, err := m.IssueAccess(42, 1, 1)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok == "" || jti == "" || exp.IsZero() {
		t.Fatal("expected non-empty token, jti, expiry")
	}
	if got := time.Until(exp); got < 23*time.Hour || got > 25*time.Hour {
		t.Fatalf("ttl outside expected window: %s", got)
	}
	uid, wsID, _, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if uid != 42 {
		t.Fatalf("uid: want 42 got %d", uid)
	}
	if wsID != 1 {
		t.Fatalf("wsID: want 1 got %d", wsID)
	}
}

func TestIssueRejectsInvalidID(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, _, _, err := m.Issue(0, 1); err == nil {
		t.Fatal("expected error for zero user id")
	}
	if _, _, _, err := m.Issue(-1, 1); err == nil {
		t.Fatal("expected error for negative user id")
	}
	// SPRINT 1.1: zero/negative workspace id is also rejected.
	if _, _, _, err := m.Issue(1, 0); err == nil {
		t.Fatal("expected error for zero workspace id")
	}
	if _, _, _, err := m.Issue(1, -1); err == nil {
		t.Fatal("expected error for negative workspace id")
	}
}

// Blocco #1.4 — Manager.Issue must reject sessionID=0 explicitly
// (was silently allowed pre-Blocco #1.4). IssueAccess already had
// this check; Issue now matches.
func TestIssueRejectsSessionIDZero(t *testing.T) {
	m := NewManager(testSecret, 24)
	// 3-arg form with sessionID=0 must error.
	tok, jti, exp, err := m.Issue(1, 1, 0)
	if err == nil {
		t.Fatalf("Issue(1, 1, 0): want error, got token=%q jti=%q exp=%v", tok, jti, exp)
	}
	if tok != "" || jti != "" || !exp.IsZero() {
		t.Errorf("Issue(1, 1, 0) on error: must return zero values; got token=%q jti=%q exp=%v", tok, jti, exp)
	}
	if !strings.Contains(err.Error(), "session") {
		t.Errorf("error message should mention session id so callers understand the contract: got %v", err)
	}
}

// Blocco #1.4 — 2-arg form (legacy callers) must now error because
// sessionID defaults to 0. Previously this minted a sid=0 token
// silently; now it fails loud.
func TestIssueRejectsSessionIDZeroViaTwoArgForm(t *testing.T) {
	m := NewManager(testSecret, 24)
	tok, jti, exp, err := m.Issue(1, 1)
	if err == nil {
		t.Fatalf("Issue(1, 1): want explicit error (sessionID=0), got token=%q jti=%q exp=%v", tok, jti, exp)
	}
	if tok != "" {
		t.Errorf("Issue on error: must NOT return a token; got %q", tok)
	}
	if !strings.Contains(err.Error(), "SessionsService") {
		t.Logf("note: error guidance points at SessionsService.Start: %v", err)
	}
}

// Blocco #1.4 — IssueAccess is the canonical production path; it
// already required sessionID>0, but we pin the explicit error here
// so a future change to IssueAccess can't silently weaken the
// contract.
func TestIssueAccessRejectsSessionIDZero(t *testing.T) {
	m := NewManager(testSecret, 24)
	cases := []struct {
		name    string
		u, w, s int64
	}{
		{"sessionID=0", 1, 1, 0},
		{"sessionID=-1", 1, 1, -1},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			tok, jti, exp, err := m.IssueAccess(c.u, c.w, c.s)
			if err == nil {
				t.Fatalf("want error, got token=%q jti=%q exp=%v", tok, jti, exp)
			}
			if tok != "" || jti != "" || !exp.IsZero() {
				t.Errorf("IssueAccess on error: must return zero values; got token=%q jti=%q exp=%v", tok, jti, exp)
			}
			if !strings.Contains(err.Error(), "session") {
				t.Errorf("error must mention session id: %v", err)
			}
		})
	}
}

// Blocco #1.4 — Verify rejects any JWT whose session id is missing
// or zero, regardless of how it was minted (Manager or hand-crafted).
// The middleware depends on this to refuse sid=0 tokens forged by an
// attacker that bypasses Issue(). We sign the test token directly
// here because Manager.Issue will no longer mint a sid=0 token.
func TestVerifyRejectsHandCraftedTokenWithSessionIDZero(t *testing.T) {
	m := NewManager(testSecret, 24)
	claims := Claims{UserID: 99, WorkspaceID: 1, SessionID: 0}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}
	if _, _, _, err := m.Verify(signed); err == nil {
		t.Fatal("expected error verifying hand-crafted sid=0 token")
	}
}

func TestVerifyRejectsMissingWorkspaceClaim(t *testing.T) {
	// A JWT minted without the workspace claim (e.g. tampered JSON
	// post-signing) MUST NOT pass verify: SPRINT 1.1 requires wsID>0.
	m := NewManager(testSecret, 24)
	claims := Claims{UserID: 99}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}
	if _, _, _, err := m.Verify(signed); err == nil {
		t.Fatal("expected error verifying token with no workspace claim")
	}
}

func TestVerifyEmptyToken(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, _, _, err := m.Verify(""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	m1 := NewManager(testSecret, 24)
	// Blocco #1.4: Issue(7, 1) is now rejected (sid=0). Use the
	// canonical IssueAccess with all three IDs positive.
	tok, _, _, err := m1.IssueAccess(7, 1, 42)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	m2 := NewManager("a-different-secret-with-32-bytes-of-content", 24)
	if _, _, _, err := m2.Verify(tok); err == nil {
		t.Fatal("expected error when verifying with wrong secret")
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, _, _, err := m.Verify("not.a.real.jwt"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}

func TestMiddleware_RejectsMissing(t *testing.T) {
	// Taglio 1.1: missing Authorization header → 401. No lenient fallback.
	m := NewManager(testSecret, 24)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(next)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if called {
		t.Fatal("next handler should not have been called")
	}
}

func TestMiddleware_RejectsInvalidScheme(t *testing.T) {
	m := NewManager(testSecret, 24)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Token xyz")
	w := httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestMiddleware_RejectsBogusBearer(t *testing.T) {
	// A Bearer prefix with an unparseable token MUST be rejected with 401,
	// not silently allowed through (Taglio 1.1: no lenient mode).
	m := NewManager(testSecret, 24)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer this.is.bogus")
	w := httptest.NewRecorder()
	m.Middleware(next).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if called {
		t.Fatal("next handler should not have been called")
	}
}

func TestMiddleware_AllowValidToken(t *testing.T) {
	m := NewManager(testSecret, 24)
	// SPRINT 7.2 fix: same — IssueAccess(u, wsID, sessionID) so
	// Manager.Verify accepts the token.
	tok, _, _, _ := m.IssueAccess(99, 1, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok || uid != 99 {
			t.Fatalf("context uid: want 99, got %v ok=%v", uid, ok)
		}
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	m.Middleware(next).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------
// Blocco #5.2 — cross-environment JWT rejection
//
// The 3 user-spec cases:
//   1. same secret + different envs          -> explicit 401 (env mismatch)
//   2. different secrets + same env          -> explicit 401 (sig mismatch)
//   3. token issued by dev that arrives on prod -> explicit 401 (env mismatch)
//
// All three must produce a 401, and the env-mismatch ones must
// surface the canonical "token environment mismatch" body so the
// rejection is visible in the operator's logs (not a silent
// pass-through or generic 401).
// ---------------------------------------------------------------------

// TestCrossEnv_SameSecretDifferentEnv confirms case (1): a token
// minted in env=dev by manager A (same secret as manager B) is
// rejected with errCrossEnvMismatch when manager B (env=staging)
// verifies it. The env-bound Manager uses WithEnv() to lock its
// own process env into the Verify path.
func TestCrossEnv_SameSecretDifferentEnv(t *testing.T) {
	const sharedSecret = "shared-secret-for-cross-env-test-32-bytes!"
	issuer := NewManager(sharedSecret, 24).WithEnv("dev")
	verifier := NewManager(sharedSecret, 24).WithEnv("staging")

	tok, _, _, err := issuer.IssueAccess(42, 1, 1)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, _, _, verr := verifier.Verify(tok)
	if verr == nil {
		t.Fatal("Verify across envs: want error, got nil")
	}
	if !errors.Is(verr, errCrossEnvMismatch) {
		t.Fatalf("Verify across envs: want errCrossEnvMismatch, got %v", verr)
	}
}

// TestCrossEnv_DifferentSecretSameEnv confirms case (2): a token
// signed with secret A is rejected by a Manager built with secret
// B, even though both managers run in the same env. The failure
// surfaces from jwt-go (signature mismatch), NOT from our
// errCrossEnvMismatch path — the test pins that distinction so a
// future refactor can't confuse the two failure modes.
func TestCrossEnv_DifferentSecretSameEnv(t *testing.T) {
	issuer := NewManager("secret-A-with-enough-bytes-for-hs256-32x", 24).WithEnv("production")
	verifier := NewManager("secret-B-with-enough-bytes-for-hs256-32x", 24).WithEnv("production")

	tok, _, _, err := issuer.IssueAccess(7, 1, 1)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, _, _, verr := verifier.Verify(tok)
	if verr == nil {
		t.Fatal("Verify with different secret: want error, got nil")
	}
	if errors.Is(verr, errCrossEnvMismatch) {
		t.Fatalf("Verify with different secret: want sig-mismatch error, NOT errCrossEnvMismatch; got %v", verr)
	}
}

// TestCrossEnv_DevTokenArrivesOnProd confirms case (3) — the
// canonical "dev token leaked into prod" attack path. The token
// was minted by an issuer configured with env=dev (via
// WithEnv("dev")); the verifier is configured with env=production.
// The verifier must reject the token with errCrossEnvMismatch
// (NOT a generic sig error — the signature is correct, only the
// env claim is wrong).
func TestCrossEnv_DevTokenArrivesOnProd(t *testing.T) {
	const sharedSecret = "another-shared-secret-for-cross-env-test-xx"
	devIssuer := NewManager(sharedSecret, 24).WithEnv("dev")
	prodVerifier := NewManager(sharedSecret, 24).WithEnv("production")

	tok, _, _, err := devIssuer.IssueAccess(99, 1, 1)
	if err != nil {
		t.Fatalf("dev issue: %v", err)
	}
	// Direct Verify: must fail with the env-mismatch sentinel.
	if _, _, _, err := prodVerifier.Verify(tok); !errors.Is(err, errCrossEnvMismatch) {
		t.Fatalf("prodVerify(devToken): want errCrossEnvMismatch, got %v", err)
	}

	// Middleware path: the rejection must surface as an explicit
	// 401 body (not a silent pass-through, not a generic 401).
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	prodVerifier.Middleware(next).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Middleware status: want 401, got %d", w.Code)
	}
	if called {
		t.Fatal("next handler must not run when env mismatched")
	}
	if !strings.Contains(w.Body.String(), "token environment mismatch") {
		t.Errorf("Middleware body: want explicit env-mismatch body, got %q", w.Body.String())
	}
}

// TestCrossEnv_NoEnvConfigured_SkipCheck confirms the
// backwards-compat path: a Manager without WithEnv() (the
// test-default + the 17+ existing test fixtures) accepts tokens
// with any env claim, including an empty one. This pins the
// “only enforce when both sides have a non-empty env” contract
// the production rollout depends on — flipping it would force
// every test that uses NewManager directly to chain WithEnv.
func TestCrossEnv_NoEnvConfigured_SkipCheck(t *testing.T) {
	issuer := NewManager(testSecret, 24)   // no WithEnv
	verifier := NewManager(testSecret, 24) // no WithEnv
	tok, _, _, err := issuer.IssueAccess(1, 1, 1)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, _, err := verifier.Verify(tok); err != nil {
		t.Errorf("Verify without WithEnv chain: want nil err, got %v", err)
	}
}

// TestCrossEnv_IssuerNoEnv_VerifierWithEnv_StillEnforced confirms
// the asymmetric case: if the verifier has WithEnv() but the
// issuer did not, the token has env="" in its claims and the
// verifier (env="production") must reject it. This catches the
// "issuer never chained WithEnv" deployment regression — every
// production binary must chain WithEnv at construction time.
func TestCrossEnv_IssuerNoEnv_VerifierWithEnv_StillEnforced(t *testing.T) {
	issuer := NewManager(testSecret, 24) // no WithEnv -> token has env=""
	verifier := NewManager(testSecret, 24).WithEnv("production")
	tok, _, _, err := issuer.IssueAccess(1, 1, 1)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_, _, _, verr := verifier.Verify(tok)
	if !errors.Is(verr, errCrossEnvMismatch) {
		t.Fatalf("Verify(issuer-no-env -> verifier-prod): want errCrossEnvMismatch, got %v", verr)
	}
}
