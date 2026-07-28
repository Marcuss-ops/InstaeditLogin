package services

import (
	"testing"
	"time"
)

// TestCoercePrivacyForUpdate pins the 4-case contract documented on
// CoercePrivacyForUpdate. Each row exercises one combination of
// (publishAt nil/zero/past/future) × (input privacyLevel) and asserts
// the helper's (privacy, publishAt) return shape. The CoercePrivacyForUpdate
// rules summarised:
//
//   - nil  publishAt  → (input privacy, nil)
//   - zero publishAt  → (input privacy, nil)   (defensive — same shape as nil)
//   - past publishAt  → (input privacy, nil)   (clear; YouTube can't reschedule in the past)
//   - future publishAt → ("private", &publishAt)  (YouTube API requires privacy="private" alongside publishAt)
//
// The future-branch privacy flip is matrixed across {public, unlisted}
// so the helper's behaviour is verified for EVERY input privacy value
// that callers can pass (the input privacy is sanitized upstream by
// UpdateVideoPrivacy's switch on the canonical 3 values, but the
// helper trusts its caller-side contract).
//
// Blocco #1 followup — Finding #2: this test is the regression guard for
// the videos.update coercion. Without it, the bypass branch in
// publish_worker would 400 from YouTube on every (privacy=public +
// publishAt=future) post.
func TestCoercePrivacyForUpdate(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	futurePublish := now.Add(24 * time.Hour)
	pastPublish := now.Add(-24 * time.Hour)
	zeroPublish := time.Time{}

	cases := []struct {
		name              string
		inPrivacy         string
		inPublishAt       *time.Time
		wantPrivacy       string
		wantPublishAtKind publishAtKind // "nil" | "same-pointer" | "different-pointer"
	}{
		// ── nil publishAt → pass-through (clears on output anyway)
		{"nil publishAt, public", "public", nil, "public", publishAtNil},
		{"nil publishAt, unlisted", "unlisted", nil, "unlisted", publishAtNil},
		{"nil publishAt, private", "private", nil, "private", publishAtNil},

		// ── zero publishAt → treated as nil (defensive)
		{"zero publishAt, public", "public", &zeroPublish, "public", publishAtNil},

		// ── past publishAt → cleared (YouTube can't reschedule in the past)
		{"past publishAt, public", "public", &pastPublish, "public", publishAtNil},
		{"past publishAt, unlisted", "unlisted", &pastPublish, "unlisted", publishAtNil},
		{"past publishAt, private", "private", &pastPublish, "private", publishAtNil},

		// ── past publishAt AT exactly `now` → cleared (After(now) is strict)
		{"now-boundary publishAt, public (boundary)", "public", &now, "public", publishAtNil},

		// ── future publishAt → ALWAYS force "private" regardless of input privacy
		{"future publishAt, public → private", "public", &futurePublish, "private", publishAtSamePointer},
		{"future publishAt, unlisted → private", "unlisted", &futurePublish, "private", publishAtSamePointer},
		{"future publishAt, private → private (no-op)", "private", &futurePublish, "private", publishAtSamePointer},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPrivacy, gotPublishAt := CoercePrivacyForUpdate(tc.inPrivacy, tc.inPublishAt, now)
			if gotPrivacy != tc.wantPrivacy {
				t.Errorf("privacy: want %q, got %q", tc.wantPrivacy, gotPrivacy)
			}
			switch tc.wantPublishAtKind {
			case publishAtNil:
				if gotPublishAt != nil {
					t.Errorf("publishAt: want nil, got %p (non-nil)", gotPublishAt)
				}
			case publishAtSamePointer:
				// Crucial: same-pointer identity proves the helper
				// preserves the input pointer so downstream callers
				// can rely on ref-equal updates staying consistent.
				if gotPublishAt == nil {
					t.Errorf("publishAt: want non-nil (future branch must propagate the input pointer), got nil")
				} else if gotPublishAt != tc.inPublishAt {
					t.Errorf("publishAt: want SAME-POINTER identity with input (%p), got distinct pointer %p", tc.inPublishAt, gotPublishAt)
				}
			case publishAtDifferentPointer:
				if gotPublishAt == tc.inPublishAt {
					t.Errorf("publishAt: want DIFFERENT pointer, got SAME-POINTER identity")
				}
			default:
				t.Fatalf("test setup: unknown publishAtKind %d", tc.wantPublishAtKind)
			}
		})
	}
}

// publishAtKind labels the expected publishAt return shape. Internal
// to the test (matches the gofmt-friendly iota pattern).
type publishAtKind int

const (
	publishAtNil publishAtKind = iota
	publishAtSamePointer
	publishAtDifferentPointer
)

// TestCoercePrivacyForUpdate_Idempotent pins the fixed-point property
// of the helper: applying it twice to the same inputs produces the
// same output as applying it once. This matters because publish_worker
// calls the helper from BOTH the bypass AND inside UpdateVideoPrivacy
// (defense in depth) — double-coercion must not change the wire-format.
//
// Blocco #1 followup — Finding #2 idempotency guarantee.
func TestCoercePrivacyForUpdate_Idempotent(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)

	type step struct {
		privacy string
		at      *time.Time
	}
	mkFuture := func(at *time.Time) step { return step{"public", at} }

	scenarios := []struct {
		name string
		in   step
	}{
		{"future+public", mkFuture(&future)},
		{"future+unlisted", mkFuture(&future)},
		{"past+public", mkFuture(&future)}, // overwritten below
	}
	scenarios[2].in.privacy = "public"
	scenarios[2].in.at = func() *time.Time { t := now.Add(-time.Hour); return &t }()

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			oncePriv, onceAt := CoercePrivacyForUpdate(sc.in.privacy, sc.in.at, now)
			twicePriv, twiceAt := CoercePrivacyForUpdate(oncePriv, onceAt, now)
			if oncePriv != twicePriv {
				t.Errorf("privacy: first call %q, second call %q — helper must be idempotent", oncePriv, twicePriv)
			}
			// Pointer-identity identity (or shared nil) is fine.
			if (onceAt == nil) != (twiceAt == nil) {
				t.Errorf("publishAt nil/non-nil mismatch: first=%v second=%v", onceAt, twiceAt)
			}
			if onceAt != nil && twiceAt != nil && onceAt != twiceAt {
				t.Errorf("publishAt pointer changed between calls: first=%p second=%p", onceAt, twiceAt)
			}
		})
	}
}
