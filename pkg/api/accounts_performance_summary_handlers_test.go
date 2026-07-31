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

func TestHandleGetAccountsPerformanceSummary_UsesInjectedClock(t *testing.T) {
	fixedNow := time.Date(2026, 7, 30, 23, 45, 0, 0, time.FixedZone("test", 5*3600))
	var gotFrom, gotTo time.Time
	store := &fakeMetricHistoryStore{
		getFn: func(_ int64, from, to time.Time) ([]repository.AccountMetricPoint, error) {
			gotFrom, gotTo = from, to
			return []repository.AccountMetricPoint{}, nil
		},
	}
	r := &Router{
		userRepo: &mockUserStore{
			listFilteredYouTubeAccountsFn: func(int64, *int64, string, string, string) ([]*models.PlatformAccount, error) {
				return []*models.PlatformAccount{{
					ID: 381, UserID: 42, Platform: "youtube", Username: "fixed-clock-channel",
				}}, nil
			},
		},
		metricHistoryStore: store,
		analyticsClock:     analytics.NewFixedClock(fixedNow),
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(42, 7, 1)))
	w := httptest.NewRecorder()
	r.handleGetAccountsPerformanceSummary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("summary: want 200, got %d (%s)", w.Code, w.Body.String())
	}

	wantTo := fixedNow.UTC()
	wantFrom := wantTo.AddDate(0, 0, -29)
	if !gotTo.Equal(wantTo) {
		t.Fatalf("summary to: want %v, got %v", wantTo, gotTo)
	}
	if !gotFrom.Equal(wantFrom) {
		t.Fatalf("summary from: want %v, got %v", wantFrom, gotFrom)
	}
}

func TestParsePerformanceSummaryFilters_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID != nil {
		t.Fatalf("expected nil workspaceID, got %v", *filters.workspaceID)
	}
	if filters.group != "" {
		t.Fatalf("expected empty group, got %q", filters.group)
	}
	if filters.language != "" {
		t.Fatalf("expected empty language, got %q", filters.language)
	}
	if filters.manager != "" {
		t.Fatalf("expected empty manager, got %q", filters.manager)
	}
}

func TestParsePerformanceSummaryFilters_AllFilters(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?workspace=7&group=Marketing&language=en&manager=Alice", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID == nil {
		t.Fatal("expected workspaceID, got nil")
	}
	if *filters.workspaceID != 7 {
		t.Fatalf("expected workspaceID 7, got %d", *filters.workspaceID)
	}
	if filters.group != "Marketing" {
		t.Fatalf("expected group Marketing, got %q", filters.group)
	}
	if filters.language != "en" {
		t.Fatalf("expected language en, got %q", filters.language)
	}
	if filters.manager != "Alice" {
		t.Fatalf("expected manager Alice, got %q", filters.manager)
	}
}

func TestParsePerformanceSummaryFilters_InvalidWorkspaceIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?workspace=not-a-number", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID != nil {
		t.Fatalf("expected nil workspaceID for invalid value, got %v", *filters.workspaceID)
	}
}

func TestParsePerformanceSummaryFilters_NonPositiveWorkspaceIgnored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/performance/summary?workspace=-5", nil)
	filters := parsePerformanceSummaryFilters(req)

	if filters.workspaceID != nil {
		t.Fatalf("expected nil workspaceID for non-positive value, got %v", *filters.workspaceID)
	}
}
