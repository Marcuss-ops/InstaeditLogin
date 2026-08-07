package api

// Unit tests for GET /api/v1/dashboard/analytics (the analytics-only
// dashboard read model). Mirrors the harness style of
// accounts_performance_summary_batch_test.go: each test wires a
// minimal Router directly (mockUserStore + fakeMetricHistoryStore +
// analytics clock) and asserts on the typed response.
//
// Coverage:
//  1. days validation — 1/7/14/28/90 accepted and echoed; invalid
//     values (5, 30, 0, non-numeric, absent) fall back to 28.
//  2. aggregate + per-channel views/revenue assembled from the
//     metric-history batch; revenue pointer semantics preserved.
//  3. top_videos fan-out: first load fetches per account, second
//     load (same user+days) serves from the 5-min cache; a
//     per-channel failure degrades to an empty ranking without
//     failing the request.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// dashboardAnalyticsYouTubeService implements both the Router's
// YouTubeOAuthService field type (via the editor mock's 12 methods)
// and the local dashboardVideoLister capability (ListAccountContent).
// listContentCalls counts every fan-out fetch so tests can assert the
// cache serves a repeat request without another YouTube call.
type dashboardAnalyticsYouTubeService struct {
	mockYouTubeOAuthServiceForEditor
	// listContentCalls counts every fan-out fetch so tests can assert
	// the cache serves a repeat request without another YouTube call.
	// Atomic: the fan-out runs one goroutine per account, so the
	// counter is incremented concurrently.
	listContentCalls atomic.Int32
	listContentFn    func(ctx context.Context, accessToken, platformUserID, cursor string, limit int, privacy string) (*models.AccountContentPage, error)
}

func (m *dashboardAnalyticsYouTubeService) ListAccountContent(ctx context.Context, accessToken, platformUserID, cursor string, limit int, privacy string) (*models.AccountContentPage, error) {
	m.listContentCalls.Add(1)
	if m.listContentFn != nil {
		return m.listContentFn(ctx, accessToken, platformUserID, cursor, limit, privacy)
	}
	return &models.AccountContentPage{Items: []models.AccountContentItem{}}, nil
}

var _ YouTubeOAuthService = (*dashboardAnalyticsYouTubeService)(nil)
var _ dashboardVideoLister = (*dashboardAnalyticsYouTubeService)(nil)

// newDashboardAnalyticsRouter builds a Router with only the fields the
// dashboard handler reads. The fixed clock anchors the window
// (to = 2026-07-30T12:00Z), matching the summary batch test style.
func newDashboardAnalyticsRouter(
	t *testing.T,
	accounts []*models.PlatformAccount,
	store *fakeMetricHistoryStore,
	svc *dashboardAnalyticsYouTubeService,
) *Router {
	t.Helper()
	return &Router{
		userRepo: &mockUserStore{
			listFilteredYouTubeAccountsFn: func(userID int64, _ *int64, _, _, _ string) ([]*models.PlatformAccount, error) {
				if userID != 42 {
					t.Fatalf("list accounts called with userID=%d, want 42", userID)
				}
				return accounts, nil
			},
		},
		metricHistoryStore: store,
		analyticsClock:     analytics.NewFixedClock(time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)),
		youTubeSvc:         svc,
		vault:              &fakeVault{},
	}
}

// doDashboardRequest performs one GET /api/v1/dashboard/analytics
// with the given days query ("" = no param) for user 42.
func doDashboardRequest(t *testing.T, r *Router, daysQuery string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/dashboard/analytics"
	if daysQuery != "" {
		path += "?days=" + daysQuery
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(42, 7, 1)))
	w := httptest.NewRecorder()
	r.handleGetDashboardAnalytics(w, req)
	return w
}

// decodeDashboardResponse decodes the wire payload into the internal
// response struct (shared with the handler's own marshal path).
func decodeDashboardResponse(t *testing.T, w *httptest.ResponseRecorder) dashboardAnalyticsResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp dashboardAnalyticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode dashboard response: %v", err)
	}
	return resp
}

func TestHandleGetDashboardAnalytics_DaysValidation(t *testing.T) {
	store := &fakeMetricHistoryStore{
		getBatchFn: func(ids []int64, _, _ time.Time) (map[int64][]repository.AccountMetricPoint, error) {
			out := make(map[int64][]repository.AccountMetricPoint, len(ids))
			for _, id := range ids {
				out[id] = []repository.AccountMetricPoint{{Date: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Views: 10}}
			}
			return out, nil
		},
	}
	svc := &dashboardAnalyticsYouTubeService{}

	cases := []struct {
		name  string
		query string
		want  int
	}{
		{name: "days=1", query: "1", want: 1},
		{name: "days=7", query: "7", want: 7},
		{name: "days=14", query: "14", want: 14},
		{name: "days=28", query: "28", want: 28},
		{name: "days=90", query: "90", want: 90},
		// 30 is a summary-period (legacy default) but NOT a dashboard
		// period: the dashboard advertises 1/7/14/28/90 only.
		{name: "days=30 rejected", query: "30", want: 28},
		{name: "days=5 rejected", query: "5", want: 28},
		{name: "days=0 rejected", query: "0", want: 28},
		{name: "non-numeric rejected", query: "abc", want: 28},
		{name: "absent defaults to 28", query: "", want: 28},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newDashboardAnalyticsRouter(t, nil, store, svc)
			resp := decodeDashboardResponse(t, doDashboardRequest(t, r, tc.query))
			if resp.PeriodDays != tc.want {
				t.Fatalf("period_days = %d, want %d", resp.PeriodDays, tc.want)
			}
		})
	}
}

func TestHandleGetDashboardAnalytics_AggregatesViewsRevenue(t *testing.T) {
	revOne := int64(5000)
	store := &fakeMetricHistoryStore{
		getBatchFn: func(ids []int64, _, _ time.Time) (map[int64][]repository.AccountMetricPoint, error) {
			return map[int64][]repository.AccountMetricPoint{
				// Two points so ViewsGrowth/RevenueGrowth are computed.
				11: {
					{Date: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), Views: 800, Videos: 8, RevenueCents: &revOne},
					{Date: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Views: 1000, Videos: 10, RevenueCents: &revOne},
				},
				// No revenue: row must render RevenueCents=nil and the
				// aggregate revenue must still reflect channel 11 only.
				22: {
					{Date: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), Views: 200, Videos: 2},
					{Date: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC), Views: 500, Videos: 5},
				},
			}, nil
		},
	}
	svc := &dashboardAnalyticsYouTubeService{}
	accounts := []*models.PlatformAccount{
		{ID: 11, UserID: 42, Platform: models.PlatformYouTube, Username: "channel-one", PlatformUserID: "UC1"},
		{ID: 22, UserID: 42, Platform: models.PlatformYouTube, Username: "channel-two", PlatformUserID: "UC2"},
	}
	r := newDashboardAnalyticsRouter(t, accounts, store, svc)
	resp := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))

	if resp.Aggregates.Channels != 2 {
		t.Fatalf("aggregates.channels = %d, want 2", resp.Aggregates.Channels)
	}
	if resp.Aggregates.Views != 1500 {
		t.Fatalf("aggregates.views = %d, want 1500", resp.Aggregates.Views)
	}
	if resp.Aggregates.Videos != 15 {
		t.Fatalf("aggregates.videos = %d, want 15", resp.Aggregates.Videos)
	}
	if resp.Aggregates.RevenueCents == nil || *resp.Aggregates.RevenueCents != 5000 {
		t.Fatalf("aggregates.revenue_cents = %v, want 5000 (channel 11 only)", resp.Aggregates.RevenueCents)
	}

	if len(resp.Channels) != 2 {
		t.Fatalf("channels rows = %d, want 2", len(resp.Channels))
	}
	byID := make(map[int64]dashboardChannelRow, len(resp.Channels))
	for _, row := range resp.Channels {
		byID[row.ID] = row
	}
	one, ok := byID[11]
	if !ok {
		t.Fatalf("missing channel row for id=11: %+v", resp.Channels)
	}
	if one.Views != 1000 {
		t.Fatalf("channel 11 views = %d, want 1000 (latest point)", one.Views)
	}
	if one.RevenueCents == nil || *one.RevenueCents != 5000 {
		t.Fatalf("channel 11 revenue_cents = %v, want 5000", one.RevenueCents)
	}
	if one.ViewsGrowth.Absolute != 200 {
		t.Fatalf("channel 11 views growth = %+v, want absolute 200", one.ViewsGrowth)
	}
	two, ok := byID[22]
	if !ok {
		t.Fatalf("missing channel row for id=22: %+v", resp.Channels)
	}
	if two.RevenueCents != nil {
		t.Fatalf("channel 22 revenue_cents = %v, want nil (no monetization data)", *two.RevenueCents)
	}
	if two.RevenueGrowth != nil {
		t.Fatalf("channel 22 revenue_growth = %+v, want nil", *two.RevenueGrowth)
	}
	if two.ViewsGrowth.Absolute != 300 {
		t.Fatalf("channel 22 views growth = %+v, want absolute 300", two.ViewsGrowth)
	}
}

func TestHandleGetDashboardAnalytics_TopVideosFanOutAndCache(t *testing.T) {
	inWindow := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)   // inside days=28 window (from 2026-07-03)
	outOfWindow := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) // before days=28 window

	svc := &dashboardAnalyticsYouTubeService{
		listContentFn: func(ctx context.Context, accessToken, platformUserID, cursor string, limit int, privacy string) (*models.AccountContentPage, error) {
			if limit != dashboardTopVideosPerAccount {
				t.Errorf("ListAccountContent limit = %d, want %d", limit, dashboardTopVideosPerAccount)
			}
			switch platformUserID {
			case "UC1":
				return &models.AccountContentPage{Items: []models.AccountContentItem{
					{ExternalID: "v1", Title: "Top video", PublicURL: "https://www.youtube.com/watch?v=v1", PublishedAt: &inWindow,
						Metrics: []models.AccountMetric{{Key: "views", Value: 900}}},
					// Out-of-window item must be filtered from the ranking.
					{ExternalID: "vOld", Title: "Old video", PublishedAt: &outOfWindow,
						Metrics: []models.AccountMetric{{Key: "views", Value: 99999}}},
				}}, nil
			case "UC2":
				return &models.AccountContentPage{Items: []models.AccountContentItem{
					{ExternalID: "v2", Title: "Second video", PublishedAt: &inWindow,
						Metrics: []models.AccountMetric{{Key: "views", Value: 500}}},
				}}, nil
			default:
				return &models.AccountContentPage{}, nil
			}
		},
	}
	accounts := []*models.PlatformAccount{
		{ID: 11, UserID: 42, Platform: models.PlatformYouTube, Username: "channel-one", PlatformUserID: "UC1"},
		{ID: 22, UserID: 42, Platform: models.PlatformYouTube, Username: "channel-two", PlatformUserID: "UC2"},
	}
	r := newDashboardAnalyticsRouter(t, accounts, &fakeMetricHistoryStore{}, svc)

	// First load: fan-out fetches both accounts (cache miss).
	first := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))
	if got := svc.listContentCalls.Load(); got != 2 {
		t.Fatalf("list calls after first load = %d, want 2 (one per account)", got)
	}
	if len(first.TopVideos) != 2 {
		t.Fatalf("top_videos = %d, want 2 (out-of-window item filtered)", len(first.TopVideos))
	}
	// Sorted by views descending: v1 (900) before v2 (500).
	if first.TopVideos[0].VideoID != "v1" || first.TopVideos[0].Views != 900 {
		t.Fatalf("top_videos[0] = %+v, want v1/900", first.TopVideos[0])
	}
	if first.TopVideos[1].VideoID != "v2" {
		t.Fatalf("top_videos[1] = %+v, want v2", first.TopVideos[1])
	}
	if first.TopVideos[0].ChannelName != "channel-one" || first.TopVideos[0].YouTubeURL == "" {
		t.Fatalf("top_videos[0] enrichment = %+v, want channel-one + youtube url", first.TopVideos[0])
	}

	// Second load (same user + days): served from the 5-min cache.
	second := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))
	if got := svc.listContentCalls.Load(); got != 2 {
		t.Fatalf("list calls after cached load = %d, want still 2 (cache hit)", got)
	}
	if len(second.TopVideos) != 2 || second.TopVideos[0].VideoID != "v1" {
		t.Fatalf("cached top_videos = %+v, want same ranking", second.TopVideos)
	}

	// Different days → different cache key → fan-out runs again.
	decodeDashboardResponse(t, doDashboardRequest(t, r, "7"))
	if got := svc.listContentCalls.Load(); got != 4 {
		t.Fatalf("list calls after days=7 load = %d, want 4 (cache miss on new key)", got)
	}
}

func TestHandleGetDashboardAnalytics_TopVideosDegradation(t *testing.T) {
	accounts := []*models.PlatformAccount{
		{ID: 11, UserID: 42, Platform: models.PlatformYouTube, Username: "channel-one", PlatformUserID: "UC1"},
		{ID: 22, UserID: 42, Platform: models.PlatformYouTube, Username: "channel-two", PlatformUserID: "UC2"},
	}

	t.Run("all channels fail -> empty ranking, request still 200", func(t *testing.T) {
		svc := &dashboardAnalyticsYouTubeService{
			listContentFn: func(context.Context, string, string, string, int, string) (*models.AccountContentPage, error) {
				return nil, errors.New("youtube quota exceeded")
			},
		}
		r := newDashboardAnalyticsRouter(t, accounts, &fakeMetricHistoryStore{}, svc)
		resp := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))
		if len(resp.TopVideos) != 0 {
			t.Fatalf("top_videos = %d, want 0 (all channels failed)", len(resp.TopVideos))
		}
		if resp.Aggregates.Channels != 2 {
			t.Fatalf("aggregates.channels = %d, want 2 (aggregates survive fan-out failure)", resp.Aggregates.Channels)
		}
	})

	t.Run("one channel fails -> other channel's videos survive", func(t *testing.T) {
		inWindow := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
		svc := &dashboardAnalyticsYouTubeService{
			listContentFn: func(ctx context.Context, accessToken, platformUserID, cursor string, limit int, privacy string) (*models.AccountContentPage, error) {
				if platformUserID == "UC1" {
					return nil, errors.New("per-channel failure")
				}
				return &models.AccountContentPage{Items: []models.AccountContentItem{
					{ExternalID: "v2", Title: "Survivor", PublishedAt: &inWindow,
						Metrics: []models.AccountMetric{{Key: "views", Value: 700}}},
				}}, nil
			},
		}
		r := newDashboardAnalyticsRouter(t, accounts, &fakeMetricHistoryStore{}, svc)
		resp := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))
		if len(resp.TopVideos) != 1 || resp.TopVideos[0].VideoID != "v2" || resp.TopVideos[0].ChannelName != "channel-two" {
			t.Fatalf("top_videos = %+v, want only channel-two v2", resp.TopVideos)
		}
	})

	t.Run("lister capability absent -> empty ranking, request still 200", func(t *testing.T) {
		// A service that satisfies YouTubeOAuthService but NOT the
		// dashboardVideoLister capability: the fan-out short-circuits
		// before any YouTube call.
		// The helper's param type requires the wrapper, so install a bare
		// editor mock afterwards: it satisfies YouTubeOAuthService but NOT
		// dashboardVideoLister, proving the capability type-assertion is
		// what gates the fan-out (no YouTube call, empty ranking).
		svc := &mockYouTubeOAuthServiceForEditor{}
		r := newDashboardAnalyticsRouter(t, accounts, &fakeMetricHistoryStore{}, &dashboardAnalyticsYouTubeService{})
		r.youTubeSvc = svc
		resp := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))
		if len(resp.TopVideos) != 0 {
			t.Fatalf("top_videos = %d, want 0 (capability absent)", len(resp.TopVideos))
		}
		if resp.Aggregates.Channels != 2 {
			t.Fatalf("aggregates.channels = %d, want 2", resp.Aggregates.Channels)
		}
	})

	t.Run("vault absent -> empty ranking, request still 200", func(t *testing.T) {
		svc := &dashboardAnalyticsYouTubeService{
			listContentFn: func(context.Context, string, string, string, int, string) (*models.AccountContentPage, error) {
				t.Fatal("ListAccountContent must not be called when the vault is nil")
				return nil, nil
			},
		}
		r := newDashboardAnalyticsRouter(t, accounts, &fakeMetricHistoryStore{}, svc)
		r.vault = nil
		resp := decodeDashboardResponse(t, doDashboardRequest(t, r, "28"))
		if len(resp.TopVideos) != 0 {
			t.Fatalf("top_videos = %d, want 0 (vault absent)", len(resp.TopVideos))
		}
	})
}

func TestHandleGetDashboardAnalytics_ErrorPaths(t *testing.T) {
	t.Run("metric history store missing -> 501", func(t *testing.T) {
		r := &Router{userRepo: &mockUserStore{}}
		w := doDashboardRequest(t, r, "")
		if w.Code != http.StatusNotImplemented {
			t.Fatalf("want 501, got %d", w.Code)
		}
	})

	t.Run("missing identity -> 401", func(t *testing.T) {
		r := newDashboardAnalyticsRouter(t, nil, &fakeMetricHistoryStore{}, &dashboardAnalyticsYouTubeService{})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard/analytics", nil)
		w := httptest.NewRecorder()
		r.handleGetDashboardAnalytics(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", w.Code)
		}
	})

	t.Run("account listing failure -> 500", func(t *testing.T) {
		r := &Router{
			userRepo: &mockUserStore{
				listFilteredYouTubeAccountsFn: func(int64, *int64, string, string, string) ([]*models.PlatformAccount, error) {
					return nil, errors.New("db down")
				},
			},
			metricHistoryStore: &fakeMetricHistoryStore{},
			analyticsClock:     analytics.NewFixedClock(time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)),
		}
		w := doDashboardRequest(t, r, "28")
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("want 500, got %d", w.Code)
		}
	})
}
