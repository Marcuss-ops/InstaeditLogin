package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type legacyMetricHistoryStore struct {
	calls int
}

func (s *legacyMetricHistoryStore) UpsertDaily(int64, time.Time, repository.AccountMetricPoint) error {
	return nil
}
func (s *legacyMetricHistoryStore) UpsertMonetary(int64, time.Time, repository.AccountMetricPoint) error {
	return nil
}
func (s *legacyMetricHistoryStore) GetHistory(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
	s.calls++
	return nil, nil
}

func TestHandleGetAccountsPerformanceSummary_LegacyStoreFallback(t *testing.T) {
	store := &legacyMetricHistoryStore{}
	r := &Router{
		userRepo: &mockUserStore{
			listFilteredYouTubeAccountsFn: func(int64, *int64, string, string, string) ([]*models.PlatformAccount, error) {
				return []*models.PlatformAccount{
					{ID: 11, UserID: 42, Platform: "youtube", Username: "one"},
					{ID: 22, UserID: 42, Platform: "youtube", Username: "two"},
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
	if store.calls != 2 {
		t.Fatalf("legacy GetHistory calls = %d, want one per account", store.calls)
	}
}
