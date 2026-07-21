package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// --- Request types -----------------------------------------------------------

// CreateGroupRequest is the JSON body for POST /api/v1/groups.
// `workspace_id` is taken from the URL query (and cross-checked against
// ownership) so the client can't cross tenants by stuffing a foreign
// workspace_id into the body.
type CreateGroupRequest struct {
	WorkspaceID   int64  `json:"workspace_id"`
	ParentGroupID *int64 `json:"parent_group_id,omitempty"`
	Name          string `json:"name"`
}

// UpdateGroupRequest is the JSON body for PATCH /api/v1/groups/{id}.
type UpdateGroupRequest struct {
	Name          string `json:"name,omitempty"`
	ParentGroupID *int64 `json:"parent_group_id,omitempty"`
}

// SetGroupAccountsRequest is the JSON body for PUT
// /api/v1/groups/{id}/accounts. The "set" semantics mirror the repo:
// wipe + re-insert in one tx.
type SetGroupAccountsRequest struct {
	AccountIDs []int64 `json:"account_ids"`
}

// --- Error mapping ----------------------------------------------------------

func mapGroupError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, repository.ErrGroupNotFound),
		errors.Is(err, repository.ErrWorkspaceNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, repository.ErrGroupCycle),
		errors.Is(err, repository.ErrGroupWorkspaceMismatch):
		return http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, repository.ErrGroupDuplicate):
		return http.StatusConflict, err.Error()
	default:
		return http.StatusInternalServerError, "failed to process group: " + err.Error()
	}
}

// requireWorkspaceOwnership loads the workspace and verifies the caller
// owns it. Cross-tenant GET/POST/PATCH/DELETE returns 404 (existence-leak
// avoidance, mirrors handleGetWorkspace / handleDeleteWorkspace).
func (r *Router) requireWorkspaceOwnership(w http.ResponseWriter, req *http.Request, workspaceID int64) (bool, *models.Workspace) {
	callerID, ok := requireUserID(w, req, r)
	if !ok {
		return false, nil
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return false, nil
	}
	ws, err := r.workspaceStore.FindByID(workspaceID)
	status, msg := mapWorkspaceError(err)
	if status != http.StatusOK {
		writeError(w, status, msg)
		return false, nil
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false, nil
	}
	if ws.OwnerID != callerID {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false, nil
	}
	return true, ws
}

// --- Handlers ---------------------------------------------------------------

// handleListGroups returns every group for the supplied workspace (the
// caller is expected to pass ?workspace_id=…). The response is a flat
// list — the frontend builds the tree from the parent_group_id pointers
// in O(N).
//
// GET /api/v1/groups?workspace_id=…
func (r *Router) handleListGroups(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	wsIDStr := req.URL.Query().Get("workspace_id")
	if wsIDStr == "" {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	wid, err := strconv.ParseInt(wsIDStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, wid); !ok {
		return
	}
	groups, err := r.groupStore.ListByWorkspace(wid)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if groups == nil {
		groups = []models.Group{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups})
}

// handleCreateGroup creates a new group in the supplied workspace. The
// parent_group_id is validated against cycles + workspace ownership by
// the repository before the INSERT. 422 for cycle / cross-workspace;
// 409 for duplicate root names; 404 for missing parent.
//
// POST /api/v1/groups
func (r *Router) handleCreateGroup(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	var body CreateGroupRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, body.WorkspaceID); !ok {
		return
	}
	g := &models.Group{
		WorkspaceID:   body.WorkspaceID,
		ParentGroupID: body.ParentGroupID,
		Name:          body.Name,
	}
	if err := r.groupStore.Create(g); err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

// handleUpdateGroup mutates an existing group's name and/or parent.
// The handler enforces caller ownership by reading the group first and
// walking workspace.WorkspaceID.
//
// PATCH /api/v1/groups/{id}
func (r *Router) handleUpdateGroup(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id: "+err.Error())
		return
	}
	// Pre-load to discover the workspace before ownership check; this
	// also surfaces the (group, workspace) mismatch as 404 rather than
	// silently logging it for a 500.
	existing, err := r.groupStore.FindByID(id)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, existing.WorkspaceID); !ok {
		return
	}
	var body UpdateGroupRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Name == "" && body.ParentGroupID == nil {
		writeError(w, http.StatusBadRequest, "name or parent_group_id required")
		return
	}
	if body.Name != "" {
		existing.Name = body.Name
	}
	if body.ParentGroupID != nil {
		existing.ParentGroupID = body.ParentGroupID
	}
	if err := r.groupStore.Update(existing); err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// handleDeleteGroup removes a group; ON DELETE CASCADE handles children
// and group_accounts rows.
//
// DELETE /api/v1/groups/{id}
func (r *Router) handleDeleteGroup(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id: "+err.Error())
		return
	}
	existing, err := r.groupStore.FindByID(id)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, existing.WorkspaceID); !ok {
		return
	}
	if err := r.groupStore.Delete(id); err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetGroup returns a single group (cross-tenant miss → 404).
//
// GET /api/v1/groups/{id}
func (r *Router) handleGetGroup(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id: "+err.Error())
		return
	}
	existing, err := r.groupStore.FindByID(id)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, existing.WorkspaceID); !ok {
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// handleListGroupAccounts returns the account ids attached directly to
// a group (NOT recursive through subgroups — the join table is
// per-row). 404 on cross-tenant or missing.
//
// GET /api/v1/groups/{id}/accounts
func (r *Router) handleListGroupAccounts(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id: "+err.Error())
		return
	}
	existing, err := r.groupStore.FindByID(id)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, existing.WorkspaceID); !ok {
		return
	}
	accounts, err := r.groupStore.ListAccountsInGroup(id)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if accounts == nil {
		accounts = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"account_ids": accounts})
}

// handleSetGroupAccounts replaces the membership list for a group.
// "Set" semantics (delete + insert in one tx) match the repo. The
// caller ID comes from the JWT (deposited by r.protected →
// r.auth.Middleware); account_ids are intersected against the
// caller's owned accounts via ValidateAccountOwnership before the
// INSERT so a hostile caller cannot attach an account they do not
// own to a foreign group — 403 on any disallowed id.
//
// PUT /api/v1/groups/{id}/accounts
func (r *Router) handleSetGroupAccounts(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid group id: "+err.Error())
		return
	}
	existing, err := r.groupStore.FindByID(id)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, existing.WorkspaceID); !ok {
		return
	}
	var body SetGroupAccountsRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	callerID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	// Cross-tenant guard: intersect the caller-supplied list against
	// accounts the caller actually owns before persisting. Refuse the
	// whole request with 403 if any id is foreign (the SPA can then
	// re-submit with the correct list). Without this check, a hostile
	// caller could attach arbitrary account_ids to a foreign group.
	validated, err := r.groupStore.ValidateAccountOwnership(callerID, existing.WorkspaceID, body.AccountIDs)
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if len(validated) != len(body.AccountIDs) {
		writeError(w, http.StatusForbidden, "one or more account_ids are not owned by the caller")
		return
	}
	if err := r.groupStore.SetAccounts(id, validated); err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	out := validated
	if out == nil {
		out = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"account_ids": out})
}
