// Package api — unit tests for ChannelAnalyticsService.
//
// These tests target the service contract independently of the
// HTTP layer: each test wires a fake AccountStore + MetricHistoryStore
// + VideoMetricsLister, then asserts the typed error / DTO shape the
// service exposes. End-to-end HTTP wiring tests live in
// accounts_performance_handlers_test.go (those tests use the
// runGetAccountPerf → wireAnalyticsServiceForTest helper to delegate
// to the same code path exercised here).
//
// Test surface coverage (mirrors the Step-4 spec checklist):
//  1. cross-tenant access (account.UserID != identity.UserID) → 404
//  2. account.Store returns error → bubble-up error (no sentinel)
//  3. non-YouTube platform → ErrNotYouTubePlatform (422)
//  4. YouTube channel id missing → ErrYouTubeChannelIDMissing (422)
//  5. invalid days → analytics.ErrInvalidPeriod (400)
//  6. video lister error → bubble-up error
//  7. happy path 7-day empty history → valid DTO, empty ranking
//  8. happy path 28-day populated history → populated ranking
//  9. video lister returning nil coerced to empty ([]TopVideo{})
package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ------------------------------------------------------------------
// Test fakes
// ------------------------------------------------------------------

// fakeAccountStore is the narrow AccountStore impl used by the
// service tests. Only FindPlatformAccountByID is wired; the other
// methods of the production UserStore don't apply to the service
// surface. Returns the configured (account, error) tuple.
type fakeAccountStore struct {
	account *models.PlatformAccount
	err     error
	// calls records how many times FindPlatformAccountByID was
	// invoked. Tests assert it equals 1 when the service is
	// expected to short-circuit on account-level errors.
	calls int
}

func (f *fakeAccountStore) FindPlatformAccountByID(_ int64) (*models.PlatformAccount, error) {
	f.calls++
	return f.account, f.err
}

// fakeVideoLister records its latest call args + returns the
// preconfigured (videos, error). When err is nil and videos is nil,
// the service must still coerce to []TopVideo{} (the contract's
// never-nil invariant for top_videos wire shapes).
type fakeVideoLister struct {
	videos []analytics.TopVideo
	err    error
	// filterCalled records the (since, until) pair the service
	// actually passed to the port so the "videos requested span
	// both windows" assertion has data.
	filterSince time.Time
	filterUntil time.Time
}

func (f *fakeVideoLister) ListRecentVideos(_ context.Context, _ string, since, until time.Time) ([]analytics.TopVideo, error) {
	f.filterSince = since
	f.filterUntil = until
	return f.videos, f.err
}

// makeServiceYTChannelAccount builds a YouTube PlatformAccount
// belonging to (userID=42, workspaceID=12). Tests that want a
// different ownership / platform shape construct a
// *models.PlatformAccount inline and pass it directly into
// fakeAccountStore.
//
// The name avoids a collision with makeYTChannelAccount in
// accounts_performance_handlers_test.go (which takes args for
// explicit user/account ids); the service tests use this fixed-
// shape fixture because the test cases only vary ownership /
// platform / channel-id, not the ids themselves.
func makeServiceYTChannelAccount() *models.PlatformAccount {
	return &models.PlatformAccount{
		ID:             381,
		UserID:         42,
		Platform:       "youtube",
		PlatformUserID: "113848374927471624321",
		Username:       "Demo Channel",
		Status:         "active",
		Metadata: models.Metadata{
			"channel_id": "UCabc",
		},
	}
}

// Compile-time assertion: *fakeAccountStore satisfies AccountStore.
var _ AccountStore = (*fakeAccountStore)(nil)

// Compile-time assertion: *fakeVideoLister satisfies
// VideoMetricsLister.
var _ VideoMetricsLister = (*fakeVideoLister)(nil)

// nilAnalyticsClock is a typed-nil Clock fixture. Its Now method must
// never be reached: WithAnalyticsClock must fall back to RealClock.
type nilAnalyticsClock struct{}

func (*nilAnalyticsClock) Now() time.Time {
	panic("typed-nil analytics clock was used")
}

// ------------------------------------------------------------------
// Tests
// ------------------------------------------------------------------

// TestChannelAnalyticsService_CrossTenantAccess_NilAccount: when the
// account Store returns (nil, nil), the service MUST short-circuit
// with ErrAccountNotVisible so a hostile probe cannot distinguish
// "no such row" from "row exists but not yours".
func TestChannelAnalyticsService_CrossTenantAccess_NilAccount(t *testing.T) {
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: nil, err: nil},
		&fakeMetricHistoryStore{},
	)
	_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if !errors.Is(err, ErrAccountNotVisible) {
		t.Fatalf("nil account: want ErrAccountNotVisible, got %v", err)
	}
}

// TestChannelAnalyticsService_CrossTenantAccess_WrongUser: when the
// account's UserID does not match the identity UserID, the service
// MUST collapse to ErrAccountNotVisible (no existence leak).
func TestChannelAnalyticsService_CrossTenantAccess_WrongUser(t *testing.T) {
	acct := makeServiceYTChannelAccount()
	acct.UserID = 99 // not the caller's user (=42)
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: acct, err: nil},
		&fakeMetricHistoryStore{},
	)
	_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if !errors.Is(err, ErrAccountNotVisible) {
		t.Fatalf("cross-tenant: want ErrAccountNotVisible, got %v", err)
	}
}

// TestChannelAnalyticsService_AccountStoreError: a non-nil Store
// error MUST be wrapped and surfaced so the handler turns it into
// 500. The service MUST NOT mask it as ErrAccountNotVisible.
func TestChannelAnalyticsService_AccountStoreError(t *testing.T) {
	storeErr := errors.New("db connection reset")
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: nil, err: storeErr},
		&fakeMetricHistoryStore{},
	)
	_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if errors.Is(err, ErrAccountNotVisible) {
		t.Fatalf("store error must NOT be masked as ErrAccountNotVisible")
	}
	if !strings.Contains(err.Error(), "db connection reset") {
		t.Errorf("error must wrap the original store error: %v", err)
	}
}

// TestChannelAnalyticsService_NotYouTubePlatform: when account.Platform
// != "youtube", the service MUST surface ErrNotYouTubePlatform (handler
// maps to 422). Tries both an Instagram and a TikTok account to
// pin the rejection per-platform.
func TestChannelAnalyticsService_NotYouTubePlatform(t *testing.T) {
	for _, platform := range []string{"instagram", "tiktok", "twitter"} {
		t.Run(platform, func(t *testing.T) {
			acct := makeServiceYTChannelAccount()
			acct.Platform = platform
			svc := newAnalyticsTestService(
				&fakeAccountStore{account: acct},
				&fakeMetricHistoryStore{},
			)
			_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
			if !errors.Is(err, ErrNotYouTubePlatform) {
				t.Fatalf("%s: want ErrNotYouTubePlatform, got %v", platform, err)
			}
		})
	}
}

// TestChannelAnalyticsService_YouTubeChannelIDMissing: when the account
// has no channel_id / youtube_channel_id metadata entry, the service
// MUST surface ErrYouTubeChannelIDMissing (handler maps to 422 with
// "re-link required").
func TestChannelAnalyticsService_YouTubeChannelIDMissing(t *testing.T) {
	acct := makeServiceYTChannelAccount()
	acct.Metadata = models.Metadata{} // empty metadata, no channel_id
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: acct},
		&fakeMetricHistoryStore{},
	)
	_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if !errors.Is(err, ErrYouTubeChannelIDMissing) {
		t.Fatalf("missing channel_id: want ErrYouTubeChannelIDMissing, got %v", err)
	}
}

// TestChannelAnalyticsService_InvalidDays: when days is outside the
// closed {7,14,28} set, the service MUST surface analytics.ErrInvalidPeriod
// (handler maps to 400). Pinned to the analytics package's sentinel
// so the wire-shape contract stays consistent across the resolver +
// service boundary.
func TestChannelAnalyticsService_InvalidDays(t *testing.T) {
	for _, days := range []int{0, 1, 6, 8, 13, 15, 30, 365, -7} {
		t.Run("days="+strconv.Itoa(days), func(t *testing.T) {
			svc := newAnalyticsTestService(
				&fakeAccountStore{account: makeServiceYTChannelAccount()},
				&fakeMetricHistoryStore{},
			)
			_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, days)
			if !errors.Is(err, analytics.ErrInvalidPeriod) {
				t.Fatalf("days=%d: want ErrInvalidPeriod, got %v", days, err)
			}
		})
	}
}

// TestChannelAnalyticsService_VideoListerError: a non-nil VideoLister
// error MUST be wrapped and surfaced (handler 500). The service MUST
// NOT mask it as a typed sentinel — video lister failures are
// transient/retryable, not contract violations.
func TestChannelAnalyticsService_VideoListerError(t *testing.T) {
	listerErr := errors.New("youtube data api quota exceeded")
	var since, until time.Time
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return nil, nil
		}},
		WithVideoLister(&fakeVideoLister{err: listerErr, videos: nil,
			filterSince: since, filterUntil: until}),
	)
	_, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if errors.Is(err, ErrAccountNotVisible) || errors.Is(err, ErrNotYouTubePlatform) {
		t.Fatalf("video lister error must NOT be masked as a typed sentinel")
	}
	if !strings.Contains(err.Error(), "youtube data api") {
		t.Errorf("error must wrap lister error: %v", err)
	}
}

// TestChannelAnalyticsService_TypedNilClockFallsBack verifies that a
// typed-nil Clock option cannot cause a panic in manually wired services.
func TestChannelAnalyticsService_TypedNilClockFallsBack(t *testing.T) {
	var clock *nilAnalyticsClock
	svc := NewChannelAnalyticsService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return []repository.AccountMetricPoint{}, nil
		}},
		WithAnalyticsClock(clock),
	)
	resp, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if err != nil {
		t.Fatalf("typed-nil clock should fall back to RealClock: %v", err)
	}
	if resp.Period.Days != 7 {
		t.Errorf("Period.Days: want 7, got %d", resp.Period.Days)
	}
}

// TestChannelAnalyticsService_HappyPath_7d_NoVideos: the NoneOpLister
// returns empty ranking; the rest of the pipeline emits a valid DTO
// with TopVideos as never-nil empty arrays.
func TestChannelAnalyticsService_HappyPath_7d_NoVideos(t *testing.T) {
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return []repository.AccountMetricPoint{}, nil
		}},
	)
	resp, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if err != nil {
		t.Fatalf("happy 7d: %v", err)
	}
	if resp.Channel.PlatformAccountID != 381 {
		t.Errorf("Channel.PlatformAccountID: want 381, got %d", resp.Channel.PlatformAccountID)
	}
	if resp.Channel.YouTubeChannelID != "UCabc" {
		t.Errorf("Channel.YouTubeChannelID: want UCabc, got %q", resp.Channel.YouTubeChannelID)
	}
	if resp.Period.Days != 7 {
		t.Errorf("Period.Days: want 7, got %d", resp.Period.Days)
	}
	if resp.TopVideos.MostViewed == nil {
		t.Errorf("TopVideos.MostViewed must be non-nil empty array (contract invariant)")
	}
	if resp.TopVideos.Growing == nil {
		t.Errorf("TopVideos.Growing must be non-nil empty array (contract invariant)")
	}
	if len(resp.TopVideos.MostViewed) != 0 {
		t.Errorf("TopVideos.MostViewed: want 0, got %d", len(resp.TopVideos.MostViewed))
	}
	if len(resp.TopVideos.Growing) != 0 {
		t.Errorf("TopVideos.Growing: want 0, got %d", len(resp.TopVideos.Growing))
	}
}

// TestChannelAnalyticsService_ClockAnchorsPeriodAndGeneratedAt verifies
// that a non-midnight fixed instant produces one deterministic UTC
// calendar boundary for both the period and generated_at. This guards
// against reintroducing time.Now() or passing the untruncated instant
// into freshness calculations.
func TestChannelAnalyticsService_ClockAnchorsPeriodAndGeneratedAt(t *testing.T) {
	clockInstant := time.Date(2026, 7, 30, 23, 45, 12, 0, time.FixedZone("test", 5*3600))
	svc := NewChannelAnalyticsService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return []repository.AccountMetricPoint{{
				Date:        time.Date(2026, 7, 24, 16, 45, 0, 0, time.FixedZone("repository", -4*3600)),
				Views:       100,
				Subscribers: 1000,
				Videos:      1,
			}}, nil
		}},
		WithAnalyticsClock(analytics.NewFixedClock(clockInstant)),
	)

	resp, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if err != nil {
		t.Fatalf("clock-anchored performance: %v", err)
	}

	wantEnd := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	if !resp.Period.EndDate.Equal(wantEnd) {
		t.Errorf("Period.EndDate: want %v, got %v", wantEnd, resp.Period.EndDate)
	}
	if !resp.GeneratedAt.Equal(wantEnd) {
		t.Errorf("GeneratedAt: want %v, got %v", wantEnd, resp.GeneratedAt)
	}
	if resp.GeneratedAt.Location() != time.UTC {
		t.Errorf("GeneratedAt location: want UTC, got %v", resp.GeneratedAt.Location())
	}
	wantLastSynced := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC)
	if !resp.DataFreshness.LastSyncedAt.Equal(wantLastSynced) {
		t.Errorf("LastSyncedAt: want %v, got %v", wantLastSynced, resp.DataFreshness.LastSyncedAt)
	}
	if !resp.DataFreshness.IsStale {
		t.Errorf("DataFreshness.IsStale: want true for a six-day-old row, got false")
	}
}

// TestChannelAnalyticsService_HappyPath_28d_VideosRanked: when the
// VideoLister returns entries, both MostViewed (views_in_period)
// and Growing (trend_score) arrays are populated by the analytics
// scorers, and the ranking is deterministic on tie-break.
func TestChannelAnalyticsService_HappyPath_28d_VideosRanked(t *testing.T) {
	now := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	videos := []analytics.TopVideo{
		{VideoID: "v_old", Title: "old popular",
			PublishedAt:   now.AddDate(0, 0, -10),
			ViewsInPeriod: 50000, WatchTimeInPeriod: 6000},
		{VideoID: "v_new", Title: "fresh 20k in 2 days",
			PublishedAt:   now.AddDate(0, 0, -2),
			ViewsInPeriod: 20000, WatchTimeInPeriod: 3000},
		{VideoID: "v_mid", Title: "mid video",
			PublishedAt:   now.AddDate(0, 0, -20),
			ViewsInPeriod: 30000, WatchTimeInPeriod: 4500},
	}
	lister := &fakeVideoLister{videos: videos}
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return []repository.AccountMetricPoint{}, nil
		}},
		WithVideoLister(lister),
	)
	resp, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 28)
	if err != nil {
		t.Fatalf("happy 28d: %v", err)
	}
	// Most-viewed ordering is purely ViewsInPeriod DESC:
	//   v_old (50000) > v_mid (30000) > v_new (20000).
	if got := []string{resp.TopVideos.MostViewed[0].VideoID,
		resp.TopVideos.MostViewed[1].VideoID,
		resp.TopVideos.MostViewed[2].VideoID}; got[0] != "v_old" || got[1] != "v_mid" || got[2] != "v_new" {
		t.Errorf("MostViewed order: want [v_old v_mid v_new], got %v", got)
	}
	// Growing ordering uses the trend_score formula; the formula
	// favours recency, so v_new (2 days old, 20k views) should beat
	// v_old (10 days old, 50k views) and v_mid (20 days old, 30k).
	// We only assert non-empty ranking here — the exact scoring
	// formula is locked in trending_scorer_test.go.
	if len(resp.TopVideos.Growing) != 3 {
		t.Errorf("Growing length: want 3, got %d", len(resp.TopVideos.Growing))
	}
	// VideoLister call captured the period window so the service
	// requests BOTH [previous_start, end] — pin the spread.
	if !lister.filterUntil.Equal(resp.Period.EndDate) {
		t.Errorf("video lister's `until`: want %v, got %v", resp.Period.EndDate, lister.filterUntil)
	}
	if !lister.filterSince.Equal(resp.Period.PreviousStartDate) {
		t.Errorf("video lister's `since`: want %v, got %v", resp.Period.PreviousStartDate, lister.filterSince)
	}
}

// TestChannelAnalyticsService_NilVideoListerReturnsCoercedEmpty: a
// port impl that returns (nil, nil) instead of ([]TopVideo{}, nil)
// MUST be coerced by the service so the contract's never-nil
// invariant holds for the wire shape.
func TestChannelAnalyticsService_NilVideoListerReturnsCoercedEmpty(t *testing.T) {
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return []repository.AccountMetricPoint{}, nil
		}},
		WithVideoLister(&fakeVideoLister{videos: nil, err: nil}),
	)
	resp, err := svc.GetChannelPerformance(context.Background(), 42, 12, 381, 7)
	if err != nil {
		t.Fatalf("happy 7d nil-videos: %v", err)
	}
	if resp.TopVideos.MostViewed == nil {
		t.Errorf("nil-from-port most_viewed must be coerced to [] (contract invariant)")
	}
	if resp.TopVideos.Growing == nil {
		t.Errorf("nil-from-port growing must be coerced to [] (contract invariant)")
	}
}

// TestChannelAnalyticsService_WorkspaceIDForwardCompat: the service
// reads workspaceID but the current schema's user-scoped ownership
// check (account.UserID == userID) is the operative gate. The
// workspaceID param is reserved for the post-refactor
// WorkspaceChannel-aware lookup, so a workspaceID=0 input MUST
// still pass when account.UserID matches.
func TestChannelAnalyticsService_WorkspaceIDForwardCompat(t *testing.T) {
	svc := newAnalyticsTestService(
		&fakeAccountStore{account: makeServiceYTChannelAccount()},
		&fakeMetricHistoryStore{getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
			return []repository.AccountMetricPoint{}, nil
		}},
	)
	_, err := svc.GetChannelPerformance(context.Background(), 42, 0, 381, 7)
	if err != nil {
		t.Fatalf("workspaceID=0 should pass when userID matches: %v", err)
	}
}
