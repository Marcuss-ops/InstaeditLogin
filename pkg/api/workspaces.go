package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// CreateWorkspaceReq is the body schema for POST /api/v1/workspaces.
// Only `name` is required; OwnerID is derived from the authenticated
// caller (never trusted from the body — that would let a user create
// workspaces owned by someone else).
type CreateWorkspaceReq struct {
	Name string `json:"name"`
}

// handleListWorkspaces (GET /api/v1/workspaces, protected) returns every
// workspace owned by the authenticated caller, newest first.
func (r *Router) handleListWorkspaces(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	workspaces, err := r.workspaceStore.ListByOwner(userID)
	if err != nil {
		logAndError(w, "failed to list workspaces", err, "user_id", userID)
		return
	}
	if workspaces == nil {
		workspaces = []models.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"workspaces": workspaces})
}

// handleCreateWorkspace (POST /api/v1/workspaces, protected) creates
// a new workspace owned by the authenticated caller.
// Validation: `name` non-empty (422). Body must be valid JSON (400).
func (r *Router) handleCreateWorkspace(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	var body CreateWorkspaceReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}

	ws := &models.Workspace{Name: body.Name, OwnerID: userID}
	if err := r.workspaceStore.Create(ws); err != nil {
		logAndError(w, "failed to create workspace", err, "user_id", userID)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// handleGetWorkspace (GET /api/v1/workspaces/{id}, protected) fetches a
// single workspace. Returns 404 (not 403) on cross-owner access so the
// endpoint doesn't leak the existence of workspaces owned by other users.
func (r *Router) handleGetWorkspace(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}

	ws, err := r.workspaceStore.FindByID(id)
	if err != nil {
		logAndError(w, "failed to find workspace", err, "id", id)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		slog.Warn("workspace cross-owner access denied",
			"user_id", userID, "workspace_id", id, "owner_id", ws.OwnerID)
		// 404 (not 403) intentionally — avoid leaking the existence of
		// other users' workspaces via differential status codes.
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, ws)
}
