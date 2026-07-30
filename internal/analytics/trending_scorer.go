package analytics

import (
	"math"
	"sort"
	"time"
)

// trendingRecencyHalfLifeDays is the half-life of the recency factor:
// a video exactly old enough to age halfway through the cap has
// recency_factor = 0.5. We picked 14 days because it matches the
// longest non-28d window the endpoint accepts (28/2 = 14), so a
// 28-day-old video is at "half-life" of the recency weight before
// the next window brings it back in. The constant is exported as
// `TrendingRecencyHalfLifeDays` so Step 4's ChannelAnalyticsService
// can audit-trail the same number in any operational log line.
//
// The shape `1 / (1 + age/halfLife)` is a simple hyperbolic decay:
// factor=1 when age=0, factor=0.5 when age=halfLife, factor→0 as
// age→∞, never negative, monotonically decreasing. No NaN/±Inf.
//
// If a future comment block wants a harder cutoff ("videos older
// than X must drop off the Growing tab"), wrap it AROUND the
// recency_factor derived here rather than changing the function
// — the function stays a pure scalar.
const trendingRecencyHalfLifeDays = 14.0

// TrendingRecencyHalfLifeDays exposes the constant for tests and
// any future operational audit. Kept exported because the formula
// is documented in the package godoc; an operator wanting a
// shorter half-life tune must change this constant AND the test
// that asserts the value.
const TrendingRecencyHalfLifeDays = trendingRecencyHalfLifeDays

// ScoreGrowing ranks the supplied candidate videos using the
// per-channel Growing tab's trend_score formula:
//
//	trend_score     = views_per_day × growth_factor × recency_factor
//	views_per_day   = ViewsInPeriod / VideoAgeDays
//	VideoAgeDays    = max(1.0, (generatedAt - PublishedAt) / 24h)
//	growth_factor   = 1.0  when GrowthPercentage is nil
//	                 max(0.1, 1 + (GrowthPercentage / 100)) otherwise
//	recency_factor  = 1 / (1 + VideoAgeDays / TrendingRecencyHalfLifeDays)
//
// Constants:
//   - VideoAgeDays is bounded BELOW by 1.0 so a video published
//     "instantly" (age = 0) does not divide by zero. The clamp
//     matches the 24h age minimum the user spec requires —
//     younger videos compute views_per_day against a 1-day window
//     rather than a stale-clock or sub-second window.
//   - growth_factor is bounded BELOW by 0.1 so a -150% weekly
//     decay still produces a positive score; otherwise an
//     extreme negative growth would invert the ranking.
//   - recency_factor is bounded INSIDE (0, 1] by the half-life
//     shape and is always finite.
//
// Deterministic tie-break: when two videos have EQUAL
// TrendScore, the stable secondary sort is ViewsInPeriod DESC,
// then PublishedAt ASC (older-vintage wins, rewarding sustained
// viewership), then VideoID ASC (alphanumeric). This order is
// fixed and documented so a refactor does not silently reorder
// equal-score pairs.
//
// Mutability: this function never mutates the caller's slice nor
// its elements. A defensive copy is taken before any field is
// rewritten; the caller's TopVideo structs keep their
// pre-existing ViewsPerDay/TrendScore values.
//
// NaN/±Inf on TrendScore and ViewsPerDay are forced to 0.0 by
// the sanitisation pass at the end — encoding/json refuses to
// marshal non-finite floats, so a NaN here would render as a 500
// rather than as a misleading zero in the SPA. The contract
// `TestNaNTrendScoreRejectedByJSONMarshal` in contract_test.go
// pins this invariant end-to-end.
//
// Empty/nil input returns `[]TopVideo{}` (NOT nil) so the JSON
// encoder emits the literal `[]` and the SPA can iterate
// unconditionally.
func ScoreGrowing(videos []TopVideo, generatedAt time.Time) []TopVideo {
	out := copyTopVideos(videos)
	for i := range out {
		age := videoAgeDays(out[i].PublishedAt, generatedAt)
		out[i].ViewsPerDay = safeViewsPerDay(out[i].ViewsInPeriod, age)
		out[i].TrendScore = computeTrendScore(out[i], age)
		out[i].TrendScore = sanitiseFloat(out[i].TrendScore)
		out[i].ViewsPerDay = sanitiseFloat(out[i].ViewsPerDay)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return GrowingLessForTest(out[i], out[j])
	})
	return out
}

// RankMostViewed sorts the supplied candidates by ViewsInPeriod
// DESC for the "Most viewed" tab. Same deterministic tie-break
// as ScoreGrowing (ViewsInPeriod is the primary key, then
// PublishedAt ASC, then VideoID ASC).
//
// No formula runs here — TrendScore and ViewsPerDay are
// preserved untouched on the returned copies, but we still run
// the sanitisation pass to guarantee TrendScore is finite
// (some upstream service might insert NaN via bad raw data;
// the contract forbids non-finite TopVideo on the wire).
func RankMostViewed(videos []TopVideo) []TopVideo {
	out := copyTopVideos(videos)
	for i := range out {
		out[i].TrendScore = sanitiseFloat(out[i].TrendScore)
		out[i].ViewsPerDay = sanitiseFloat(out[i].ViewsPerDay)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return MostViewedLessForTest(out[i], out[j])
	})
	return out
}

// copyTopVideos returns a fresh slice with shallow copies of each
// TopVideo. Mutating out[i].X does not touch the caller's
// videos[i].X, satisfying the no-mutation invariant.
func copyTopVideos(videos []TopVideo) []TopVideo {
	if len(videos) == 0 {
		return []TopVideo{}
	}
	out := make([]TopVideo, len(videos))
	copy(out, videos)
	return out
}

// videoAgeDays returns the age of the video in days, clamped to a
// minimum of 1.0. The clamp serves TWO purposes per the
// thinker's spec:
//
//  1. "video appena pubblicati" (just-published videos) have a
//     PublishedAt close to generatedAt — without the clamp, age
//     would be sub-day and views_per_day would be inflated by 24x
//     or more.
//  2. Clock skew or future-dated PublishedAt (which the contract
//     doc warns about for newly fetched YouTube data) is bound to
//     a sane 1-day minimum rather than emitting a NaN from a
//     negative duration.
func videoAgeDays(publishedAt, generatedAt time.Time) float64 {
	hours := generatedAt.Sub(publishedAt).Hours()
	if hours <= 0 {
		return 1.0
	}
	days := hours / 24.0
	if days < 1.0 {
		return 1.0
	}
	return days
}

// safeViewsPerDay = views_in_period / age. Returns 0 when
// ViewsInPeriod is 0 (a published-but-unwatched video) so the
// trend_score product is zero, NOT NaN.
func safeViewsPerDay(viewsInPeriod int64, ageDays float64) float64 {
	if viewsInPeriod <= 0 || ageDays <= 0 {
		return 0
	}
	return float64(viewsInPeriod) / ageDays
}

// growthFactor returns the multiplier in the trend_score product.
//   - When GrowthPercentage is nil (newly published, no previous
//     window data), emit a neutral 1.0 so the candidate is
//     comparable to peers without dragging into a zero bias.
//   - When GrowthPercentage is non-nil, apply 1 + (pct/100), but
//     clamp the lower bound to 0.1 so a -150% weekly decline
//     doesn't flip the ranking by emitting a negative score.
//
// Whoever assigns the result on the returned TopVideo MUST pass
// it through sanitiseFloat as well, in case upstream data
// signals NaN.
func growthFactor(p *float64) float64 {
	if p == nil {
		return 1.0
	}
	g := 1.0 + *p/100.0
	if g < 0.1 {
		return 0.1
	}
	return g
}

// recencyFactor returns the age-decay multiplier in (0, 1]. The
// half-life constant is exported (TrendingRecencyHalfLifeDays) so
// the test can pin the exact implementation.
func recencyFactor(ageDays float64) float64 {
	return 1.0 / (1.0 + ageDays/trendingRecencyHalfLifeDays)
}

// computeTrendScore applies the product formula and returns a
// finite float. The caller wraps the final result with
// sanitiseFloat so any sub-product NaN from upstream raw data
// cannot leak.
func computeTrendScore(v TopVideo, ageDays float64) float64 {
	return safeViewsPerDay(v.ViewsInPeriod, ageDays) *
		growthFactor(v.GrowthPercentage) *
		recencyFactor(ageDays)
}

// sanitiseFloat replaces NaN, +Inf and -Inf with 0.0. encoding/json
// refuses to marshal non-finite floats, so a NaN on TrendScore or
// ViewsPerDay becomes a 500 instead of a misleading zero. The
// contract test `TestNaNTrendScoreRejectedByJSONMarshal` pins
// this invariant end-to-end.
func sanitiseFloat(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// GrowingLessForTest exposes the internal Growing comparator
// for the test suite. Production code MUST NOT call this directly —
// sort.SliceStable(ScoreGrowing(out,...), GrowingLessForTest) is the
// pipeline. Exporting the helper under a name that screams "for
// test only" lets tests pin each tier of the cascade without
// forcing the test to recompute every score via the formula.
func GrowingLessForTest(i, j TopVideo) bool {
	if i.TrendScore != j.TrendScore {
		return i.TrendScore > j.TrendScore
	}
	if i.ViewsInPeriod != j.ViewsInPeriod {
		return i.ViewsInPeriod > j.ViewsInPeriod
	}
	if !i.PublishedAt.Equal(j.PublishedAt) {
		return i.PublishedAt.Before(j.PublishedAt)
	}
	return i.VideoID < j.VideoID
}

// MostViewedLessForTest exposes the internal Most Viewed comparator
// for the test suite. Production code MUST NOT call this directly.
// TrendScore is intentionally NOT a sort key here — the ranking is
// strictly body-content (views_in_period).
func MostViewedLessForTest(i, j TopVideo) bool {
	if i.ViewsInPeriod != j.ViewsInPeriod {
		return i.ViewsInPeriod > j.ViewsInPeriod
	}
	if !i.PublishedAt.Equal(j.PublishedAt) {
		return i.PublishedAt.Before(j.PublishedAt)
	}
	return i.VideoID < j.VideoID
}
