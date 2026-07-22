package velox

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// listWorkers implements GET /api/v1/velox/workers.
//
// Returns the workers visible to the session's workspace. The
// workspace scope is signed into the outbound JWT so Velox only
// returns workers the caller is authorised to see.
func (b *bff) listWorkers(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	workers, err := b.deps.Client.ListWorkers(req.Context(), wsID, userID)
	if err != nil {
		slog.Error("velox bff: list workers failed", "workspace_id", wsID, "err", err)
		writeError(w, http.StatusInternalServerError, "upstream call failed")
		return
	}
	// Defense-in-depth: drop any worker whose WorkspaceID does not
	// match the session. Velox should already scope by the signed
	// JWT, but this prevents a misconfigured Velox from leaking
	// cross-tenant rows.
	safe := workers[:0]
	for _, wkr := range workers {
		if wkr.WorkspaceID == wsID {
			safe = append(safe, wkr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workers": safe,
	})
}

// getWorker implements GET /api/v1/velox/workers/{id}.
//
// Verifies the returned worker belongs to the session's workspace
// before returning. Mismatch → 404 (no existence leak).
func (b *bff) getWorker(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	workerID := chi.URLParam(req, "id")
	if workerID == "" {
		writeError(w, http.StatusBadRequest, "worker id required")
		return
	}
	wkr, err := b.deps.Client.GetWorker(req.Context(), wsID, userID, workerID)
	if err != nil {
		slog.Error("velox bff: get worker failed", "worker_id", workerID, "err", err)
		mapClientError(w, err)
		return
	}
	if !verifyOwnership(w, wkr.WorkspaceID, wsID) {
		return
	}
	writeJSON(w, http.StatusOK, wkr)
}
