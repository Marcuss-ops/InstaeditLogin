package api

import (
	"strings"
	"sync"
	"time"
)

// ttlCache is the single generic TTL-cache authority for the Router.
//
// Before this type existed, every Router-level cache hand-rolled the same
// mutex + map + lazy-expiry-sweep + bounded-evict + insert sequence
// (media preview URLs, media resolve URLs, dashboard analytics responses,
// YouTube group videos). Five copies of one mechanism meant five places for
// semantic drift; this type is the one implementation every Router cache
// goes through.
//
// The zero value is ready to use (mutex usable, map lazily allocated,
// defaultTTLCacheMax bound): Router instances built by struct literals
// (tests, partial wiring) get a working cache without constructor wiring.
//
// Semantics (the contract every former clone implemented):
//   - get: hit only while now < expiresAt; expired entries are dropped
//     lazily on access.
//   - store: first sweeps expired entries, then — when still full — evicts
//     one arbitrary live entry. The caches are optimizations, not
//     correctness stores; bounded memory beats perfect LRU bookkeeping.
//     A non-positive TTL stores nothing (callers use it to disable caching).
//   - deletePrefix: drops every key with the given prefix (account-scoped
//     invalidation); returns the number of removed entries.
//
// Value contract: values are returned as-is (no defensive copy) and MUST be
// treated as read-only by callers. This removes the per-hit/per-store slice
// copies the former YouTube cache performed; every current consumer only
// serializes or reads the cached value.
type ttlCache[T any] struct {
	mu      sync.Mutex
	entries map[string]ttlCacheEntry[T]
	max     int
}

// defaultTTLCacheMax is the bound for zero-value caches (no explicit max).
const defaultTTLCacheMax = 512

type ttlCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

// makeTTLCache constructs an explicit-bound cache value for NewRouter.
func makeTTLCache[T any](maxEntries int) ttlCache[T] {
	return ttlCache[T]{entries: make(map[string]ttlCacheEntry[T]), max: maxEntries}
}

// get returns the live cached value for key (zero T + false on miss/expiry).
func (c *ttlCache[T]) get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		var zero T
		return zero, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		var zero T
		return zero, false
	}
	return entry.value, true
}

// store inserts value under key with the given TTL, sweeping expired
// entries and evicting one arbitrary live entry when the cache is full.
// A non-positive TTL stores nothing (callers use it to disable caching).
func (c *ttlCache[T]) store(key string, value T, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]ttlCacheEntry[T])
	}
	if c.max <= 0 {
		c.max = defaultTTLCacheMax
	}
	now := time.Now()
	for k, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, k)
		}
	}
	for len(c.entries) >= c.max {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = ttlCacheEntry[T]{value: value, expiresAt: now.Add(ttl)}
}

// deletePrefix removes every entry whose key starts with prefix.
func (c *ttlCache[T]) deletePrefix(prefix string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for key := range c.entries {
		if strings.HasPrefix(key, prefix) {
			delete(c.entries, key)
			removed++
		}
	}
	return removed
}

// len reports the number of entries currently held (including expired ones
// not yet swept — len is a diagnostics helper, not a liveness guarantee).
func (c *ttlCache[T]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
