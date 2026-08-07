package api

// Post creation handler; read and mutation flows live in bounded files.

import (
	"net/http"
)

func (r *Router) handleCreatePost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	// Phase 1: read body bytes + hash + decode + schema validation
	// (workspace_id, status, targets). The body bytes + hash are
	// reused by the idempotency gate (phase 3).
	body, bodyBytes, done := decodeCreatePostRequest(w, req)
	if done {
		return
	}
	hash := idempotencyHash(bodyBytes)

	// Phase 2: workspace lookup + ownership check. Runs BEFORE the
	// cache replay so a forged cross-tenant (workspace_id, key)
	// tuple cannot leak another tenant's resource.
	ws, done := r.lookupCreatePostWorkspace(w, req, userID, body.WorkspaceID)
	if done {
		return
	}

	// Phase 3: idempotency gate keyed on (ws.ID, idemKey) — replay /
	// conflict / continue.
	idemKey, done := r.applyCreatePostIdempotency(w, req, ws.ID, hash)
	if done {
		return
	}

	// Phase 4: canonical publish cursor (publish_at / scheduled_at
	// alias), PUBLISH_HORIZON_DAYS cap, status promotion.
	publishAt, status, done := r.resolveCreatePostSchedule(w, req, body)
	if done {
		return
	}

	// Phase 5 (terminal): media resolution, persist, idempotency
	// record, 201 response.
	r.createPostPersist(w, userID, ws, body, publishAt, status, idemKey, hash)
}
