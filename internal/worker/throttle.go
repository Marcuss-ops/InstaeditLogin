package worker

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// Per-platform rate limits (requests per second). These are
// conservative defaults tuned to stay well under the platform's
// documented limits. Operators can override via environment
// variables if needed (future enhancement).
var defaultPlatformLimits = map[string]rate.Limit{
	// Meta family (Instagram, Facebook, Threads): Graph API allows
	// ~200 calls/user/hour ≈ 1 call every 18s. We set a generous
	// 2 req/s which is well under the limit for 50 concurrent
	// users' worth of queued publishes.
	"instagram": rate.Limit(2),
	"facebook":  rate.Limit(2),
	"threads":   rate.Limit(2),

	// TikTok: video.publish endpoint has a documented limit of
	// ~1 req every 2 seconds per app. Conservative.
	"tiktok": rate.Limit(0.5), // 1 req every 2s

	// YouTube: Data API v3 has a quota system (10,000 units/day).
	// Uploads cost ~1600 units each. 0.33 req/s ≈ ~3 uploads every
	// 10s, which stays under the quota for typical usage.
	"youtube": rate.Limit(0.33), // ~1 req every 3s

	// Twitter/X: v2 API allows ~300 tweets/3h per user. 1 req/s
	// is well within the per-app limit.
	"twitter": rate.Limit(1),

	// LinkedIn: Posts API is ~100 calls/day per app. 0.5 req/s
	// leaves headroom.
	"linkedin": rate.Limit(0.5),
}

// defaultBurst is the maximum number of tokens that can be consumed
// in a single burst. Set to 1 so each publish must wait its turn
// — no burst, pure spacing.
const defaultBurst = 1

// PlatformThrottle is a per-platform rate limiter that spaces out
// API calls to avoid triggering third-party rate limit bans.
//
// FASE 1.3: the PublishWorker calls Wait() before each publishTarget
// call. If the platform's bucket is empty, Wait() blocks until a
// token is available. This guarantees that even if 100 posts are all
// scheduled for the same second (e.g., 09:00), they'll be spaced out
// at the platform's permissible rate instead of blasting all 100 at
// once and getting the app banned.
//
// The throttle is PER WORKER PROCESS (not shared across replicas).
// If N replicas are running, each replica spaces out its own stream
// independently. The total rate across N replicas could exceed the
// platform's limit, but in practice the platform limits are per-app
// and the app ID is shared — so this is a best-effort local throttle,
// not a distributed rate limiter. A future enhancement (FASE 1.3+)
// could use a shared Redis counter for cross-replica coordination.
//
// Safe for concurrent use.
type PlatformThrottle struct {
	mu      sync.Mutex
	entries map[string]*rate.Limiter
}

// NewPlatformThrottle creates a throttle with the default per-platform
// limits. Limits not in the map default to 1 req/s.
func NewPlatformThrottle() *PlatformThrottle {
	return &PlatformThrottle{
		entries: make(map[string]*rate.Limiter),
	}
}

// Wait blocks until a token is available for the given platform, or
// ctx is cancelled. Returns nil if a token was acquired; returns
// ctx.Err() if the context was cancelled while waiting.
//
// The caller should always check the error — if the context was
// cancelled (e.g., graceful shutdown), the publish should be
// abandoned without calling the platform API.
func (pt *PlatformThrottle) Wait(ctx context.Context, platform string) error {
	lim := pt.getLimiter(platform)
	return lim.Wait(ctx)
}

// getLimiter returns the per-platform limiter, creating one with
// the default rate if not already cached.
func (pt *PlatformThrottle) getLimiter(platform string) *rate.Limiter {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if lim, ok := pt.entries[platform]; ok {
		return lim
	}

	r, ok := defaultPlatformLimits[platform]
	if !ok {
		r = rate.Limit(1) // unknown platform: 1 req/s
	}
	lim := rate.NewLimiter(r, defaultBurst)
	pt.entries[platform] = lim
	return lim
}
