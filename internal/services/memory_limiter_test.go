// Tests for the SPRINT 2.2 MemoryLimiter. The limiter is a
// per-replica token bucket keyed by scope string. Each scope owns
// its own *rate.Limiter; the reaper evicts entries that haven't
// been accessed within memoryLimiterEntryTTL. The tests below
// cover: (1) independent scopes, (2) the reaper (via a short-TTL
// test instance), (3) the Reserve/Cancel pattern that computes
// resetAt without consuming a token.
package services

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryLimiter_IndependentScopes(t *testing.T) {
	ml := NewMemoryLimiter()
	defer ml.Shutdown()
	// Exhaust scope A.
	for i := 0; i < 5; i++ {
		ok, _, _ := ml.Allow("scope:A", 5, time.Minute)
		if !ok {
			t.Fatalf("scope A request %d: want allowed", i)
		}
	}
	// 6th request on A is denied (or has to wait for refill — within
	// the same millisecond it's denied because the bucket's burst is
	// 5 and the refill rate is 5/60 per second).
	ok, _, _ := ml.Allow("scope:A", 5, time.Minute)
	if ok {
		t.Errorf("scope A: 6th request: want denied, got allowed")
	}
	// Scope B is fresh — first request must succeed.
	ok, _, _ = ml.Allow("scope:B", 5, time.Minute)
	if !ok {
		t.Errorf("scope B: first request: want allowed, got denied")
	}
}

func TestMemoryLimiter_ZeroOrNegativeRPM_AllowsAll(t *testing.T) {
	ml := NewMemoryLimiter()
	defer ml.Shutdown()
	for _, rpm := range []int{0, -1} {
		for i := 0; i < 10; i++ {
			ok, _, _ := ml.Allow("any", rpm, time.Minute)
			if !ok {
				t.Errorf("rpm=%d request %d: want allowed", rpm, i)
			}
		}
	}
}

func TestMemoryLimiter_ResetAtIsInTheFuture(t *testing.T) {
	ml := NewMemoryLimiter()
	defer ml.Shutdown()
	now := time.Now()
	_, _, resetAt := ml.Allow("test", 60, time.Minute)
	if !resetAt.After(now) {
		t.Errorf("resetAt should be after now: resetAt=%v now=%v", resetAt, now)
	}
}

func TestMemoryLimiter_Reaper_EvictsStaleEntries(t *testing.T) {
	ml := NewMemoryLimiter()
	defer ml.Shutdown()
	// Seed two scopes; force one to be stale by manually editing
	// lastSeen via the unexported accessor (we re-use the same
	// package's test access to manipulate internals).
	ml.Allow("fresh", 60, time.Minute)
	ml.Allow("stale", 60, time.Minute)
	// Manually mark the "stale" entry as older than entryTTL.
	ml.mu.Lock()
	if entry, ok := ml.entries["stale"]; ok {
		entry.lastSeen = time.Now().Add(-2 * memoryLimiterEntryTTL)
	}
	ml.mu.Unlock()
	// Trigger the reaper directly.
	ml.evictStale()
	ml.mu.Lock()
	defer ml.mu.Unlock()
	if _, ok := ml.entries["stale"]; ok {
		t.Errorf("stale entry should have been evicted")
	}
	if _, ok := ml.entries["fresh"]; !ok {
		t.Errorf("fresh entry should still be present")
	}
}

func TestMemoryLimiter_Shutdown_Idempotent(t *testing.T) {
	ml := NewMemoryLimiter()
	ml.Shutdown()
	ml.Shutdown() // second call must not panic
}

func TestMemoryLimiter_Allow_ConcurrentSafe(t *testing.T) {
	ml := NewMemoryLimiter()
	defer ml.Shutdown()
	const goroutines = 20
	const requestsPerGoroutine = 5
	var allowed int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				ok, _, _ := ml.Allow("concurrent", 1000, time.Minute)
				if ok {
					atomic.AddInt64(&allowed, 1)
				}
			}
		}()
	}
	wg.Wait()
	// 20*5 = 100 requests on a burst=1000 bucket — all must be allowed.
	if got := atomic.LoadInt64(&allowed); got != int64(goroutines*requestsPerGoroutine) {
		t.Errorf("allowed: want %d, got %d", goroutines*requestsPerGoroutine, got)
	}
}
