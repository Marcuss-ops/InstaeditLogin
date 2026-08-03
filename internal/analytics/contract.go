// Package analytics defines the canonical JSON contract for the
// per-channel performance endpoint:
//
//	GET /api/v1/accounts/{platform_account_id}/performance?days=7|14|28
//
// The contract is the single source of truth shared between:
//   - the HTTP handler in pkg/api,
//   - the ChannelAnalyticsService (added in a follow-up step),
//   - the per-channel SPA view in VeloxFrontend.
//
// Only the wire shape and its invariants live here. Computation,
// fetching and authorization all belong to the service / repository
// / handler layers — this package MUST stay side-effect free.
package analytics

import "time"

// AllowedPeriodDays is the closed set of period values the per-channel
// endpoint canonical accepts. The resolver rejects any value outside
// this set with HTTP 400 BEFORE this contract is instantiated.
var AllowedPeriodDays = []int{7, 14, 28}

// IsValidPeriod reports whether the supplied day count is one of the
// three canonical values. Used by the resolver and as a defensive
// check on the assembly side.
func IsValidPeriod(days int) bool {
	for _, v := range AllowedPeriodDays {
		if v == days {
			return true
		}
	}
	return false
}

// PeriodUTC is the canonical timezone the contract normalises every
// date pair to. The frontend may render in any locale, but the wire
// shape always carries UTC so two clients in different timezones see
// the same period boundaries.
const PeriodUTC = "UTC"

// ChannelPerformanceResponse is the full wire shape for
// GET /api/v1/accounts/{platform_account_id}/performance?days=7|14|28.
//
// The top-level fields are the structural sections promised to the
// frontend. The contract is intentionally extensions-friendly: new
// fields can be added with sensible defaults without breaking older
// SPA decoders, and optional metrics are pointers with `omitempty`
// so a channel that lacks CTR data is rendered as "no data" rather
// than as a deceptive zero.
//
// Upgrade path: the v0 surface lives in
// pkg/api/accounts_performance_handlers.go (accountPerformanceResponse)
// and continues to be returned by the existing handler. Step 2 must
// either (a) migrate that handler to emit this contract and add a
// field-by-field mapping layer, or (b) keep both contracts and let
// the new endpoint path return this richer shape. Whichever is
// chosen, the existing endpoint MUST keep its current wire format
// until the SPA confirms it consumes the enriched contract.
//
// Implementation prerequisites (to be satisfied by Steps 4-5):
//   - Summary.SubscribersGained / SubscribersLost require the
//     account_metric_history table to expose gained/lost per day
//     (today it stores only the absolute subscriber count);
//   - DailyPoint.EstimatedRevenueCents and
//     TopVideo.RevenueCentsInPeriod require a per-video revenue
//     source that the current repository does not expose.
type ChannelPerformanceResponse struct {
	Channel       ChannelInfo      `json:"channel"`
	Period        Period           `json:"period"`
	Summary       Summary          `json:"summary"`
	Comparison    Comparison       `json:"comparison"`
	DailySeries   []DailyPoint     `json:"daily_series"`
	TopVideos     TopVideosRanking `json:"top_videos"`
	GeneratedAt   time.Time        `json:"generated_at"`
	DataFreshness DataFreshness    `json:"data_freshness"`
}

// ChannelInfo identifies the YouTube account the response is about.
// PlatformAccountID is the stable identifier the SPA must use when
// navigating to or refreshing this view — never the channel name,
// the username, or the underlying YouTube channel ID.
//
// YouTubeChannelID has NO `omitempty`: a channel whose
// YouTubeChannelID is unknown MUST be rejected upstream
// (resolver / service), not silently delivered with an empty ID.
type ChannelInfo struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	YouTubeChannelID  string `json:"youtube_channel_id"`
	ChannelName       string `json:"channel_name"`
	AvatarURL         string `json:"avatar_url,omitempty"`
	Status            string `json:"status"`
}

// Period captures the resolved current and previous windows the
// service computed for the comparison. The Previous* fields always
// cover an equivalent-length window (7 vs 7, 14 vs 14, 28 vs 28) —
// the resolver rejects any request that would otherwise mix windows.
type Period struct {
	Days              int       `json:"days"`
	StartDate         time.Time `json:"start_date"`
	EndDate           time.Time `json:"end_date"`
	PreviousStartDate time.Time `json:"previous_start_date"`
	PreviousEndDate   time.Time `json:"previous_end_date"`
	Timezone          string    `json:"timezone"`
}

// Summary aggregates the headline KPIs for the requested window.
// CTR and Impressions are pointers because they are only meaningful
// when the account's OAuth scope + analytics granularity actually
// surface them — the contract MUST NOT silently coerce them to 0.
type Summary struct {
	Views                 int64   `json:"views"`
	WatchTimeMinutes      int64   `json:"watch_time_minutes"`
	SubscribersGained     int64   `json:"subscribers_gained"`
	SubscribersLost       int64   `json:"subscribers_lost"`
	SubscribersNet        int64   `json:"subscribers_net"`
	EstimatedRevenueCents *int64  `json:"estimated_revenue_cents,omitempty"`
	VideosPublished       int64   `json:"videos_published"`
	AverageViewsPerVideo  float64 `json:"average_views_per_video"`
	// Optional analytics — populated only when the source data
	// genuinely supports them. Missing values render as "no data"
	// in the frontend rather than a misleading zero.
	Impressions *int64   `json:"impressions,omitempty"`
	CTR         *float64 `json:"ctr,omitempty"`
}

// Comparison is the per-KPI delta between the current and previous
// equivalent-length windows. The comparison MUST always compare
// same-length windows (7 vs 7, 14 vs 14); never a 7-day vs a 28-day
// window.
type Comparison struct {
	Views                MetricComparison `json:"views"`
	WatchTimeMinutes     MetricComparison `json:"watch_time_minutes"`
	SubscribersNet       MetricComparison `json:"subscribers_net"`
	EstimatedRevenue     MetricComparison `json:"estimated_revenue"`
	VideosPublished      MetricComparison `json:"videos_published"`
	AverageViewsPerVideo MetricComparison `json:"average_views_per_video"`
}

// MetricComparison is the four-tuple the SPA renders inside each KPI
// card. PercentageChange is a pointer so a previous-period value of
// zero renders as "no comparison" instead of an Infinity percent
// change that would crash the frontend chart.
type MetricComparison struct {
	CurrentValue     float64  `json:"current_value"`
	PreviousValue    float64  `json:"previous_value"`
	AbsoluteChange   float64  `json:"absolute_change"`
	PercentageChange *float64 `json:"percentage_change,omitempty"`
}

// DailyPoint is one element of the per-day series the chart consumes.
// The slice MUST have exactly Period.Days elements — the service is
// responsible for filling missing days with zeros so the chart never
// has gaps.
type DailyPoint struct {
	Date                  time.Time `json:"date"`
	Views                 int64     `json:"views"`
	WatchTimeMinutes      int64     `json:"watch_time_minutes"`
	SubscribersNet        int64     `json:"subscribers_net"`
	EstimatedRevenueCents *int64    `json:"estimated_revenue_cents,omitempty"`
}

// TopVideosRanking is the dual-list ranking the SPA renders behind
// the "Most viewed" and "Growing" tabs. Both arrays are calculated
// server-side by the (future) TrendingVideoScorer; the SPA MUST NOT
// recompute them client-side.
type TopVideosRanking struct {
	MostViewed []TopVideo `json:"most_viewed"`
	Growing    []TopVideo `json:"growing"`
}

// TopVideo is a single ranked video entry. TrendScore is the
// server-computed floating point the Growing tab sorts on; the field
// is plain JSON so the SPA can read it without any decoder
// round-trips. GrowthPercentage and RevenueCentsInPeriod are
// pointers for the same reason as in Summary: zero is not a
// meaningful value when the underlying data is absent.
//
// TrendScore sanitisation invariant: the scorer (future
// internal/analytics.TrendingVideoScorer) MUST replace NaN, +Inf
// and -Inf with 0 before populating this field.
// encoding/json refuses to marshal non-finite floats, so a scorer
// that fails to sanitise will surface as a 500 rather than as a
// misleading zero rendered by the SPA.
type TopVideo struct {
	VideoID              string    `json:"video_id"`
	Title                string    `json:"title"`
	ThumbnailURL         string    `json:"thumbnail_url,omitempty"`
	PublishedAt          time.Time `json:"published_at"`
	ViewsInPeriod        int64     `json:"views_in_period"`
	WatchTimeInPeriod    int64     `json:"watch_time_in_period"`
	RevenueCentsInPeriod *int64    `json:"revenue_cents_in_period,omitempty"`
	ViewsPerDay          float64   `json:"views_per_day"`
	GrowthPercentage     *float64  `json:"growth_percentage,omitempty"`
	// TrendScore is the growing-rank score the growing tab sorts
	// on. The scorer (future internal/analytics.TrendingVideoScorer)
	// MUST replace NaN, +Inf and -Inf with 0 before populating —
	// encoding/json refuses non-finite floats, and a failure here
	// surfaces as a 500 rather than as a misleading zero in the SPA.
	TrendScore float64 `json:"trend_score"`
	YouTubeURL string  `json:"youtube_url"`
}

// DataFreshness surfaces whether the cached metrics are still fresh.
// IsStale is true when the age of LastSyncedAt has crossed the
// period-appropriate cache TTL (5 min for 7d, 15-30 min for 14/28d).
// The frontend MUST surface stale data instead of pretending it is
// fresh; the manual "Refresh" button is what regenerates it.
type DataFreshness struct {
	LastSyncedAt time.Time `json:"last_synced_at"`
	IsStale      bool      `json:"is_stale"`
}
