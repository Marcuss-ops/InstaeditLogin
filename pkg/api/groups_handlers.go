// Package-level note: the /api/v1/groups handlers are split per domain
// (split-by-concern, 2026-08):
//
//	groups_handlers.go — this file: shared types + error mapping +
//	                     requireWorkspaceOwnership + the 6 CRUD/list
//	                     handlers (List / ListWithAccounts / Create /
//	                     Update / Delete / Get)
//	groups_accounts.go — handleListGroupAccounts + handleSetGroupAccounts
//	                     + SetGroupAccountsRequest (membership domain)
//	groups_settings.go — handleUpdateGroupSettings +
//	                     UpdateGroupSettingsRequest (settings domain)
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
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
	case errors.Is(err, repository.ErrGroupAccountOwnership):
		return http.StatusForbidden, err.Error()
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

// handleListGroups returns every group for the active workspace of the
// caller. If ?workspace_id=… is supplied the handler honors it as an
// explicit override; otherwise it falls back to the workspace stamped
// on the JWT/API-key identity by the auth middleware
// (auth.IdentityFromContext). An empty value (?workspace_id=) is
// treated identically to a fully absent query — it falls back to the
// identity just like the implicit path. The response is a flat list —
// the frontend builds the tree from the parent_group_id pointers in
// O(N).
//
// GET /api/v1/groups?workspace_id=…      (explicit override)
// GET /api/v1/groups                      (implicit: identity.WorkspaceID)
//
// In the implicit path, auth.Manager.Verify (see internal/auth/jwt.go)
// refuses to stamp an identity whose JWT `ws` claim is zero, so the
// fallback wid is guaranteed > 0 in production. The `if wid <= 0`
// guard below is a defence-in-depth branch for misconfigured
// fixtures (e.g. a test that stamps auth.WithIdentity(ctx, …) with a
// zero ws claim by accident).
func (r *Router) handleListGroups(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	wsIDStr := req.URL.Query().Get("workspace_id")
	var wid int64
	if wsIDStr != "" {
		parsed, err := strconv.ParseInt(wsIDStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		wid = parsed
	} else if id := auth.IdentityFromContext(req.Context()); id != nil {
		wid = id.WorkspaceID()
	}
	if wid <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, wid); !ok {
		return
	}
	if req.URL.Query().Get("cursor") == "" && req.URL.Query().Get("limit") == "" {
		groups, listErr := r.groupStore.ListByWorkspace(wid)
		if listErr != nil {
			status, msg := mapGroupError(listErr)
			writeError(w, status, msg)
			return
		}
		if groups == nil {
			groups = []models.Group{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups})
		return
	}
	limit, rawCursor, err := parseListPageWithBounds(req.URL.Query(), 50, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursorContext := listCursorFilterContext(req.URL.Query(), "workspace_id")
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "groups", cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cursorNull && rawCursor != "" {
		writeError(w, http.StatusBadRequest, "invalid list cursor: group cursor timestamp is required")
		return
	}
	var groups []models.Group
	hasMore := false
	if paged, ok := r.groupStore.(interface {
		ListByWorkspacePage(int64, *time.Time, int64, int) ([]models.Group, bool, error)
	}); ok {
		var afterTime *time.Time
		var afterID int64
		if rawCursor != "" {
			afterTime = &cursorTime
			afterID, err = strconv.ParseInt(cursorID, 10, 64)
			if err != nil || afterID <= 0 {
				writeError(w, http.StatusBadRequest, "invalid list cursor")
				return
			}
		}
		groups, hasMore, err = paged.ListByWorkspacePage(wid, afterTime, afterID, limit)
	} else {
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this group store")
			return
		}
		groups, err = r.groupStore.ListByWorkspace(wid)
		if len(groups) > limit {
			hasMore = true
			groups = groups[:limit]
		}
	}
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if groups == nil {
		groups = []models.Group{}
	}
	writeJSON(w, http.StatusOK, groupListResponse(groups, hasMore, cursorContext))
}

// handleListGroupsWithAccounts returns all groups and their direct account
// memberships for one owned workspace. Unlike the legacy list endpoint,
// this read model is assembled by the repository in one query, avoiding
// the frontend's per-group /accounts fan-out.
//
// GET /api/v1/groups/aggregate?workspace_id=…
// GET /api/v1/groups/aggregate (uses the workspace in the auth identity)
func (r *Router) handleListGroupsWithAccounts(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	wsIDStr := req.URL.Query().Get("workspace_id")
	var workspaceID int64
	if wsIDStr != "" {
		parsed, err := strconv.ParseInt(wsIDStr, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		workspaceID = parsed
	} else if identity := auth.IdentityFromContext(req.Context()); identity != nil {
		workspaceID = identity.WorkspaceID()
	}
	if workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, workspaceID); !ok {
		return
	}
	if req.URL.Query().Get("cursor") == "" && req.URL.Query().Get("limit") == "" {
		groups, listErr := r.groupStore.ListByWorkspaceWithAccounts(workspaceID)
		if listErr != nil {
			status, msg := mapGroupError(listErr)
			writeError(w, status, msg)
			return
		}
		if groups == nil {
			groups = []models.GroupWithAccounts{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"groups": groups})
		return
	}
	limit, rawCursor, err := parseListPageWithBounds(req.URL.Query(), 50, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursorContext := listCursorFilterContext(req.URL.Query(), "workspace_id")
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "groups-aggregate", cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cursorNull && rawCursor != "" {
		writeError(w, http.StatusBadRequest, "invalid list cursor: group cursor timestamp is required")
		return
	}
	var groups []models.GroupWithAccounts
	hasMore := false
	if paged, ok := r.groupStore.(interface {
		ListByWorkspaceWithAccountsPage(int64, *time.Time, int64, int) ([]models.GroupWithAccounts, bool, error)
	}); ok {
		var afterTime *time.Time
		var afterID int64
		if rawCursor != "" {
			afterTime = &cursorTime
			afterID, err = strconv.ParseInt(cursorID, 10, 64)
			if err != nil || afterID <= 0 {
				writeError(w, http.StatusBadRequest, "invalid list cursor")
				return
			}
		}
		groups, hasMore, err = paged.ListByWorkspaceWithAccountsPage(workspaceID, afterTime, afterID, limit)
	} else {
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this group store")
			return
		}
		groups, err = r.groupStore.ListByWorkspaceWithAccounts(workspaceID)
		if len(groups) > limit {
			hasMore = true
			groups = groups[:limit]
		}
	}
	if err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	if groups == nil {
		groups = []models.GroupWithAccounts{}
	}
	response := map[string]interface{}{"groups": groups, "has_more": hasMore}
	if hasMore && len(groups) > 0 {
		last := groups[len(groups)-1].Group
		response["next_cursor"] = encodeListCursorForContext("groups-aggregate", cursorContext, last.CreatedAt, strconv.FormatInt(last.ID, 10))
	}
	writeJSON(w, http.StatusOK, response)
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
