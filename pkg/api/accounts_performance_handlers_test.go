// Package api — handler-level (HTTP) tests for
// handleGetAccountPerformance. Step 6 of the per-channel analytics
// rollout wires every spec'd scenario with a focused in-memory
// fake so neither Postgres nor a YouTube client are needed.
//
// Test architecture (chosen by the design pass):
//   - Use the handler directly: r := &Router{userRepo: …,
//     metricHistoryStore: …}; r.handleGetAccountPerformance(w, req).
//     Skips chi.Mux + middleware wiring entirely so each scenario is
//     isolated to its own URL+identity+mock triple.
//   - Identity injection: httptest.NewRequest + req.WithContext(
//     auth.WithIdentity(ctx, auth.NewUserIdentity(uid, ws, sid))).
//     Mirrors the production JWT middleware that production wiring
//     (cmd/server/main.go → r.protected → r.auth.Middleware) calls.
//     If identity production code drifts, this test will drift too
//     and fail to compile at the auth import boundary.
//   - Path-param injection: req.SetPathValue("id", "381") — the
//     handler's parsePathIDAsInt64 reads via Go 1.22+ req.PathValue,
//     not chi.URLParam, so the chi.RouteCtx shim path isn't needed.
//   - UserStore: reuse mockUserStore from common_test.go (already
//     implements all interface methods; we override findPlatformAccountFn
//     for the cross-tenant / not-found / success cases).
//   - MetricHistoryStore: declared inline (no shared mock exists).
//     Two methods are wired per scenario (GetHistory mocked; the two
//     Upsert* methods short-circuit to nil so the compile-time
//     interface assertion holds even if the handler's writer path is
//     ever instrumented).
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// compitanl test assertion: any drift in MetricHistoryStore fails
// the build rather than silently returning 500 in production.
var _ MetricHistoryStore = (*fakeMetricHistoryStore)(nil)

// fakeMetricHistoryStore is the in-memory MetricHistoryStore used
// exclusively by handleGetAccountPerformance tests. Only GetHistory
// has a configurable hook; the two Upsert methods short-circuit to
// nil to keep the compile-time interface assertion intact.
type fakeMetricHistoryStore struct {
	getFn          func(platformAccountID int64, from, to time.Time) ([]repository.AccountMetricPoint, error)
	getBatchFn     func(platformAccountIDs []int64, from, to time.Time) (map[int64][]repository.AccountMetricPoint, error)
	getCallCount   int
	getBatchCount  int
	lastGetAccount int64
}

// GetHistory implements MetricHistoryStore. Records the call so the
// "local-DB cache only" test can assert a call happened.
func (f *fakeMetricHistoryStore) GetHistory(platformAccountID int64, from, to time.Time) ([]repository.AccountMetricPoint, error) {
	f.getCallCount++
	f.lastGetAccount = platformAccountID
	if f.getFn != nil {
		return f.getFn(platformAccountID, from, to)
	}
	return nil, nil
}

func (f *fakeMetricHistoryStore) GetHistoryBatch(platformAccountIDs []int64, from, to time.Time) (map[int64][]repository.AccountMetricPoint, error) {
	f.getBatchCount++
	if f.getBatchFn != nil {
		return f.getBatchFn(platformAccountIDs, from, to)
	}
	out := make(map[int64][]repository.AccountMetricPoint, len(platformAccountIDs))
	for _, id := range platformAccountIDs {
		history, err := f.GetHistory(id, from, to)
		if err != nil {
			return nil, err
		}
		out[id] = history
	}
	return out, nil
}

func (f *fakeMetricHistoryStore) UpsertDaily(int64, time.Time, repository.AccountMetricPoint) error {
	return nil
}

func (f *fakeMetricHistoryStore) UpsertMonetary(int64, time.Time, repository.AccountMetricPoint) error {
	return nil
}

// newPerfRequest builds a GET /api/v1/accounts/{id}/performance[?days=]
// request with the supplied path-id string + identity stamped into
// the request context (mirrors what the JWT middleware writes in
// production). identity == nil represents "no auth middleware ran".
func newPerfRequest(t *testing.T, pathIDStr, daysQuery string, identity auth.Identity) *http.Request {
	t.Helper()
	u := "/api/v1/accounts/" + pathIDStr + "/performance"
	if daysQuery != "" {
		u += "?days=" + daysQuery
	}
	req := httptest.NewRequest(http.MethodGet, u, nil)
	// parsePathIDAsInt64 reads via Go 1.22+ req.PathValue("id");
	// SetPathValue is how the handler tests pre-populate it
	// without spinning up chi.Mux. Equals what the production
	// router does after the {id} path template matches.
	req.SetPathValue("id", pathIDStr)
	if identity != nil {
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	}
	return req
}

// perfIdentity returns a UserIdentity stamped at workspace 7. The
// workspace id is not used by the per-channel analytics path
// (loadOwnAccountByID gates on user_id only) but a non-zero ws
// keeps the UserIdentity well-formed so refactor drift toward the
// workspace-as-tenant-gate future design fails fast.
func perfIdentity(userID int64) auth.Identity {
	return auth.NewUserIdentity(userID, 7, 0)
}

// makeYTChannelAccount returns a YouTube PlatformAccount owned by
// `ownerUserID` with id `accountID`. The id is parameterised (not
// hardcoded) so the cross-tenant + same-id fixture used by the
// isolation test reuses the helper without a future refactor
// accidentally proving a stale foreign-row match.
func makeYTChannelAccount(ownerUserID, accountID int64) *models.PlatformAccount {
	return &models.PlatformAccount{
		ID:             accountID,
		UserID:         ownerUserID,
		Platform:       "youtube",
		PlatformUserID: "113848374927471624321",
		Username:       "Demo Channel",
		Status:         "active",
		Metadata: models.Metadata{
			"channel_id": "UC-test",
		},
	}
}

// compile-time assertion: *mockUserStore (from common_test.go)
// is the production UserStore contract. AttachPlatformAccount +
// MarkReauthRequired use pointer receivers, so this MUST be a
// pointer assertion — value-type fails to satisfy the interface.
// Reusing it here keeps the per-test boilerplate to just
// findPlatformAccountFn.
var _ UserStore = (*mockUserStore)(nil)

// runGetAccountPerf is the canonical "fire the handler" helper.
// Returns the recorded response.
func runGetAccountPerf(t *testing.T, r *Router, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	wireAnalyticsServiceForTest(r)
	w := httptest.NewRecorder()
	r.handleGetAccountPerformance(w, req)
	return w
}

// wireAnalyticsServiceForTest lazily wires a ChannelAnalyticsService
// into the test Router using whatever userRepo + metricHistoryStore
// the test already configured. Production wiring uses
// NewRouter() + WithChannelAnalyticsService() OR MustNewRouter();
// tests build the Router struct directly, so this helper is the
// bridge that keeps every existing fixture working without forcing
// every test function to duplicate the constructor wiring.
//
// Idempotent: a test that already set channelAnalyticsService
// explicitly is left untouched.
func wireAnalyticsServiceForTest(r *Router) {
	if r.channelAnalyticsService != nil {
		return
	}
	if r.userRepo == nil || r.metricHistoryStore == nil {
		return
	}
	r.channelAnalyticsService = newAnalyticsTestService(r.userRepo, r.metricHistoryStore)
}

// ---------------------------------------------------------------------------
// 1. Happy-path period coverage: ?days=7 | 14 | 28 each → 200 OK
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_HappyPath_Periods(t *testing.T) {
	cases := []struct {
		name   string
		daysQs string
	}{
		{"7", "7"},
		{"14", "14"},
		{"28", "28"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Router{
				userRepo: &mockUserStore{
					findPlatformAccountFn: func(_ int64) (*models.PlatformAccount, error) {
						return makeYTChannelAccount(42, 381), nil
					},
				},
				metricHistoryStore: &fakeMetricHistoryStore{
					getFn: func(_ int64, _, _ time.Time) ([]repository.AccountMetricPoint, error) {
						return []repository.AccountMetricPoint{}, nil
					},
				},
			}
			req := newPerfRequest(t, "381", tc.daysQs, perfIdentity(42))
			w := runGetAccountPerf(t, r, req)
			if w.Code != http.StatusOK {
				t.Fatalf("?days=%s: want 200, got %d (%s)", tc.daysQs, w.Code, w.Body.String())
			}
			var resp analytics.ChannelPerformanceResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v (body=%s)", err, w.Body.String())
			}
			got := -1
			switch tc.daysQs {
			case "7":
				got = 7
			case "14":
				got = 14
			case "28":
				got = 28
			}
			if resp.Period.Days != got {
				t.Errorf("Period.Days: want %d, got %d", got, resp.Period.Days)
			}
			if resp.Channel.PlatformAccountID != 381 {
				t.Errorf("Channel.PlatformAccountID: want 381, got %d", resp.Channel.PlatformAccountID)
			}
			if resp.Channel.YouTubeChannelID != "UC-test" {
				t.Errorf("Channel.YouTubeChannelID: want UC-test, got %q", resp.Channel.YouTubeChannelID)
			}
			// Daily series must always be period.Days entries — the
			// gap-fill invariant the assembler enforces.
			if len(resp.DailySeries) != got {
				t.Errorf("DailySeries length: want %d, got %d", got, len(resp.DailySeries))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Bad ?days= → 400. Out of {7,14,28}, non-numeric, missing.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_InvalidDays(t *testing.T) {
	cases := []struct {
		name string
		qs   string
	}{
		{"missing", ""},
		{"zero", "0"},
		{"one", "1"},
		{"eight", "8"},
		{"thirty", "30"},
		{"two_eighty_eight", "288"},
		{"negative", "-7"},
		{"non_numeric", "abc"},
		// The literal "7; DROP TABLE" decoy would panic httptest.NewRequest
		// (it interprets the URL's whitespace as the HTTP protocol line).
		// Use a safe equivalent — URL-encoded OR-clause — that exercises
		// the same strconv-parse rejection path without breaking the
		// test-driver URL parser.
		{"or_decoy", "7%20OR%201%3D1"},
	}
	// The handler validates the path and ?days= query before delegating
	// to the service. Invalid input must fail fast: no account lookup
	// and no history query are allowed.
	// The case-counter assertion below pins the no-lookup invariant.
	findCalls := 0
	store := &fakeMetricHistoryStore{
		getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
			t.Errorf("GetHistory MUST NOT be called when ?days= is invalid")
			return nil, errors.New("unexpected call")
		},
	}
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				findCalls++
				return makeYTChannelAccount(42, 381), nil
			},
		},
		metricHistoryStore: store,
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := findCalls
			req := newPerfRequest(t, "381", tc.qs, perfIdentity(42))
			w := runGetAccountPerf(t, r, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("?days=%q: want 400, got %d (%s)", tc.qs, w.Code, w.Body.String())
			}
			if findCalls != before {
				t.Errorf("FindPlatformAccountByID MUST NOT be called for invalid days, got delta=%d",
					findCalls-before)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. Account not in DB → 404. No existence leak.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_AccountNotFound(t *testing.T) {
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				return nil, nil // not found
			},
		},
		metricHistoryStore: &fakeMetricHistoryStore{},
	}
	req := newPerfRequest(t, "9999", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 4. Account belongs to a DIFFERENT user → 404, NOT 403. Defence
// against cross-tenant existence probes.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_CrossUserIsolation(t *testing.T) {
	// Account owned by user 999; caller is user 42.
	foreignAccount := makeYTChannelAccount(999, 381)
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				return foreignAccount, nil
			},
		},
		metricHistoryStore: &fakeMetricHistoryStore{
			getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
				// Defence-in-depth: GetHistory MUST NOT run for a
				// foreign account. The cross-tenant check in
				// loadOwnAccountByID blocks BEFORE the DB call.
				t.Errorf("GetHistory MUST NOT be called for foreign account")
				return nil, errors.New("unexpected call")
			},
		},
	}
	req := newPerfRequest(t, "381", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (no existence leak), got %d (%s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 5. Account platform != "youtube" → 422.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_NonYouTubePlatform(t *testing.T) {
	igAccount := makeYTChannelAccount(42, 381)
	igAccount.Platform = "instagram"
	// No channel_id needed — Instagram doesn't have one, and the
	// 422 is the platform-mismatch gate, NOT the channel_id gate.
	delete(igAccount.Metadata, "channel_id")
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				return igAccount, nil
			},
		},
		metricHistoryStore: &fakeMetricHistoryStore{},
	}
	req := newPerfRequest(t, "381", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for non-YouTube, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not a YouTube account") {
		t.Errorf("body should explain the YouTube-only gate, got: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 6. YouTube account missing channel_id → 422 (re-link prompt).
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_MissingChannelID(t *testing.T) {
	orphan := makeYTChannelAccount(42, 381)
	orphan.Metadata = models.Metadata{} // strip channel_id (both key spellings)
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				return orphan, nil
			},
		},
		metricHistoryStore: &fakeMetricHistoryStore{},
	}
	req := newPerfRequest(t, "381", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for re-link, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "re-link") {
		t.Errorf("body should suggest re-link, got: %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 7. metricHistoryStore not wired → 501 (operator misconfiguration).
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_StoreUnconfigured(t *testing.T) {
	r := &Router{
		userRepo:           &mockUserStore{},
		metricHistoryStore: nil, // misconfigured boot
	}
	req := newPerfRequest(t, "381", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501 when store not wired, got %d (%s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 8. GetHistory returns error → 500 (logged with request_id).
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_HistoryError(t *testing.T) {
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				return makeYTChannelAccount(42, 381), nil
			},
		},
		metricHistoryStore: &fakeMetricHistoryStore{
			getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
				return nil, errors.New("simulated upstream postgres error")
			},
		},
	}
	req := newPerfRequest(t, "381", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on GetHistory error, got %d (%s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 9. NO DATA case → 200 (NOT 404). Channel exists, DB has zero rows.
//   - This is one of the spec-mandated invariants; the dashboard MUST
//     render an empty state, not a failed-to-load state.
//   - The test also pins that ALL headline KPI fields are zeroed (not
//     just Views=0) so a future refactor that omits WatchTimeMinutes
//     from the empty-data path would fail fast.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_NoDataReturns200(t *testing.T) {
	r := &Router{
		userRepo: &mockUserStore{
			findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
				return makeYTChannelAccount(42, 381), nil
			},
		},
		metricHistoryStore: &fakeMetricHistoryStore{
			getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
				return nil, nil // empty data — NO rows in the window
			},
		},
	}
	req := newPerfRequest(t, "381", "7", perfIdentity(42))
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusOK {
		t.Fatalf("no-data MUST return 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp analytics.ChannelPerformanceResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.DailySeries) != 7 {
		t.Errorf("DailySeries length: want 7 even with empty data, got %d", len(resp.DailySeries))
	}
	// All headline KPIs MUST be zero on empty data, not just Views.
	zeroSummary := resp.Summary
	if zeroSummary.Views != 0 {
		t.Errorf("Summary.Views on empty data: want 0, got %d", zeroSummary.Views)
	}
	if zeroSummary.WatchTimeMinutes != 0 {
		t.Errorf("Summary.WatchTimeMinutes on empty data: want 0, got %d", zeroSummary.WatchTimeMinutes)
	}
	if zeroSummary.SubscribersGained != 0 || zeroSummary.SubscribersLost != 0 ||
		zeroSummary.SubscribersNet != 0 {
		t.Errorf("Summary.Subscribers{...} on empty data: all zero, got gained=%d lost=%d net=%d",
			zeroSummary.SubscribersGained, zeroSummary.SubscribersLost, zeroSummary.SubscribersNet)
	}
	if zeroSummary.VideosPublished != 0 {
		t.Errorf("Summary.VideosPublished on empty data: want 0, got %d", zeroSummary.VideosPublished)
	}
	// No-data MUST report stale (no last_synced_at signal exists
	// in the window) so the SPA shows the "Last synced: …" badge
	// as the period.EndDate fallback (still a usable timestamp).
	if !resp.DataFreshness.IsStale {
		t.Errorf("IsStale on empty data: want true (no signal), got false")
	}
}

// ---------------------------------------------------------------------------
// 10. Unparseable / non-positive account ID in path → 400.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_UnparseableAccountID(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"non_numeric", "abc"},
		{"zero", "0"},
		{"negative", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Router{
				userRepo: &mockUserStore{
					findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
						t.Errorf("FindPlatformAccountByID MUST NOT be called when id url is bogus")
						return nil, errors.New("unexpected call")
					},
				},
				metricHistoryStore: &fakeMetricHistoryStore{
					getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
						t.Errorf("GetHistory MUST NOT be called when id url is bogus")
						return nil, errors.New("unexpected call")
					},
				},
			}
			req := newPerfRequest(t, tc.id, "7", perfIdentity(42))
			w := runGetAccountPerf(t, r, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("id=%q: want 400, got %d (%s)", tc.id, w.Code, w.Body.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 11. Missing identity in context → 401 (defence-in-depth: production
// r.protected() should have already thrown, but the auth check
// inside loadOwnAccountByID is the second wall).
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_MissingIdentity(t *testing.T) {
	r := &Router{
		userRepo: &mockUserStore{},
		metricHistoryStore: &fakeMetricHistoryStore{
			getFn: func(int64, time.Time, time.Time) ([]repository.AccountMetricPoint, error) {
				t.Errorf("GetHistory MUST NOT be called when identity is missing")
				return nil, errors.New("unexpected call")
			},
		},
	}
	req := newPerfRequest(t, "381", "7", nil) // no identity
	w := runGetAccountPerf(t, r, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on missing identity, got %d (%s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 12. Cache layer / freshness table: the handler reads ONLY from the
// local-DB cache (metricHistoryStore). The "cache fresca" path
// reports is_stale=false; the "cache scaduta" path reports
// is_stale=true; NEITHER triggers an upstream YouTube refresh
// (that's a separate endpoint triggered manually by the SPA).
//
// The fixture controls last_synced_at indirectly via the mock so
// the handler's own clock is exercised end-to-end:
//   - "fresh" case: history row at end_date → within TTL → fresh
//   - "stale" case: history row at end_date - 72h → past TTL → stale
//   - "no data" case: 0 rows → period.EndDate fallback → always stale
//
// In all three cases the response is 200, GetHistory is the only DB
// call, and is_stale reflects the cache state honestly.
// ---------------------------------------------------------------------------

func TestHandle_GetAccountPerformance_CacheFreshnessTable(t *testing.T) {
	// harness uses an OFFSET rather than a fixed date because
	// analytics.Resolve(7) truncates time.Now() to today's mid-night
	// UTC, so period.EndDate = 2026-07-30 00:00:00 with running wall
	// clock at 2026-07-30 12:38. A rowDate offset from time.Now()
	// would land AFTER period.EndDate and be filtered out by
	// splitHistoryByPeriod (lastDate fallback would then be
	// period.EndDate — which is already past the 10-min 7d TTL, so
	// IsStale would always be true even on the supposedly fresh case).
	// Deriving the row offset against the `to` bound the mock receives
	// from the handler sidesteps the truncation entirely.
	type harness struct {
		name       string
		ageFromEnd time.Duration
		wantStale  bool
	}
	cases := []harness{
		{
			name: "fresh_recent_history",
			// History dates are calendar dates in the assembler. A
			// one-second subtraction from midnight crosses into the
			// previous calendar day and is therefore a stale row.
			ageFromEnd: 0,
			wantStale:  false,
		},
		{
			name:       "stale_old_history",
			ageFromEnd: 72 * time.Hour,
			wantStale:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeMetricHistoryStore{
				getFn: func(_ int64, _, to time.Time) ([]repository.AccountMetricPoint, error) {
					return []repository.AccountMetricPoint{
						{Date: to.Add(-tc.ageFromEnd), Subscribers: 1000, Views: 5000, Videos: 5},
					}, nil
				},
			}
			r := &Router{
				userRepo: &mockUserStore{
					findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
						return makeYTChannelAccount(42, 381), nil
					},
				},
				metricHistoryStore: store,
			}
			req := newPerfRequest(t, "381", "7", perfIdentity(42))
			w := runGetAccountPerf(t, r, req)
			if w.Code != http.StatusOK {
				t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
			}
			// Decode so the JSON shape round-trip is exercised
			// (a refactor that drops is_stale from the wire would
			// surface here as a failure, not as a silent regression).
			var resp analytics.ChannelPerformanceResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v (body=%s)", err, w.Body.String())
			}
			if resp.DataFreshness.IsStale != tc.wantStale {
				t.Errorf("IsStale: want %v, got %v", tc.wantStale, resp.DataFreshness.IsStale)
			}
			if store.getCallCount != 1 {
				t.Errorf("expected exactly 1 GetHistory call (no upstream refresh), got %d", store.getCallCount)
			}
		})
	}
	// No-data sub-case covered separately by
	// TestHandle_GetAccountPerformance_NoDataReturns200.
}
