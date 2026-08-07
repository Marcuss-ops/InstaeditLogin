package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// dashboardAnalyticsResponse is the wire shape for
// GET /api/v1/dashboard/analytics?days=1|7|14|28|90. It is the
// analytics-only view the SPA dashboard renders: aggregate KPIs,
// per-channel Views/Revenue rows, and a cross-channel "Migliori video"
// ranking. Group memberships deliberately live in /api/v1/groups/*
// (the Groups page), so this endpoint never exposes group data.
type dashboardAnalyticsResponse struct {
	PeriodDays  int                   `json:"period_days"`
	Aggregates  dashboardAggregates   `json:"aggregates"`
	Channels    []dashboardChannelRow `json:"channels"`
	TopVideos   []dashboardTopVideo   `json:"top_videos"`
	GeneratedAt time.Time             `json:"generated_at"`
}

type dashboardAggregates struct {
	Channels     int    `json:"channels"`
	Views        int64  `json:"views"`
	Videos       int64  `json:"videos"`
	RevenueCents *int64 `json:"revenue_cents,omitempty"`
}

// dashboardChannelRow is one row of the Views / Revenue tables.
// Revenue and its growth are pointers because a channel without
// monetization data renders as "—" rather than a deceptive 0.
type dashboardChannelRow struct {
	ID            int64                           `json:"id"`
	Username      string                          `json:"username"`
	Views         int64                           `json:"views"`
	ViewsGrowth   accountPerformanceMetricGrowth  `json:"views_growth"`
	RevenueCents  *int64                          `json:"revenue_cents,omitempty"`
	RevenueGrowth *accountPerformanceMetricGrowth `json:"revenue_growth,omitempty"`
}

// dashboardTopVideo is one row of the "Migliori video" table: a video
// published within the requested window, ranked by total views (the
// real per-video statistic from videos.list statistics).
type dashboardTopVideo struct {
	VideoID      string     `json:"video_id"`
	Title        string     `json:"title"`
	ThumbnailURL string     `json:"thumbnail_url,omitempty"`
	Views        int64      `json:"views"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
	ChannelName  string     `json:"channel_name"`
	YouTubeURL   string     `json:"youtube_url"`
}

// dashboardTopVideosPerAccount caps the videos.list rows fetched per
// channel during the fan-out. Videos are returned newest-first by the
// uploads playlist; 20 is enough to rank the best of any channel.
const dashboardTopVideosPerAccount = 20

// dashboardTopVideosMaxTotal caps the aggregated ranking across all
// channels so a large fleet cannot saturate the dashboard response.
const dashboardTopVideosMaxTotal = 20

// dashboardTopVideosCacheTTL bounds how long a dashboard top-videos
// ranking is reused before the fan-out refetches from YouTube. The
// dashboard renders on every app visit, so a short TTL keeps the
// YouTube quota cost of 62-channel fan-outs bounded.
const dashboardTopVideosCacheTTL = 5 * time.Minute

// dashboardVideoLister is the narrow optional capability the
// dashboard fan-out needs to read per-video views. Defined as a local
// interface (not added to the large YouTubeOAuthService surface) so
// test fakes and unrelated routers stay untouched; a concrete
// *services.YouTubeOAuthService implements it.
type dashboardVideoLister interface {
	ListAccountContent(ctx context.Context, accessToken, platformUserID, cursor string, limit int, privacyFilter string) (*models.AccountContentPage, error)
}

// dashboardTopVideosCacheEntry is the per-(user, days) cached ranking.
type dashboardTopVideosCacheEntry struct {
	videos    []dashboardTopVideo
	expiresAt time.Time
}

// isAllowedDashboardDay reports whether days is one of the canonical
// dashboard periods (1, 7, 14, 28, 90).
func isAllowedDashboardDay(days int) bool {
	return days == 1 || days == 7 || days == 14 || days == 28 || days == 90
}

// handleGetDashboardAnalytics is the HTTP boundary for
// GET /api/v1/dashboard/analytics?days=1|7|14|28|90 (default 28).
//
// It aggregates the user's YouTube metric history (views, revenue)
// and fan-outs a real per-video ranking via videos.list statistics.
// Degradation policy: the aggregate + per-channel sections always
// render from the metric history store; a YouTube fan-out failure
// only empties the top_videos array (the dashboard keeps working).
func (r *Router) handleGetDashboardAnalytics(w http.ResponseWriter, req *http.Request) {
	if r.metricHistoryStore == nil {
		writeError(w, http.StatusNotImplemented, "metric history store not configured")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	days := 28
	if d := req.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && isAllowedDashboardDay(parsed) {
			days = parsed
		}
	}

	accounts, err := r.userRepo.ListFilteredYouTubeAccounts(identity.UserID(), nil, "", "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}

	clock := r.analyticsClock
	if isNilAnalyticsClock(clock) {
		clock = analytics.RealClock{}
	}
	to := clock.Now().UTC()
	from := to.AddDate(0, 0, -days+1)

	// Load the same batch history the summary endpoint uses so the
	// aggregate + per-channel sections share one code path.
	histories := make(map[int64][]repository.AccountMetricPoint, len(accounts))
	if batcher, ok := r.metricHistoryStore.(BatchMetricHistoryStore); ok && len(accounts) > 0 {
		accountIDs := make([]int64, 0, len(accounts))
		for _, a := range accounts {
			accountIDs = append(accountIDs, a.ID)
		}
		histories, err = batcher.GetHistoryBatch(accountIDs, from, to)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load performance history: "+err.Error())
			return
		}
	} else {
		for _, a := range accounts {
			history, historyErr := r.metricHistoryStore.GetHistory(a.ID, from, to)
			if historyErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to load performance history: "+historyErr.Error())
				return
			}
			histories[a.ID] = history
		}
	}

	resp := dashboardAnalyticsResponse{
		PeriodDays:  days,
		Channels:    make([]dashboardChannelRow, 0, len(accounts)),
		TopVideos:   []dashboardTopVideo{},
		GeneratedAt: to,
	}

	var totalRevenue int64
	hasRevenue := false
	for _, a := range accounts {
		history := histories[a.ID]
		row := dashboardChannelRow{ID: a.ID, Username: a.Username}
		if len(history) > 0 {
			latest := history[len(history)-1]
			row.Views = latest.Views
			row.RevenueCents = latest.RevenueCents
			resp.Aggregates.Views += latest.Views
			resp.Aggregates.Videos += latest.Videos
			if latest.RevenueCents != nil {
				totalRevenue += *latest.RevenueCents
				hasRevenue = true
			}
			if len(history) >= 2 {
				first := history[0]
				row.ViewsGrowth = growth(first.Views, latest.Views)
				if first.RevenueCents != nil && latest.RevenueCents != nil {
					g := growth(*first.RevenueCents, *latest.RevenueCents)
					row.RevenueGrowth = &g
				}
			}
		}
		resp.Aggregates.Channels++
		resp.Channels = append(resp.Channels, row)
	}
	if hasRevenue {
		resp.Aggregates.RevenueCents = &totalRevenue
	}

	// Top-videos fan-out (cached). Failure only empties the ranking;
	// the aggregate + tables above remain fully functional.
	resp.TopVideos = r.dashboardTopVideosRanking(req.Context(), identity.UserID(), accounts, from, to, days)

	writeJSON(w, http.StatusOK, resp)
}

// dashboardTopVideosRanking returns the cross-channel "Migliori video"
// ranking for the user, ranked by total views and capped at
// dashboardTopVideosMaxTotal. Results are cached per (user, days) for
// dashboardTopVideosCacheTTL so repeated dashboard loads do not burn
// YouTube quota. A per-account failure skips that channel; the whole
// ranking degrades to empty only when every channel fails or the
// video lister capability is absent.
func (r *Router) dashboardTopVideosRanking(
	ctx context.Context,
	userID int64,
	accounts []*models.PlatformAccount,
	from, to time.Time,
	days int,
) []dashboardTopVideo {
	lister, ok := r.youTubeSvc.(dashboardVideoLister)
	if !ok || lister == nil || r.vault == nil {
		return []dashboardTopVideo{}
	}

	cacheKey := fmt.Sprintf("%d|%d", userID, days)
	now := time.Now()
	r.dashboardTopVideosCacheMu.Lock()
	if r.dashboardTopVideosCache == nil {
		r.dashboardTopVideosCache = make(map[string]dashboardTopVideosCacheEntry)
	}
	if cached, hit := r.dashboardTopVideosCache[cacheKey]; hit && cached.expiresAt.After(now) {
		out := append([]dashboardTopVideo(nil), cached.videos...)
		r.dashboardTopVideosCacheMu.Unlock()
		return out
	}
	r.dashboardTopVideosCacheMu.Unlock()

	sem := make(chan struct{}, groupYouTubeVideosFanoutConcurrency)
	type result struct {
		videos []dashboardTopVideo
	}
	results := make(chan result, len(accounts))
	var wg sync.WaitGroup
	for _, acc := range accounts {
		acc := acc
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			accCtx, cancel := context.WithTimeout(ctx, groupYouTubeVideosPerAccountTimeout)
			defer cancel()
			token, tokenErr := r.vault.Renew(accCtx, acc.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
			if tokenErr != nil {
				results <- result{}
				return
			}
			page, listErr := lister.ListAccountContent(accCtx, token.AccessToken, acc.PlatformUserID, "", dashboardTopVideosPerAccount, "")
			if listErr != nil {
				results <- result{}
				return
			}
			videos := make([]dashboardTopVideo, 0, len(page.Items))
			for _, item := range page.Items {
				if item.PublishedAt == nil || item.PublishedAt.Before(from) || item.PublishedAt.After(to) {
					continue
				}
				videos = append(videos, dashboardTopVideo{
					VideoID:      item.ExternalID,
					Title:        item.Title,
					ThumbnailURL: item.ThumbnailURL,
					Views:        metricValueByKey(item.Metrics, "views"),
					PublishedAt:  item.PublishedAt,
					ChannelName:  displayChannelName(acc),
					YouTubeURL:   item.PublicURL,
				})
			}
			results <- result{videos: videos}
		}()
	}
	wg.Wait()
	close(results)

	all := make([]dashboardTopVideo, 0, dashboardTopVideosMaxTotal*2)
	for res := range results {
		all = append(all, res.videos...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Views > all[j].Views })
	if len(all) > dashboardTopVideosMaxTotal {
		all = all[:dashboardTopVideosMaxTotal]
	}

	r.dashboardTopVideosCacheMu.Lock()
	r.dashboardTopVideosCache[cacheKey] = dashboardTopVideosCacheEntry{
		videos:    append([]dashboardTopVideo(nil), all...),
		expiresAt: now.Add(dashboardTopVideosCacheTTL),
	}
	r.dashboardTopVideosCacheMu.Unlock()
	return all
}

// metricValueByKey extracts a metric value by key (e.g. "views") from
// an AccountContentItem's Metrics slice; returns 0 when absent.
func metricValueByKey(metrics []models.AccountMetric, key string) int64 {
	for _, m := range metrics {
		if m.Key == key {
			return m.Value
		}
	}
	return 0
}

// displayChannelName returns the username, falling back to the
// platform user id for channels with no display name.
func displayChannelName(acc *models.PlatformAccount) string {
	if acc == nil {
		return ""
	}
	if name := strings.TrimSpace(acc.Username); name != "" {
		return name
	}
	return acc.PlatformUserID
}
