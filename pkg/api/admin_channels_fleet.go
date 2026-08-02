package api

import (
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// handleAdminYouTubeFleetReadiness (GET /admin/youtube/fleet_readiness)
// is the Definition-of-Done rollout snapshot endpoint. On each
// call the AdminRepository aggregates the 12 DoD counters in one
// COUNT(*) FILTER roundtrip and persists one row per YouTube
// platform_account into fleet_readiness_snapshot_channels so a
// later diff against the prior snapshot highlights which channels
// transitioned between states. The 12 fields + snapshot_id flow
// back to the operator dashboard as JSON.
//
// Authz:
//   - non-admin callers -> 403 (adminAuthMiddleware short-circuits
//     upstream of this handler; the defensive IsAdmin re-check here
//     catches any future wiring that drops the middleware on a per-
//     route basis).
//   - adminStore nil -> 501 (mounting the admin repo is a deliberate
//     subset-setup; tests can omit it without 500-ing the endpoint).
//
// The handler is intentionally read-only + idempotent: a hostile
// retry of the same call yields a NEW snapshot row + the same JSON
// counts (calls converge on idempotency at the counts layer; the
// audit history diverges by taken_at).
func (m *AdminModule) handleAdminYouTubeFleetReadiness(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || !identity.IsAdmin() {
		writeError(w, http.StatusForbidden, "requires admin privileges")
		return
	}
	adminID := identity.UserID()
	if adminID == 0 {
		writeError(w, http.StatusForbidden, "requires authenticated admin identity")
		return
	}

	snap, err := m.deps.AdminStore.CreateFleetReadinessSnapshot(req.Context(), adminID)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			"could not take fleet readiness snapshot: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, snap)
}
