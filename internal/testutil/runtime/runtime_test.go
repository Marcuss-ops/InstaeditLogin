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
	// it. The lower bound subtracts 50ms of slack for heavily-loaded
	// CI runners (Goroutine scheduling latency can dwarf the
	// 50ms per-iteration sleep). The upper bound adds 500ms to catch
	// infinite-loop regressions (a runaway loop would blow past
	// this and fail the test).
	minExpected := time.Duration(failUntil-1)*backoff - 50*time.Millisecond
	maxExpected := time.Duration(failUntil-1)*backoff + 500*time.Millisecond
	if elapsed < minExpected {
		t.Errorf("elapsed: want >= %v (= %d backoffs - 50ms slack), got %v", minExpected, failUntil-1, elapsed)
	}
	if elapsed > maxExpected {
		t.Errorf("elapsed: want <= %v (= %d backoffs + 500ms ceiling), got %v (possible infinite loop regression)", maxExpected, failUntil-1, elapsed)
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

	t.Run("DefaultsAreSensible", func(t *testing.T) {
		// The default-resolution path in WaitReady is:
		//   if deadline <= 0 { deadline = WaitReadyDefaultDeadline }
		//   if backoff  <= 0 { backoff  = WaitReadyDefaultBackoff  }
		// This subtest locks in the constant values (the intended
		// defaults) so an accidental change to 30s/500ms is caught
		// by a test, not by surprise in production. The DeadlineZero
		// + BackoffZero subtests above prove the resolution code
		// path doesn't crash on 0; this one proves the values the
		// path uses are the values we expect.
		if runtime.WaitReadyDefaultDeadline != 15*time.Second {
			t.Errorf("WaitReadyDefaultDeadline: want 15s, got %v", runtime.WaitReadyDefaultDeadline)
		}
		if runtime.WaitReadyDefaultBackoff != 200*time.Millisecond {
			t.Errorf("WaitReadyDefaultBackoff: want 200ms, got %v", runtime.WaitReadyDefaultBackoff)
		}
	})

	t.Run("ZeroDefaults_Behavioral", func(t *testing.T) {
		// Pass deadline=0 + backoff=0 + a 2-attempt-fail-then-succeed
		// ping. With correct default-resolution: deadline=15s,
		// backoff=200ms. The loop sleeps 200ms between the 2 attempts
		// and the second attempt succeeds. Total wall-clock ~200ms.
		//
		// With BROKEN default-resolution:
		//   - If backoff=0 means "no sleep": loop runs 2 attempts
		//     with no sleep between them, wall-clock ~0ms
		//     (microseconds). Lower-bound assertion fails.
		//   - If deadline=0 means "no time allowed": the deadline
		//     check fires after the first failed attempt and
		//     t.Fatalf is called. The test fails loudly via Fatalf
		//     (not a value mismatch — an unmissable failure).
		//
		// Behavioral complement to the DefaultsAreSensible constant-
		// check subtest: that one would still pass if the resolution
		// code were entirely removed (the constants are still 15s/
		// 200ms, just not USED). This one actually exercises the
		// resolution path — it's the only subtest that would catch
		// a regression like "someone deletes the `if deadline <= 0`
		// block".
		calls := 0
		start := time.Now()
		runtime.WaitReady(t, func() error {
			calls++
			if calls < 2 {
				return errors.New("transient")
			}
			return nil
		}, 0, 0)
		elapsed := time.Since(start)

		if calls != 2 {
			t.Errorf("ping calls: want 2, got %d (deadline=0 + backoff=0 should resolve to defaults, allowing the 2nd attempt to succeed within WaitReadyDefaultDeadline=15s)", calls)
		}

		// With correct resolution: 1 default backoff (200ms)
		// between the 2 attempts. Allow 150ms..700ms slack
		// (50ms floor for goroutine scheduling jitter; 500ms
		// ceiling catches "deadline=0 resolved to 15s but
		// ping stalled" or other regression shapes).
		minExpected := 150 * time.Millisecond
		maxExpected := 700 * time.Millisecond
		if elapsed < minExpected {
			t.Errorf("elapsed: want >= %v (= 1 default backoff - 50ms slack), got %v (backoff=0 must have resolved to WaitReadyDefaultBackoff=200ms)",
				minExpected, elapsed)
		}
		if elapsed > maxExpected {
			t.Errorf("elapsed: want <= %v (1 default backoff + 500ms ceiling), got %v (possible default-resolution regression)",
				maxExpected, elapsed)
		}
	})
}

// TestWaitReadyMatch_SuccessOnFirstAttempt: a match that returns
// (true, nil) on the first call must cause WaitReadyMatch to return
// immediately, with exactly one match call.
func TestWaitReadyMatch_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	match := func() (bool, error) {
		calls++
		return true, nil
	}

	runtime.WaitReadyMatch(t, match, time.Second, 100*time.Millisecond)

	if calls != 1 {
		t.Errorf("match calls: want 1, got %d (WaitReadyMatch should return on the first match without polling)", calls)
	}
}

// TestWaitReadyMatch_SuccessOnNthAttempt: a match that returns
// (false, nil) K-1 times then (true, nil) on call K must cause
// WaitReadyMatch to return after K calls, with exactly K match
// calls. The wall-clock duration is at least (K-1) * backoff —
// each non-matching attempt is followed by a backoff sleep, but
// the final matching attempt has no sleep after it.
func TestWaitReadyMatch_SuccessOnNthAttempt(t *testing.T) {
	const matchUntil = 3
	calls := 0
	match := func() (bool, error) {
		calls++
		if calls < matchUntil {
			return false, nil
		}
		return true, nil
	}

	const backoff = 50 * time.Millisecond
	start := time.Now()
	runtime.WaitReadyMatch(t, match, time.Second, backoff)
	elapsed := time.Since(start)

	if calls != matchUntil {
		t.Errorf("match calls: want %d, got %d (WaitReadyMatch should retry until match)", matchUntil, calls)
	}

	// (K-1) backoff sleeps happened (after each of the K-1
	// non-matching attempts); the final matching attempt has no
	// sleep after it. The lower bound subtracts 50ms of slack for
	// heavily-loaded CI runners; the upper bound adds 500ms to
	// catch infinite-loop regressions.
	minExpected := time.Duration(matchUntil-1)*backoff - 50*time.Millisecond
	maxExpected := time.Duration(matchUntil-1)*backoff + 500*time.Millisecond
	if elapsed < minExpected {
		t.Errorf("elapsed: want >= %v (= %d backoffs - 50ms slack), got %v", minExpected, matchUntil-1, elapsed)
	}
	if elapsed > maxExpected {
		t.Errorf("elapsed: want <= %v (= %d backoffs + 500ms ceiling), got %v (possible infinite loop regression)", maxExpected, matchUntil-1, elapsed)
	}
}

// TestWaitReadyMatch_TimeoutFatalfMessageFormat: a match that
// always returns (false, nil) must cause WaitReadyMatch to call
// Fatalf with a message that names the attempt count, the
// deadline, and the last match error. This locks in the CI-debug
// artifact format so a future refactor that breaks the format
// also breaks a test (not a CI failure whose root cause is
// hidden by a different message).
//
// Distinct from WaitReady's format: WaitReadyMatch says
// "last match error" instead of WaitReady's "last ping error".
// The two Fatalf substrings are intentionally different so
// CI-debugging scripts can grep them apart.
func TestWaitReadyMatch_TimeoutFatalfMessageFormat(t *testing.T) {
	fake := &fakeTB{T: t}

	attempts := 0
	match := func() (bool, error) {
		attempts++
		return false, nil
	}

	const deadline = 50 * time.Millisecond
	const backoff = 10 * time.Millisecond
	runtime.WaitReadyMatch(fake, match, deadline, backoff)

	if !fake.failed {
		t.Fatal("WaitReadyMatch should have called Fatalf (the deadline expired and the loop never saw a match)")
	}
	if fake.lastFormat == "" {
		t.Fatal("fakeTB.Fatalf was called with an empty format string (should never happen)")
	}

	// Assert the format string contains the canonical substrings.
	expectedFormatSubstrings := []string{
		"WaitReadyMatch",
		"timeout",
		"attempt(s)",
		"last match error",
	}
	for _, want := range expectedFormatSubstrings {
		if !strings.Contains(fake.lastFormat, want) {
			t.Errorf("Fatalf format missing %q\nfull format: %s", want, fake.lastFormat)
		}
	}

	// Assert the args contain the expected typed values:
	//   args[0] = attempt count (int)
	//   args[1] = deadline (time.Duration)
	//   args[2] = lastErr (error, may be nil if the match never errored)
	if len(fake.lastArgs) < 3 {
		t.Fatalf("Fatalf args: want >=3 (attempt, deadline, lastErr), got %d: %v",
			len(fake.lastArgs), fake.lastArgs)
	}
	if attempt, ok := fake.lastArgs[0].(int); !ok {
		t.Errorf("args[0]: want int (attempt count), got %T", fake.lastArgs[0])
	} else if attempt < 1 {
		t.Errorf("attempt count: want >=1, got %d", attempt)
	} else if attempt != attempts {
		// The count of attempts observed in the match closure
		// must match the attempt number reported in the Fatalf
		// message. A drift here would mean the loop's attempt
		// counter is off-by-one vs. the match's actual call
		// count.
		t.Errorf("attempt count: want %d (observed in match), got %d (in Fatalf)",
			attempts, attempt)
	}
	if d, ok := fake.lastArgs[1].(time.Duration); !ok {
		t.Errorf("args[1]: want time.Duration (deadline), got %T", fake.lastArgs[1])
	} else if d != deadline {
		t.Errorf("deadline: want %v, got %v", deadline, d)
	}
	// args[2] (lastErr) is allowed to be nil because the match
	// closure here returns (false, nil) — the timeout can be a
	// clean state-mismatch rather than an underlying error. The
	// format string prints "<nil>" in that case (Go's %v on a
	// nil error).

	// The loop should have iterated at least 2 times (50ms
	// deadline / 10ms backoff = 5 expected iterations; allow
	// some slack on the lower bound).
	if attempts < 2 {
		t.Errorf("attempts: want >=2 (50ms deadline, 10ms backoff), got %d", attempts)
	}
}

// TestWaitReadyMatch_DefaultResolution: passing deadline=0 or
// backoff=0 must resolve to WaitReadyDefaultDeadline /
// WaitReadyDefaultBackoff. Mirrors TestWaitReady_DefaultResolution
// — same structure, different helper.
func TestWaitReadyMatch_DefaultResolution(t *testing.T) {
	t.Run("DeadlineZero", func(t *testing.T) {
		// Pass deadline=0 + immediate-match. WaitReadyMatch should
		// resolve deadline=0 to WaitReadyDefaultDeadline (15s) and
		// return after the first match. If deadline=0 were
		// misinterpreted as "no time allowed", the very first
		// poll's deadline check would fire and the helper would
		// log a spurious "WaitReadyMatch: timeout after 1
		// attempt(s) over 0s" before any actual poll.
		calls := 0
		runtime.WaitReadyMatch(t, func() (bool, error) {
			calls++
			return true, nil
		}, 0, 10*time.Millisecond)
		if calls != 1 {
			t.Errorf("match calls: want 1, got %d (deadline=0 should resolve to WaitReadyDefaultDeadline, not 'no time allowed')",
				calls)
		}
	})

	t.Run("BackoffZero", func(t *testing.T) {
		// Pass backoff=0 + 2-attempt-match. WaitReadyMatch should
		// resolve backoff=0 to WaitReadyDefaultBackoff (200ms) and
		// sleep ~200ms between the 2 attempts. If backoff=0 were
		// misinterpreted as "no sleep", the loop would spin at
		// 100% CPU and complete in microseconds; the wall-clock
		// should prove the default was used.
		calls := 0
		start := time.Now()
		runtime.WaitReadyMatch(t, func() (bool, error) {
			calls++
			if calls < 2 {
				return false, nil
			}
			return true, nil
		}, time.Second, 0)
		elapsed := time.Since(start)

		if calls != 2 {
			t.Errorf("match calls: want 2, got %d", calls)
		}
		// backoff=0 should resolve to WaitReadyDefaultBackoff
		// (200ms). One backoff sleep happened (between the 2
		// attempts). Allow 100ms..600ms slack for CI jitter.
		minExpected := 100 * time.Millisecond
		maxExpected := 600 * time.Millisecond
		if elapsed < minExpected || elapsed > maxExpected {
			t.Errorf("elapsed: want %v..%v (backoff=0 should resolve to WaitReadyDefaultBackoff=200ms), got %v",
				minExpected, maxExpected, elapsed)
		}
	})

	t.Run("DefaultsAreSensible", func(t *testing.T) {
		// Same as the WaitReady DefaultsAreSensible subtest — the
		// constants are shared between WaitReady and
		// WaitReadyMatch (both default to the same 15s/200ms
		// budget). Tested once for WaitReady, tested again here
		// for WaitReadyMatch so a future refactor that splits the
		// constants (e.g., per-helper defaults) breaks the right
		// test.
		if runtime.WaitReadyDefaultDeadline != 15*time.Second {
			t.Errorf("WaitReadyDefaultDeadline: want 15s, got %v", runtime.WaitReadyDefaultDeadline)
		}
		if runtime.WaitReadyDefaultBackoff != 200*time.Millisecond {
			t.Errorf("WaitReadyDefaultBackoff: want 200ms, got %v", runtime.WaitReadyDefaultBackoff)
		}
	})

	t.Run("ZeroDefaults_Behavioral", func(t *testing.T) {
		// Behavioral complement to DefaultsAreSensible — see the
		// WaitReady ZeroDefaults_Behavioral subtest for the full
		// reasoning. Same shape: deadline=0 + backoff=0 + a
		// 2-attempt-fail-then-succeed match. Wall-clock assertion
		// proves both deadline and backoff resolutions are wired
		// into the loop, not just declared as constants.
		calls := 0
		start := time.Now()
		runtime.WaitReadyMatch(t, func() (bool, error) {
			calls++
			if calls < 2 {
				return false, nil
			}
			return true, nil
		}, 0, 0)
		elapsed := time.Since(start)

		if calls != 2 {
			t.Errorf("match calls: want 2, got %d (deadline=0 + backoff=0 should resolve to defaults, allowing the 2nd attempt to match within WaitReadyDefaultDeadline=15s)", calls)
		}

		// With correct resolution: 1 default backoff (200ms)
		// between the 2 attempts. Same slack as the WaitReady
		// version: 150ms..700ms.
		minExpected := 150 * time.Millisecond
		maxExpected := 700 * time.Millisecond
		if elapsed < minExpected {
			t.Errorf("elapsed: want >= %v (= 1 default backoff - 50ms slack), got %v (backoff=0 must have resolved to WaitReadyDefaultBackoff=200ms)",
				minExpected, elapsed)
		}
		if elapsed > maxExpected {
			t.Errorf("elapsed: want <= %v (1 default backoff + 500ms ceiling), got %v (possible default-resolution regression)",
				maxExpected, elapsed)
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
//
// SINGLE-GOROUTINE ONLY: the failed/lastFormat/lastArgs fields
// are not guarded by a mutex. A future test that splits across
// parallel subtests (t.Parallel()) and routes Fatalf through this
// fake would race on these fields. WaitReady itself is
// single-goroutine, so this is safe today; the comment is
// defensive for future contributors.
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
