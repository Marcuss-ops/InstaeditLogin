package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestHandleGetAccountsPerformanceSummary_UsesBatchHistory(t *testing.T) {
	batchCalls := 0
	individualCalls := 0
	store := &fakeMetricHistoryStore{
		getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
			individualCalls++
			return nil, nil
		},
		getBatchFn: func(ids []int64, _, _ time.Time) (map[int64][]repository.AccountMetricPoint, error) {
			batchCalls++
			if len(ids) != 3 || ids[0] != 11 || ids[1] != 22 || ids[2] != 33 {
				t.Fatalf("batch account ids = %v, want [11 22 33]", ids)
			}
			return map[int64][]repository.AccountMetricPoint{
				11: {{Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Views: 100, Videos: 1}},
				22: {{Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Views: 200, Videos: 2}},
				33: {{Date: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), Views: 300, Videos: 3}},
			}, nil
		},
	}
	r := &Router{
		userRepo: &mockUserStore{
			listFilteredYouTubeAccountsFn: func(int64, *int64, string, string, string) ([]*models.PlatformAccount, error) {
				return []*models.PlatformAccount{
					{ID: 11, UserID: 42, Platform: "youtube", Username: "one"},
					{ID: 22, UserID: 42, Platform: "youtube", Username: "two"},
					{ID: 33, UserID: 42, Platform: "youtube", Username: "three"},
				}, nil
			},
		},
		metricHistoryStore: store,
		analyticsClock:     analytics.NewFixedClock(time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?days=30", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(42, 7, 1)))
	w := httptest.NewRecorder()
	r.handleGetAccountsPerformanceSummary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("summary: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if batchCalls != 1 {
		t.Fatalf("batch history calls = %d, want 1", batchCalls)
	}
	if individualCalls != 0 {
		t.Fatalf("individual history calls = %d, want 0", individualCalls)
	}

	var response accountsPerformanceSummaryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode summary response: %v", err)
	}
	if response.Aggregates.Channels != 3 || response.Aggregates.Views != 600 || response.Aggregates.Videos != 6 {
		t.Fatalf("aggregate KPIs = %+v, want channels=3 views=600 videos=6", response.Aggregates)
	}
	if len(response.Channels) != 3 || response.Channels[0].Metrics.Views != 100 {
		t.Fatalf("channel metrics were not assembled from batch history: %+v", response.Channels)
	}
	if len(response.Rankings.ByViews) != 3 || response.Rankings.ByViews[0].ID != 33 || response.Rankings.ByViews[0].Value != 300 {
		t.Fatalf("view ranking = %+v, want account 33 first with 300", response.Rankings.ByViews)
	}
	if len(response.Trends) != 1 || response.Trends[0].Views != 600 {
		t.Fatalf("trend aggregation = %+v, want only the persisted snapshot day with 600 views", response.Trends)
	}
}

func TestBuildTrendsDoesNotFabricateMissingDays(t *testing.T) {
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	accounts := []*models.PlatformAccount{{ID: 11}}
	histories := map[int64][]repository.AccountMetricPoint{
		11: {
			{Date: from, Views: 100, Videos: 1},
			{Date: to, Views: 150, Videos: 2},
		},
	}

	trends := buildTrends(accounts, histories, from, to)
	if len(trends) != 2 {
		t.Fatalf("trend points = %+v, want only the two persisted days", trends)
	}
	if trends[0].Date != "2026-07-01" || trends[0].Views != 100 {
		t.Fatalf("first trend = %+v, want July 1 snapshot", trends[0])
	}
	if trends[1].Date != "2026-07-03" || trends[1].Views != 150 {
		t.Fatalf("second trend = %+v, want July 3 snapshot", trends[1])
	}
}
