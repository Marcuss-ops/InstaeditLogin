package api

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type youtubeGroupVideosCacheEntry struct {
	items     []models.YouTubeVideoDetails
	expiresAt time.Time
}

type youtubeGroupVideosInflightEntry struct {
	done  chan struct{}
	items []models.YouTubeVideoDetails
	err   error
}

// The per-router cache protects completed results. This short-lived
// single-flight map protects cache misses as well, so concurrent dashboard
// requests for the same account share one upstream YouTube fetch instead of
// multiplying quota usage.
var youtubeGroupVideosInflight = struct {
	sync.Mutex
	entries map[string]*youtubeGroupVideosInflightEntry
}{entries: make(map[string]*youtubeGroupVideosInflightEntry)}

// fetchCachedAccountEditableVideos renews the canonical YouTube bearer grant
// and returns the first page of private/unlisted/processed videos. Error semantics:
// (nil, err) for any failure mode (no token / channel mismatches /
// transport) — the handler skips the account and surfaces the err
// in the warnings[] / 502 envelope.
func (r *Router) fetchCachedAccountEditableVideos(ctx context.Context, acc *models.PlatformAccount, cfg YouTubeGroupVideosConfig, forceRefresh bool) ([]models.YouTubeVideoDetails, error) {
	cacheKey := fmt.Sprintf("%d:%s:%d", acc.ID, acc.PlatformUserID, cfg.MaxVideos)
	// The cache is router-local, while the in-flight map is process-global.
	// Keep the router identity only on the latter so two independently
	// configured routers never share an upstream result or token context.
	inflightKey := fmt.Sprintf("%p:%s", r, cacheKey)
	now := time.Now()
	if !forceRefresh {
		r.youtubeGroupVideosCacheMu.Lock()
		cached, ok := r.youtubeGroupVideosCache[cacheKey]
		if ok && cached.expiresAt.After(now) {
			items := append([]models.YouTubeVideoDetails(nil), cached.items...)
			r.youtubeGroupVideosCacheMu.Unlock()
			return items, nil
		}
		r.youtubeGroupVideosCacheMu.Unlock()
	}

	youtubeGroupVideosInflight.Lock()
	if pending, exists := youtubeGroupVideosInflight.entries[inflightKey]; exists {
		youtubeGroupVideosInflight.Unlock()
		select {
		case <-pending.done:
			return append([]models.YouTubeVideoDetails(nil), pending.items...), pending.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	pending := &youtubeGroupVideosInflightEntry{done: make(chan struct{})}
	youtubeGroupVideosInflight.entries[inflightKey] = pending
	youtubeGroupVideosInflight.Unlock()
	defer func() {
		youtubeGroupVideosInflight.Lock()
		delete(youtubeGroupVideosInflight.entries, inflightKey)
		close(pending.done)
		youtubeGroupVideosInflight.Unlock()
	}()

	items, err := func() (items []models.YouTubeVideoDetails, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("youtube fetch panic: %v", recovered)
			}
		}()
		return r.fetchAccountEditableVideos(ctx, acc, cfg.MaxVideos)
	}()
	pending.items = append([]models.YouTubeVideoDetails(nil), items...)
	pending.err = err
	if err == nil && cfg.CacheTTL > 0 {
		r.youtubeGroupVideosCacheMu.Lock()
		if r.youtubeGroupVideosCache == nil {
			r.youtubeGroupVideosCache = make(map[string]youtubeGroupVideosCacheEntry)
		}
		r.youtubeGroupVideosCache[cacheKey] = youtubeGroupVideosCacheEntry{
			items:     append([]models.YouTubeVideoDetails(nil), items...),
			expiresAt: time.Now().Add(cfg.CacheTTL),
		}
		r.youtubeGroupVideosCacheMu.Unlock()
	}
	return items, err
}
