package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// accountPerformanceResponse is the wire shape for
// GET /api/v1/accounts/{id}/performance.
type accountPerformanceResponse struct {
	Summary    accountPerformanceSummary       `json:"summary"`
	Growth     accountPerformanceGrowth        `json:"growth"`
	History    []repository.AccountMetricPoint `json:"history"`
	PeriodDays int                             `json:"period_days"`
}

type accountPerformanceSummary struct {
	Subscribers int64 `json:"subscribers"`
	Views       int64 `json:"views"`
	Videos      int64 `json:"videos"`
}

type accountPerformanceMetricGrowth struct {
	Absolute int64   `json:"absolute"`
	Percent  float64 `json:"percent"`
}

type accountPerformanceGrowth struct {
	Subscribers accountPerformanceMetricGrowth `json:"subscribers"`
	Views       accountPerformanceMetricGrowth `json:"views"`
	Videos      accountPerformanceMetricGrowth `json:"videos"`
}

// handleGetAccountPerformance returns a historical performance view
// for a single connected account. Supports ?days=7|30|90 (default 30).
// Returns 501 if the metric history store is not wired.
func (r *Router) handleGetAccountPerformance(w http.ResponseWriter, req *http.Request) {
	if r.metricHistoryStore == nil {
		writeError(w, http.StatusNotImplemented, "metric history store not configured")
		return
	}

	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	days := 30
	if d := req.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && (parsed == 7 || parsed == 30 || parsed == 90) {
			days = parsed
		}
	}

	to := time.Now().UTC()
	from := to.AddDate(0, 0, -days+1)

	history, err := r.metricHistoryStore.GetHistory(account.ID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load performance history: "+err.Error())
		return
	}

	resp := accountPerformanceResponse{
		PeriodDays: days,
		History:    history,
		Summary:    accountPerformanceSummary{},
		Growth:     accountPerformanceGrowth{},
	}

	if len(history) > 0 {
		latest := history[len(history)-1]
		resp.Summary = accountPerformanceSummary{
			Subscribers: latest.Subscribers,
			Views:       latest.Views,
			Videos:      latest.Videos,
		}
	}

	if len(history) >= 2 {
		first := history[0]
		latest := history[len(history)-1]
		resp.Growth.Subscribers = growth(first.Subscribers, latest.Subscribers)
		resp.Growth.Views = growth(first.Views, latest.Views)
		resp.Growth.Videos = growth(first.Videos, latest.Videos)
	}

	writeJSON(w, http.StatusOK, resp)
}

func growth(previous, current int64) accountPerformanceMetricGrowth {
	g := accountPerformanceMetricGrowth{Absolute: current - previous}
	if previous != 0 {
		g.Percent = float64(g.Absolute) / float64(previous) * 100
	}
	return g
}
