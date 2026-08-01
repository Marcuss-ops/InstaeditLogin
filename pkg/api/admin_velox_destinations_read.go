package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// VeloxDestinationResponse is the wire shape for a single destination
// returned by GET /api/v1/integrations/velox/destinations/{id} and
// each element of GET /api/v1/integrations/velox/destinations (list).
// WorkspaceID is included so the handler can verify ownership before
// returning the row; it is NOT serialized to the browser (json:"-").
//
// Status mirrors the enabled column: "active" when enabled=true,
// "disabled" when enabled=false. The SPA renders this as a badge.
type VeloxDestinationResponse struct {
	ExternalDestinationID string          `json:"external_destination_id"`
	WorkspaceID           int64           `json:"-"`
	PlatformAccountID     int64           `json:"platform_account_id"`
	SourceSystem          string          `json:"source_system"`
	Status                string          `json:"status"`
	Defaults              json.RawMessage `json:"defaults"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

// toVeloxDestinationResponse converts a models.ExternalDestination to
// the wire response. Status is derived from the Enabled flag.
func toVeloxDestinationResponse(d *models.ExternalDestination) VeloxDestinationResponse {
	status := "disabled"
	if d.Enabled {
		status = "active"
	}
	return VeloxDestinationResponse{
		ExternalDestinationID: d.ID,
		WorkspaceID:           d.WorkspaceID,
		PlatformAccountID:     d.PlatformAccountID,
		SourceSystem:          d.SourceSystem,
		Status:                status,
		Defaults:              d.DefaultMetadata,
		CreatedAt:             d.CreatedAt,
		UpdatedAt:             d.UpdatedAt,
	}
}

// handleListIntegrationVeloxDestinations implements
// GET /api/v1/integrations/velox/destinations?workspace_id=<int>.
//
// Returns all destinations for the caller's workspace. The
// workspace_id query parameter is required; the handler verifies
// the caller owns it (403 if not). Only enabled destinations are
// returned by default; pass ?include_disabled=true to include
// disabled rows.
func (m *IntegrationsModule) handleListIntegrationVeloxDestinations(w http.ResponseWriter, req *http.Request) {
	if m.deps.ExternalDestinationStore == nil {
		writeError(w, http.StatusNotImplemented, "external destinations store not configured")
		return
	}
	if m.deps.WorkspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}

	userID := adminIdentityUserID(req)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}

	wsIDStr := req.URL.Query().Get("workspace_id")
	if wsIDStr == "" {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required")
		return
	}
	workspaceID, err := strconv.ParseInt(wsIDStr, 10, 64)
	if err != nil || workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return
	}

	// Workspace ownership check (403 if not owner).
	ws, err := m.deps.WorkspaceStore.FindByID(workspaceID)
	if err != nil {
		slog.Error("velox destination list: workspace lookup failed",
			"user_id", userID, "workspace_id", workspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by caller")
		return
	}

	enabledOnly := true
	if req.URL.Query().Get("include_disabled") == "true" {
		enabledOnly = false
	}

	dests, err := m.deps.ExternalDestinationStore.ListByWorkspace(req.Context(), workspaceID, enabledOnly)
	if err != nil {
		slog.Error("velox destination list: query failed",
			"workspace_id", workspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination list failed")
		return
	}

	// Defense-in-depth: filter out any row whose WorkspaceID does
	// not match (a misconfigured query should never leak cross-
	// tenant rows, but the filter costs nothing).
	safe := make([]VeloxDestinationResponse, 0, len(dests))
	for i := range dests {
		if dests[i].WorkspaceID != workspaceID {
			continue
		}
		safe = append(safe, toVeloxDestinationResponse(&dests[i]))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"destinations": safe,
	})
}

// handleGetIntegrationVeloxDestination implements
// GET /api/v1/integrations/velox/destinations/{id}.
//
// Returns a single destination by its opaque id. The handler
// verifies the destination belongs to a workspace the caller owns
// (404 on mismatch — collapses "not yours" with "does not exist"
// so the caller cannot enumerate by id).
func (m *IntegrationsModule) handleGetIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	if m.deps.ExternalDestinationStore == nil {
		writeError(w, http.StatusNotImplemented, "external destinations store not configured")
		return
	}
	if m.deps.WorkspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}

	userID := adminIdentityUserID(req)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}

	destID := chi.URLParam(req, "id")
	if destID == "" {
		writeError(w, http.StatusBadRequest, "destination id required")
		return
	}

	dest, err := m.deps.ExternalDestinationStore.GetByID(req.Context(), destID)
	if err != nil {
		slog.Error("velox destination get: lookup failed",
			"id", destID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	// Ownership check: the destination's workspace must be owned by
	// the caller. 404 (not 403) on mismatch so the caller cannot
	// enumerate by id.
	ws, err := m.deps.WorkspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		slog.Error("velox destination get: workspace lookup failed",
			"id", destID, "workspace_id", dest.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	writeJSON(w, http.StatusOK, toVeloxDestinationResponse(dest))
}
