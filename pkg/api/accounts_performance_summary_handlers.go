package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// channelPerformanceSummary is the per-account wire shape returned by
// GET /api/v1/accounts/performance/summary.
type channelPerformanceSummary struct {
	ID       int64                       `json:"id"`
	Platform string                      `json:"platform"`
	Username string                     `json:"username"`
	Metrics  accountPerformanceSummary   `json:"metrics"`
	Growth   accountPerformanceGrowth    `json:"growth"`
}

// rankingItem is a single leaderboard entry (channel + metric value).
type rankingItem struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Value    int64  `json:"value"`
}

// rankings aggregates several leaderboards derived from the latest
// metric history for the user's connected YouTube channels.
type rankings struct {
	BySubscribers        []rankingItem `json:"by_subscribers"`
	ByViews              []rankingItem `json:"by_views"`
	ByVideos             []rankingItem `json:"by_videos"`
	FastestGrowingSubs   []rankingItem `json:"fastest_growing_subscribers"`
	FastestGrowingViews  []rankingItem `json:"fastest_growing_views"`
}

// enrichedChannel holds the intermediate metrics + growth for one
// account while building the summary response.
type enrichedChannel struct {
	account  *models.PlatformAccount
	metrics  accountPerformanceSummary
	growth   accountPerformanceGrowth
	pctSubs  float64
	pctViews float64
}

// accountsPerformanceSummaryResponse is the wire shape for
// GET /api/v1/accounts/performance/summary.
type accountsPerformanceSummaryResponse struct {
	PeriodDays int `json:"period_days"`
	Aggregates struct {
		Channels    int   `json:"channels"`
		Subscribers int64 `json:"subscribers"`
		Views       int64 `json:"views"`
		Videos      int64 `json:"videos"`
	} `json:"aggregates"`
	Channels []channelPerformanceSummary `json:"channels"`
	Rankings rankings                    `json:"rankings"`
}

// handleGetAccountsPerformanceSummary returns aggregated KPIs and
// rankings across all connected YouTube channels for the authenticated
// user. Supports ?days=7|30|90 (default 30). Returns 501 if the metric
// history store is not wired.
func (r *Router) handleGetAccountsPerformanceSummary(w http.ResponseWriter, req *http.Request) {
	if r.metricHistoryStore == nil {
		writeError(w, http.StatusNotImplemented, "metric history store not configured")
		return
	}

	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	days := 30
	if d := req.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && (parsed == 7 || parsed == 30 || parsed == 90) {
			days = parsed
		}
	}

	accounts, err := r.userRepo.ListPlatformAccountsByUser(identity.UserID(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}

	// Scope to YouTube only. Future iterations can accept a
	// ?platform=... filter; today the dashboard is YouTube-specific.
	youtubeAccounts := make([]*models.PlatformAccount, 0, len(accounts))
	for _, a := range accounts {
		if a.Platform == "youtube" {
			youtubeAccounts = append(youtubeAccounts, a)
		}
	}

	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days+1)

	enrichedList := make([]enrichedChannel, 0, len(youtubeAccounts))

	for _, a := range youtubeAccounts {
		history, err := r.metricHistoryStore.GetHistory(a.ID, from, to)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load performance history: "+err.Error())
			return
		}

		item := enrichedChannel{account: a}
		if len(history) > 0 {
			latest := history[len(history)-1]
			item.metrics = accountPerformanceSummary{
				Subscribers: latest.Subscribers,
				Views:       latest.Views,
				Videos:      latest.Videos,
			}
			if len(history) >= 2 {
				first := history[0]
				item.growth.Subscribers = growth(first.Subscribers, latest.Subscribers)
				item.growth.Views = growth(first.Views, latest.Views)
				item.growth.Videos = growth(first.Videos, latest.Videos)
				item.pctSubs = item.growth.Subscribers.Percent
				item.pctViews = item.growth.Views.Percent
			}
		}
		enrichedList = append(enrichedList, item)
	}

	resp := accountsPerformanceSummaryResponse{PeriodDays: days}
	resp.Channels = make([]channelPerformanceSummary, 0, len(enrichedList))

	for _, e := range enrichedList {
		resp.Channels = append(resp.Channels, channelPerformanceSummary{
			ID:       e.account.ID,
			Platform: e.account.Platform,
			Username: e.account.Username,
			Metrics:  e.metrics,
			Growth:   e.growth,
		})
		resp.Aggregates.Channels++
		resp.Aggregates.Subscribers += e.metrics.Subscribers
		resp.Aggregates.Views += e.metrics.Views
		resp.Aggregates.Videos += e.metrics.Videos
	}

	resp.Rankings = buildRankings(enrichedList)

	writeJSON(w, http.StatusOK, resp)
}

func buildRankings(items []enrichedChannel) rankings {
	r := rankings{}

	// Latest metric leaderboards.
	r.BySubscribers = sortedRanking(items, func(e enrichedChannel) int64 { return e.metrics.Subscribers })
	r.ByViews = sortedRanking(items, func(e enrichedChannel) int64 { return e.metrics.Views })
	r.ByVideos = sortedRanking(items, func(e enrichedChannel) int64 { return e.metrics.Videos })

	// Fastest growing by percent change over the period.
	r.FastestGrowingSubs = sortedRanking(items, func(e enrichedChannel) int64 {
		// Guard against precision loss: percent is already float,
		// but ranking stores int64. Convert 1-decimal percent * 10
		// to keep ordering stable without floating point in JSON.
		return int64(e.pctSubs * 10)
	})
	r.FastestGrowingViews = sortedRanking(items, func(e enrichedChannel) int64 {
		return int64(e.pctViews * 10)
	})

	return r
}

func sortedRanking(items []enrichedChannel, valueFn func(enrichedChannel) int64) []rankingItem {
	type pair struct {
		item  enrichedChannel
		value int64
	}
	pairs := make([]pair, 0, len(items))
	for _, it := range items {
		pairs = append(pairs, pair{item: it, value: valueFn(it)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].value == pairs[j].value {
			return pairs[i].item.account.Username < pairs[j].item.account.Username
		}
		return pairs[i].value > pairs[j].value
	})

	out := make([]rankingItem, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, rankingItem{
			ID:       p.item.account.ID,
			Username: p.item.account.Username,
			Value:    p.value,
		})
	}
	return out
}
