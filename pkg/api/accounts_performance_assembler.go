package api

import (
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// cacheFreshnessTTL is the period-aware cache window the data_freshness
// section stamps. The user's plan calls for 5-15 min on 7-day windows
// and 15-30 min on 14/28-day windows; the constants below pin the
// upper end so a deterministic test can flip is_stale=true by clock
// advancement rather than guessing the lower bound.
//
// IMPORTANT: any change here is a CONTRACT change for the SPA's
// "data refreshed at" badge — bump the version in api/openapi.yaml
// if you change these. Step 4's ChannelAnalyticsService is expected
// to read this same constant so the badge stays consistent between
// the dashboard's manual refresh button and the per-channel view.
var cacheFreshnessTTL = map[int]time.Duration{
	7:  10 * time.Minute,
	14: 20 * time.Minute,
	28: 30 * time.Minute,
}

// dataFreshnessTTL returns the cache validity window for the given
// period. Returns 0 when the period is unknown so is_stale can never
// report "fresh" on an unrecognised window — the safe default.
func dataFreshnessTTL(days int) time.Duration {
	if ttl, ok := cacheFreshnessTTL[days]; ok {
		return ttl
	}
	return 0
}

// assembleChannelPerformance turns an owned PlatformAccount + a
// repository.GetHistory window covering BOTH [previous_start, prev_end]
// AND [current_start, current_end] into the canonical
// analytics.ChannelPerformanceResponse the per-channel endpoint emits.
//
// The function is intentionally pure: no I/O, no clock reads, no
// assumptions about ordering other than what the repository contract
// guarantees (rows sorted by metric_date ASC). The caller passes
// generatedAt explicitly so unit tests can pin it.
//
// youtubeChannelID MUST be the YouTube channel's stable UC identifier
// from the OAuth channel-binding; the handler is responsible for
// resolving it (account.Metadata["channel_id"] today; the snapshot
// store's external_id tomorrow). The contract is explicit that an
// empty YouTubeChannelID MUST be rejected upstream — we surface it
// from the public assembler API because every legitimate test path
// supplies a non-empty id and a "missing id" emitter test is simpler
// at the handler layer.
//
// Gap-filling rule for daily_series (locked by contract_test.go): the
// slice MUST have exactly period.Days elements; missing days MUST be
// zero so the chart is gap-free. Subscribers-Net is a delta (not an
// absolute), so zero-fill is honest: no subscriber movement on a given
// day is reported as zero, NOT carried-forward from yesterday. Carry
// would overstate growth on quiet days and silently inflate summary
// totals.
//
// Comparison rule (locked by contract_test.go): percentage_change is
// OMITTED (not zero, not null) when previous == 0 to avoid encoding an
// Infinity float that encoding/json accepts as `null`. Negative
// deltas with previous != 0 MUST round-trip as a present-and-negative
// value; flattening to nil would be a regression.
//
// top_videos is emitted as empty arrays (NOT nil) so the SPA can
// iterate unconditionally without null-checks. Step 5's
// TrendingVideoScorer is the future owner of these arrays; today
// the per-video revenue source does not exist.
func assembleChannelPerformance(
	account *models.PlatformAccount,
	youtubeChannelID string,
	history []repository.AccountMetricPoint,
	period analytics.Period,
	generatedAt time.Time,
	topVideos analytics.TopVideosRanking,
) analytics.ChannelPerformanceResponse {
	current, previous := splitHistoryByPeriod(history, period)
	currentSum := sumWindow(current)
	previousSum := sumWindow(previous)

	summary := buildSummary(currentSum)
	comparison := buildComparison(currentSum, previousSum)
	dailySeries := buildDailySeries(history, period)
	freshness := buildDataFreshness(currentSum, period, generatedAt)

	return analytics.ChannelPerformanceResponse{
		Channel: analytics.ChannelInfo{
			PlatformAccountID: account.ID,
			YouTubeChannelID:  youtubeChannelID,
			ChannelName:       account.Username,
			// Inline avatar_url lookup: the snapshot store will
			// override this when available (always-fresh), but the
			// shared Metadata blob is the lightweight fallback the
			// endpoint uses today. Inlined (vs. a helper) because the
			// shape is one map type assertion and a reader can
			// verify it on sight without loading auxiliary functions.
			AvatarURL: avatarURLFromMetadata(account),
			Status:    account.Status,
		},
		Period:        period,
		Summary:       summary,
		Comparison:    comparison,
		DailySeries:   dailySeries,
		TopVideos:     topVideos,
		GeneratedAt:   generatedAt,
		DataFreshness: freshness,
	}
}

// windowRowStats carries the per-window aggregates the assembler
// derives from raw AccountMetricPoint rows. Pointer fields stay
// nil when no row surface them, so the JSON contract's optional
// fields never surface a deceptive zero.
type windowRowStats struct {
	viewsSum                int64
	watchTimeMinutesSum     int64
	subscribersEnd          int64
	subscribersStart        int64
	videosPublished         int64
	estimatedRevenueCents   *int64
	impressions             *int64
	ctr                     *float64
	lastDate                time.Time
	hasData                 bool
}

// splitHistoryByPeriod slices a single GetHistory result into the
// (current, previous) window slices the assembler needs. The two
// windows are non-overlapping AND abutted (previous_end + 1 day ==
// current_start), as guaranteed by analytics.Period.Resolve. Both
// returned slices are filtered, NOT re-fetched, so a malformed
// history row (e.g. nil WatchTimeMins) is silently skipped instead
// of crashing the request.
func splitHistoryByPeriod(
	history []repository.AccountMetricPoint,
	period analytics.Period,
) (current, previous []repository.AccountMetricPoint) {
	for _, row := range history {
		date := row.Date.UTC()
		switch {
		case !date.Before(period.PreviousStartDate) && !date.After(period.PreviousEndDate):
			previous = append(previous, row)
		case !date.Before(period.StartDate) && !date.After(period.EndDate):
			current = append(current, row)
		}
	}
	return current, previous
}

// sumWindow aggregates the per-window metrics. The subscribers_end
// field captures the LAST row's absolute subscriber count (the
// handler-side "gives us where the channel is NOW" signal) while
// subscribers_start captures the FIRST row's absolute count ("where
// it was at the start of the window"). Both endpoints feed into the
// subscribers-gained / subscribers-lost best-effort in buildSummary.
func sumWindow(rows []repository.AccountMetricPoint) windowRowStats {
	if len(rows) == 0 {
		return windowRowStats{}
	}
	stats := windowRowStats{
		subscribersStart: rows[0].Subscribers,
		videosPublished:  rows[len(rows)-1].Videos - rows[0].Videos,
		lastDate:         rows[len(rows)-1].Date,
		hasData:          true,
	}
	if stats.videosPublished < 0 {
		// Defensive: account_metric_history VIDEOS column should be
		// monotonically non-decreasing, but a back-fill or partial
		// outage can produce a sad path. Clamp at zero so the SPA
		// renders "no new videos" rather than a negative count.
		stats.videosPublished = 0
	}
	for i, row := range rows {
		if i == len(rows)-1 {
			stats.subscribersEnd = row.Subscribers
		}
		stats.viewsSum += row.Views
		if row.WatchTimeMins != nil {
			stats.watchTimeMinutesSum += *row.WatchTimeMins
		}
		// EstimatedRevenue + Impressions + CTR are accumulator-style
		// metrics surfaced only on rows where the upstream hook
		// (yt-analytics-monetary.readonly / yt-analytics.readonly)
		// succeeded. Take the LAST ROW's value (the most recent
		// reconciled value); summing would over-count duplicate
		// snapshots on adjacent days.
		if row.RevenueCents != nil {
			stats.estimatedRevenueCents = row.RevenueCents
		}
		if row.Impressions != nil {
			stats.impressions = row.Impressions
		}
		if row.CTR != nil {
			stats.ctr = row.CTR
		}
	}
	return stats
}

// buildSummary computes the headline KPIs using the current window
// stats. subscribers_gained / subscribers_lost are a best-effort
// signed-delta approximation: today the account_metric_history
// columns are absolute (NOT gained/lost split), so this conflates
// gross churn into a net delta. Step 4's ChannelAnalyticsService
// will replace this with proper daily-direction counters when the
// add-gained/lost columns land.
//
// average_views_per_video is denormalised as views_sum /
// videos_published. Zero-guard returns 0 (the contract explicitly
// forbids a NaN that would crash JSON marshaling).
func buildSummary(s windowRowStats) analytics.Summary {
	delta := s.subscribersEnd - s.subscribersStart
	gained := int64(0)
	lost := int64(0)
	if delta > 0 {
		gained = delta
	} else if delta < 0 {
		lost = -delta
	}
	avgPerVideo := 0.0
	if s.videosPublished > 0 {
		avgPerVideo = float64(s.viewsSum) / float64(s.videosPublished)
	}
	return analytics.Summary{
		Views:                 s.viewsSum,
		WatchTimeMinutes:      s.watchTimeMinutesSum,
		SubscribersGained:     gained,
		SubscribersLost:       lost,
		SubscribersNet:        delta,
		EstimatedRevenueCents: s.estimatedRevenueCents,
		VideosPublished:       s.videosPublished,
		AverageViewsPerVideo:  avgPerVideo,
		Impressions:           s.impressions,
		CTR:                   s.ctr,
	}
}

// metricComparison is a single row of (current, previous, abs, pct)
// the buildComparison helper fills in. innerFloat is a tiny local
// helper so percentage_change stays a *float64 (omitempty-safe) when
// previous == 0 instead of leaking 0 (which would lie if the metric
// had no comparison available).
func buildComparison(current, previous windowRowStats) analytics.Comparison {
	return analytics.Comparison{
		Views:    metricComparisonFloat(float64(current.viewsSum), float64(previous.viewsSum)),
		WatchTimeMinutes: metricComparisonFloat(
			float64(current.watchTimeMinutesSum), float64(previous.watchTimeMinutesSum)),
		// SubscribersNet comparison: CURRENT-WINDOW net growth vs
		// PREVIOUS-WINDOW net growth. Comparing two zero-baseline
		// deltas is more informative than comparing two absolute
		// counts (which Summary.SubscribersNet already covers).
		SubscribersNet: metricComparisonFloat(
			currentSubscribersNet(current),
			currentSubscribersNet(previous),
		),
		// SubscribersNet's "previous" is intentionally the previous
		// WINDOW's net change, NOT the previous-window subscribers_end.
		// Comparing two net deltas is more informative than comparing
		// two absolute counts; the user sees "growth is faster/slower
		// than last period", not "the channel has more subscribers than
		// before" (which the absolute Summary already covers).
		EstimatedRevenue: metricComparisonCents(
			current.estimatedRevenueCents, previous.estimatedRevenueCents),
		VideosPublished: metricComparisonFloat(
			float64(current.videosPublished), float64(previous.videosPublished)),
		AverageViewsPerVideo: metricComparisonFloat(
			currentAvgPerVideoOrZero(current), currentAvgPerVideoOrZero(previous)),
	}
}

// currentAvgPerVideoOrZero is the zero-guarded helper for
// average_views_per_video's comparison denominator.
func currentAvgPerVideoOrZero(s windowRowStats) float64 {
	if s.videosPublished <= 0 {
		return 0
	}
	return float64(s.viewsSum) / float64(s.videosPublished)
}

// currentSubscribersNet returns the within-window net subscriber
// change (ending count minus starting count). Called TWICE by
// buildComparison so the deliverable comparison becomes
// "current window's net growth vs previous window's net growth" —
// the operator-facing signal in the SubscribersNet KPI card. The
// absolute subscriber count is reported separately in
// Summary.SubscribersNet.
func currentSubscribersNet(s windowRowStats) float64 {
	return float64(s.subscribersEnd - s.subscribersStart)
}

// metricComparisonFloat returns a MetricComparison with a present-or-
// nil percentage_change. nil when previous == 0 (JSON omitempty) so
// encoding/json never sees an Infinity float.
func metricComparisonFloat(current, previous float64) analytics.MetricComparison {
	out := analytics.MetricComparison{
		CurrentValue:   current,
		PreviousValue:  previous,
		AbsoluteChange: current - previous,
	}
	if previous != 0 {
		pct := (current - previous) / previous * 100
		out.PercentageChange = &pct
	}
	return out
}

// metricComparisonCents is the optional-revenue variant of
// metricComparisonFloat. When either input is nil the comparison is
// uncomputable; previous=0 hides the percentage as in the float form.
func metricComparisonCents(current, previous *int64) analytics.MetricComparison {
	out := analytics.MetricComparison{}
	if current != nil {
		out.CurrentValue = float64(*current)
	}
	if previous != nil {
		out.PreviousValue = float64(*previous)
	}
	out.AbsoluteChange = out.CurrentValue - out.PreviousValue
	if previous != nil && *previous != 0 {
		pct := (out.CurrentValue - out.PreviousValue) / float64(*previous) * 100
		out.PercentageChange = &pct
	}
	return out
}

// buildDailySeries produces exactly period.Days elements sorted by
// date ASC. The chart consumer expects this invariant; otherwise it
// has to detect and pad its own gaps, which leaks formatting logic
// into the SPA.
//
// SubscribersNet per day = abs(subscribers_today) - abs(subscribers_yesterday)
// using the REPOSITORY row's absolute Subscribers count (not the
// DailyPoint's accumulated value) as the baseline, tracked in a
// local var. Missing calendar days (no repo row at all) render as
// zero: no subscriber movement on a day with no data is reported as
// zero, NOT carried forward (carry would silently inflate growth on
// quiet days).
//
// Daily revenue point: the LAST KNOWN revenue row's value carries
// forward to fill any gap day. Revenue is a CUMULATIVE snapshot
// (yt-analytics API returns the reconciled earning amount per
// complete day), so a missing snapshot ought to surface as "earnings
// roll forward unchanged", NOT "earnings vanished". The first day
// (len(out) == 0) skips the carry-forward and reads revenue directly
// — if it's nil we simply omit (per contract: revenue is optional).
func buildDailySeries(
	history []repository.AccountMetricPoint,
	period analytics.Period,
) []analytics.DailyPoint {
	if period.Days <= 0 {
		return []analytics.DailyPoint{}
	}
	byDate := make(map[string]repository.AccountMetricPoint, len(history))
	for _, row := range history {
		key := row.Date.UTC().Format("2006-01-02")
		byDate[key] = row
	}
	out := make([]analytics.DailyPoint, 0, period.Days)
	var lastRev *int64
	var prevSubscribers *int64
	for d := period.StartDate; !d.After(period.EndDate); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		row, ok := byDate[key]
		pt := analytics.DailyPoint{Date: d}
		// rowRev captures the row's RevenueCents pointer inside
		// the if-ok block so we never dereference outside; a nil
		// rowRev means "no snapshot today" (gap day).
		var rowRev *int64
		if ok {
			rowRev = row.RevenueCents
			pt.Views = row.Views
			if row.WatchTimeMins != nil {
				pt.WatchTimeMinutes = *row.WatchTimeMins
			}
			if prevSubscribers != nil {
				pt.SubscribersNet = row.Subscribers - *prevSubscribers
			} else {
				pt.SubscribersNet = 0
			}
			today := row.Subscribers
			prevSubscribers = &today
		}
		// Revenue carry-forward: a CUMULATIVE snapshot
		// (yt-analytics returns the reconciled earning amount per
		// complete day), so a missing value rolls forward the LAST
		// KNOWN revenue (whether the row exists at all OR the
		// monetary value isn't stamped today). Runs OUTSIDE the
		// if-ok block so a gap day still inherits.
		if rowRev != nil {
			v := *rowRev
			pt.EstimatedRevenueCents = &v
			lastRev = &v
		} else if lastRev != nil {
			v := *lastRev
			pt.EstimatedRevenueCents = &v
		}
		out = append(out, pt)
	}
	return out
}



// avatarURLFromMetadata reads the account.Metadata JSONB blob for
// the avatar_url key. Returns empty string (which omitempty will
// drop from the JSON) when the key is missing OR not a string.
//
// Kept as a one-liner helper so the assembler body reads at a
// glance; the snapshot store will eventually replace this with
// snapshot.Profile["avatar_url"] (always-fresh), but the shared
// Metadata blob is the lightweight fallback used today.
func avatarURLFromMetadata(account *models.PlatformAccount) string {
	if v, ok := account.Metadata["avatar_url"].(string); ok {
		return v
	}
	return ""
}

// buildDataFreshness stamps the data_freshness section. last_synced_at
// is the latest row date with non-zero data (falling back to
// period.EndDate when the window has no data so the SPA can still
// render a timestamp). is_stale follows the TTL map; an unknown
// period (defensive — Resolver would have rejected) always reports
// stale so the operator falls back to a manual refresh.
func buildDataFreshness(current windowRowStats, period analytics.Period, generatedAt time.Time) analytics.DataFreshness {
	last := current.lastDate
	if last.IsZero() {
		last = period.EndDate
	}
	ttl := dataFreshnessTTL(period.Days)
	stale := true
	if ttl > 0 && generatedAt.Sub(last) <= ttl {
		stale = false
	}
	// No data in the window = always stale. The above TTL math
	// can return stale=false for ``generatedAt=period.EndDate``
	// + lastDate=period.EndDate (zero gap), which would mislead
	// the SPA into showing "fresh" on a channel with no metrics.
	// Forcing stale=true here preserves the no-data affordance
	// the SPA uses to surface the "Refresh data" button.
	if !current.hasData {
		stale = true
	}
	return analytics.DataFreshness{LastSyncedAt: last, IsStale: stale}
}

// resolvedYouTubeChannelID reads the YouTube channel's stable UC id
// from the OAuth-binding record stored under
// account.Metadata["channel_id"]. Empty string is a sentinel:
// the handler must reject any account whose id resolves to empty
// rather than emitting a contract-broken response.
//
// Kept in this file (instead of an internal/analytics helper) to
// keep internal/analytics side-effect free — the assembler package
// never imports models directly. If a future refactor pulls
// channel resolution into internal/auth or internal/models, this
// helper moves with it.
func resolvedYouTubeChannelID(account *models.PlatformAccount) string {
	for _, key := range []string{"channel_id", "youtube_channel_id"} {
		if v, ok := account.Metadata[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
