package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// SetGroupAccountsRequest is the JSON body for PUT
// /api/v1/groups/{id}/accounts. The "set" semantics mirror the repo:
// wipe + re-insert in one tx.
type SetGroupAccountsRequest struct {
	AccountIDs []int64 `json:"account_ids"`
}

// handleRemoveGroupAccount detaches a single account from a group — the
// dedicated "Rimuovi dalla cartella" operation (the folder trash icon).
// It deletes only the group_accounts membership and resyncs the
// workspace_channels binding; platform_accounts and OAuth grants are
// never touched — disconnect and hard-delete live on /accounts.
//
// DELETE /api/v1/groups/{id}/accounts/{accountId}
func (r *Router) handleRemoveGroupAccount(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	groupID, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil || groupID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid group id")
		return
	}
	accountID, err := strconv.ParseInt(chi.URLParam(req, "accountId"), 10, 64)
	if err != nil || accountID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}
	existing, err := r.groupStore.FindByID(groupID)
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
	if err := r.groupStore.RemoveAccountFromGroupTx(req.Context(), groupID, existing.WorkspaceID, accountID); err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if r.workspaceStore != nil {
		for _, accountID := range validated {
			if _, err := r.workspaceStore.AttachChannel(req.Context(), existing.WorkspaceID, accountID, existing.Name); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to bind group channel: "+err.Error())
				return
			}
		}
	}
	out := validated
	if out == nil {
		out = []int64{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"account_ids": out})
}
