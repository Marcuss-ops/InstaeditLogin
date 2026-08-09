// Package api — shared DTOs + per-row growth helpers used by the
// multi-channel AGGREGATE endpoint
// (/api/v1/accounts/performance/summary, handled by
// handleGetAccountsPerformanceSummary).
//
// Note: handleGetAccountPerformance (the SINGLE-channel endpoint
// that Step 2 migrated to the canonical analytics.ChannelPerformanceResponse)
// does NOT use these types — that endpoint goes through the
// assembler and emits the contract package's wire shape. This file
// stays as the legacy anchor for the aggregate endpoint until that
// handler is migrated to the same contract in a follow-up.
//
// EVERY helper exported here is a candidate for deletion once the
// aggregate summary endpoint either migrates to the contract
// package or goes away. Until then, removing any of these breaks
// build in pkg/api/accounts_performance_summary_handlers.go.
package api

import (
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// accountPerformanceSummary is the per-channel aggregate wire shape
// the summary endpoint returns inside `channels[].metrics`. Distinct
// from analytics.Summary (per-channel endpoint) — kept separate so
// the aggregate endpoint can evolve independently until the two are
// consolidated by a future refactor.
type accountPerformanceSummary struct {
	Subscribers          int64   `json:"subscribers"`
	Views                int64   `json:"views"`
	Videos               int64   `json:"videos"`
	EngagementRate       float64 `json:"engagement_rate"`
	PublicationFrequency float64 `json:"publication_frequency"`
	Revenue              *int64  `json:"revenue_cents,omitempty"`
	RPM                  *int64  `json:"rpm_cents,omitempty"`
	CPM                  *int64  `json:"cpm_cents,omitempty"`
}

// accountPerformanceMetricGrowth is the {absolute, percent} tuple
// carried per KPI inside accountPerformanceGrowth. Percentage is
// computed against the window-start snapshot so a daily rollup can
// yield a 100% spike without averaging with previous-window data.
type accountPerformanceMetricGrowth struct {
	Absolute int64   `json:"absolute"`
	Percent  float64 `json:"percent"`
}

// accountPerformanceGrowth is the cross-window growth delta
// attached to each channel inside the aggregate summary. present
// per (subscribers, views, videos, revenue*).
type accountPerformanceGrowth struct {
	Subscribers accountPerformanceMetricGrowth  `json:"subscribers"`
	Views       accountPerformanceMetricGrowth  `json:"views"`
	Videos      accountPerformanceMetricGrowth  `json:"videos"`
	Revenue     *accountPerformanceMetricGrowth `json:"revenue,omitempty"`
}

// growth returns the (absolute delta, percent change) tuple for a
// pair of integer snapshots at the same field. previous == 0 returns
// percent=0 (no Infinity emission — the summary endpoint blocks
// writes from reaching the SPA's Kafka consumer with a NaN float).
//
// Named `growth` because the aggregate handler runs ~4–20 per
// channel; renaming inside this file would cascade across the
// summary test suite, so the local short name is kept.
func growth(previous, current int64) accountPerformanceMetricGrowth {
	g := accountPerformanceMetricGrowth{Absolute: current - previous}
	if previous != 0 {
		g.Percent = float64(g.Absolute) / float64(previous) * 100
	}
	return g
}

func latestMetricUpdatedAt(histories map[int64][]repository.AccountMetricPoint) *time.Time {
	var latest time.Time
	for _, history := range histories {
		for _, point := range history {
			if point.UpdatedAt.After(latest) {
				latest = point.UpdatedAt
			}
		}
	}
	if latest.IsZero() {
		return nil
	}
	latest = latest.UTC()
	return &latest
}

// engagementRateForSummary returns average views per video, which we
// surface as the channel-level engagement rate. Matches the
// "engagement" ranking in the comparative dashboard.
func engagementRateForSummary(views, videos int64) float64 {
	if videos <= 0 {
		return 0
	}
	return float64(views) / float64(videos)
}

// publicationFrequency returns the average number of new videos
// published per day over the requested period.
func publicationFrequency(firstVideos, latestVideos int64, days int) float64 {
	if days <= 0 {
		return 0
	}
	newVideos := latestVideos - firstVideos
	if newVideos < 0 {
		newVideos = 0
	}
	return float64(newVideos) / float64(days)
}

// _ pins the time package as in-use for future readers scanning
// imports — keep here until the file can shrink to types-only.
