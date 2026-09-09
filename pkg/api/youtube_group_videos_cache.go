package api

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type youtubeGroupVideosInflightEntry struct {
	done  chan struct{}
	items []models.YouTubeVideoDetails
	err   error
}

// youtubeGroupVideosCacheMax bounds the cached account-video pages. Each
// entry holds up to MaxVideos details (title/description/thumbnail refs);
// without a bound the cache grew with the account count for the cache
// lifetime. 256 accounts' first pages is ample for one Router's working set.
const youtubeGroupVideosCacheMax = 256

// invalidateAccountCachedVideos drops the cached editable-videos entries
// for one account. Called after an out-of-band metadata change (e.g.
// PATCH group video metadata) so the next group list reflects the new
// title/description/category without waiting out the cache TTL.
func (r *Router) invalidateAccountCachedVideos(acc *models.PlatformAccount) {
	// Cache keys are "%d:%s:%d" (account id : platform user id : max).
	prefix := fmt.Sprintf("%d:%s:", acc.ID, acc.PlatformUserID)
	r.youtubeGroupVideosCache.deletePrefix(prefix)
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
	if !forceRefresh {
		if cached, ok := r.youtubeGroupVideosCache.get(cacheKey); ok {
			return cached, nil
		}
	}

	r.youtubeGroupVideosInflightMu.Lock()
	if r.youtubeGroupVideosInflight == nil {
		r.youtubeGroupVideosInflight = make(map[string]*youtubeGroupVideosInflightEntry)
	}
	if pending, exists := r.youtubeGroupVideosInflight[inflightKey]; exists {
		r.youtubeGroupVideosInflightMu.Unlock()
		select {
		case <-pending.done:
			return pending.items, pending.err
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
	pending.items = items
	pending.err = err
	// Cache the fetched page through the single ttlCache authority.
	// Unlike the pre-refactor map-based cache this one is size-bounded
	// (youtubeGroupVideosCacheMax entries): the value is shared as-is and
	// treated as read-only, so the three defensive slice copies per fetch
	// (plus one per cache hit) are gone.
	if err == nil {
		r.youtubeGroupVideosCache.store(cacheKey, items, cfg.CacheTTL)
	}
	return items, err
}
