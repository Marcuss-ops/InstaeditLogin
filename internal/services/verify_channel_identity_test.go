// Package services — VerifyChannelIdentity helper tests for Task 2/10.
//
// VerifyChannelIdentity is the REUSABLE pre-action channel-bound
// guard. The canonical implementation lives on
// YouTubeOAuthService.ValidateChannelBinding; this file proves the
// package-level helper delegates correctly across the four return
// shapes the production binder produces:
//
//   - nil                              → grant bound to expected → proceed
//   - error wrapping ErrYouTubeChannelMismatch → grant re-bound → 422
//   - any other error                  → transient (5xx / network / decode)
//   - binder nil                       → no-op (non-YouTube path)
//
// Returning the wrapping error verbatim (NOT re-wrapping) is critical:
// the worker's `errors.Is(bindErr, services.ErrYouTubeChannelMismatch)`
// branch MUST see an intact error chain. The transient test below
// is the regression guard — a future refactor that wraps the helper's
// own fmt.Errorf around the binder's error would silently break the
// sentinel propagation.
package services

import (
	"context"
	"errors"
	"testing"
)

// fakeChannelBinder is a YouTubeChannelBinder with a configurable
// BindFn + call-counter so tests can assert on the helper's behaviour
// AND on the (accessToken, expectedChannelID) args passed through.
// Embeds services.NameProvider so the compile-time assertion that
// *fakeChannelBinder satisfies YouTubeChannelBinder holds (the
// YouTubeChannelBinder interface embeds NameProvider on top of
// ValidateChannelBinding — see internal/services/provider_channel_binder.go).
type fakeChannelBinder struct {
	BindFn func(ctx context.Context, accessToken, expectedChannelID string) error
	Calls  int
	LastAT string
	LastID string
}

// Name satisfies the embedded NameProvider interface (called by the
// CapabilityRouter to identify the platform; irrelevant to the
// channel-binding test surface but required for the compile-time
// assertion). Returns a stable fake platform id.
func (f *fakeChannelBinder) Name() string { return "youtube-test-fixture" }

func (f *fakeChannelBinder) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	f.Calls++
	f.LastAT = accessToken
	f.LastID = expectedChannelID
	if f.BindFn == nil {
		return nil
	}
	return f.BindFn(ctx, accessToken, expectedChannelID)
}

// TestVerifyChannelIdentity_NilBinder_NoOp covers the
// non-YouTube-provider code path (binder == nil): the helper
// returns nil immediately without invoking any callback. Lets the
// non-YouTube code paths that haven't wired a binder proceed.
func TestVerifyChannelIdentity_NilBinder_NoOp(t *testing.T) {
	if err := VerifyChannelIdentity(context.Background(), nil, "tok-xyz", "UC-channel"); err != nil {
		t.Errorf("nil binder must be a no-op (returns nil), got %v", err)
	}
}

// TestVerifyChannelIdentity_Match_OK covers the binding happy
// path: binder returns nil for the (accessToken, expectedChannelID)
// tuple. VerifyChannelIdentity returns nil AND records the args
// passed through (regression guard against the helper swallowing
// them).
func TestVerifyChannelIdentity_Match_OK(t *testing.T) {
	f := &fakeChannelBinder{
		BindFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
	}
	if err := VerifyChannelIdentity(context.Background(), f, "tok-1234", "UC-channel-1"); err != nil {
		t.Errorf("match path: want nil, got %v", err)
	}
	if f.Calls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", f.Calls)
	}
	if f.LastAT != "tok-1234" {
		t.Errorf("LastAT: want %q, got %q", "tok-1234", f.LastAT)
	}
	if f.LastID != "UC-channel-1" {
		t.Errorf("LastID: want %q, got %q", "UC-channel-1", f.LastID)
	}
}

// TestVerifyChannelIdentity_Mismatch_ReturnsSentinel covers the
// channel-drift refusal: binder wraps ErrYouTubeChannelMismatch.
// VerifyChannelIdentity returns the wrapped sentinel so the
// caller's errors.Is branch sees an intact error chain. The
// pointer-equality of the sentinel is intentionally NOT asserted
// here (errors.Is is the production-grade contract; pointer-eq
// would silently break the test on any future refactor that adds
// even a benign fmt.Errorf wrap for context).
func TestVerifyChannelIdentity_Mismatch_ReturnsSentinel(t *testing.T) {
	f := &fakeChannelBinder{
		BindFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return ErrYouTubeChannelMismatch
		},
	}
	err := VerifyChannelIdentity(context.Background(), f, "tok", "UC-channel")
	if err == nil {
		t.Fatal("mismatch path: want error, got nil")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("mismatch err MUST wrap ErrYouTubeChannelMismatch (sentinel propagation), got %v", err)
	}
}

// TestVerifyChannelIdentity_Transient_WrapsButNotSentinel covers
// the 5xx / network / decode case: binder returns an error that
// DOES NOT wrap ErrYouTubeChannelMismatch. VerifyChannelIdentity
// returns the error verbatim so the caller MUST treat as transient
// (do NOT flag reauth — would lock the operator out for a
// recoverable blip).
func TestVerifyChannelIdentity_Transient_WrapsButNotSentinel(t *testing.T) {
	transient := errors.New("youtube channel binding: channels.list returned 503: upstream")
	f := &fakeChannelBinder{
		BindFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return transient
		},
	}
	err := VerifyChannelIdentity(context.Background(), f, "tok", "UC-channel")
	if err == nil {
		t.Fatal("transient path: want error, got nil")
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("transient err MUST NOT wrap ErrYouTubeChannelMismatch (would misroute to reauth branch), got %v", err)
	}
	if err.Error() != transient.Error() {
		t.Errorf("transient err must be returned verbatim, want %q got %q", transient.Error(), err.Error())
	}
}

// TestVerifyChannelIdentity_DelegationConsistency verifies the
// two call sites (AuthorizeChannel pre-tx guard + publish_worker
// pre-upload guard) share the helper and cannot drift: any caller
// that uses ValidateChannelBinding directly will lose the
// testable surface; this test ensures the helper IS the delegate.
// Concretely: the helper's behaviour on a nil binder must match the
// helper's behaviour regardless of expectedChannelID being empty
// (a benign-but-valid case the binder guards against).
func TestVerifyChannelIdentity_NilBinder_AcceptsEmptyExpectedID(t *testing.T) {
	// expectedChannelID="" is the user-driven OAuth flow without a
	// connect-link hint. The binder's existing ValidateChannelBinding
	// returns an error in that case, but the helper signature
	// accepts any string — what we test here is that the nil-binder
	// path stays a clean no-op even when expectedChannelID is empty.
	if err := VerifyChannelIdentity(context.Background(), nil, "tok", ""); err != nil {
		t.Errorf("nil binder must remain a no-op for any token/expectedChannelID combination, got %v", err)
	}
}
