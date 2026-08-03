package analytics

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// expectedTopLevelKeys is the closed set of structural sections the
// SPA decoder and the OpenAPI spec rely on. Adding a key here is a
// deliberate wire-shape change and MUST be coordinated with the front
// end and openapi.yaml.
var expectedTopLevelKeys = []string{
	"channel", "comparison", "daily_series", "data_freshness",
	"generated_at", "period", "summary", "top_videos",
}

// responseFixture is the canonical round-trip fixture used by the
// marshal / unmarshal assertions. It exercises every optional field
// (CTR, EstimatedRevenue, RevenueCentsInPeriod, GrowthPercentage,
// PercentageChange) so the test catches accidental pointer/zero
// regressions for every field the contract claims to support.
func responseFixture(t *testing.T) ChannelPerformanceResponse {
	t.Helper()
	rev := int64(1234)
	revPerVideo := int64(5000)
	pct := 12.5
	pctNeg := -69.15
	ctrVal := 0.045
	return ChannelPerformanceResponse{
		Channel: ChannelInfo{
			PlatformAccountID: 381,
			YouTubeChannelID:  "UCabc",
			ChannelName:       "Demo Channel",
			AvatarURL:         "https://example.test/a.png",
			Status:            "active",
		},
		Period: Period{
			Days:              7,
			StartDate:         time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC),
			EndDate:           time.Date(2026, 7, 29, 23, 59, 59, 0, time.UTC),
			PreviousStartDate: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
			PreviousEndDate:   time.Date(2026, 7, 22, 23, 59, 59, 0, time.UTC),
			Timezone:          PeriodUTC,
		},
		Summary: Summary{
			Views:                 100000,
			WatchTimeMinutes:      50000,
			SubscribersGained:     200,
			SubscribersLost:       50,
			SubscribersNet:        150,
			EstimatedRevenueCents: &rev,
			VideosPublished:       5,
			AverageViewsPerVideo:  20000,
			Impressions:           nil, // MUST be omitted in JSON
			CTR:                   &ctrVal,
		},
		Comparison: Comparison{
			Views: MetricComparison{
				CurrentValue:     100000,
				PreviousValue:    80000,
				AbsoluteChange:   20000,
				PercentageChange: &pct,
			},
			WatchTimeMinutes: MetricComparison{
				CurrentValue:     50000,
				PreviousValue:    0,
				AbsoluteChange:   50000,
				PercentageChange: nil, // MUST be omitted (no Infinity in JSON)
			},
			// SubscribersNet: positive delta → percentage present.
			SubscribersNet: MetricComparison{CurrentValue: 150, PreviousValue: 100, AbsoluteChange: 50, PercentageChange: &pct},
			// EstimatedRevenue: NEGATIVE delta with previous != 0.
			// The contract requires percentage_change to be PRESENT
			// whenever previous != 0 — negative deltas are valid
			// comparisons and MUST NOT be flattened to nil/omitted.
			EstimatedRevenue:     MetricComparison{CurrentValue: 1234, PreviousValue: 4000, AbsoluteChange: -2766, PercentageChange: &pctNeg},
			VideosPublished:      MetricComparison{CurrentValue: 5, PreviousValue: 4, AbsoluteChange: 1, PercentageChange: &pct},
			AverageViewsPerVideo: MetricComparison{CurrentValue: 20000, PreviousValue: 16000, AbsoluteChange: 4000, PercentageChange: &pct},
		},
		DailySeries: []DailyPoint{
			{Date: time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC), Views: 12000, WatchTimeMinutes: 6000, SubscribersNet: 20},
			{Date: time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC), Views: 15000, WatchTimeMinutes: 7500, SubscribersNet: 25, EstimatedRevenueCents: &revPerVideo},
			// Missing day filled with zeros so the chart is gap-free.
			{Date: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)},
		},
		TopVideos: TopVideosRanking{
			MostViewed: []TopVideo{{
				VideoID:              "vid1",
				Title:                "Top viewed",
				ThumbnailURL:         "https://example.test/t1.jpg",
				PublishedAt:          time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
				ViewsInPeriod:        50000,
				WatchTimeInPeriod:    25000,
				RevenueCentsInPeriod: &revPerVideo,
				ViewsPerDay:          7142,
				GrowthPercentage:     &pct,
				TrendScore:           500.5,
				YouTubeURL:           "https://youtube.com/watch?v=vid1",
			}},
			Growing: []TopVideo{{
				VideoID:           "vid2",
				Title:             "Growing fast",
				PublishedAt:       time.Date(2026, 7, 28, 8, 0, 0, 0, time.UTC),
				ViewsInPeriod:     20000,
				WatchTimeInPeriod: 10000,
				ViewsPerDay:       10000,
				GrowthPercentage:  nil, // recent videos MUST omit
				TrendScore:        950.25,
				YouTubeURL:        "https://youtube.com/watch?v=vid2",
			}},
		},
		GeneratedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		DataFreshness: DataFreshness{
			LastSyncedAt: time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
			IsStale:      false,
		},
	}
}

// staleResponseFixture mirrors responseFixture but flips IsStale so
// TestDataFreshnessBothStates can lock the wire shape on both values.
func staleResponseFixture(t *testing.T) ChannelPerformanceResponse {
	t.Helper()
	base := responseFixture(t)
	base.DataFreshness = DataFreshness{
		LastSyncedAt: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		IsStale:      true,
	}
	return base
}

// TestResponseMarshalsExpectedShape locks the wire format. Any
// change here is breaking for the SPA — updating this test is the
// signal to also bump the contract version in api/openapi.yaml.
//
// The test decodes the marshal output as a generic
// map[string]json.RawMessage so a future field like "channel_id"
// can NOT silently satisfy a Contains("channel") check.
func TestResponseMarshalsExpectedShape(t *testing.T) {
	resp := responseFixture(t)
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("decode into map: %v", err)
	}

	// Exact key-set assertion: NO allowed extra top-level keys.
	gotKeys := make([]string, 0, len(asMap))
	for k := range asMap {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	wantKeys := slices.Clone(expectedTopLevelKeys)
	sort.Strings(wantKeys)
	if !slices.Equal(gotKeys, wantKeys) {
		t.Errorf("top-level key set drift\n got:  %v\n want: %v", gotKeys, wantKeys)
	}

	s := string(raw)

	// Optional fields MUST round-trip when present…
	for _, mustContain := range []string{
		`"estimated_revenue_cents":1234`,
		`"ctr":0.045`,
		`"revenue_cents_in_period":5000`,
		`"growth_percentage":12.5`,
		`"trend_score":500.5`,
		`"youtube_channel_id":"UCabc"`,
		// Negative percentage on a non-zero previous period MUST be
		// present — the contract only omits percentage_change when
		// previous==0, NOT when the delta is negative.
		`"percentage_change":-69.15`,
	} {
		if !strings.Contains(s, mustContain) {
			t.Errorf("response missing value %q\nJSON: %s", mustContain, s)
		}
	}

	// …and MUST be omitted when nil (no silent coercion to 0).
	// Otherwise the frontend would render "0 impressions" as truth.
	if strings.Contains(s, `"impressions":`) {
		t.Errorf("impressions must be omitted when nil, found: %s", s)
	}
	// Although omitempty on a nil pointer omits (not nulls) the key,
	// lock the rule explicitly so a future encoding change can't
	// regress to "percentage_change":null when previous=0.
	if strings.Contains(s, `"percentage_change":null`) {
		t.Errorf("percentage_change must be OMITTED (not null) when previous=0\nJSON: %s", s)
	}

	// Round-trip: the nested struct must decode back without loss.
	var back ChannelPerformanceResponse
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Channel section.
	if back.Channel.PlatformAccountID != 381 {
		t.Errorf("channel.platform_account_id round-trip: want 381, got %d", back.Channel.PlatformAccountID)
	}
	if back.Channel.YouTubeChannelID != "UCabc" {
		t.Errorf("channel.youtube_channel_id round-trip: want UCabc, got %q", back.Channel.YouTubeChannelID)
	}

	// Period section.
	if back.Period.Days != 7 {
		t.Errorf("period.days round-trip: want 7, got %d", back.Period.Days)
	}
	if !back.Period.StartDate.Equal(resp.Period.StartDate) {
		t.Errorf("period.start_date round-trip mismatch: %v vs %v", back.Period.StartDate, resp.Period.StartDate)
	}

	// Summary section — including SubscribersGained/Lost which the
	// previous round-trip skipped.
	if back.Summary.SubscribersGained != 200 {
		t.Errorf("summary.subscribers_gained round-trip: want 200, got %d", back.Summary.SubscribersGained)
	}
	if back.Summary.SubscribersLost != 50 {
		t.Errorf("summary.subscribers_lost round-trip: want 50, got %d", back.Summary.SubscribersLost)
	}
	if back.Summary.SubscribersNet != 150 {
		t.Errorf("summary.subscribers_net round-trip: want 150, got %d", back.Summary.SubscribersNet)
	}
	if back.Summary.EstimatedRevenueCents == nil || *back.Summary.EstimatedRevenueCents != 1234 {
		t.Errorf("summary.estimated_revenue_cents round-trip: want 1234, got %v", back.Summary.EstimatedRevenueCents)
	}
	if back.Summary.CTR == nil || *back.Summary.CTR != 0.045 {
		t.Errorf("summary.ctr round-trip: want 0.045, got %v", back.Summary.CTR)
	}
	if back.Summary.Impressions != nil {
		t.Errorf("summary.impressions round-trip: want nil, got %v", *back.Summary.Impressions)
	}

	// Comparison section — negative-delta percentage MUST still be
	// present and round-trip to -69.15. The previous version of this
	// fixture flattened ANY negative delta to nil, which contradicted
	// the contract doc (percentage_change is omitted ONLY when
	// previous == 0).
	if back.Comparison.Views.PercentageChange == nil || *back.Comparison.Views.PercentageChange != 12.5 {
		t.Errorf("comparison.views.percentage_change round-trip: want 12.5, got %v", back.Comparison.Views.PercentageChange)
	}
	if back.Comparison.WatchTimeMinutes.PercentageChange != nil {
		t.Errorf("comparison.watch_time_minutes.percentage_change must be nil (previous=0), got %v", *back.Comparison.WatchTimeMinutes.PercentageChange)
	}
	if back.Comparison.EstimatedRevenue.PreviousValue != 4000 {
		t.Errorf("comparison.estimated_revenue.previous_value round-trip: want 4000, got %v", back.Comparison.EstimatedRevenue.PreviousValue)
	}
	if back.Comparison.EstimatedRevenue.PercentageChange == nil || *back.Comparison.EstimatedRevenue.PercentageChange != -69.15 {
		t.Errorf("comparison.estimated_revenue.percentage_change must round-trip to -69.15 (NOT nil) for negative delta with previous != 0, got %v", back.Comparison.EstimatedRevenue.PercentageChange)
	}

	// Daily series — len MUST be exactly period.days so the chart is
	// gap-free; spot-check the gap-fill rule.
	if len(back.DailySeries) != 3 {
		t.Fatalf("daily_series round-trip: want 3 points, got %d", len(back.DailySeries))
	}
	if back.DailySeries[2].Views != 0 || back.DailySeries[2].SubscribersNet != 0 {
		t.Errorf("daily_series gap-fill: zero day should round-trip with zero KPIs, got %+v", back.DailySeries[2])
	}
	if back.DailySeries[1].EstimatedRevenueCents == nil || *back.DailySeries[1].EstimatedRevenueCents != 5000 {
		t.Errorf("daily_series[1].estimated_revenue_cents round-trip: want 5000, got %v", back.DailySeries[1].EstimatedRevenueCents)
	}

	// Top videos — both rankings MUST round-trip with all optional
	// fields intact (TrendScore is the scorer-emitted scalar the
	// previous round-trip skipped).
	if len(back.TopVideos.MostViewed) != 1 || back.TopVideos.MostViewed[0].VideoID != "vid1" {
		t.Errorf("top_videos.most_viewed[0].video_id round-trip: want vid1, got %+v", back.TopVideos.MostViewed)
	}
	if len(back.TopVideos.Growing) != 1 || back.TopVideos.Growing[0].VideoID != "vid2" {
		t.Errorf("top_videos.growing[0].video_id round-trip: want vid2, got %+v", back.TopVideos.Growing)
	}
	if back.TopVideos.MostViewed[0].TrendScore != 500.5 {
		t.Errorf("top_videos.most_viewed[0].trend_score round-trip: want 500.5, got %v", back.TopVideos.MostViewed[0].TrendScore)
	}
	if back.TopVideos.MostViewed[0].RevenueCentsInPeriod == nil || *back.TopVideos.MostViewed[0].RevenueCentsInPeriod != 5000 {
		t.Errorf("top_videos.most_viewed[0].revenue_cents_in_period round-trip: want 5000, got %v", back.TopVideos.MostViewed[0].RevenueCentsInPeriod)
	}
	if back.TopVideos.MostViewed[0].GrowthPercentage == nil || *back.TopVideos.MostViewed[0].GrowthPercentage != 12.5 {
		t.Errorf("top_videos.most_viewed[0].growth_percentage round-trip: want 12.5, got %v", back.TopVideos.MostViewed[0].GrowthPercentage)
	}
	if back.TopVideos.Growing[0].GrowthPercentage != nil {
		t.Errorf("top_videos.growing[0].growth_percentage must be nil (recent video), got %v", *back.TopVideos.Growing[0].GrowthPercentage)
	}

	// Data freshness (this branch covers IsStale=false; the true
	// branch is exercised by TestDataFreshnessBothStates below).
	if back.DataFreshness.IsStale {
		t.Errorf("data_freshness.is_stale round-trip: want false, got true")
	}
}

// TestDataFreshnessBothStates locks the wire shape of IsStale on
// BOTH true and false so a future bool→nullable migration is a
// deliberate change rather than a silent regression.
func TestDataFreshnessBothStates(t *testing.T) {
	t.Run("is_stale_false", func(t *testing.T) {
		resp := responseFixture(t)
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal (fresh): %v", err)
		}
		if !strings.Contains(string(raw), `"is_stale":false`) {
			t.Errorf("is_stale:false must appear literally in wire JSON\nJSON: %s", string(raw))
		}
		var back ChannelPerformanceResponse
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal (fresh): %v", err)
		}
		if back.DataFreshness.IsStale {
			t.Errorf("data_freshness.is_stale round-trip (fresh): want false, got true")
		}
	})
	t.Run("is_stale_true", func(t *testing.T) {
		resp := staleResponseFixture(t)
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal (stale): %v", err)
		}
		if !strings.Contains(string(raw), `"is_stale":true`) {
			t.Errorf("is_stale:true must appear literally in wire JSON\nJSON: %s", string(raw))
		}
		var back ChannelPerformanceResponse
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal (stale): %v", err)
		}
		if !back.DataFreshness.IsStale {
			t.Errorf("data_freshness.is_stale round-trip (stale): want true, got false")
		}
	})
}

// TestNaNTrendScoreRejectedByJSONMarshal guards the sanitisation
// invariant documented on TopVideo.TrendScore: the scorer MUST
// convert non-finite floats to 0 BEFORE populating, because
// encoding/json refuses to marshal them. This test pins the
// behaviour so a regression surfaces loudly rather than as a
// confusing 500 in production.
func TestNaNTrendScoreRejectedByJSONMarshal(t *testing.T) {
	bad := TopVideo{TrendScore: math.NaN()}
	if _, err := json.Marshal(bad); err == nil {
		t.Fatal("json.Marshal MUST refuse NaN TrendScore; scorer sanitisation invariant violated")
	}
}

// TestIsValidPeriod locks the closed period set: introducing 30 or 90
// here would re-open the comparison-mixing bug the spec explicitly
// forbids (7 vs 28 produces meaningless growth percentages).
func TestIsValidPeriod(t *testing.T) {
	cases := []struct {
		days int
		ok   bool
	}{
		{7, true},
		{14, true},
		{28, true},
		{0, false},
		{1, false},
		{8, false},
		{30, false},
		{90, false},
		{-7, false},
		{365, false},
	}
	for _, tc := range cases {
		if got := IsValidPeriod(tc.days); got != tc.ok {
			t.Errorf("IsValidPeriod(%d): want %v, got %v", tc.days, tc.ok, got)
		}
	}
}

// TestAllowedPeriodDays guards the ordering of the closed set so the
// UI tab order (shortest → longest) does not silently change.
func TestAllowedPeriodDays(t *testing.T) {
	want := []int{7, 14, 28}
	if len(AllowedPeriodDays) != len(want) {
		t.Fatalf("AllowedPeriodDays length: want %d, got %d", len(want), len(AllowedPeriodDays))
	}
	for i, v := range want {
		if AllowedPeriodDays[i] != v {
			t.Errorf("AllowedPeriodDays[%d]: want %d, got %d", i, v, AllowedPeriodDays[i])
		}
	}
}
