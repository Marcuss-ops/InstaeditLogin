package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// CreateWorkspaceRequest is the JSON body for POST /workspaces/.
type CreateWorkspaceRequest struct {
	Name string `json:"name"`
}

// mapWorkspaceError translates repo errors for the workspace endpoints into
// HTTP statuses. Shares the same policy as mapRepoError: ErrWorkspaceNotFound
// and sql.ErrNoRows both map to 404 (indistinguishable — both mean "no row at
// this id"). Unknown errors fall through to 500.
func mapWorkspaceError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, repository.ErrWorkspaceNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "workspace not found"
	default:
		return http.StatusInternalServerError, "failed to process workspace: " + err.Error()
	}
}

// handleCreateWorkspace creates a workspace owned by the authenticated user.
// OwnerID is populated from JWT context (strict) or omitted (lenient).
func (r *Router) handleCreateWorkspace(w http.ResponseWriter, req *http.Request) {
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}
	var body CreateWorkspaceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	ws := &models.Workspace{Name: body.Name, OwnerID: userID}
	if err := r.workspaceRepo.Create(ws); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workspace: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// handleListWorkspaces returns every workspace owned by the authenticated user.
func (r *Router) handleListWorkspaces(w http.ResponseWriter, req *http.Request) {
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}
	list, err := r.workspaceRepo.ListByOwner(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces: "+err.Error())
		return
	}
	if list == nil {
		list = []models.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"workspaces": list})
}

// handleGetWorkspace fetches a single workspace by id.
// 404 if the id doesn't match any row; 500 on driver errors.
func (r *Router) handleGetWorkspace(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	ws, err := r.workspaceRepo.FindByID(id)
	status, msg := mapWorkspaceError(err)
	if status != http.StatusOK {
		writeError(w, status, msg)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// handleDeleteWorkspace removes a workspace by id. Only workspace owners
// may delete; cross-tenant attempts are mapped to 403, missing rows to 404.
func (r *Router) handleDeleteWorkspace(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}
	// Pre-check existence + ownership so we can return 404 vs 403 distinctly.
	existing, err := r.workspaceRepo.FindByID(id)
	status, msg := mapWorkspaceError(err)
	if status == http.StatusNotFound {
		writeError(w, status, msg)
		return
	}
	if err != nil {
		writeError(w, status, msg)
		return
	}
	if existing.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return
	}
	if err := r.workspaceRepo.Delete(id); err != nil {
		status, msg := mapWorkspaceError(err)
		writeError(w, status, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
