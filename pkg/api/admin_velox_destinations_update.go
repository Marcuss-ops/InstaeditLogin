package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// handleDeleteIntegrationVeloxDestination implements
// DELETE /api/v1/integrations/velox/destinations/{id}.
//
// Hard-removes the destination row. Returns 204 No Content on
// success. The handler verifies the destination belongs to a
// workspace the caller owns (404 on mismatch). If the destination
// has dependent deliveries (FK RESTRICT), the repository returns
// ErrExternalDestinationHasDependents which maps to 409 Conflict.
// An audit log entry is written on success (best-effort).
func (m *IntegrationsModule) handleDeleteIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
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

	// Fetch the row first so we can check ownership before deleting.
	dest, err := m.deps.ExternalDestinationStore.GetByID(req.Context(), destID)
	if err != nil {
		slog.Error("velox destination delete: lookup failed",
			"id", destID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	// Ownership check: 404 (not 403) on mismatch.
	ws, err := m.deps.WorkspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		slog.Error("velox destination delete: workspace lookup failed",
			"id", destID, "workspace_id", dest.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	if err := m.deps.ExternalDestinationStore.Delete(req.Context(), destID); err != nil {
		if errors.Is(err, repository.ErrExternalDestinationHasDependents) {
			writeError(w, http.StatusConflict,
				"destination has dependent deliveries; disable instead of deleting")
			return
		}
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		slog.Error("velox destination delete: failed",
			"id", destID, "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination delete failed")
		return
	}

	// Audit log: best-effort.
	if m.deps.AuditLogStore != nil {
		if err := m.deps.AuditLogStore.Log(req.Context(),
			"external_destination_deleted",
			strconv.FormatInt(userID, 10),
			"external_destination",
			destID,
			map[string]interface{}{
				"workspace_id":        dest.WorkspaceID,
				"platform_account_id": dest.PlatformAccountID,
				"source_system":       dest.SourceSystem,
			},
		); err != nil {
			slog.Warn("velox destination delete: audit log failed",
				"external_destination_id", destID, "err", err)
		}
	}

	slog.Info("velox destination: deleted",
		"external_destination_id", destID,
		"user_id", userID,
		"workspace_id", dest.WorkspaceID,
	)

	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateIntegrationVeloxDestination implements
// PATCH /api/v1/integrations/velox/destinations/{id}.
//
// AUTH/AUTHZ — same as GET-by-id and DELETE: 401 if no user identity,
// 404 if the row does not exist OR the destination's workspace is
// not owned by the caller (collapses "not yours" with "does not
// exist" to prevent id enumeration).
//
// BODY — JSON object containing any subset of { enabled: bool,
// defaults: object }. At least one field MUST be present (a no-op
// PATCH is rejected with 400 to avoid re-stamping updated_at for no
// observable change). Defaults must be valid JSON if present.
//
// RESPONSE — 200 with the refreshed VeloxDestinationResponse so the
// frontend can pick up the new updated_at + defaults without a
// follow-up GET roundtrip.
//
// IDEMPOTENT — same body applied twice yields the same final state
// (only updated_at bumps on each call). The repo calls
// UpdateEnabledAndDefaults, which performs a single atomic UPDATE;
// it surfaces ErrExternalDestinationNotFound → 404 so a concurrent
// DELETE between authz and update degrades safely without a 500.
func (m *IntegrationsModule) handleUpdateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
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

	// Fetch first so we can check ownership before any mutation.
	dest, err := m.deps.ExternalDestinationStore.GetByID(req.Context(), destID)
	if err != nil {
		slog.Error("velox destination update: lookup failed", "id", destID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	ws, err := m.deps.WorkspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		slog.Error("velox destination update: workspace lookup failed",
			"id", destID, "workspace_id", dest.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	var payload UpdateVeloxDestinationRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		slog.Warn("velox destination update: invalid JSON", "id", destID, "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Validation: at least one field must be present and defaults
	// must be valid JSON if non-empty.
	defaultsTrimmed := strings.TrimSpace(string(payload.Defaults))
	if payload.Enabled == nil && len(defaultsTrimmed) == 0 {
		writeError(w, http.StatusBadRequest, "validation: at least one of enabled, defaults is required")
		return
	}
	if len(defaultsTrimmed) > 0 && !json.Valid(payload.Defaults) {
		writeError(w, http.StatusBadRequest, "validation: defaults is not valid JSON")
		return
	}

	// Apply mutations in a SINGLE atomic postgres UPDATE via
	// COALESCE — this closes the partial-write window that a
	// previous two-verb sequence left open: a concurrent DELETE
	// between independent UPDATEs could leave the row half-updated.
	// The combined verb returns ErrExternalDestinationNotFound on
	// zero rows (concurrent DELETE finished after our authz GetByID)
	// which maps to 404. The audit-log shape stays exactly the same:
	// {enabled, defaults_changed} keyed by the PATCH body, not
	// by the post-UPDATE row state.
	//
	// `defaultsToUpdate` collapses the body-supplied defaults to
	// either the raw bytes (when the body contained any non-empty
	// payload, including the literal `"null"`) or a zero-length
	// json.RawMessage (which the repo binds as SQL NULL so
	// COALESCE preserves the existing default_metadata column).
	auditDeltas := VeloxDestinationUpdateAuditDeltas{
		Enabled:         payload.Enabled,
		DefaultsChanged: len(defaultsTrimmed) > 0,
	}
	defaultsToUpdate := json.RawMessage("")
	if len(defaultsTrimmed) > 0 {
		defaultsToUpdate = payload.Defaults
	}
	if err := m.deps.ExternalDestinationStore.UpdateEnabledAndDefaults(req.Context(), destID, payload.Enabled, defaultsToUpdate); err != nil {
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, "destination not found")
			return
		}
		slog.Error("velox destination update: failed",
			"id", destID, "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination update failed")
		return
	}

	// Refresh for the response — picks up the new updated_at. A
	// nil row here means concurrent DELETE finished after our last
	// update; map to 404 to keep the contract consistent.
	dest, err = m.deps.ExternalDestinationStore.GetByID(req.Context(), destID)
	if err != nil {
		slog.Error("velox destination update: refresh failed", "id", destID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination refresh failed")
		return
	}
	if dest == nil {
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	// Audit log: best-effort — never fails the user-visible write.
	if m.deps.AuditLogStore != nil {
		if err := m.deps.AuditLogStore.Log(req.Context(),
			"external_destination_updated",
			strconv.FormatInt(userID, 10),
			"external_destination",
			destID,
			auditMetadataFromDeltas(auditDeltas),
		); err != nil {
			slog.Warn("velox destination update: audit log failed",
				"external_destination_id", destID, "err", err)
		}
	}

	slog.Info("velox destination: updated",
		"external_destination_id", destID,
		"user_id", userID,
		"workspace_id", dest.WorkspaceID,
		"audit_deltas", auditDeltas,
	)

	writeJSON(w, http.StatusOK, toVeloxDestinationResponse(dest))
}

// auditMetadataFromDeltas converts a typed VeloxDestinationUpdateAuditDeltas
// into the map[string]interface{} shape that AuditLogStore.Log expects,
// pinning the emitted JSON keys exactly via Marshal/Unmarshal. The
// round-trip is deterministic for the simple-primitive struct we
// expose; the swallowed errors are safe because none of the fields
// can produce unserialisable data or a non-encodable value.
func auditMetadataFromDeltas(d VeloxDestinationUpdateAuditDeltas) map[string]interface{} {
	b, _ := json.Marshal(d)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	return m
}
