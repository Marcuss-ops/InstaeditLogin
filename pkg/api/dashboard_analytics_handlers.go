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
	PeriodDays    int                   `json:"period_days"`
	Aggregates    dashboardAggregates   `json:"aggregates"`
	Channels      []dashboardChannelRow `json:"channels"`
	TopVideos     []dashboardTopVideo   `json:"top_videos"`
	GeneratedAt   time.Time             `json:"generated_at"`
	DataUpdatedAt *time.Time            `json:"data_updated_at,omitempty"`
}

type dashboardAggregates struct {
	Channels     int    `json:"channels"`
	Views        int64  `json:"views"`
	Subscribers  int64  `json:"subscribers"`
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

// dashboardAnalyticsCacheTTL bounds how long the FULL dashboard
// analytics response (aggregates + per-channel rows + top videos) is
// reused per (user, days) before the handler recomputes it. The
// dashboard renders on every app visit, so a 1-hour TTL keeps both
// the DB metric-history reads and the YouTube quota cost of the
// top-videos fan-out bounded.
const dashboardAnalyticsCacheTTL = time.Hour

// dashboardVideoLister is the narrow optional capability the
// dashboard fan-out needs to read per-video views. Defined as a local
// interface (not added to the large YouTubeOAuthService surface) so
// test fakes and unrelated routers stay untouched; a concrete
// *services.YouTubeOAuthService implements it.
type dashboardVideoLister interface {
	ListAccountContent(ctx context.Context, accessToken, platformUserID, cursor string, limit int, privacyFilter string) (*models.AccountContentPage, error)
}

// dashboardAnalyticsCacheEntry is the per-(user, days) cached full
// dashboard response.
type dashboardAnalyticsCacheEntry struct {
	resp      dashboardAnalyticsResponse
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
// It aggregates the user's YouTube metric history (views,
// subscribers, revenue) and fan-outs a real per-video ranking via
// videos.list statistics. Subscribers uses the latest known point in
// the window per channel (a snapshot sum, like views).
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

	// Full-response cache: the dashboard renders on every app visit,
	// so a fresh entry for (user, days) short-circuits before any DB
	// history read or YouTube fan-out.
	cacheKey := fmt.Sprintf("%d|%d", identity.UserID(), days)
	now := time.Now()
	forceRefresh := req.URL.Query().Get("refresh") == "1" || req.URL.Query().Get("refresh") == "true"
	r.dashboardAnalyticsCacheMu.Lock()
	if r.dashboardAnalyticsCache == nil {
		r.dashboardAnalyticsCache = make(map[string]dashboardAnalyticsCacheEntry)
	}
	if !forceRefresh {
		if cached, hit := r.dashboardAnalyticsCache[cacheKey]; hit && cached.expiresAt.After(now) {
			r.dashboardAnalyticsCacheMu.Unlock()
			writeJSON(w, http.StatusOK, cached.resp)
			return
		}
	}
	r.dashboardAnalyticsCacheMu.Unlock()

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
	resp.DataUpdatedAt = latestMetricUpdatedAt(histories)

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
			resp.Aggregates.Subscribers += latest.Subscribers
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

	// Top-videos fan-out. Failure only empties the ranking; the
	// aggregate + tables above remain fully functional.
	var fanoutDegraded bool
	resp.TopVideos, fanoutDegraded = r.dashboardTopVideosRanking(req.Context(), accounts, from, to)

	// Cache the full response for (user, days) so repeated dashboard
	// loads skip both the DB history read and the YouTube fan-out.
	// Degraded responses (at least one channel's fan-out failed) are
	// NOT cached: a transient YouTube outage must not pin an empty or
	// partial ranking for the full TTL.
	if !fanoutDegraded {
		r.dashboardAnalyticsCacheMu.Lock()
		r.dashboardAnalyticsCache[cacheKey] = dashboardAnalyticsCacheEntry{resp: resp, expiresAt: now.Add(dashboardAnalyticsCacheTTL)}
		r.dashboardAnalyticsCacheMu.Unlock()
	}

	writeJSON(w, http.StatusOK, resp)
}

// dashboardTopVideosRanking returns the cross-channel "Migliori video"
// ranking for the user, ranked by total views and capped at
// dashboardTopVideosMaxTotal, plus a degraded flag that is true when
// at least one channel's fan-out failed (the ranking may be partial
// or empty because of an upstream error, not because there are no
// videos). The caller (handleGetDashboardAnalytics) caches the FULL
// response for dashboardAnalyticsCacheTTL, so this fan-out runs at
// most once per (user, days) window — and skips the cache entirely
// when degraded.
func (r *Router) dashboardTopVideosRanking(
	ctx context.Context,
	accounts []*models.PlatformAccount,
	from, to time.Time,
) ([]dashboardTopVideo, bool) {
	lister, ok := r.youTubeSvc.(dashboardVideoLister)
	if !ok || lister == nil || r.vault == nil {
		return []dashboardTopVideo{}, false
	}

	sem := make(chan struct{}, groupYouTubeVideosFanoutConcurrency)
	type result struct {
		videos []dashboardTopVideo
		failed bool
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
				results <- result{failed: true}
				return
			}
			page, listErr := lister.ListAccountContent(accCtx, token.AccessToken, acc.PlatformUserID, "", dashboardTopVideosPerAccount, "")
			if listErr != nil {
				results <- result{failed: true}
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
	degraded := false
	for res := range results {
		all = append(all, res.videos...)
		if res.failed {
			degraded = true
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Views > all[j].Views })
	if len(all) > dashboardTopVideosMaxTotal {
		all = all[:dashboardTopVideosMaxTotal]
	}
	return all, degraded
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
