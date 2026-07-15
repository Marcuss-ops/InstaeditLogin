// Package services — MemoryLimiter.
//
// Per-replica in-memory token-bucket limiter used as a coarse
// backstop for the per-IP (OAuth start) and per-endpoint
// (media presign) tiers. The real per-IP gate lives at the edge
// (Cloudflare / reverse proxy) — see docs/OPERATIONS.md.
//
// Each scope string owns its own *rate.Limiter. A background
// goroutine reaps entries that haven't been accessed within
// entryTTL to bound memory growth.
//
// SPRINT 2.2: introduced alongside RateLimitService. The
// MemoryLimiter is a deliberately small surface — only the methods
// the service needs. Tests construct a MemoryLimiter directly
// (NewMemoryLimiter) and pre-seed scopes via the exported
// getOrCreate path or by calling Allow() once with a known scope.
package services

import (
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

const (
	// memoryLimiterCleanupInterval is how often the reaper evicts
	// stale entries. Cheap (one map iteration under lock).
	memoryLimiterCleanupInterval = 5 * time.Minute
	// memoryLimiterEntryTTL is how long an entry lives without
	// being accessed. Must exceed the longest window we use
	// (time.Minute) so a slow caller doesn't lose its bucket
	// mid-window.
	memoryLimiterEntryTTL = 10 * time.Minute
)

// MemoryLimiter is a thread-safe per-scope token-bucket limiter.
// Each scope string maps to one *rate.Limiter; the limiter is
// created on first access and evicted after entryTTL of
// inactivity.
//
// Commit DI refactor: the stopOnce sync.Once field was removed —
// the user's bootstrap DI refactor asked to drop "i sync.Once
// sparsi (... memory limiter)". atomic.Bool.CompareAndSwap gives
// the same exactly-once guarantee on close(stopCh) without
// pulling in the sync primitive. Note the distinction: this is
// not a process-wide lazy-init singleton (compare to pkg/metrics/
// metrics.go's `metricsHandlerOnce`, which IS one and will be
// dropped in commit 2 of this refactor); the CAS guard here is
// an instance-level one-shot-close guard, equivalent to the
// pattern used by pkg/api/onetimecode.go's stopCh lifecycle.
type MemoryLimiter struct {
	mu      sync.Mutex
	entries map[string]*memoryLimiterEntry
	stopCh  chan struct{}
	closed  atomic.Bool // commit DI refactor: replaces stopOnce sync.Once
}

type memoryLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewMemoryLimiter creates a new MemoryLimiter and starts its
// background reaper. Call Shutdown() to stop the reaper.
func NewMemoryLimiter() *MemoryLimiter {
	ml := &MemoryLimiter{
		entries: make(map[string]*memoryLimiterEntry),
		stopCh:  make(chan struct{}),
	}
	go ml.cleanupLoop()
	return ml
}

// Shutdown stops the background reaper. Idempotent — atomic
// CompareAndSwap guarantees the underlying close happens exactly
// once across all callers (replaces the previous sync.Once field;
// commit DI refactor).
func (ml *MemoryLimiter) Shutdown() {
	if ml.closed.CompareAndSwap(false, true) {
		close(ml.stopCh)
	}
}

// Allow checks (and consumes) one token for the supplied scope
// at the supplied rpm (per minute). Returns (allowed, remaining,
// resetAt). When allowed is false, remaining is 0 and resetAt is
// the unix-second when the bucket would refill a token at the
// configured rate.
func (ml *MemoryLimiter) Allow(scope string, rpm int, window time.Duration) (bool, int, time.Time) {
	if rpm <= 0 {
		return true, 0, time.Now().Add(window)
	}
	// Convert rpm -> rate.Limit (events per second).
	lim := ml.getLimiter(scope, rpm)

	// rate.Limiter.Reserve() returns a Reservation that tells us
	// when the next token would be available. We use Allow() for
	// the actual consume and Tokens() for the remaining count
	// (a non-atomic snapshot, but it's the closest the standard
	// library offers).
	if !lim.Allow() {
		// Compute resetAt: the time at which a token would be
		// available. Reserve() is the canonical API for that
		// without consuming; we Cancel() so the reservation
		// doesn't permanently affect the bucket.
		r := lim.Reserve()
		resetAt := r.Delay()
		r.Cancel()
		return false, 0, time.Now().Add(resetAt)
	}
	remaining := int(lim.Tokens())
	if remaining < 0 {
		remaining = 0
	}
	return true, remaining, time.Now().Add(window)
}

func (ml *MemoryLimiter) getLimiter(scope string, rpm int) *rate.Limiter {
	ml.mu.Lock()
	defer ml.mu.Unlock()

	if entry, ok := ml.entries[scope]; ok {
		entry.lastSeen = time.Now()
		return entry.limiter
	}
	// rate.Limit is events per second; rpm is per minute.
	r := rate.Limit(float64(rpm) / 60.0)
	lim := rate.NewLimiter(r, rpm)
	ml.entries[scope] = &memoryLimiterEntry{
		limiter:  lim,
		lastSeen: time.Now(),
	}
	return lim
}

func (ml *MemoryLimiter) cleanupLoop() {
	ticker := time.NewTicker(memoryLimiterCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ml.stopCh:
			return
		case <-ticker.C:
			ml.evictStale()
		}
	}
}

func (ml *MemoryLimiter) evictStale() {
	ml.mu.Lock()
	defer ml.mu.Unlock()
	now := time.Now()
	for scope, entry := range ml.entries {
		if now.Sub(entry.lastSeen) > memoryLimiterEntryTTL {
			delete(ml.entries, scope)
		}
	}
}
