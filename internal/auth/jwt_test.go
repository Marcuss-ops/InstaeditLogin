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

// TestIssueAccessWithJTI_UsesSuppliedJTI pins the contract that the
// caller-supplied JTI is the one embedded in the JWT's RegisteredClaims.ID.
// This keeps sessions.access_jti and the access token in sync.
func TestIssueAccessWithJTI_UsesSuppliedJTI(t *testing.T) {
	m := NewManager(testSecret, 24)
	wantJTI := "aabbccdd001aabbccdd001aabbccdd00"

	tok, jti, exp, err := m.IssueAccessWithJTI(42, 1, 1, wantJTI)
	if err != nil {
		t.Fatalf("IssueAccessWithJTI: %v", err)
	}
	if jti != wantJTI {
		t.Fatalf("returned jti: want %q, got %q", wantJTI, jti)
	}
	if exp.IsZero() {
		t.Fatal("expected non-zero expiry")
	}

	claims := &Claims{}
	if _, err := jwt.ParseWithClaims(tok, claims, func(_ *jwt.Token) (interface{}, error) { return []byte(testSecret), nil }); err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if claims.ID != wantJTI {
		t.Fatalf("JWT claims.ID: want %q, got %q", wantJTI, claims.ID)
	}
	if claims.UserID != 42 || claims.WorkspaceID != 1 || claims.SessionID != 1 {
		t.Fatalf("unexpected claims: uid=%d ws=%d sid=%d", claims.UserID, claims.WorkspaceID, claims.SessionID)
	}
}

// TestIssueAccessWithJTI_RejectsEmptyJTI confirms the helper refuses
// to sign a token without an explicit JTI, preventing a caller from
// accidentally creating a token whose ID is empty.
func TestIssueAccessWithJTI_RejectsEmptyJTI(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, _, _, err := m.IssueAccessWithJTI(42, 1, 1, ""); err == nil {
		t.Fatal("expected error for empty JTI")
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

// TestVerifyConnectLinkState_RejectsWrongIssuerAudienceOrAlg pins the
// same issuer/audience/algorithm validation for connect-link state JWTs.
func TestVerifyConnectLinkState_RejectsWrongIssuerAudienceOrAlg(t *testing.T) {
	m := NewManager(testSecret, 24)
	const channel = "UC1234567890abcdefghij"

	// Wrong issuer.
	claimsIss := ConnectLinkStateClaims{
		StateType:         "connect_link",
		ExpectedChannelID: channel,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "wrong-issuer",
			Audience:  jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokIss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsIss).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign wrong-issuer state token: %v", err)
	}
	if _, err := m.VerifyConnectLinkState(tokIss); err == nil {
		t.Fatal("VerifyConnectLinkState: want error for wrong issuer, got nil")
	}

	// Wrong audience.
	claimsAud := ConnectLinkStateClaims{
		StateType:         "connect_link",
		ExpectedChannelID: channel,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"wrong-audience"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokAud, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsAud).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign wrong-audience state token: %v", err)
	}
	if _, err := m.VerifyConnectLinkState(tokAud); err == nil {
		t.Fatal("VerifyConnectLinkState: want error for wrong audience, got nil")
	}

	// Disallowed signing method ("none").
	claimsAlg := ConnectLinkStateClaims{
		StateType:         "connect_link",
		ExpectedChannelID: channel,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokAlg, err := jwt.NewWithClaims(jwt.SigningMethodNone, claimsAlg).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none state token: %v", err)
	}
	if _, err := m.VerifyConnectLinkState(tokAlg); err == nil {
		t.Fatal("VerifyConnectLinkState: want error for 'none' signing method, got nil")
	}
}

// TestVerifyConnectLinkState_ExpiredReturnsErrMalformed pins the
// HARDENING requirement from the connect-link spec: a state JWT whose
// ExpiresAt has passed MUST be rejected with ErrMalformedConnectLinkState
// so the OAuth callback can map it to a 4xx (vs. a 5xx for unrelated
// parse failures). The Manager's IssueConnectLinkState hardcodes a
// 30-minute TTL so a real-time sleep would be slow + flaky — instead
// we hand-craft the JWT via the same jwt-go SignedString the
// production code uses, with ExpiresAt = now-1h, and confirm the
// rejection path.
//
// Why hand-craft vs. IssueConnectLinkState with TTL trick:
//   - IssueConnectLinkState does not expose a ttl parameter; the 30
//     minutes is hardcoded to keep the production call-site narrow.
//   - Reaching inside Manager to forge an expired token at the same
//     SecretString boundary a real attacker would need to bypass
//     proves the rejection is a PARSER-level check, not a Manager
//     convenience.
func TestVerifyConnectLinkState_ExpiredReturnsErrMalformed(t *testing.T) {
	m := NewManager(testSecret, 24)
	claims := ConnectLinkStateClaims{
		StateType:         "connect_link",
		ExpectedChannelID: "UC1234567890abcdefghij",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)), // expired 1h ago
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("forge expired state JWT: %v", err)
	}
	_, verr := m.VerifyConnectLinkState(signed)
	if verr == nil {
		t.Fatal("VerifyConnectLinkState on expired state: want error, got nil")
	}
	if !errors.Is(verr, ErrMalformedConnectLinkState) {
		t.Errorf("VerifyConnectLinkState on expired state: want errors.Is(ErrMalformedConnectLinkState), got %v", verr)
	}
}

// TestVerifyConnectLinkState_FreshStateRoundTrips is the positive
// control that pairs with TestVerifyConnectLinkState_ExpiredReturnsErrMalformed:
// if a regression DROPS the ExpiresAt check (so the parser stops
// flagging expired states), the rejected test still passes because
// errors.Is is satisfied by ANY parse error. This test asserts
// the manager happily accepts a freshly minted state AND returns
// the original expected_channel_id, so the rejection case above
// has a working positive baseline in the same file.
func TestVerifyConnectLinkState_FreshStateRoundTrips(t *testing.T) {
	m := NewManager(testSecret, 24)
	const wantChannel = "UC1234567890abcdefghij"
	signed, err := m.IssueConnectLinkState(wantChannel)
	if err != nil {
		t.Fatalf("IssueConnectLinkState: %v", err)
	}
	gotChannel, verr := m.VerifyConnectLinkState(signed)
	if verr != nil {
		t.Fatalf("VerifyConnectLinkState on fresh state: want nil err, got %v", verr)
	}
	if gotChannel != wantChannel {
		t.Errorf("ExpectedChannelID: want %q, got %q", wantChannel, gotChannel)
	}
}

// TestTypedTTLDefaults pins the explicit access/refresh constructor:
// NewManager(secret, accessTTL, refreshTTL) must surface the exact
// durations through AccessTTL/RefreshTTL. This is the production
// bootstrap path (15m access / 30d refresh) and must never silently
// fall back to 24 hours.
func TestTypedTTLDefaults(t *testing.T) {
	m := NewManager(testSecret, 15*time.Minute, 30*24*time.Hour)
	if got := m.AccessTTL(); got != 15*time.Minute {
		t.Errorf("AccessTTL: want 15m, got %s", got)
	}
	if got := m.RefreshTTL(); got != 30*24*time.Hour {
		t.Errorf("RefreshTTL: want 30d, got %s", got)
	}
}

// TestLegacyIntHoursStillWorks ensures the existing 17+ test fixtures
// and any operator still passing an integer hours value keep working.
// The integer is interpreted as the access TTL in hours; the refresh
// TTL falls back to the 30-day default.
func TestLegacyIntHoursStillWorks(t *testing.T) {
	m := NewManager(testSecret, 24)
	if got := m.AccessTTL(); got != 24*time.Hour {
		t.Errorf("AccessTTL: want 24h, got %s", got)
	}
	if got := m.RefreshTTL(); got != 30*24*time.Hour {
		t.Errorf("RefreshTTL: want 30d default, got %s", got)
	}
}

// TestAccessTokenExpiresWithinAccessTTL verifies that the access token
// issued with the production TTL (15 minutes) expires inside that
// window. A short-lived access token limits the exposure window when a
// session is revoked: the refresh token is rejected immediately on
// revocation, and the access token becomes invalid within 15 minutes.
func TestAccessTokenExpiresWithinAccessTTL(t *testing.T) {
	m := NewManager(testSecret, 15*time.Minute, 30*24*time.Hour)
	_, _, exp, err := m.IssueAccess(42, 1, 1)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	ttl := time.Until(exp)
	if ttl <= 0 || ttl > 15*time.Minute {
		t.Fatalf("access token TTL out of range: %s", ttl)
	}
}

// TestVerifyRejectsWrongIssuerAudienceOrAlg pins the explicit
// issuer/audience/algorithm validation added to jwt.ParseWithClaims.
// Tokens that carry a different issuer, audience, or signing method
// must be rejected even when the signature is otherwise valid.
func TestVerifyRejectsWrongIssuerAudienceOrAlg(t *testing.T) {
	m := NewManager(testSecret, 24)

	// Wrong issuer.
	claimsIss := Claims{
		UserID: 1, WorkspaceID: 1, SessionID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "wrong-issuer",
			Audience:  jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokIss, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsIss).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign wrong-issuer token: %v", err)
	}
	if _, _, _, err := m.Verify(tokIss); err == nil {
		t.Fatal("Verify: want error for wrong issuer, got nil")
	}

	// Wrong audience.
	claimsAud := Claims{
		UserID: 1, WorkspaceID: 1, SessionID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"wrong-audience"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokAud, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claimsAud).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign wrong-audience token: %v", err)
	}
	if _, _, _, err := m.Verify(tokAud); err == nil {
		t.Fatal("Verify: want error for wrong audience, got nil")
	}

	// Disallowed signing method ("none").
	claimsAlg := Claims{
		UserID: 1, WorkspaceID: 1, SessionID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokAlg, err := jwt.NewWithClaims(jwt.SigningMethodNone, claimsAlg).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}
	if _, _, _, err := m.Verify(tokAlg); err == nil {
		t.Fatal("Verify: want error for 'none' signing method, got nil")
	}
}

// TestVerifyIsStatelessAndIgnoresLogicalRevocation documents the
// stateless access-token contract: Manager.Verify only checks
// signature, expiry, and env claim. It does NOT consult the sessions
// table, so a revoked session's access token remains usable until it
// expires. The short 15-minute access TTL is the mitigation. This test
// pins that behavior so a future "real-time revocation" change is
// explicit and tested.
func TestVerifyIsStatelessAndIgnoresLogicalRevocation(t *testing.T) {
	m := NewManager(testSecret, 15*time.Minute, 30*24*time.Hour)
	tok, _, _, err := m.IssueAccess(42, 1, 1)
	if err != nil {
		t.Fatalf("IssueAccess: %v", err)
	}
	// Simulate the moment immediately after the session is revoked in
	// the database. Verify still accepts the token because it is
	// stateless.
	uid, wsID, sid, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("Verify immediately after logical revoke: %v", err)
	}
	if uid != 42 || wsID != 1 || sid != 1 {
		t.Fatalf("Verify returned unexpected ids: uid=%d ws=%d sid=%d", uid, wsID, sid)
	}
}
