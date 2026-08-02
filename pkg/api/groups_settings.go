package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UpdateGroupSettingsRequest is the JSON body for PATCH
// /api/v1/groups/{id}/settings. Membership and account language metadata
// are persisted together by one repository transaction.
type UpdateGroupSettingsRequest struct {
	Accounts []models.GroupAccountLanguageUpdate `json:"accounts"`
}

// handleUpdateGroupSettings atomically saves the group's membership and
// each member's language metadata. The repository owns the SQL transaction;
// this handler only performs request parsing and workspace authentication.
//
// PATCH /api/v1/groups/{id}/settings
func (r *Router) handleUpdateGroupSettings(w http.ResponseWriter, req *http.Request) {
	if r.groupStore == nil {
		writeError(w, http.StatusNotImplemented, "groups not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid group id")
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
	callerID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	var body UpdateGroupSettingsRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	seen := make(map[int64]struct{}, len(body.Accounts))
	for _, account := range body.Accounts {
		if account.AccountID <= 0 {
			writeError(w, http.StatusBadRequest, "account_id must be positive")
			return
		}
		if _, duplicate := seen[account.AccountID]; duplicate {
			writeError(w, http.StatusBadRequest, "duplicate account_id")
			return
		}
		seen[account.AccountID] = struct{}{}
	}
	if err := r.groupStore.UpdateSettings(req.Context(), id, existing.WorkspaceID, callerID, body.Accounts); err != nil {
		status, msg := mapGroupError(err)
		writeError(w, status, msg)
		return
	}
	// Group membership is also a publishable workspace binding. Keep the
	// two projections in sync so the editor and Velox can resolve every
	// channel selected in the group.
	if r.workspaceStore != nil {
		for _, account := range body.Accounts {
			if _, err := r.workspaceStore.AttachChannel(req.Context(), existing.WorkspaceID, account.AccountID, existing.Name); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to bind group channel: "+err.Error())
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"group_id": id,
		"accounts": body.Accounts,
	})
}
