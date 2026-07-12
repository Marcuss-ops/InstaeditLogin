// Package services — SPRINT 5.2 (P1#10) unit tests for the
// rate-limit error + Retry-After parser added to provider.go.
//
// Covers the user-spec scenario: "retry su rate_limited rispettando
// Retry-After (non backoff fisso)". The PublishWorker's path on a
// RateLimitError is to set next_retry_at = now + RetryAfter (the
// platform's hint, not a fixed backoff) and clear the lease. These
// tests pin the parser + typed-error plumbing that drive that path.
package services

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRateLimitError_ImplementsError confirms the typed error
// implements the error interface (compile-time via the var _ error
// assignment + runtime via Error()).
func TestRateLimitError_ImplementsError(t *testing.T) {
	var _ error = &RateLimitError{RetryAfter: 90 * time.Second}
	rle := &RateLimitError{RetryAfter: 90 * time.Second}
	got := rle.Error()
	if !strings.Contains(got, "rate limited") {
		t.Fatalf("expected error message to mention 'rate limited', got %q", got)
	}
	if !strings.Contains(got, "1m30s") {
		t.Fatalf("expected error message to include RetryAfter duration %v, got %q", rle.RetryAfter, got)
	}
}

// TestIsRateLimitError_DetectsTyped covers the canonical case: the
// provider returned a *RateLimitError directly.
func TestIsRateLimitError_DetectsTyped(t *testing.T) {
	err := &RateLimitError{RetryAfter: 60 * time.Second}
	if !IsRateLimitError(err) {
		t.Fatal("IsRateLimitError should return true for *RateLimitError")
	}
}

// TestIsRateLimitError_DetectsWrapped covers the case where a
// platform wraps the error via fmt.Errorf("...: %w", rle). The
// worker must detect the wrapped form because real provider
// implementations rarely return the typed error directly — they
// wrap it with platform context.
func TestIsRateLimitError_DetectsWrapped(t *testing.T) {
	rle := &RateLimitError{RetryAfter: 30 * time.Second}
	wrapped := fmt.Errorf("tiktok publish: %w", rle)
	if !IsRateLimitError(wrapped) {
		t.Fatal("IsRateLimitError should return true for a wrapped *RateLimitError")
	}
}

// TestIsRateLimitError_RejectsOtherErrors confirms unrelated errors
// are not classified as rate-limit (the typed-detection must be
// strict, not string-match).
func TestIsRateLimitError_RejectsOtherErrors(t *testing.T) {
	cases := []error{
		nil,
		errors.New("network timeout"),
		fmt.Errorf("tiktok: server returned 500"),
		&someOtherErrorType{msg: "rate limited"}, // string-match must NOT match
	}
	for i, err := range cases {
		if IsRateLimitError(err) {
			t.Errorf("case %d (%v): IsRateLimitError should be false", i, err)
		}
	}
}

// someOtherErrorType is a non-RateLimitError type whose Error()
// string happens to contain "rate limited". The IsRateLimitError
// helper must NOT match it — typed detection only.
type someOtherErrorType struct{ msg string }

func (e *someOtherErrorType) Error() string { return e.msg }

// TestParseRetryAfter_DeltaSeconds is the canonical RFC 7231 form:
// "120" → 120s.
func TestParseRetryAfter_DeltaSeconds(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	got := ParseRetryAfter("120", now)
	if got != 120*time.Second {
		t.Fatalf("ParseRetryAfter(\"120\"): want 120s, got %v", got)
	}
}

// TestParseRetryAfter_GoDuration is the second canonical form:
// "2m30s" → 150s.
func TestParseRetryAfter_GoDuration(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	got := ParseRetryAfter("2m30s", now)
	if got != 150*time.Second {
		t.Fatalf("ParseRetryAfter(\"2m30s\"): want 150s, got %v", got)
	}
}

// TestParseRetryAfter_HTTPLongDate is the third RFC 7231 form:
// HTTP-date relative to now().
func TestParseRetryAfter_HTTPLongDate(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	// Build a date 90 seconds in the future.
	future := now.Add(90 * time.Second).UTC().Format(time.RFC1123)
	got := ParseRetryAfter(future, now)
	if got < 89*time.Second || got > 91*time.Second {
		t.Fatalf("ParseRetryAfter(HTTP-date): want ~90s, got %v", got)
	}
}

// TestParseRetryAfter_UnixEpoch covers the X-RateLimit-Reset
// convention: a large integer (year > 2026) is treated as epoch
// seconds and converted to a relative duration.
func TestParseRetryAfter_UnixEpoch(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	// 60 seconds in the future = 1799985540 (or thereabouts).
	futureEpoch := now.Add(60 * time.Second).Unix()
	got := ParseRetryAfter(fmt.Sprintf("%d", futureEpoch), now)
	if got < 59*time.Second || got > 61*time.Second {
		t.Fatalf("ParseRetryAfter(epoch): want ~60s, got %v", got)
	}
}

// TestParseRetryAfter_Empty covers the empty input case: the
// worker falls back to the decorrelated-jitter backoff.
func TestParseRetryAfter_Empty(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	got := ParseRetryAfter("", now)
	if got != 0 {
		t.Fatalf("ParseRetryAfter(\"\"): want 0 (use default backoff), got %v", got)
	}
	got = ParseRetryAfter("   ", now)
	if got != 0 {
		t.Fatalf("ParseRetryAfter(\"   \"): want 0 (whitespace), got %v", got)
	}
}

// TestParseRetryAfter_Malformed covers the unparseable case: the
// worker falls back to the default backoff (no panic, no negative).
func TestParseRetryAfter_Malformed(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	got := ParseRetryAfter("not-a-number-or-date", now)
	if got != 0 {
		t.Fatalf("ParseRetryAfter(malformed): want 0, got %v", got)
	}
}

// TestParseRetryAfter_AlreadyExpired covers the past-tense edge
// case: an already-expired Retry-After (negative duration) is
// clamped to 0 so the worker retries immediately rather than
// waiting a negative amount of time.
func TestParseRetryAfter_AlreadyExpired(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	// Past date.
	past := now.Add(-1 * time.Hour).UTC().Format(time.RFC1123)
	got := ParseRetryAfter(past, now)
	if got != 0 {
		t.Fatalf("ParseRetryAfter(past date): want 0 (clamp to immediate), got %v", got)
	}
	// Past epoch.
	pastEpoch := now.Add(-1 * time.Hour).Unix()
	got = ParseRetryAfter(fmt.Sprintf("%d", pastEpoch), now)
	if got != 0 {
		t.Fatalf("ParseRetryAfter(past epoch): want 0, got %v", got)
	}
	// Negative delta-seconds (malformed-but-numeric).
	got = ParseRetryAfter("-5", now)
	if got != 0 {
		t.Fatalf("ParseRetryAfter(\"-5\"): want 0, got %v", got)
	}
}

// TestParseRetryAfter_SmallIntegerIsDelta is the boundary test
// for the delta-vs-epoch heuristic: an integer <= 1e7 is treated
// as a relative duration (delta-seconds per RFC 7231), not as
// epoch seconds. 1e7 seconds = ~115 days, the max meaningful
// delta-seconds a platform would send.
func TestParseRetryAfter_SmallIntegerIsDelta(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	// 1e7 is the boundary — must be a delta, not an epoch.
	got := ParseRetryAfter("10000000", now)
	if got != 1e7*time.Second {
		t.Fatalf("ParseRetryAfter(\"10000000\"): want 1e7 seconds (delta), got %v", got)
	}
	// 1e7+1 must be epoch. Use a FUTURE epoch (~now + 60s) so the
	// expected duration is positive (already-past epochs are
	// clamped to 0 by the test for already-expired Retry-After
	// values, which would make this assertion a no-op).
	epoch := now.Add(60 * time.Second).Unix()
	got = ParseRetryAfter(fmt.Sprintf("%d", epoch), now)
	if got < 59*time.Second || got > 61*time.Second {
		t.Fatalf("ParseRetryAfter(epoch %d ~ %s): want ~60s, got %v",
			epoch, time.Unix(epoch, 0).UTC().Format(time.RFC3339), got)
	}
}
