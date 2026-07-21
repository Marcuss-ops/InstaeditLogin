package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// --- Request types ----------------------------------------------------------

// AttachWorkspaceChannelRequest is the JSON body for
// POST /api/v1/workspaces/{id}/channels. `workspace_id` is taken
// from the URL (and ownership-checked server-side) so the client
// cannot cross tenants by stuffing a foreign workspace_id in the
// body.
type AttachWorkspaceChannelRequest struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	GroupName         string `json:"group_name,omitempty"`
}

// UpdateWorkspaceChannelRequest is the JSON body for PATCH
// /api/v1/workspaces/{id}/channels/{accountId}. Both fields are
// optional and use pointer encoding so the SQL COALESCE pattern
// preserves whatever the caller did not specify. To clear the
// group_name, pass empty string; to leave it intact, omit the key
// (or pass nil).
type UpdateWorkspaceChannelRequest struct {
	GroupName *string `json:"group_name,omitempty"`
	Enabled   *bool   `json:"enabled,omitempty"`
}

// --- Error mapping ----------------------------------------------------------

// mapWorkspaceChannelError translates repo errors for the
// workspace_channels endpoints into HTTP statuses. Reuses the
// existing mapWorkspaceError sentinel for ErrWorkspaceNotFound (404).
// Foreign-key violations (pq SQLSTATE 23503, e.g. supplied
// platform_account_id does not exist) surface as 400 Bad Request —
// the operator's request is the cause, not a system failure. Every
// other error falls through to 500 Internal Server Error (transient
// DB blips, lock timeouts, deadlocks) so the operator sees the
// correct retry semantics — a 400 on a transient error would mislead
// the dashboard into thinking the input was malformed.
func mapWorkspaceChannelError(err error) (int, string) {
	status, msg := mapWorkspaceError(err)
	if status != http.StatusOK {
		return status, msg
	}
	if err == nil {
		return http.StatusOK, ""
	}
	// pq SQLSTATE 23503 (foreign_key_violation) — the operator
	// supplied an id that does not exist in the parent table.
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23503" {
		return http.StatusBadRequest, "foreign key violation: " + pqErr.Message
	}
	return http.StatusInternalServerError, "failed to process workspace channel: " + err.Error()
}

// --- Handlers ---------------------------------------------------------------

// handleAttachWorkspaceChannel attaches a platform_account to a
// workspace under an optional group_name. Idempotent via the
// repository's ON CONFLICT DO UPDATE — re-calling with a different
// group_name rewrites the binding's group_name and is the canonical
// way to move a channel between groups without a separate UPDATE
// endpoint roundtrip.
//
// POST /api/v1/workspaces/{id}/channels
func (r *Router) handleAttachWorkspaceChannel(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	wsID, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, wsID); !ok {
		return
	}
	var body AttachWorkspaceChannelRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.PlatformAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required and must be > 0")
		return
	}
	channel, err := r.workspaceStore.AttachChannel(req.Context(), wsID, body.PlatformAccountID, body.GroupName)
	if err != nil {
		status, msg := mapWorkspaceChannelError(err)
		writeError(w, status, msg)
		return
	}
	writeJSON(w, http.StatusCreated, channel)
}

// handleListWorkspaceChannels returns every channel bound to the
// workspace, ordered by created_at DESC. The frontend uses this for
// the "Channels" tab in the workspace dashboard.
//
// GET /api/v1/workspaces/{id}/channels
func (r *Router) handleListWorkspaceChannels(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	wsID, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, wsID); !ok {
		return
	}
	channels, err := r.workspaceStore.ListChannels(req.Context(), wsID)
	if err != nil {
		status, msg := mapWorkspaceChannelError(err)
		writeError(w, status, msg)
		return
	}
	if channels == nil {
		channels = []models.WorkspaceChannel{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"channels": channels})
}

// handleUpdateWorkspaceChannel mutates an existing binding's
// group_name and/or enabled flag. Both fields are optional; the
// repository's COALESCE pattern preserves whatever the caller does
// not specify. Passing an empty string group_name clears the group
// (NULLIF maps to SQL NULL).
//
// PATCH /api/v1/workspaces/{id}/channels/{accountId}
func (r *Router) handleUpdateWorkspaceChannel(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	wsID, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, wsID); !ok {
		return
	}
	accountID, ok := parsePathIDAsInt64(w, req, "accountId")
	if !ok {
		return
	}
	var body UpdateWorkspaceChannelRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.GroupName == nil && body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "group_name or enabled required")
		return
	}
	if err := r.workspaceStore.UpdateChannel(req.Context(), wsID, accountID, body.GroupName, body.Enabled); err != nil {
		status, msg := mapWorkspaceChannelError(err)
		writeError(w, status, msg)
		return
	}
	// PK-indexed single-row read-back (cheap; no O(N) workspace scan).
	channel, err := r.workspaceStore.FindChannel(req.Context(), wsID, accountID)
	if err != nil {
		// The UPDATE itself succeeded — surface the read-back
		// failure as 500 so the operator knows the persisted
		// state is uncertain. 204 would mask the race window
		// where the row was deleted between UPDATE and SELECT.
		status, msg := mapWorkspaceChannelError(err)
		writeError(w, status, msg)
		return
	}
	if channel == nil {
		// Race: the row was deleted between UPDATE and SELECT.
		// 404 is honest; the operator can re-POST to recreate it.
		writeError(w, http.StatusNotFound, "workspace channel not found after update")
		return
	}
	writeJSON(w, http.StatusOK, channel)
}

// handleDetachWorkspaceChannel removes the (workspace_id,
// platform_account_id) binding. Idempotent at the SQL level but
// the handler returns 404 when no row matched — the operator
// expects REST semantics for DELETE.
//
// DELETE /api/v1/workspaces/{id}/channels/{accountId}
func (r *Router) handleDetachWorkspaceChannel(w http.ResponseWriter, req *http.Request) {
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	wsID, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	if ok, _ := r.requireWorkspaceOwnership(w, req, wsID); !ok {
		return
	}
	accountID, ok := parsePathIDAsInt64(w, req, "accountId")
	if !ok {
		return
	}
	if err := r.workspaceStore.DetachChannel(req.Context(), wsID, accountID); err != nil {
		status, msg := mapWorkspaceChannelError(err)
		writeError(w, status, msg)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
