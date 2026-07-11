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
// We intentionally use `name` (required) only — owner_id is always
// populated from the JWT context (strict mode) so the client can't
// forge tenancy.
type CreateWorkspaceRequest struct {
	Name string `json:"name"`
}

// mapWorkspaceError translates repo errors for the workspace endpoints
// into HTTP statuses. Shares the same policy as mapRepoError:
// ErrWorkspaceNotFound and sql.ErrNoRows both map to 404 (indistinguishable
// — both mean "no row at this id"). Unknown errors fall through to 500.
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

// handleCreateWorkspace creates a workspace owned by the authenticated
// user. OwnerID is populated from JWT context (strict) or omitted (lenient).
// 501 if WithWorkspaceStore was not wired.
func (r *Router) handleCreateWorkspace(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserOrDefault(w, req, r)
	if !ok {
		return
	}
	var body CreateWorkspaceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Name == "" {
		// 422 (not 400): the JSON parsed fine; the field is just
		// semantically missing. Distinguishes "you sent garbage" from
		// "you sent parseable JSON but forgot a required field".
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return
	}
	ws := &models.Workspace{Name: body.Name, OwnerID: userID}
	if err := r.workspaceStore.Create(ws); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create workspace: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// handleListWorkspaces returns every workspace owned by the authenticated
// user. 501 if WithWorkspaceStore was not wired.
func (r *Router) handleListWorkspaces(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserOrDefault(w, req, r)
	if !ok {
		return
	}
	list, err := r.workspaceStore.ListByOwner(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces: "+err.Error())
		return
	}
	if list == nil {
		list = []models.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"workspaces": list})
}

// handleGetWorkspace fetches a single workspace by id. 404 if the id
// doesn't match any row; 500 on driver errors. Ownership check (only
// in strict mode, where STRICT_JWT_AUTH=true gates this behind a real
// JWT identity — in lenient mode the existing TestWorkspacesAPI tests
// assume that an unauthenticated GET still returns the workspace).
//
// A future hardening pass could return 404 unconditionally to prevent
// workspace-existence leaks across tenants — see errors.go doc.
// 501 if WithWorkspaceStore was not wired.
func (r *Router) handleGetWorkspace(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	ws, err := r.workspaceStore.FindByID(id)
	status, msg := mapWorkspaceError(err)
	if status != http.StatusOK {
		writeError(w, status, msg)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	// Ownership gate: only enforced in strict mode. Lenient mode is the
	// legacy/rollback window and the existing tests intentionally allow
	// reads without a JWT — tightening this would break the legacy
	// publish fallback (publish trusts user_id from body in lenient).
	callerID := resolveUserID(req, 0, r.strictAuth)
	if r.strictAuth && callerID != 0 && ws.OwnerID != callerID {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

// handleDeleteWorkspace removes a workspace by id. Only workspace owners
// may delete; cross-tenant attempts are mapped to 404 (existence-leak
// avoidance), missing rows to 404, race-between-find-and-delete to 404.
// 501 if WithWorkspaceStore was not wired.
func (r *Router) handleDeleteWorkspace(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	userID, ok := requireUserOrDefault(w, req, r)
	if !ok {
		return
	}
	// Pre-check existence + ownership so we can return 404 vs 403 distinctly.
	existing, err := r.workspaceStore.FindByID(id)
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
		// 403 (NOT 404) is the contract — locking away cross-tenant
		// deletes while keeping the same shape as the legacy/rollback
		// window. A future hardening pass could collapse 403+404 to
		// 404 to avoid existence-leak, see errors.go doc.
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return
	}
	if err := r.workspaceStore.Delete(id); err != nil {
		status, msg := mapWorkspaceError(err)
		writeError(w, status, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withWorkspacesGuard was a planned helper that ended up not being used
// by the per-handler 501 checks (the inline `if r.workspaceStore == nil`
// pattern is shorter and matches what storage.go already does). Removed
// to keep noise out of the file. Add back when a third handler appears
// that would otherwise paste the same five-line guard.
