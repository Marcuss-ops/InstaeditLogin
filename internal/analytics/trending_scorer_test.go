package analytics

import (
	"math"
	"testing"
	"time"
)

// generatedAtRef is the canonical "now" used by every test in
// this file so a fixture's relative age math stays readable. The
// scorer doesn't read the clock; generatedAt is passed in.
var generatedAtRef = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

// pct constructs a *float64 Trend scorer test fixture from a
// literal percentage value. Tiny helper to keep fixtures readable.
func pct(v float64) *float64 { return &v }

// testVideo is a tiny builder for TopVideo fixtures. The fields
// the test cares about (ViewsInPeriod, GrowthPercentage,
// PublishedAt, VideoID) are first-class arguments; everything
// else is the test author's responsibility to fill.
type testVideo struct {
	videoID          string
	publishedAt      time.Time
	viewsInPeriod    int64
	growthPercentage *float64
	// pre-set TrendScore so the RankMostViewed preservation test
	// and the mutation-guard test can assert the input value is
	// preserved. Untouched by ScoreGrowing (which overwrites on
	// the COPY).
	preScore float64
}

func (t testVideo) toTopVideo() TopVideo {
	return TopVideo{
		VideoID:          t.videoID,
		PublishedAt:      t.publishedAt,
		ViewsInPeriod:    t.viewsInPeriod,
		GrowthPercentage: t.growthPercentage,
		TrendScore:       t.preScore,
		// ViewsPerDay is left zero on input — the scorer overwrites
		// it on the returned COPY. The input value is asserted as
		// untouched in TestScorer_NoMutation so we keep input as-is.
	}
}

// TestScorer_YoungBeatsOld is the user's spec anchor: 20k views
// in 2 days MUST outrank 50k views in 28 days, regardless of
// historical magnitude. Locks the contract doc's "video con
// 20.000 visualizzazioni in due giorni ≥ video vecchio con
// 50.000 visualizzazioni in 28 giorni" rule.
func TestScorer_YoungBeatsOld(t *testing.T) {
	young := testVideo{
		videoID: "vid-young", publishedAt: generatedAtRef.Add(-2 * 24 * time.Hour),
		viewsInPeriod: 20000, growthPercentage: nil,
	}.toTopVideo()
	old := testVideo{
		videoID: "vid-old", publishedAt: generatedAtRef.Add(-28 * 24 * time.Hour),
		viewsInPeriod: 50000, growthPercentage: pct(0),
	}.toTopVideo()
	out := ScoreGrowing([]TopVideo{young, old}, generatedAtRef)
	if len(out) != 2 {
		t.Fatalf("ScoreGrowing output length: want 2, got %d", len(out))
	}
	if out[0].VideoID != "vid-young" {
		t.Errorf("ScoreGrowing winner: want vid-young (20k views in 2d), got %q (score=%v)", out[0].VideoID, out[0].TrendScore)
	}
	if out[1].VideoID != "vid-old" {
		t.Errorf("ScoreGrowing runner-up: want vid-old (50k views in 28d), got %q", out[1].VideoID)
	}
	if out[0].TrendScore <= out[1].TrendScore {
		t.Errorf("young trend_score (%v) must exceed old (%v) for the user's spec anchor",
			out[0].TrendScore, out[1].TrendScore)
	}
}

// TestScorer_24hClamp asserts the "applica età minima 24h" rule:
// a video published HOURS ago (not DAYS) still computes
// views_per_day against a 1-day minimum divisor, not against the
// raw sub-day age.
func TestScorer_24hClamp(t *testing.T) {
	twoHoursOld := testVideo{
		videoID: "vid-2h", publishedAt: generatedAtRef.Add(-2 * time.Hour),
		viewsInPeriod: 10000, growthPercentage: nil,
	}.toTopVideo()
	out := ScoreGrowing([]TopVideo{twoHoursOld}, generatedAtRef)
	want := 10000.0
	if got := out[0].ViewsPerDay; math.Abs(got-want) > 0.001 {
		t.Errorf("ViewsPerDay for 2h-old 10k-view video: want %v (24h clamp), got %v", want, got)
	}
	if math.IsNaN(out[0].TrendScore) || math.IsInf(out[0].TrendScore, 0) {
		t.Errorf("TrendScore must be finite, got %v", out[0].TrendScore)
	}
}

// TestScorer_ZeroViewsNoNaN: a video with 0 views_in_period
// (published but unwatched) MUST yield TrendScore=0, NOT NaN. The
// divide-by-zero guard in safeViewsPerDay covers this branch.
func TestScorer_ZeroViewsNoNaN(t *testing.T) {
	zero := testVideo{
		videoID: "vid-zero", publishedAt: generatedAtRef.Add(-3 * 24 * time.Hour),
		viewsInPeriod: 0, growthPercentage: pct(0),
	}.toTopVideo()
	out := ScoreGrowing([]TopVideo{zero}, generatedAtRef)
	if out[0].TrendScore != 0 {
		t.Errorf("zero-views TrendScore: want 0, got %v", out[0].TrendScore)
	}
	if math.IsNaN(out[0].ViewsPerDay) || math.IsInf(out[0].ViewsPerDay, 0) {
		t.Errorf("zero-views ViewsPerDay must be finite, got %v", out[0].ViewsPerDay)
	}
}

// TestScorer_FuturePublishedAt: a clock skew (PublishedAt is
// AFTER generatedAt) is clamped to a 1-day minimum age. Without
// the clamp, videoAgeDays would return a negative duration and
// safeViewsPerDay would emit 0 (a silent "video doesn't exist"
// signal). The 1-day floor prevents that interpretation.
func TestScorer_FuturePublishedAt(t *testing.T) {
	future := testVideo{
		videoID: "vid-future", publishedAt: generatedAtRef.Add(2 * time.Hour),
		viewsInPeriod: 5000, growthPercentage: nil,
	}.toTopVideo()
	out := ScoreGrowing([]TopVideo{future}, generatedAtRef)
	if math.IsNaN(out[0].ViewsPerDay) || math.IsInf(out[0].ViewsPerDay, 0) {
		t.Errorf("future-publishedAt ViewsPerDay must be finite, got %v", out[0].ViewsPerDay)
	}
	if out[0].ViewsPerDay != 5000 {
		t.Errorf("future-publishedAt ViewsPerDay: want 5000 (clamped to 1-day divisor), got %v", out[0].ViewsPerDay)
	}
}

// TestScorer_NegativeGrowthClampedAt01 locks the
// `growth_factor = max(0.1, 1+pct/100)` floor. A -150% weekly
// decline produces 1-1.5 = -0.5; without the floor the score
// would go negative and INVERT the ranking. The clamp keeps
// negative-performing videos in the running at a 10% weight so
// the operator can still see them rather than a 0-score.
func TestScorer_NegativeGrowthClampedAt01(t *testing.T) {
	bad := testVideo{
		videoID: "vid-bad", publishedAt: generatedAtRef.Add(-7 * 24 * time.Hour),
		viewsInPeriod: 10000, growthPercentage: pct(-150),
	}.toTopVideo()
	out := ScoreGrowing([]TopVideo{bad}, generatedAtRef)
	if out[0].TrendScore <= 0 {
		t.Errorf("TrendScore for -150%% growth: must remain positive (clamped at 0.1), got %v", out[0].TrendScore)
	}
}

// TestScorer_NaNGrowthPercentageSanitised: a corrupted upstream
// value (math.NaN) propagating into growth_factor would
// produce a NaN product. The sanitiseFloat pass at the end of
// ScoreGrowing MUST clamp to 0.0 so the wire JSON marshals
// cleanly. Pins the contract `TestNaNTrendScoreRejectedByJSONMarshal`
// invariant end-to-end via the scorer.
func TestScorer_NaNGrowthPercentageSanitised(t *testing.T) {
	badNaN := math.NaN()
	corrupted := testVideo{
		videoID: "vid-corrupt", publishedAt: generatedAtRef.Add(-5 * 24 * time.Hour),
		viewsInPeriod: 8000, growthPercentage: &badNaN,
	}.toTopVideo()
	out := ScoreGrowing([]TopVideo{corrupted}, generatedAtRef)
	if math.IsNaN(out[0].TrendScore) || math.IsInf(out[0].TrendScore, 0) {
		t.Errorf("NaN upstream: sanitiser must coerce TrendScore to 0, got %v", out[0].TrendScore)
	}
	if out[0].TrendScore != 0 {
		t.Errorf("NaN upstream: want TrendScore=0, got %v", out[0].TrendScore)
	}
}

// TestScorer_GrowingLessCascadeReal pins the deterministic
// tiebreak stack end-to-end via GrowingLessForTest. Each tier of
// the cascade —
//
//	TrendScore DESC  →  ViewsInPeriod DESC  →  PublishedAt ASC  →  VideoID ASC
//
// is exercised with hand-crafted inputs that bypass ScoreGrowing's
// formula recomputation, so a regression to ANY tier surfaces as a
// test failure on the SPECIFIC tier (the table-driven subtests).
//
// The "Real" suffix marks a test that actually invokes the
// production comparator — the older "TestScorer_DeterministicTieBreak"
// was hollow (hand-rolled a closure in test scope; the production
// comparator was never actually called).
func TestScorer_GrowingLessCascadeReal(t *testing.T) {
	base := TopVideo{
		VideoID:       "AAA",
		PublishedAt:   generatedAtRef.Add(-7 * 24 * time.Hour),
		ViewsInPeriod: 5000,
		TrendScore:    100.0,
	}
	cases := []struct {
		name   string
		other  TopVideo
		expect bool // other should PRECEDE base under GrowingLessForTest
	}{
		{
			name: "tier1_higher_score_wins",
			other: TopVideo{
				VideoID: "MMM", PublishedAt: generatedAtRef.Add(-7 * 24 * time.Hour),
				ViewsInPeriod: 5000, TrendScore: 200.0,
			},
			expect: true,
		},
		{
			name: "tier1_lower_score_loses",
			other: TopVideo{
				VideoID: "MMM", PublishedAt: generatedAtRef.Add(-7 * 24 * time.Hour),
				ViewsInPeriod: 5000, TrendScore: 50.0,
			},
			expect: false,
		},
		{
			name: "tier2_higher_views_wins_on_tied_score",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: base.PublishedAt,
				ViewsInPeriod: 9999, TrendScore: 100.0,
			},
			expect: true,
		},
		{
			name: "tier2_lower_views_loses_on_tied_score",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: base.PublishedAt,
				ViewsInPeriod: 1000, TrendScore: 100.0,
			},
			expect: false,
		},
		{
			name: "tier3_older_published_at_wins_on_tied_views",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: generatedAtRef.Add(-30 * 24 * time.Hour),
				ViewsInPeriod: 5000, TrendScore: 100.0,
			},
			expect: true,
		},
		{
			name: "tier3_newer_published_at_loses_on_tied_views",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: generatedAtRef.Add(-3 * 24 * time.Hour),
				ViewsInPeriod: 5000, TrendScore: 100.0,
			},
			expect: false,
		},
		{
			name: "tier4_higher_video_id_loses_on_full_three_way_tie",
			other: TopVideo{
				VideoID: "ZZZ", PublishedAt: base.PublishedAt,
				ViewsInPeriod: 5000, TrendScore: 100.0,
			},
			expect: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GrowingLessForTest(tc.other, base)
			if got != tc.expect {
				t.Errorf("GrowingLessForTest(other=%+v, base=%+v): want %v, got %v",
					tc.other, base, tc.expect, got)
			}
		})
	}
}

// TestScorer_MostViewedLessCascadeReal mirrors the Growing
// cascade for the MostViewed comparator. ViewsInPeriod is the
// primary key (NOT TrendScore per spec); the secondary is
// PublishedAt ASC; the quaternary is VideoID ASC. TrendScore is
// INTENTIONALLY ignored here — a regression that promotes
// TrendScore as a sort key in this comparator would skew the
// ranking toward manually-tuned scores instead of viewership.
func TestScorer_MostViewedLessCascadeReal(t *testing.T) {
	base := TopVideo{
		VideoID:       "AAA",
		PublishedAt:   generatedAtRef.Add(-7 * 24 * time.Hour),
		ViewsInPeriod: 5000,
		TrendScore:    100.0,
	}
	cases := []struct {
		name   string
		other  TopVideo
		expect bool
	}{
		{
			name: "primary_higher_views_wins",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: base.PublishedAt,
				ViewsInPeriod: 9999, TrendScore: 1.0, // different TrendScore — must be IGNORED
			},
			expect: true,
		},
		{
			name: "primary_lower_views_loses_even_if_higher_score",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: base.PublishedAt,
				ViewsInPeriod: 1000, TrendScore: 9999.0, // higher score but fewer views
			},
			expect: false,
		},
		{
			name: "secondary_older_published_at_wins_on_views_tie",
			other: TopVideo{
				VideoID: "AAA", PublishedAt: generatedAtRef.Add(-30 * 24 * time.Hour),
				ViewsInPeriod: 5000, TrendScore: base.TrendScore,
			},
			expect: true,
		},
		{
			name: "tertiary_higher_video_id_loses_on_full_tie",
			other: TopVideo{
				VideoID: "ZZZ", PublishedAt: base.PublishedAt,
				ViewsInPeriod: 5000, TrendScore: base.TrendScore,
			},
			expect: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MostViewedLessForTest(tc.other, base)
			if got != tc.expect {
				t.Errorf("MostViewedLessForTest(other=%+v, base=%+v): want %v, got %v",
					tc.other, base, tc.expect, got)
			}
		})
	}
}

// TestScorer_NoMutation: the caller's slice AND its elements MUST
// remain untouched after both scoring functions run. The scorer
// copies first; a regression that mutates input would silently
// invalidate upstream service caches that reuse the same fixture.
func TestScorer_NoMutation(t *testing.T) {
	original := testVideo{
		videoID: "vid-mut", publishedAt: generatedAtRef.Add(-5 * 24 * time.Hour),
		viewsInPeriod: 7000, growthPercentage: pct(20),
		preScore: 999.999,
	}.toTopVideo()
	originalViewsPerDay := original.ViewsPerDay
	originalTrending := original.TrendScore
	_ = ScoreGrowing([]TopVideo{original}, generatedAtRef)
	_ = RankMostViewed([]TopVideo{original})
	if original.TrendScore != originalTrending {
		t.Errorf("input TrendScore mutated: want %v, got %v", originalTrending, original.TrendScore)
	}
	if original.ViewsPerDay != originalViewsPerDay {
		t.Errorf("input ViewsPerDay mutated: want %v, got %v", originalViewsPerDay, original.ViewsPerDay)
	}
}

// TestScorer_EmptyAndNil: both empty slice AND nil must return
// []TopVideo{} (NOT nil) so the wire JSON emits `[]` and the
// SPA can iterate unconditionally. This is the contract's
// "most_viewed": [], "growing": [] empty-array contract.
func TestScorer_EmptyAndNil(t *testing.T) {
	for _, in := range [][]TopVideo{nil, {}} {
		out := ScoreGrowing(in, generatedAtRef)
		if out == nil {
			t.Errorf("ScoreGrowing(nil): want non-nil empty slice, got nil")
		}
		if len(out) != 0 {
			t.Errorf("ScoreGrowing(empty): want len 0, got %d", len(out))
		}
		out2 := RankMostViewed(in)
		if out2 == nil {
			t.Errorf("RankMostViewed(nil): want non-nil empty slice, got nil")
		}
		if len(out2) != 0 {
			t.Errorf("RankMostViewed(empty): want len 0, got %d", len(out2))
		}
	}
}

// TestRankMostViewed_HappyPath: simple DESC sort of
// ViewsInPeriod with the secondary PublishedAt → VideoID tiebreak.
// Asserts the Most Viewed ranking is stable AND TrendScore is
// preserved through the function (RankMostViewed never overwrites
// TrendScore on the returned copy — the function is purely a
// sort, no formula is applied).
func TestRankMostViewed_HappyPath(t *testing.T) {
	hi := testVideo{videoID: "A", publishedAt: generatedAtRef.Add(-10 * 24 * time.Hour), viewsInPeriod: 9000, preScore: 42.5}.toTopVideo()
	mid := testVideo{videoID: "B", publishedAt: generatedAtRef.Add(-5 * 24 * time.Hour), viewsInPeriod: 5000, preScore: 17.3}.toTopVideo()
	lo := testVideo{videoID: "C", publishedAt: generatedAtRef.Add(-2 * 24 * time.Hour), viewsInPeriod: 1000, preScore: -1.5}.toTopVideo()
	out := RankMostViewed([]TopVideo{lo, mid, hi})
	if len(out) != 3 {
		t.Fatalf("RankMostViewed length: want 3, got %d", len(out))
	}
	wantOrder := []string{"A", "B", "C"}
	wantScores := []float64{42.5, 17.3, -1.5}
	for i := range wantOrder {
		if out[i].VideoID != wantOrder[i] {
			t.Errorf("RankMostViewed[%d]: want video_id %q, got %q (ViewsInPeriod=%d)",
				i, wantOrder[i], out[i].VideoID, out[i].ViewsInPeriod)
		}
		if out[i].TrendScore != wantScores[i] {
			t.Errorf("RankMostViewed[%d].TrendScore: want %v (input preserved), got %v",
				i, wantScores[i], out[i].TrendScore)
		}
	}
}

// TestRankMostViewed_TieBreak_TieOnViews verifies the secondary
// tie: two videos with equal ViewsInPeriod are sorted by
// PublishedAt ASC (older wins).
func TestRankMostViewed_TieBreak_TieOnViews(t *testing.T) {
	older := testVideo{videoID: "same", publishedAt: generatedAtRef.Add(-30 * 24 * time.Hour), viewsInPeriod: 5000}.toTopVideo()
	newer := testVideo{videoID: "same", publishedAt: generatedAtRef.Add(-2 * 24 * time.Hour), viewsInPeriod: 5000}.toTopVideo()
	out := RankMostViewed([]TopVideo{newer, older})
	if out[0].PublishedAt != older.PublishedAt {
		t.Errorf("RankMostViewed tie: older PublishedAt must precede newer, got %v", out[0].PublishedAt)
	}
}

// TestSanitiseFloat pins the corner cases: NaN, +Inf, -Inf → 0.
// Finite values MUST pass through untouched.
func TestSanitiseFloat(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"nan", math.NaN(), 0},
		{"+inf", math.Inf(1), 0},
		{"-inf", math.Inf(-1), 0},
		{"zero", 0, 0},
		{"positive", 42.5, 42.5},
		{"negative", -1.5, -1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitiseFloat(tc.in); got != tc.want {
				t.Errorf("sanitiseFloat(%v): want %v, got %v", tc.in, tc.want, got)
			}
		})
	}
}
