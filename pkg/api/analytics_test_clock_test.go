package api

import (
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
)

var analyticsTestNow = time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)

func fixedAnalyticsClock() analytics.Clock {
	return analytics.NewFixedClock(analyticsTestNow)
}

func resolveAnalyticsPeriod(days int) (analytics.Period, error) {
	return analytics.NewResolver().WithClock(fixedAnalyticsClock()).Resolve(days)
}

func newAnalyticsTestService(accountStore AccountStore, historyStore MetricHistoryStore, opts ...ChannelAnalyticsServiceOption) *ChannelAnalyticsService {
	// Keep the deterministic test clock as the default while allowing
	// an individual test to override it explicitly.
	opts = append([]ChannelAnalyticsServiceOption{WithAnalyticsClock(fixedAnalyticsClock())}, opts...)
	return NewChannelAnalyticsService(accountStore, historyStore, opts...)
}
