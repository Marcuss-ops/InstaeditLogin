package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestVerifyOAuthFlowState_FreshStateRoundTrips is the positive control
// for the signed OAuth flow state: a freshly issued state must verify
// and return the expected_channel_id, workspace_id and jti exactly as
// issued. The pool client key is deliberately NOT part of the JWT — it
// round-trips in the sibling oauth_state_{provider}_oauth_client cookie
// (covered by the pkg/api handler tests).
func TestVerifyOAuthFlowState_FreshStateRoundTrips(t *testing.T) {
	m := NewManager(testSecret, 24)
	const (
		wantChannel = "UC1234567890abcdefghij"
		wantWS      = int64(7)
	)
	signed, nonce, expiresAt, err := m.IssueOAuthFlowState(wantChannel, wantWS)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if signed == "" || nonce == "" {
		t.Fatal("IssueOAuthFlowState: expected non-empty state + nonce")
	}
	if expiresAt.IsZero() {
		t.Fatal("IssueOAuthFlowState: expected non-zero expiry")
	}
	if ttl := time.Until(expiresAt); ttl < 9*time.Minute || ttl > 10*time.Minute {
		t.Fatalf("IssueOAuthFlowState: expiry outside 10-minute window: %s", ttl)
	}

	claims, verr := m.VerifyOAuthFlowState(signed)
	if verr != nil {
		t.Fatalf("VerifyOAuthFlowState on fresh state: want nil err, got %v", verr)
	}
	if claims.StateType != "oauth_flow" {
		t.Errorf("StateType: want oauth_flow, got %q", claims.StateType)
	}
	if claims.ExpectedChannelID != wantChannel {
		t.Errorf("ExpectedChannelID: want %q, got %q", wantChannel, claims.ExpectedChannelID)
	}
	if claims.WorkspaceID != wantWS {
		t.Errorf("WorkspaceID: want %d, got %d", wantWS, claims.WorkspaceID)
	}
	if claims.ID != nonce {
		t.Errorf("JTI: want %q, got %q", nonce, claims.ID)
	}
}

// TestVerifyOAuthFlowState_ExpiredReturnsErrMalformed pins that an
// oauth-flow state whose ExpiresAt has passed is rejected with
// ErrMalformedOAuthFlowState so the callback maps it to a 4xx.
func TestVerifyOAuthFlowState_ExpiredReturnsErrMalformed(t *testing.T) {
	m := NewManager(testSecret, 24)
	claims := OAuthFlowStateClaims{
		StateType:         "oauth_flow",
		ExpectedChannelID: "UC1234567890abcdefghij",
		WorkspaceID:       1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			ID:        "deadbeefdeadbeef",
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("forge expired oauth flow state: %v", err)
	}
	_, verr := m.VerifyOAuthFlowState(signed)
	if verr == nil {
		t.Fatal("VerifyOAuthFlowState on expired state: want error, got nil")
	}
	if !errors.Is(verr, ErrMalformedOAuthFlowState) {
		t.Errorf("VerifyOAuthFlowState on expired state: want errors.Is(ErrMalformedOAuthFlowState), got %v", verr)
	}
}

// TestVerifyOAuthFlowState_WrongSecretRejected pins that a state signed
// with a different secret fails verification.
func TestVerifyOAuthFlowState_WrongSecretRejected(t *testing.T) {
	m1 := NewManager(testSecret, 24)
	signed, _, _, err := m1.IssueOAuthFlowState("", 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	m2 := NewManager("a-different-secret-with-32-bytes-of-content", 24)
	if _, verr := m2.VerifyOAuthFlowState(signed); verr == nil {
		t.Fatal("VerifyOAuthFlowState with wrong secret: want error, got nil")
	}
}

// TestVerifyOAuthFlowState_RejectsNegativeWorkspace pins that a
// negative workspace_id cannot be smuggled into a valid state.
func TestVerifyOAuthFlowState_RejectsNegativeWorkspace(t *testing.T) {
	m := NewManager(testSecret, 24)
	claims := OAuthFlowStateClaims{
		StateType:   "oauth_flow",
		WorkspaceID: -1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			ID:        "deadbeefdeadbeef",
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("forge state with negative workspace: %v", err)
	}
	if _, verr := m.VerifyOAuthFlowState(signed); !errors.Is(verr, ErrMalformedOAuthFlowState) {
		t.Errorf("VerifyOAuthFlowState with negative workspace: want ErrMalformedOAuthFlowState, got %v", verr)
	}
	// Issue side must also refuse a negative workspace.
	if _, _, _, err := m.IssueOAuthFlowState("", -1); err == nil {
		t.Error("IssueOAuthFlowState with negative workspace: want error, got nil")
	}
}

// TestVerifyOAuthFlowState_RejectsMissingJTI pins that a state without
// the single-use jti is rejected (replay protection impossible).
func TestVerifyOAuthFlowState_RejectsMissingJTI(t *testing.T) {
	m := NewManager(testSecret, 24)
	claims := OAuthFlowStateClaims{
		StateType:   "oauth_flow",
		WorkspaceID: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "instaeditlogin",
			Audience:  jwt.ClaimStrings{"api"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
			// Deliberately no ID.
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("forge state without jti: %v", err)
	}
	if _, verr := m.VerifyOAuthFlowState(signed); !errors.Is(verr, ErrMalformedOAuthFlowState) {
		t.Errorf("VerifyOAuthFlowState without jti: want ErrMalformedOAuthFlowState, got %v", verr)
	}
}

// TestVerifyOAuthFlowState_StateTypesAreMutuallyExclusive pins that the
// callback's dispatch (connect-link vs oauth-flow) cannot cross wires:
// a connect-link JWT fails VerifyOAuthFlowState and an oauth-flow JWT
// fails VerifyConnectLinkState, so resolveCallbackState can try
// connect-link first and fall through cleanly.
func TestVerifyOAuthFlowState_StateTypesAreMutuallyExclusive(t *testing.T) {
	m := NewManager(testSecret, 24)

	flowState, _, _, err := m.IssueOAuthFlowState("UC1234567890abcdefghij", 1)
	if err != nil {
		t.Fatalf("IssueOAuthFlowState: %v", err)
	}
	if _, verr := m.VerifyConnectLinkState(flowState); !errors.Is(verr, ErrMalformedConnectLinkState) {
		t.Errorf("VerifyConnectLinkState(oauth_flow state): want ErrMalformedConnectLinkState, got %v", verr)
	}

	linkState, _, _, err := m.IssueConnectLinkState("UC1234567890abcdefghij")
	if err != nil {
		t.Fatalf("IssueConnectLinkState: %v", err)
	}
	if _, verr := m.VerifyOAuthFlowState(linkState); !errors.Is(verr, ErrMalformedOAuthFlowState) {
		t.Errorf("VerifyOAuthFlowState(connect_link state): want ErrMalformedOAuthFlowState, got %v", verr)
	}
}
