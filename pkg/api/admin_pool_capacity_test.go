package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestHandleAdminYouTubeOAuthPoolCapacity_NonAdmin_Forbidden(t *testing.T) {
	store := &stubAdminStore{}
	m := &AdminModule{deps: AdminModuleDeps{AdminStore: store}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/oauth_pool_capacity", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 42, isAdmin: false}))

	m.handleAdminYouTubeOAuthPoolCapacity(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: want 403, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

func TestHandleAdminYouTubeOAuthPoolCapacity_NilAdminStore_NotImplemented(t *testing.T) {
	m := &AdminModule{deps: AdminModuleDeps{}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/oauth_pool_capacity", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 42, isAdmin: true}))

	m.handleAdminYouTubeOAuthPoolCapacity(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status: want 501, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// poolCapacityFixture seeds a two-client fleet: pool A at 48 active
// grants, pool B at 43, under one Google manager holding two channels
// (one reauth-required).
func poolCapacityFixture() repository.YouTubePoolCapacityReport {
	return repository.YouTubePoolCapacityReport{
		PoolUsage: []repository.YouTubePoolUsageCount{
			{OAuthClientKey: "youtube_pool_a", ActiveRefreshTokens: 48},
			{OAuthClientKey: "youtube_pool_b", ActiveRefreshTokens: 43},
		},
		Managers: []repository.YouTubePoolManager{
			{
				ProviderSubjectID:      "google-sub-111",
				OAuthClientKey:         "youtube_pool_a",
				GrantStatus:            "active",
				ChannelsTotal:          2,
				ChannelsReauthRequired: 1,
				Channels: []repository.YouTubePoolChannel{
					{
						PlatformAccountID: 10, PlatformUserID: "UCchannelA",
						Username: "Channel A", Status: "active",
						OAuthClientKey: "youtube_pool_a", GrantStatus: "active",
					},
					{
						PlatformAccountID: 11, PlatformUserID: "UCchannelB",
						Username: "Channel B", Status: "reauth_required",
						OAuthClientKey: "youtube_pool_a", GrantStatus: "active",
					},
				},
			},
		},
	}
}

func TestHandleAdminYouTubeOAuthPoolCapacity_Admin_OK_WithRegistry(t *testing.T) {
	store := &stubAdminStore{capacityResp: poolCapacityFixture()}
	m := &AdminModule{deps: AdminModuleDeps{
		AdminStore:                 store,
		YouTubeOAuthClientRegistry: newTestPoolRegistry(t),
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/oauth_pool_capacity", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 9999, isAdmin: true}))

	m.handleAdminYouTubeOAuthPoolCapacity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	var got YouTubeOAuthPoolCapacityResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON: %v (body=%q)", err, rec.Body.String())
	}

	if len(got.Pools) != 2 {
		t.Fatalf("pools: want 2 (registry order A,B), got %d", len(got.Pools))
	}
	poolA := got.Pools[0]
	if poolA.OAuthClientKey != "youtube_pool_a" || poolA.ActiveRefreshTokens != 48 ||
		poolA.RecommendedCapacity != 50 || poolA.RemainingCapacity != 2 ||
		poolA.ProviderLimit != 100 || poolA.Health != "healthy" {
		t.Errorf("pool A state: got %+v", poolA)
	}
	poolB := got.Pools[1]
	if poolB.OAuthClientKey != "youtube_pool_b" || poolB.ActiveRefreshTokens != 43 ||
		poolB.RecommendedCapacity != 50 || poolB.RemainingCapacity != 7 ||
		poolB.ProviderLimit != 100 || poolB.Health != "healthy" {
		t.Errorf("pool B state: got %+v", poolB)
	}

	if got.Totals.ManagersTotal != 1 || got.Totals.ChannelsTotal != 2 || got.Totals.ChannelsReauthRequired != 1 {
		t.Errorf("totals: got %+v", got.Totals)
	}
	if len(got.Managers) != 1 {
		t.Fatalf("managers: want 1, got %d", len(got.Managers))
	}
	mgr := got.Managers[0]
	if mgr.ProviderSubjectID != "google-sub-111" || mgr.OAuthClientKey != "youtube_pool_a" ||
		mgr.ChannelsTotal != 2 || mgr.ChannelsReauthRequired != 1 {
		t.Errorf("manager aggregate: got %+v", mgr)
	}
	if len(mgr.Channels) != 2 || mgr.Channels[1].Status != "reauth_required" ||
		mgr.Channels[1].GrantStatus != "active" {
		t.Errorf("manager channels drill-down: got %+v", mgr.Channels)
	}
}

func TestHandleAdminYouTubeOAuthPoolCapacity_HealthBands(t *testing.T) {
	report := poolCapacityFixture()
	report.PoolUsage = []repository.YouTubePoolUsageCount{
		{OAuthClientKey: "youtube_pool_a", ActiveRefreshTokens: 92}, // over critical → blocked
		{OAuthClientKey: "youtube_pool_b", ActiveRefreshTokens: 80}, // high
	}
	store := &stubAdminStore{capacityResp: report}
	m := &AdminModule{deps: AdminModuleDeps{
		AdminStore:                 store,
		YouTubeOAuthClientRegistry: newTestPoolRegistry(t),
	}}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/oauth_pool_capacity", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 1, isAdmin: true}))

	m.handleAdminYouTubeOAuthPoolCapacity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	var got YouTubeOAuthPoolCapacityResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if got.Pools[0].Health != "blocked" || got.Pools[0].RemainingCapacity != -42 {
		t.Errorf("pool A (92 active): want health=blocked remaining=-42, got %+v", got.Pools[0])
	}
	if got.Pools[1].Health != "high" {
		t.Errorf("pool B (80 active): want health=high, got %+v", got.Pools[1])
	}
}

func TestHandleAdminYouTubeOAuthPoolCapacity_NoRegistry_LegacyFallback(t *testing.T) {
	report := poolCapacityFixture()
	store := &stubAdminStore{capacityResp: report}
	m := &AdminModule{deps: AdminModuleDeps{AdminStore: store}} // no registry

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/youtube/oauth_pool_capacity", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), staffIdentity{uid: 1, isAdmin: true}))

	m.handleAdminYouTubeOAuthPoolCapacity(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body=%q)", rec.Code, rec.Body.String())
	}
	var got YouTubeOAuthPoolCapacityResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if len(got.Pools) != 2 {
		t.Fatalf("pools: want 2 (derived from data keys), got %d", len(got.Pools))
	}
	// Keys are sorted: youtube_pool_a before youtube_pool_b.
	if got.Pools[0].RecommendedCapacity != 50 || got.Pools[0].Health != "healthy" {
		t.Errorf("legacy fallback pool A: got %+v", got.Pools[0])
	}
}
