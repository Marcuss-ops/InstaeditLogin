// SPRINT 1.1: POST /api/v1/workspaces/{id}/switch.
//
// Switches the caller's active workspace. The handler:
//
//	1. Authenticates the caller via Manager.Middleware (Cookie or Bearer).
//	2. Verifies the caller is a member of the requested workspace via
//	   teamStore.GetRole (admin/editor/viewer all qualify; the role only
//	   governs permission scope, not the switch itself).
//	3. Re-issues a JWT whose ws claim == requested workspace id.
//	4. Writes a fresh HttpOnly session cookie with the new JWT so the
//	   next request automatically operates against the new workspace.
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
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
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
	// Re-issue the JWT carrying the new ws claim. The previous
	// cookie is replaced atomically by WriteHeader (Set-Cookie with
	// MaxAge>0 overrides the prior cookie because they share the
	// name "session").
	jwtToken, _, _, err := r.auth.Issue(id.UserID(), targetWS)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue session token: "+err.Error())
		return
	}
	metrics.IncJWTIssued()
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    jwtToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workspace_id": targetWS,
		"role":         role,
	})
}
