package api

import (
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// handleSyncAllAccounts (POST /api/v1/accounts/sync-all) is the
// "refresh all channels" action. It enqueues every non-deleted account
// owned by the caller for a background snapshot refresh in ONE bulk
// statement and returns immediately with 202 Accepted — the request
// NEVER fans out into per-account provider (YouTube) calls. The
// snapshot-refresh sweep worker drains the queue with bounded
// concurrency (4), so "refresh all" on a 50-channel account never opens
// 50 simultaneous Google requests.
func (r *Router) handleSyncAllAccounts(w http.ResponseWriter, req *http.Request) {
	if r.snapshotStore == nil {
		writeError(w, http.StatusNotImplemented, "snapshot store not configured")
		return
	}
	id := auth.IdentityFromContext(req.Context())
	if id == nil || id.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	count, err := r.snapshotStore.MarkAllSnapshotRefreshesPending(id.UserID(), time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to schedule snapshot refresh: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "scheduled",
		"count":  count,
	})
}
