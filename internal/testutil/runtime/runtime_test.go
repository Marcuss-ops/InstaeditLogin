// Package runtime_test — unit tests for internal/testutil/runtime.
//
// Pure-callback tests; no Docker required. The test file uses
// package runtime_test (external) to test the public API; the
// fakeTB helper at the bottom is local to this file.
//
// The 4 test cases the user spec mandates:
//
//  1. TestWaitReady_SuccessOnFirstAttempt — ping returns nil on
//     the first call; WaitReady returns immediately.
//  2. TestWaitReady_SuccessOnNthAttempt — ping fails K-1 times
//     then succeeds on call K; WaitReady returns after K calls.
//  3. TestWaitReady_TimeoutFatalfMessageFormat — ping always
//     fails; WaitReady calls Fatalf with a message that names the
//     attempt count, the deadline, and the last ping error.
//     Asserts on the captured Fatalf format string + args. Locks
//     in the CI-debugging artifact format.
//  4. TestWaitReady_DefaultResolution — passing deadline=0 or
//     backoff=0 must resolve to WaitReadyDefaultDeadline /
//     WaitReadyDefaultBackoff. Verified behaviorally.
package runtime_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/runtime"
)

// TestWaitReady_SuccessOnFirstAttempt: a ping that returns nil on
// the first call must cause WaitReady to return immediately, with
// exactly one ping call. No Fatalf should fire.
func TestWaitReady_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	ping := func() error {
		calls++
		return nil
	}

	runtime.WaitReady(t, ping, time.Second, 100*time.Millisecond)

	if calls != 1 {
		t.Errorf("ping calls: want 1, got %d (WaitReady should return on the first successful ping without polling)", calls)
	}
}

// TestWaitReady_SuccessOnNthAttempt: a ping that fails K-1 times
// then succeeds on call K must cause WaitReady to return after
// the Kth call, with exactly K ping calls. The wall-clock duration
// is at least (K-1) * backoff — each failed attempt is followed
// by a backoff sleep, but the final successful attempt has no
// sleep after it.
func TestWaitReady_SuccessOnNthAttempt(t *testing.T) {
	const failUntil = 3
	calls := 0
	ping := func() error {
		calls++
		if calls < failUntil {
			return errors.New("transient ping failure")
		}
		return nil
	}

	const backoff = 50 * time.Millisecond
	start := time.Now()
	runtime.WaitReady(t, ping, time.Second, backoff)
	elapsed := time.Since(start)

	if calls != failUntil {
		t.Errorf("ping calls: want %d, got %d (WaitReady should retry until success)", failUntil, calls)
	}

	// (K-1) backoff sleeps happened (after each of the K-1 failed
	// attempts); the final successful attempt has no sleep after
	// it. Allow a 30ms floor for scheduler jitter on the lower
	// bound.
	minExpected := time.Duration(failUntil-1)*backoff - 30*time.Millisecond
	if elapsed < minExpected {
		t.Errorf("elapsed: want >= %v (= %d backoffs), got %v", minExpected, failUntil-1, elapsed)
	}
}

// TestWaitReady_TimeoutFatalfMessageFormat: a ping that always
// fails must cause WaitReady to call Fatalf with a message that
// names the attempt count, the deadline, and the last ping error.
// This locks in the CI-debug artifact format so a future refactor
// that breaks the format also breaks a test (not a CI failure
// whose root cause is hidden by a different message).
//
// Uses fakeTB to capture the Fatalf call: format string + args.
// The fake TB does NOT propagate the failure to the parent test,
// so the parent can assert on the captured values.
func TestWaitReady_TimeoutFatalfMessageFormat(t *testing.T) {
	fake := &fakeTB{T: t}

	const sentinelErr = "sentinel-error-text-7c4a2"
	attempts := 0
	ping := func() error {
		attempts++
		return errors.New(sentinelErr)
	}

	const deadline = 50 * time.Millisecond
	const backoff = 10 * time.Millisecond
	runtime.WaitReady(fake, ping, deadline, backoff)

	if !fake.failed {
		t.Fatal("WaitReady should have called Fatalf (the deadline expired and the loop never saw a successful ping)")
	}
	if fake.lastFormat == "" {
		t.Fatal("fakeTB.Fatalf was called with an empty format string (should never happen)")
	}

	// Assert the format string contains the canonical substrings.
	// The exact format is documented in runtime.WaitReady's doc —
	// a future refactor that breaks the format would also break
	// CI-debugging scripts that grep for these substrings.
	expectedFormatSubstrings := []string{
		"WaitReady",
		"timeout",
		"attempt(s)",
		"last ping error",
	}
	for _, want := range expectedFormatSubstrings {
		if !strings.Contains(fake.lastFormat, want) {
			t.Errorf("Fatalf format missing %q\nfull format: %s", want, fake.lastFormat)
		}
	}

	// Assert the args contain the expected typed values:
	//   args[0] = attempt count (int)
	//   args[1] = deadline (time.Duration)
	//   args[2] = lastErr (error)
	if len(fake.lastArgs) < 3 {
		t.Fatalf("Fatalf args: want >=3 (attempt, deadline, lastErr), got %d: %v",
			len(fake.lastArgs), fake.lastArgs)
	}
	if attempt, ok := fake.lastArgs[0].(int); !ok {
		t.Errorf("args[0]: want int (attempt count), got %T", fake.lastArgs[0])
	} else if attempt < 1 {
		t.Errorf("attempt count: want >=1, got %d", attempt)
	} else if attempt != attempts {
		// The count of attempts observed in the ping closure must
		// match the attempt number reported in the Fatalf message.
		// A drift here would mean the loop's attempt counter is
		// off-by-one vs. the ping's actual call count.
		t.Errorf("attempt count: want %d (observed in ping), got %d (in Fatalf)",
			attempts, attempt)
	}
	if d, ok := fake.lastArgs[1].(time.Duration); !ok {
		t.Errorf("args[1]: want time.Duration (deadline), got %T", fake.lastArgs[1])
	} else if d != deadline {
		t.Errorf("deadline: want %v, got %v", deadline, d)
	}
	if lastErr, ok := fake.lastArgs[2].(error); !ok {
		t.Errorf("args[2]: want error, got %T", fake.lastArgs[2])
	} else if lastErr.Error() != sentinelErr {
		t.Errorf("lastErr: want %q, got %q", sentinelErr, lastErr.Error())
	}

	// The loop should have iterated at least 2 times (50ms
	// deadline / 10ms backoff = 5 expected iterations; allow some
	// slack on the lower bound).
	if attempts < 2 {
		t.Errorf("attempts: want >=2 (50ms deadline, 10ms backoff), got %d", attempts)
	}
}

// TestWaitReady_DefaultResolution: passing deadline=0 or backoff=0
// must resolve to WaitReadyDefaultDeadline / WaitReadyDefaultBackoff.
// Verified behaviorally rather than by reading the constant values
// (which would just test the constant declarations and miss a
// refactor that broke the resolution path).
func TestWaitReady_DefaultResolution(t *testing.T) {
	t.Run("DeadlineZero", func(t *testing.T) {
		// Pass deadline=0 + immediate-success ping. WaitReady
		// should resolve deadline=0 to WaitReadyDefaultDeadline
		// (15s) and return after the first successful ping. If
		// deadline=0 were misinterpreted as "no time allowed", the
		// very first ping's deadline check would fire and the
		// helper would log a spurious "WaitReady: timeout after 1
		// attempt(s) over 0s" before any actual readiness attempt.
		calls := 0
		runtime.WaitReady(t, func() error {
			calls++
			return nil
		}, 0, 10*time.Millisecond)
		if calls != 1 {
			t.Errorf("ping calls: want 1, got %d (deadline=0 should resolve to WaitReadyDefaultDeadline, not 'no time allowed')",
				calls)
		}
	})

	t.Run("BackoffZero", func(t *testing.T) {
		// Pass backoff=0 + 2-attempt-success ping. WaitReady should
		// resolve backoff=0 to WaitReadyDefaultBackoff (200ms) and
		// sleep ~200ms between the 2 attempts. If backoff=0 were
		// misinterpreted as "no sleep", the loop would spin at
		// 100% CPU and complete in microseconds; the wall-clock
		// should prove the default was used.
		calls := 0
		start := time.Now()
		runtime.WaitReady(t, func() error {
			calls++
			if calls < 2 {
				return errors.New("transient")
			}
			return nil
		}, time.Second, 0)
		elapsed := time.Since(start)

		if calls != 2 {
			t.Errorf("ping calls: want 2, got %d", calls)
		}
		// backoff=0 should resolve to WaitReadyDefaultBackoff
		// (200ms). One backoff sleep happened (between the 2
		// attempts). Allow 100ms..600ms slack for CI jitter
		// and goroutine scheduling latency.
		minExpected := 100 * time.Millisecond
		maxExpected := 600 * time.Millisecond
		if elapsed < minExpected || elapsed > maxExpected {
			t.Errorf("elapsed: want %v..%v (backoff=0 should resolve to WaitReadyDefaultBackoff=200ms), got %v",
				minExpected, maxExpected, elapsed)
		}
	})
}

// fakeTB is a minimal testing.TB implementation that captures
// Fatalf calls (format string + args) without actually failing
// the parent test. Used to test WaitReady's timeout path in
// isolation — the alternative (os.Pipe stdout capture or recover
// + test-state inspection) is fragile because the testing package
// buffers output internally and does not expose the formatted
// Fatalf message after a Goexit.
//
// The struct embeds *testing.T to inherit the unexported methods
// (notably private()) that the testing.TB interface requires. We
// only override the public methods we want to intercept — all
// other TB methods (Helper, Skipf, Cleanup, etc.) are inherited
// from the embedded *testing.T and behave normally.
type fakeTB struct {
	*testing.T

	// failed is set to true when Fatalf is called. Replaces the
	// testing framework's internal failure tracking for the
	// purposes of this test (the framework's tracking is for the
	// PARENT test; fakeTB's override lets the test continue past
	// the Fatalf to assert on the captured args).
	failed bool

	// lastFormat + lastArgs capture the most recent Fatalf call's
	// format string + args. The test asserts on these to verify
	// the timeout message contains the expected fields.
	lastFormat string
	lastArgs   []any
}

// Fatalf captures the format + args and marks failed=true. We do
// NOT call t.Fatalf or runtime.Goexit — the parent test
// continues to assert on lastFormat/lastArgs after WaitReady
// returns. This is the entire point of the fake: it lets the test
// observe a Fatalf call without it terminating the test
// goroutine.
func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.lastFormat = format
	f.lastArgs = args
}
