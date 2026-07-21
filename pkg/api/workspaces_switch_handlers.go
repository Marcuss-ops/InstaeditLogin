// SPRINT 1.1: POST /api/v1/workspaces/{id}/switch.
//
// Switches the caller's active workspace. The handler:
//
//  1. Authenticates the caller via Manager.Middleware (Cookie or Bearer).
//  2. Verifies the caller is a member of the requested workspace via
//     teamStore.GetRole (admin/editor/viewer all qualify; the role only
//     governs permission scope, not the switch itself).
//  3. SPRINT 7.4 (P0#14-blocco-1.4): rotates the session — revokes
//     the previous session row (best-effort) and creates a new one
//     bound to the requested workspace_id via SessionsService.Start.
//     The cookie is rotated atomically (Set-Cookie overrides the
//     prior session cookie because they share the name "session").
//  4. Returns the workspace_id + role as JSON.
//
// Failure modes:
//   - missing or invalid JWT  → 401 (Re-issue of session middleware failure)
//   - workspace does not exist for caller → 403 (cross-tenant; do NOT
//     return 404 to avoid existence-leak avenue against caller probes)
//   - invalid id param → 400
//
// Wiring is unconditional inside the Router.Setup() workspace group;
// the auth middleware chain still front-runs every request.
package api

import (
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

func (r *Router) handleSwitchWorkspace(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.teamStore == nil || r.auth == nil {
		writeError(w, http.StatusNotImplemented, "workspace switch not configured on this server")
		return
	}
	if r.sessionsSvc == nil {
		// SPRINT 7.4: workspace switch + cookie rotation requires a
		// session row (otherwise the new JWT would have sessionID=0
		// and Manager.Verify would reject it). Fail-fast the misconfig
		// so production wiring is self-documenting.
		writeError(w, http.StatusInternalServerError, "sessions service not configured (Blocco #1.4 migration requires it)")
		return
	}
	targetWS, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	role, err := r.teamStore.GetRole(targetWS, id.UserID())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check membership: "+err.Error())
		return
	}
	if role == "" {
		// 403: the workspace exists but caller is not a member, OR
		// the workspace does not exist for this caller. Both map to
		// the same response so attackers cannot enumerate workspace
		// ids by probing /switch/ID responses.
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return
	}

	// SPRINT 7.4: revoke the OLD session row (best-effort) and
	// allocate a NEW one bound to the target workspace. We revoke
	// first so a partial-failure (revoke succeeded but Start
	// failed) leaves the user logged out rather than with a stale
	// session — keeping `Verify` invariants consistent.
	if oldSID := id.SessionID(); oldSID > 0 {
		if err := r.sessionsSvc.Revoke(oldSID, id.UserID(), "workspace_switch"); err != nil && err != services.ErrSessionForbidden {
			// Don't fail the request on a revoke error — the user
			// will still get the new session and the old refresh
			// cookie will simply stop working at its next refresh.
			// Surface to logs though.
			slog.Warn("workspace switch: revoke old session failed", "old_session_id", oldSID, "error", err)
		}
	}

	result, err := r.sessionsSvc.Start(services.StartSessionRequest{
		UserID:      id.UserID(),
		WorkspaceID: targetWS,
		UserAgent:   req.UserAgent(),
		IP:          clientIP(req),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start session: "+err.Error())
		return
	}
	metrics.IncJWTIssued()
	r.setSessionCookie(w, req, result)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workspace_id": targetWS,
		"role":         role,
		"session_id":   result.SessionID,
	})
}
