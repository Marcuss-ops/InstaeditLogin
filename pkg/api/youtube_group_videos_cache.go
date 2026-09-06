package api

import (
	"context"
	"fmt"
	"strings"
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

// invalidateAccountCachedVideos drops the cached editable-videos entries
// for one account. Called after an out-of-band metadata change (e.g.
// PATCH group video metadata) so the next group list reflects the new
// title/description/category without waiting out the cache TTL.
func (r *Router) invalidateAccountCachedVideos(acc *models.PlatformAccount) {
	// Cache keys are "%d:%s:%d" (account id : platform user id : max).
	prefix := fmt.Sprintf("%d:%s:", acc.ID, acc.PlatformUserID)
	r.youtubeGroupVideosCacheMu.Lock()
	defer r.youtubeGroupVideosCacheMu.Unlock()
	for key := range r.youtubeGroupVideosCache {
		if strings.HasPrefix(key, prefix) {
			delete(r.youtubeGroupVideosCache, key)
		}
	}
}

// fetchCachedAccountEditableVideos renews the canonical YouTube bearer grant
// and returns the first page of private/unlisted/processed videos. Error semantics:
// (nil, err) for any failure mode (no token / channel mismatches /
// transport) — the handler skips the account and surfaces the err
// in the warnings[] / 502 envelope.
func (r *Router) fetchCachedAccountEditableVideos(ctx context.Context, acc *models.PlatformAccount, cfg YouTubeGroupVideosConfig, forceRefresh bool) ([]models.YouTubeVideoDetails, error) {
	cacheKey := fmt.Sprintf("%d:%s:%d", acc.ID, acc.PlatformUserID, cfg.MaxVideos)
	// Both the result cache and the single-flight map live on the Router:
	// two independently configured routers never share an upstream result
	// or token context, and no router-identity key is needed (the process-
	// global variant required one because its entries outlived any single
	// router; a %p pointer key there could be reused after a Router was
	// freed, silently cross-joining two routers' fetches).
	inflightKey := cacheKey
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

	r.youtubeGroupVideosInflightMu.Lock()
	if r.youtubeGroupVideosInflight == nil {
		r.youtubeGroupVideosInflight = make(map[string]*youtubeGroupVideosInflightEntry)
	}
	if pending, exists := r.youtubeGroupVideosInflight[inflightKey]; exists {
		r.youtubeGroupVideosInflightMu.Unlock()
		select {
		case <-pending.done:
			return append([]models.YouTubeVideoDetails(nil), pending.items...), pending.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	pending := &youtubeGroupVideosInflightEntry{done: make(chan struct{})}
	r.youtubeGroupVideosInflight[inflightKey] = pending
	r.youtubeGroupVideosInflightMu.Unlock()
	defer func() {
		r.youtubeGroupVideosInflightMu.Lock()
		delete(r.youtubeGroupVideosInflight, inflightKey)
		close(pending.done)
		r.youtubeGroupVideosInflightMu.Unlock()
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
