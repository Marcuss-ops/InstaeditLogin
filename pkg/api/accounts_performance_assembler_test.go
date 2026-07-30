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
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
	resp := assembleChannelPerformance(account, "UCabc", current, period, d(2026, 7, 30))
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
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
	resp := assembleChannelPerformance(account, "UCabc", current, period, d(2026, 7, 30))
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
		// Fresh: generatedAt = last + (TTL - 1s).
		genFresh := period.EndDate.Add(tc.ttl - time.Second)
		resp := assembleChannelPerformance(account, "UCabc", nil, period, genFresh)
		if resp.DataFreshness.IsStale {
			t.Errorf("TTL=%s: is_stale should be FRESH when generatedAt is within TTL, got stale", tc.ttl)
		}
		// Stale: generatedAt = last + (TTL + 1s).
		genStale := period.EndDate.Add(tc.ttl + time.Second)
		resp2 := assembleChannelPerformance(account, "UCabc", nil, period, genStale)
		if !resp2.DataFreshness.IsStale {
			t.Errorf("TTL=%s: is_stale should be STALE 1s past TTL, got fresh", tc.ttl)
		}
		if resp2.DataFreshness.LastSyncedAt.IsZero() {
			t.Errorf("LastSyncedAt fallback: must equal Period.EndDate when history is empty, got zero")
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
	resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
		resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
		resp := assembleChannelPerformance(account, "UCabc", history, period, d(2026, 7, 30))
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
	resp := assembleChannelPerformance(account, "UCabc", current, period, period.EndDate)
	want := period.StartDate.AddDate(0, 0, 6)
	if !resp.DataFreshness.LastSyncedAt.Equal(want) {
		t.Errorf("last_synced_at: want %v (last repo row date), got %v",
			want, resp.DataFreshness.LastSyncedAt)
	}
}
