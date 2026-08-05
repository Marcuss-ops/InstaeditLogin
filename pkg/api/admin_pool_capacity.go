package api

import (
	"net/http"
	"sort"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// YouTubeOAuthPoolState is one pool client's dashboard state: the
// active-grant count (fleet-wide), the recommended soft capacity from
// the registry (default 50), Google's hard per-(account, client) limit
// (100), the remaining theoretical slots and the health band.
type YouTubeOAuthPoolState struct {
	OAuthClientKey      string `json:"oauth_client_key"`
	ActiveRefreshTokens int64  `json:"active_refresh_tokens"`
	RecommendedCapacity int64  `json:"recommended_capacity"`
	ProviderLimit       int64  `json:"provider_limit"`
	RemainingCapacity   int64  `json:"remaining_capacity"`
	Health              string `json:"health"`
}

// YouTubeOAuthPoolTotals is the dashboard's headline rollup across
// every Google manager.
type YouTubeOAuthPoolTotals struct {
	ManagersTotal          int64 `json:"managers_total"`
	ChannelsTotal          int64 `json:"channels_total"`
	ChannelsReauthRequired int64 `json:"channels_reauth_required"`
}

// YouTubeOAuthPoolCapacityResponse is the JSON body of
// GET /admin/youtube/oauth_pool_capacity.
type YouTubeOAuthPoolCapacityResponse struct {
	Pools       []YouTubeOAuthPoolState         `json:"pools"`
	Managers    []repository.YouTubePoolManager `json:"managers"`
	Totals      YouTubeOAuthPoolTotals          `json:"totals"`
	GeneratedAt int64                           `json:"generated_at_unix"`
}

// legacyPoolCapacity is the recommended per-client capacity applied
// when no pool registry is configured (the operator dashboard still
// shows the legacy single-client grant distribution). Mirrors the
// registry's default (half of Google's 100 per-(account, client) cap).
const legacyPoolCapacity int64 = 50

// handleAdminYouTubeOAuthPoolCapacity (GET /admin/youtube/oauth_pool_capacity)
// is the R8 operator dashboard endpoint: per Google manager account it
// reports the pool client that issued the grant, the grant status, the
// channel totals and the per-channel drill-down; fleet-wide it reports
// each pool client's active-grant count, recommended capacity,
// remaining theoretical slots and the health band (0–60 healthy,
// 61–75 warning, 76–85 high, 86–90 critical, >90 blocked per client).
//
// Capacity + health come from the wired pool registry when present
// (RecommendedCapacity + YouTubeOAuthPoolHealthFor); without a registry
// (legacy single-client deployments) the pools are derived from the
// observed client keys with the default capacity.
//
// Authz: admin-only, symmetric to handleAdminYouTubeFleetReadiness
// (adminAuthMiddleware upstream + the defensive IsAdmin re-check).
func (m *AdminModule) handleAdminYouTubeOAuthPoolCapacity(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || !identity.IsAdmin() {
		writeError(w, http.StatusForbidden, "requires admin privileges")
		return
	}

	report, err := m.deps.AdminStore.YouTubePoolCapacity(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"could not load youtube oauth pool capacity: "+err.Error())
		return
	}

	// (1) Fleet-wide pool state. Registry capacity wins when wired;
	// otherwise the observed keys get the legacy default capacity.
	usedByKey := make(map[string]int64, len(report.PoolUsage))
	for _, u := range report.PoolUsage {
		usedByKey[u.OAuthClientKey] = u.ActiveRefreshTokens
	}
	var pools []YouTubeOAuthPoolState
	if reg := m.deps.YouTubeOAuthClientRegistry; reg != nil {
		for _, key := range reg.Keys() {
			client, cErr := reg.Resolve(key)
			if cErr != nil {
				writeError(w, http.StatusInternalServerError,
					"could not resolve configured pool client: "+cErr.Error())
				return
			}
			used := usedByKey[key]
			pools = append(pools, YouTubeOAuthPoolState{
				OAuthClientKey:      key,
				ActiveRefreshTokens: used,
				RecommendedCapacity: int64(client.RecommendedCapacity),
				ProviderLimit:       services.GoogleOAuthClientRefreshTokenLimit,
				RemainingCapacity:   int64(client.RecommendedCapacity) - used,
				Health:              string(services.YouTubeOAuthPoolHealthFor(used)),
			})
		}
	} else {
		for _, key := range sortedPoolKeys(usedByKey) {
			used := usedByKey[key]
			pools = append(pools, YouTubeOAuthPoolState{
				OAuthClientKey:      key,
				ActiveRefreshTokens: used,
				RecommendedCapacity: legacyPoolCapacity,
				ProviderLimit:       services.GoogleOAuthClientRefreshTokenLimit,
				RemainingCapacity:   legacyPoolCapacity - used,
				Health:              string(services.YouTubeOAuthPoolHealthFor(used)),
			})
		}
	}

	// (2) Headline rollup.
	var totals YouTubeOAuthPoolTotals
	totals.ManagersTotal = int64(len(report.Managers))
	for i := range report.Managers {
		totals.ChannelsTotal += report.Managers[i].ChannelsTotal
		totals.ChannelsReauthRequired += report.Managers[i].ChannelsReauthRequired
	}

	writeJSON(w, http.StatusOK, YouTubeOAuthPoolCapacityResponse{
		Pools:       pools,
		Managers:    report.Managers,
		Totals:      totals,
		GeneratedAt: nowUnix(),
	})
}

// sortedPoolKeys returns the pool usage keys in ascending order so the
// legacy (no-registry) pools list is deterministic.
func sortedPoolKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
