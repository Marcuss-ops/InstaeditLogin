// Package api — unit tests for the per-channel performance assembler.
//
// The assembler is a pure function: no I/O, no clock reads, no HTTP.
// These tests cover the CONTRACT-relevant behaviour only — JSON
// shape decisions live in the analytics package's own contract tests.
//
// The tests deliberately use synthesised AccountMetricPoint fixtures
// rather than the repository; covering the data-shape transformations
// here keeps the http-handler tests (when added in Step 6) focused on
// auth / routing / status-code mapping.
package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ptRow is a tiny helper for building AccountMetricPoint fixtures
// inside a single test — saves typing the full struct literal every
// time. Pointer fields stay nil by default so the JSON omission rules
// are exercised cleanly.
type ptRow struct {
	date         time.Time
	subscribers  int64
	views        int64
	watchTime    *int64
	videos       int64
	impressions  *int64
	ctr          *float64
	revenueCents *int64
}

func (p ptRow) toPoint() repository.AccountMetricPoint {
	return repository.AccountMetricPoint{
		Date:          p.date,
		Subscribers:   p.subscribers,
		Views:         p.views,
		Videos:        p.videos,
		WatchTimeMins: p.watchTime,
		Impressions:   p.impressions,
		CTR:           p.ctr,
		RevenueCents:  p.revenueCents,
	}
}

// makeAccount returns a YouTube PlatformAccount with channel id
// populated in BOTH common key spellings so resolvedYouTubeChannelID
// returns a stable string. Tests that need the missing-id path
// override via direct unkeyed assembler call.
func makeAccount() *models.PlatformAccount {
	return &models.PlatformAccount{
		ID:             381,
		UserID:         42,
		Platform:       "youtube",
		PlatformUserID: "113848374927471624321",
		Username:       "Demo Channel",
		Status:         "active",
		Metadata: models.Metadata{
			"channel_id":         "UCabc",
			"youtube_channel_id": "UCabc",
			"avatar_url":         "https://example.test/a.png",
		},
	}
}

// d returns midnight UTC for the supplied date — the assembler is
// timezone-naive on input rows but the contract demands UTC output.
func d(y, m, day int) time.Time {
	return time.Date(y, time.Month(m), day, 0, 0, 0, 0, time.UTC)
}

// intTestPtr is a small helper for fixtures. Renamed from int64Ptr
// to dodge the helper of the same name in drive_batch_v2_test.go
// (same package, shared test binary); collision would surface as a
// "redeclared in this block" vet failure.
func intTestPtr(v int64) *int64 { return &v }

// TestAssembleChannelPerformance_CanonicalShape asserts the
// top-level keys match the contract and ChannelInfo pulls from the
// account.
func TestAssembleChannelPerformance_CanonicalShape(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	var history []repository.AccountMetricPoint
	// 17 rows of monotonically growing data spanning both windows (the
	// previous-window starts at calendar day ~10 of the month, the
	// current window at ~day 24; we just need two non-empty halves).
	dayCount := 17
	for i := 0; i < dayCount; i++ {
		subs := int64(1000 + i*10)
		views := int64(5000 + i*100)
		videos := int64(50 + i)
		row := ptRow{
			date: d(2026, 7, i+1).UTC(), subscribers: subs, views: views, videos: videos,
		}.toPoint()
		row.WatchTimeMins = intTestPtr(600)
		rev := int64(1000 + i*50)
		row.RevenueCents = &rev
		history = append(history, row)
	}
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	raw, _ := json.Marshal(resp)
	var asMap map[string]json.RawMessage
	_ = json.Unmarshal(raw, &asMap)

	wantKeys := []string{"channel", "comparison", "daily_series", "data_freshness",
		"generated_at", "period", "summary", "top_videos"}
	for _, k := range wantKeys {
		if _, ok := asMap[k]; !ok {
			t.Errorf("canonical key %q missing from response", k)
		}
	}

	if resp.Channel.PlatformAccountID != 381 {
		t.Errorf("Channel.PlatformAccountID: want 381, got %d", resp.Channel.PlatformAccountID)
	}
	if resp.Channel.YouTubeChannelID != "UCabc" {
		t.Errorf("Channel.YouTubeChannelID: want UCabc, got %q", resp.Channel.YouTubeChannelID)
	}
	if resp.Channel.ChannelName != "Demo Channel" {
		t.Errorf("Channel.ChannelName: want Demo Channel, got %q", resp.Channel.ChannelName)
	}
	if resp.Channel.AvatarURL != "https://example.test/a.png" {
		t.Errorf("Channel.AvatarURL: want https://example.test/a.png, got %q", resp.Channel.AvatarURL)
	}
	if resp.Channel.Status != "active" {
		t.Errorf("Channel.Status: want active, got %q", resp.Channel.Status)
	}
	if resp.TopVideos.MostViewed == nil || len(resp.TopVideos.MostViewed) != 0 {
		t.Errorf("TopVideos.MostViewed: want []TopVideo{}, got %+v", resp.TopVideos.MostViewed)
	}
	if resp.TopVideos.Growing == nil || len(resp.TopVideos.Growing) != 0 {
		t.Errorf("TopVideos.Growing: want []TopVideo{}, got %+v", resp.TopVideos.Growing)
	}
}

// TestAssembleChannelPerformance_DailySeriesIgnoresPreviousWindow:
// the daily_series slice MUST cover exactly period.Days elements
// representing the CURRENT window only; previous-window rows MUST
// NOT leak in.
func TestAssembleChannelPerformance_DailySeriesIgnoresPreviousWindow(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	history := make([]repository.AccountMetricPoint, 0, 12)
	for i := 0; i < 7; i++ {
		history = append(history, ptRow{
			date: period.PreviousStartDate.AddDate(0, 0, i),
		}.toPoint())
	}
	for i := 0; i < 5; i++ {
		history = append(history, ptRow{
			date: period.StartDate.AddDate(0, 0, i),
		}.toPoint())
	}
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	if got := len(resp.DailySeries); got != period.Days {
		t.Fatalf("DailySeries length: want %d (one per current-window day), got %d", period.Days, got)
	}
	for i, pt := range resp.DailySeries {
		want := period.StartDate.AddDate(0, 0, i)
		if !pt.Date.Equal(want) {
			t.Errorf("DailySeries[%d].Date: want %v, got %v", i, want, pt.Date)
		}
	}
}

// TestAssembleChannelPerformance_DailySeriesGapFill rules: missing
// days render as zero for all deltas. This is the contract test
// the chart consumer relies on.
func TestAssembleChannelPerformance_DailySeriesGapFill(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	history := []repository.AccountMetricPoint{
		ptRow{date: period.StartDate.AddDate(0, 0, 0), views: 100, subscribers: 100, videos: 1}.toPoint(),
		ptRow{date: period.StartDate.AddDate(0, 0, 2), views: 200, subscribers: 110, videos: 2}.toPoint(),
		ptRow{date: period.StartDate.AddDate(0, 0, 4), views: 400, subscribers: 120, videos: 4}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	if len(resp.DailySeries) != period.Days {
		t.Fatalf("DailySeries length: want %d, got %d", period.Days, len(resp.DailySeries))
	}
	if d := resp.DailySeries[1]; d.Views != 0 || d.WatchTimeMinutes != 0 || d.SubscribersNet != 0 {
		t.Errorf("DailySeries[1] (gap day): want all zeros, got %+v", d)
	}
	if d := resp.DailySeries[2]; d.SubscribersNet != 10 {
		t.Errorf("DailySeries[2].SubscribersNet: want 10, got %d", d.SubscribersNet)
	}
	if d := resp.DailySeries[2]; d.Views != 200 {
		t.Errorf("DailySeries[2].Views: want 200, got %d", d.Views)
	}
}

// TestAssembleChannelPerformance_ComparisonPreviousZeroOmitted:
// when previous == 0, percentage_change MUST be omitted in JSON for
// the views KPI (no Infinity, no `null` literal). The contract test
// fixture on the analytics package asserts the same rule.
func TestAssembleChannelPerformance_ComparisonPreviousZeroOmitted(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	var current []repository.AccountMetricPoint
	for i := 0; i < 7; i++ {
		current = append(current, ptRow{
			date:        period.StartDate.AddDate(0, 0, i),
			views:       int64(100 * (i + 1)),
			subscribers: int64(1000 + i*10),
			videos:      int64(i + 1),
		}.toPoint())
	}
	resp := assembleChannelPerformance(account, "UCabc", current, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	if resp.Comparison.Views.PercentageChange != nil {
		t.Errorf("Comparison.Views.PercentageChange: want nil (previous=0), got %v", *resp.Comparison.Views.PercentageChange)
	}
	if resp.Comparison.Views.CurrentValue != 2800 {
		t.Errorf("Comparison.Views.CurrentValue: want 2800, got %v", resp.Comparison.Views.CurrentValue)
	}
	if resp.Comparison.Views.PreviousValue != 0 {
		t.Errorf("Comparison.Views.PreviousValue: want 0, got %v", resp.Comparison.Views.PreviousValue)
	}
	// Type-safe negative assertion: parse the comparison struct out
	// of the wire JSON and confirm PercentageChange is absent (not
	// null, not zero). Catches a contract regression without
	// substring matching a broad "views": key that the same string
	// also appears inside daily_series.
	raw, _ := json.Marshal(resp)
	var decodedComparison analytics.Comparison
	if err := json.Unmarshal(raw, &decodedComparison); err != nil {
		t.Fatalf("decode comparison: %v", err)
	}
	if decodedComparison.Views.PercentageChange != nil {
		t.Errorf("wire JSON: percentage_change for views must be OMITTED when previous=0")
	}
	// Belt-and-suspenders: confirm the literal "null" never shows up
	// for percentage_change in this payload.
	if strings.Contains(string(raw), `"percentage_change":null`) {
		t.Errorf("wire JSON must not contain \"percentage_change\":null literal: %s", string(raw))
	}
}

// TestAssembleChannelPerformance_ComparisonNegativeDeltaPresent:
// the spec requires percentage_change stays present (and negative)
// when current < previous AND previous != 0. Matches the
// analytics package's contract_test.go fixture assertion.
func TestAssembleChannelPerformance_ComparisonNegativeDeltaPresent(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	revCur := int64(1234)
	revPrev := int64(4000)
	current := []repository.AccountMetricPoint{
		ptRow{date: period.StartDate, revenueCents: &revCur}.toPoint(),
	}
	for i := 1; i < 7; i++ {
		current = append(current, ptRow{date: period.StartDate.AddDate(0, 0, i), revenueCents: &revCur}.toPoint())
	}
	previous := []repository.AccountMetricPoint{
		ptRow{date: period.PreviousStartDate, revenueCents: &revPrev}.toPoint(),
	}
	for i := 1; i < 7; i++ {
		previous = append(previous, ptRow{date: period.PreviousStartDate.AddDate(0, 0, i), revenueCents: &revPrev}.toPoint())
	}
	history := append(previous, current...)
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	pct := resp.Comparison.EstimatedRevenue.PercentageChange
	if pct == nil {
		t.Fatalf("Comparison.EstimatedRevenue.PercentageChange must be non-nil for negative delta with previous != 0")
	}
	want := -69.15
	got := *pct
	if got < want-0.01 || got > want+0.01 {
		t.Errorf("Comparison.EstimatedRevenue.PercentageChange: want ~%v, got %v", want, got)
	}
}

// TestAssembleChannelPerformance_OptionalFieldsOmitted: CTR /
// Impressions / Revenue MUST be omitted in JSON when no row supplies
// them. The contract doc requires "no silent zero".
func TestAssembleChannelPerformance_OptionalFieldsOmitted(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	current := []repository.AccountMetricPoint{
		ptRow{date: period.StartDate, views: 0, subscribers: 0, videos: 0}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", current, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	raw, _ := json.Marshal(resp)
	s := string(raw)
	if strings.Contains(s, `"impressions":`) {
		t.Errorf("impressions must be OMITTED when nil, found: %s", s)
	}
	if strings.Contains(s, `"ctr":`) {
		t.Errorf("ctr must be OMITTED when nil, found: %s", s)
	}
	if strings.Contains(s, `"estimated_revenue_cents":`) {
		t.Errorf("estimated_revenue_cents must be OMITTED when nil, found: %s", s)
	}
}

// TestAssembleChannelPerformance_DataFreshnessTTLTable asserts the
// period-aware TTL thresholds: 7d = 10m, 14d = 20m, 28d = 30m.
// generatedAt-(last_synced_at) within TTL → fresh; past TTL → stale.
func TestAssembleChannelPerformance_DataFreshnessTTLTable(t *testing.T) {
	account := makeAccount()
	cases := []struct {
		days int
		ttl  time.Duration
	}{
		{7, 10 * time.Minute},
		{14, 20 * time.Minute},
		{28, 30 * time.Minute},
	}
	for _, tc := range cases {
		period, err := analytics.Resolve(tc.days)
		if err != nil {
			t.Errorf("Resolve(%d): %v", tc.days, err)
			continue
		}
		// Populate one row dated at period.StartDate so the window
		// has data (round-9's no-data gate requires hasData=true for
		// the TTL math to apply; an empty window forces stale=true
		// regardless of TTL — that path is owned by
		// NoDataReturns200, not by this TTL-boundary table test).
		history := []repository.AccountMetricPoint{
			ptRow{date: period.StartDate, views: 100, subscribers: 1000, videos: 1}.toPoint(),
		}
		// Fresh: generatedAt = last_synced_at + (TTL - 1s).
		genFresh := period.StartDate.Add(tc.ttl - time.Second)
		resp := assembleChannelPerformance(account, "UCabc", history, period, genFresh, analytics.TopVideosRanking{})
		if resp.DataFreshness.IsStale {
			t.Errorf("TTL=%s: is_stale should be FRESH when generatedAt is within TTL, got stale", tc.ttl)
		}
		// Stale: generatedAt = last_synced_at + (TTL + 1s).
		genStale := period.StartDate.Add(tc.ttl + time.Second)
		resp2 := assembleChannelPerformance(account, "UCabc", history, period, genStale, analytics.TopVideosRanking{})
		if !resp2.DataFreshness.IsStale {
			t.Errorf("TTL=%s: is_stale should be STALE 1s past TTL, got fresh", tc.ttl)
		}
		wantSync := period.StartDate
		if !resp2.DataFreshness.LastSyncedAt.Equal(wantSync) {
			t.Errorf("LastSyncedAt: want %v (last repo row date), got %v",
				wantSync, resp2.DataFreshness.LastSyncedAt)
		}
	}
}

// TestAssembleChannelPerformance_VideosPublishedDeltaNet: when the
// repo's videos column jumps DOWN between two rows (rare but
// possible during a backfill), the assembler must clamp
// videos_published at zero rather than reporting a negative count.
//
// Uses a non-degenerate regression scenario: day-0 videos=50, day-6
// videos=49. The "all equal" case is a degenerate that masks the
// regression check; a true regression-oriented test exercises the
// boundary with a non-zero subscribers delta so otherwise-correct
// math still maps to the tightened clamp.
func TestAssembleChannelPerformance_VideosPublishedDeltaNet(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	history := []repository.AccountMetricPoint{
		ptRow{date: period.StartDate.AddDate(0, 0, 0), views: 100, subscribers: 100, videos: 50}.toPoint(),
		ptRow{date: period.StartDate.AddDate(0, 0, 6), views: 250, subscribers: 130, videos: 49}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	if resp.Summary.VideosPublished != 0 {
		t.Errorf("Summary.VideosPublished must clamp at 0 on regression (50→49), got %d", resp.Summary.VideosPublished)
	}
	if resp.Summary.SubscribersNet != 30 {
		t.Errorf("Summary.SubscribersNet: want 30 (100→130), got %d", resp.Summary.SubscribersNet)
	}
}

// TestAssembleChannelPerformance_SubscribersGainedLostSignedDelta:
// until Step 4 service lands gained/lost columns, the assembler
// derives these from the signed delta. The sign convention is
// preserved: positive delta → gained=delta, lost=0; negative delta
// → lost=|delta|, gained=0.
func TestAssembleChannelPerformance_SubscribersGainedLostSignedDelta(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	t.Run("growth", func(t *testing.T) {
		history := []repository.AccountMetricPoint{
			ptRow{date: period.StartDate, subscribers: 100, videos: 0}.toPoint(),
			ptRow{date: period.StartDate.AddDate(0, 0, 6), subscribers: 150, videos: 5}.toPoint(),
		}
		resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
		if resp.Summary.SubscribersGained != 50 || resp.Summary.SubscribersLost != 0 || resp.Summary.SubscribersNet != 50 {
			t.Errorf("growth: gained=%d lost=%d net=%d, want 50/0/50",
				resp.Summary.SubscribersGained, resp.Summary.SubscribersLost, resp.Summary.SubscribersNet)
		}
	})
	t.Run("churn", func(t *testing.T) {
		history := []repository.AccountMetricPoint{
			ptRow{date: period.StartDate, subscribers: 200, videos: 0}.toPoint(),
			ptRow{date: period.StartDate.AddDate(0, 0, 6), subscribers: 150, videos: 5}.toPoint(),
		}
		resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
		if resp.Summary.SubscribersGained != 0 || resp.Summary.SubscribersLost != 50 || resp.Summary.SubscribersNet != -50 {
			t.Errorf("churn: gained=%d lost=%d net=%d, want 0/50/-50",
				resp.Summary.SubscribersGained, resp.Summary.SubscribersLost, resp.Summary.SubscribersNet)
		}
	})
}

// TestAssembleChannelPerformance_LastDateInvariant pins the
// "lastDate == last repo row's Date" invariant. Future refactors
// that promote this to "max-by-date" semantics silently change
// downstream consumers (data_freshness.last_synced_at); this test
// surface as a failure the day that decision flips.
func TestAssembleChannelPerformance_LastDateInvariant(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	current := []repository.AccountMetricPoint{
		ptRow{date: period.StartDate, views: 100, subscribers: 1000, videos: 1}.toPoint(),
		ptRow{date: period.StartDate.AddDate(0, 0, 3), views: 200, subscribers: 1100, videos: 2}.toPoint(),
		ptRow{date: period.StartDate.AddDate(0, 0, 6), views: 300, subscribers: 1200, videos: 3}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", current, period, period.EndDate, analytics.TopVideosRanking{})
	want := period.StartDate.AddDate(0, 0, 6)
	if !resp.DataFreshness.LastSyncedAt.Equal(want) {
		t.Errorf("last_synced_at: want %v (last repo row date), got %v",
			want, resp.DataFreshness.LastSyncedAt)
	}
}

// TestAssembleChannelPerformance_DailySeriesRevenueCarryForwardOnGap
// pins the "revenue carries forward" rule the assembler applies on
// gap days. Revenue is a CUMULATIVE snapshot (yt-analytics returns
// the reconciled earning amount per complete day) so a missing
// snapshot ought to surface as "earnings roll forward unchanged"
// rather than "earnings vanished".
//
// Without this pinning, a refactor that drops the lastRev closure
// would silently regress the chart to zero-fill revenue on quiet
// days, mirroring the inviolate subscribers-net=0 rule but the
// OPPOSITE mathematical meaning (subscribers is a delta, revenue
// is a cumulative snapshot).
func TestAssembleChannelPerformance_DailySeriesRevenueCarryForwardOnGap(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	rev := int64(25000)
	history := []repository.AccountMetricPoint{
		ptRow{
			date: period.StartDate, views: 100, subscribers: 100, videos: 1,
			revenueCents: &rev,
		}.toPoint(),
		// Gap day 2 has NO repo row → revenue MUST inherit day-1's $250.
		ptRow{
			date: period.StartDate.AddDate(0, 0, 3), views: 200, subscribers: 110,
			videos: 2, revenueCents: &rev,
		}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	if len(resp.DailySeries) != 7 {
		t.Fatalf("DailySeries length: want 7, got %d", len(resp.DailySeries))
	}
	// Day 0 (the first repo row) must surface its own revenue.
	if v := resp.DailySeries[0].EstimatedRevenueCents; v == nil || *v != 25000 {
		t.Errorf("DailySeries[0].EstimatedRevenueCents: want 25000, got %v", v)
	}
	// Day 1 (gap day with NO row) must carry forward $250 unchanged.
	if v := resp.DailySeries[1].EstimatedRevenueCents; v == nil || *v != 25000 {
		t.Errorf("DailySeries[1] (gap day).EstimatedRevenueCents: want inherited 25000, got %v", v)
	}
	// Day 2 (the second repo row) must surface its own revenue.
	if v := resp.DailySeries[2].EstimatedRevenueCents; v == nil || *v != 25000 {
		t.Errorf("DailySeries[2].EstimatedRevenueCents: want 25000, got %v", v)
	}
	// Subsequent gap days MUST continue to carry forward the last
	// known revenue value.
	for i := 3; i < 7; i++ {
		v := resp.DailySeries[i].EstimatedRevenueCents
		if v == nil || *v != 25000 {
			t.Errorf("DailySeries[%d] (post-row gap).EstimatedRevenueCents: want inherited 25000, got %v", i, v)
		}
	}
}

// TestAssembleChannelPerformance_DailySeriesRevenueOmittedOnFirstGap
// pins the dual rule: revenue is omitted (not zero) until we see
// the first row carrying revenue. Without this, the SPA's renderer
// would draw a $0 line on day 0/1 (literal false data) and a $X
// line on day 2+ (carry-forward), which is the same visual signal
// but with a different truth meaning.
func TestAssembleChannelPerformance_DailySeriesRevenueOmittedOnFirstGap(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	rev := int64(1500)
	history := []repository.AccountMetricPoint{
		// Only the LAST day has revenue; days 0..5 must be nil (not 0).
		ptRow{
			date: period.EndDate, views: 100, subscribers: 100, videos: 1,
			revenueCents: &rev,
		}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	for i := 0; i < 6; i++ {
		if resp.DailySeries[i].EstimatedRevenueCents != nil {
			t.Errorf("DailySeries[%d] (pre-revenue).EstimatedRevenueCents: want nil, got %v",
				i, *resp.DailySeries[i].EstimatedRevenueCents)
		}
	}
	// Day 6 has the actual row.
	if v := resp.DailySeries[6].EstimatedRevenueCents; v == nil || *v != 1500 {
		t.Errorf("DailySeries[6].EstimatedRevenueCents: want 1500, got %v", v)
	}
}

// TestAssembleChannelPerformance_ComparisonPrevVideosZeroOmittedPercentage
// pins the AverageViewsPerVideo percentage_change omission rule
// when previous videosPublished = 0. The contract says: NEVER
// encode an Infinity float into JSON (encoding/json refuses to
// marshal +Inf / -Inf; it writes `null`, which the SPA then has
// to special-case). The omission rule (vs. a zero literal) is
// what makes the wire shape forward-compatible.
//
// Regression direction a future refactor could introduce:
//   - accidentally summing two averages via the viewsRatio formula
//     (current-prev / prev) — when prev=0, this yields +Inf.
//   - comparing videos counts directly instead of average per video.
func TestAssembleChannelPerformance_ComparisonPrevVideosZeroOmittedPercentage(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	// Current: videos GROW across rows so videos_published > 0
	// (delta = last_videos - first_videos, the assembler formula).
	// views > 0 too so the average is computable.
	current := []repository.AccountMetricPoint{}
	for i := 0; i < 7; i++ {
		current = append(current, ptRow{
			date:        period.StartDate.AddDate(0, 0, i),
			views:       int64(1400 + i*100),
			subscribers: int64(1000 + i*10),
			videos:      int64(i + 1), // monotonically increasing → delta > 0
		}.toPoint())
	}
	// Previous: 0 videos (regression fixture). avg_per_video == 0.
	previous := []repository.AccountMetricPoint{}
	for i := 0; i < 7; i++ {
		previous = append(previous, ptRow{
			date: period.PreviousStartDate.AddDate(0, 0, i),
			views: 0, subscribers: 0, videos: 0,
		}.toPoint())
	}
	history := append(previous, current...)
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30), analytics.TopVideosRanking{})

	if resp.Summary.VideosPublished <= 0 {
		t.Fatalf("current VideosPublished must be > 0 (sanity): got %d",
			resp.Summary.VideosPublished)
	}
	if resp.Summary.AverageViewsPerVideo <= 0 {
		t.Fatalf("current AverageViewsPerVideo must be > 0 (sanity): got %v",
			resp.Summary.AverageViewsPerVideo)
	}

	// The pivot: comparison MUST have PercentageChange = nil (NOT
	// +Inf, NOT null wire literal). Same wire-shape rule as views
	// when previous == 0.
	if resp.Comparison.AverageViewsPerVideo.PercentageChange != nil {
		t.Errorf("AverageViewsPerVideo.PercentageChange: want nil (previous.videosPublished=0), got %v",
			*resp.Comparison.AverageViewsPerVideo.PercentageChange)
	}
	if resp.Comparison.AverageViewsPerVideo.PreviousValue != 0 {
		t.Errorf("AverageViewsPerVideo.PreviousValue: want 0, got %v",
			resp.Comparison.AverageViewsPerVideo.PreviousValue)
	}
	raw, _ := json.Marshal(resp)
	if strings.Contains(string(raw), `"percentage_change":null`) {
		t.Errorf("wire JSON must not contain \"percentage_change\":null literal: %s", string(raw))
	}
}

// TestAssembleChannelPerformance_TopVideosExplicitSlices pins the
// "TopVideos emitted as empty arrays, NOT nil" contract decision.
//
// The handler contract says top_videos { most_viewed: [], growing:
// [] }. If a refactor changes the assembler to nil these slices
// (e.g. by `return analytics.TopVideosRanking{}` with zero-value
// fields), the SPA's unconditional iteration over `.most_viewed`
// would crash with "cannot read property 'length' of null".
//
// Empty data is the realistic default today (the per-video metrics
// source does not exist yet; scorer lands in Step 4 wiring), so
// this is the production wire shape we're emitting on every
// instance until then.
func TestAssembleChannelPerformance_TopVideosExplicitSlices(t *testing.T) {
	period, err := analytics.Resolve(7)
	if err != nil {
		t.Fatalf("Resolve(7): %v", err)
	}
	account := makeAccount()
	// Construct a populated history so we rule out "empty data, so
	// we just didn't reach the emit" as a false positive.
	current := []repository.AccountMetricPoint{
		ptRow{date: period.StartDate, views: 1000, subscribers: 1100, videos: 5}.toPoint(),
		ptRow{date: period.StartDate.AddDate(0, 0, 6), views: 1500, subscribers: 1200, videos: 6}.toPoint(),
	}
	resp := assembleChannelPerformance(account, "UCabc", current, period, d(2026, 7, 30), analytics.TopVideosRanking{})
	if resp.TopVideos.MostViewed == nil {
		t.Errorf("TopVideos.MostViewed: want []TopVideo{} (non-nil empty), got nil")
	}
	if len(resp.TopVideos.MostViewed) != 0 {
		t.Errorf("TopVideos.MostViewed length: want 0, got %d", len(resp.TopVideos.MostViewed))
	}
	if resp.TopVideos.Growing == nil {
		t.Errorf("TopVideos.Growing: want []TopVideo{} (non-nil empty), got nil")
	}
	if len(resp.TopVideos.Growing) != 0 {
		t.Errorf("TopVideos.Growing length: want 0, got %d", len(resp.TopVideos.Growing))
	}
	raw, _ := json.Marshal(resp)
	if strings.Contains(string(raw), `"most_viewed":null`) {
		t.Errorf("wire JSON: top_videos.most_viewed must be [] / [], never null: %s", string(raw))
	}
	if strings.Contains(string(raw), `"growing":null`) {
		t.Errorf("wire JSON: top_videos.growing must be [] / [], never null: %s", string(raw))
	}
}
