package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"

	"golang.org/x/time/rate"
)

const (
	// Rate limit for anonymous IPs (requests per minute).
	anonRPM = 100
	// Rate limit for authenticated requests (requests per minute).
	authRPM = 1000
	// cleanupInterval is how often stale entries are purged.
	cleanupInterval = 5 * time.Minute
	// entryTTL is how long an entry lives without being accessed.
	entryTTL = 10 * time.Minute
)

// rateLimiter implements a token-bucket-per-IP rate limiter with
// in-memory state. Safe for concurrent use.
//
// FASE 1.2: protects all API routes from abuse by limiting requests
// per IP. Anonymous IPs get 100 req/min; requests carrying auth
// credentials (Bearer header or session cookie) get 1000 req/min.
// The auth check is a heuristic (presence of credentials, not
// validation) so the middleware can run before the auth chain.
type rateLimiter struct {
	mu       sync.Mutex
	entries  map[string]*rateLimiterEntry
	stopCh   chan struct{}
	stopOnce sync.Once
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newRateLimiter creates a new rate limiter and starts a background
// goroutine to evict stale entries. Call Shutdown() to stop the
// background goroutine.
func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{
		entries: make(map[string]*rateLimiterEntry),
		stopCh:  make(chan struct{}),
	}
	go rl.cleanupLoop()
	return rl
}

// Shutdown stops the background cleanup goroutine.
func (rl *rateLimiter) Shutdown() {
	rl.stopOnce.Do(func() {
		close(rl.stopCh)
	})
}

// middleware returns an http.Handler that rate-limits requests by IP.
// It must be wrapped around the entire mux to protect ALL routes.
func (rl *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		limit := rl.limit(r)

		lim := rl.getLimiter(ip, limit)
		if !lim.Allow() {
			w.Header().Set("Retry-After", "60")
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}

		// Set rate limit headers for observability.
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(int(lim.Tokens())))
		// X-RateLimit-Reset: Unix timestamp when the bucket refills.
		resetAt := time.Now().Add(time.Duration(float64(time.Minute) / float64(limit)))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))

		next.ServeHTTP(w, r)
	})
}

// getLimiter returns the per-IP rate limiter, creating one if needed.
func (rl *rateLimiter) getLimiter(ip string, rpm int) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.entries[ip]
	if ok {
		entry.lastSeen = time.Now()
		return entry.limiter
	}

	// Create a new token bucket: `rpm` tokens per minute, burst of
	// `rpm` (allow the full minute worth in one burst).
	// rate.Limiter uses events per SECOND internally, so we convert.
	r := rate.Limit(float64(rpm) / 60.0)
	lim := rate.NewLimiter(r, rpm)
	rl.entries[ip] = &rateLimiterEntry{
		limiter:  lim,
		lastSeen: time.Now(),
	}
	return lim
}

// limit returns the RPM limit for this request. Authenticated
// requests (those carrying a Bearer token or session cookie) get
// the higher limit; anonymous requests get the lower limit.
//
// This is a heuristic — the credentials are NOT validated here,
// only checked for presence. The actual validation happens later
// in the auth middleware chain.
func (rl *rateLimiter) limit(r *http.Request) int {
	// Check for Authorization: Bearer header.
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return authRPM
		}
	}
	// Check for session cookie (uses the same constant as auth.Middleware).
	if _, err := r.Cookie(auth.SessionCookieName); err == nil {
		return authRPM
	}
	return anonRPM
}

// cleanupLoop periodically evicts entries that haven't been accessed
// within entryTTL. Runs in a background goroutine.
func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.evictStale()
		}
	}
}

func (rl *rateLimiter) evictStale() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, entry := range rl.entries {
		if now.Sub(entry.lastSeen) > entryTTL {
			delete(rl.entries, ip)
		}
	}
}

// extractIP returns the client IP address from the request. Checks
// X-Forwarded-For first (for reverse-proxy deployments), then
// X-Real-IP, then falls back to RemoteAddr.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the leftmost (original client) IP.
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	// Strip port from RemoteAddr (e.g. "192.168.1.1:54321" → "192.168.1.1").
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[:idx]
	}
	return addr
}
