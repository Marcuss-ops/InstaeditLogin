package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// CreateVeloxDestinationRequest is the body for
// POST /api/v1/integrations/velox/destinations.
//
// The Velox worker references the resulting row by the opaque
// external_destination_id in /internal/v1/deliveries POSTs. The
// owner of this row resolves to a workspace + platform_account +
// OAuth token at runtime; Velox never sees workspace_id or
// platform_account_id verbatim.
//
// Defaults is generic json.RawMessage so future metadata additions
// (privacy_status, language, timezone, upload_defaults) slot in
// without a Go-struct change — the DB column is JSONB and the
// downstream worker decodes per-key as needed.
type CreateVeloxDestinationRequest struct {
	WorkspaceID       int64           `json:"workspace_id"`
	PlatformAccountID int64           `json:"platform_account_id"`
	Defaults          json.RawMessage `json:"defaults"`
}

// CreateVeloxDestinationResponse is the 201 body. Distinct shape
// from the standard writeError envelope so the SPA can
// pattern-match the field names reliably (external_destination_id
// always present when 201; status always "active" at creation).
//
// Status="active" reflects enabled=true (the create-row default);
// the row can later be flipped to disabled via PATCH/DELETE that
// this endpoint does not expose yet — see PATCH for the toggling
// path.
type CreateVeloxDestinationResponse struct {
	ExternalDestinationID string `json:"external_destination_id"`
	Status                string `json:"status"`
}

// UpdateVeloxDestinationRequest is the body for
// PATCH /api/v1/integrations/velox/destinations/{id}.
//
// Both fields are optional but at least one MUST be present; the
// handler rejects an empty body with 400 to prevent a no-op
// mutation that still re-stamps updated_at. JSON tags use lowercase
// snake_case to mirror the VeloxFrontend client
// (VeloxFrontend/web/src/lib/api/socialDestinationsApi.ts:
// updateSocialDestination(id, { defaults?: Record<string, unknown> })).
type UpdateVeloxDestinationRequest struct {
	Enabled  *bool           `json:"enabled,omitempty"`
	Defaults json.RawMessage `json:"defaults,omitempty"`
}

// veloxIntegrationSourceSystem is the source_system column value
// written on every destination this endpoint creates. Hardcoded
// today (matches veloxSourceSystemTag in internal_velox.go); a
// future multi-source extension (e.g. Dropbox joining the same
// code path) lifts this into a WithSourceSystem RouterOption.
const veloxIntegrationSourceSystem = "velox"

// handleCreateIntegrationVeloxDestination implements
// POST /api/v1/integrations/velox/destinations.
//
// AUTH — distinct from /internal/v1/* (which uses the
// service-to-service internalVeloxAuth Bearer middleware). This
// endpoint sits under the standard user JWT chain (auth.Manager
// middleware stamps auth.IdentityFromContext); adminIdentityUserID
// extracts the user_id from the same identity used by the admin
// handlers — the helper is misnamed but works for any
// authenticated caller.
//
// AUTHZ — 403 if the caller's user_id does NOT own the workspace.
// Strict ownership matches the user spec "403 se workspace
// non-owned"; team-membership does NOT extend here intentionally,
// so a misfired "link a workspace I belong to via team" doesn't
// get through user RBAC. A future Taglio can add ListByMember +
// check if needed.
//
// 422 for platform_account missing or not active/reauth_required
// (defense-in-depth: pa.Status AND pa.ReauthRequiredAt both
// checked, mirroring the validate handler). Matches user spec
// "422 se platform_account non esiste/non abilitato".
//
// IDEMPOTENCY — UNIQUE(source_system, workspace_id,
// platform_account_id) in migration 054 surfaces as
// repository.ErrExternalDestinationAlreadyExists. Handler maps
// that to 409 Conflict so a SPA double-click on "Connetti" doesn't
// surface as 500 Server Error.
//
// AUDIT — AuditLogStore.Log fires after a successful insert with
// event_type=external_destination_created and actor_id = user_id.
// Best-effort: a transient audit-store failure is logged + swallowed
// so a down audit_log table doesn't fail the user-visible insert.
func (m *IntegrationsModule) handleCreateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	if m.deps.ExternalDestinationStore == nil {
		writeError(w, http.StatusNotImplemented, "external destinations store not configured")
		return
	}
	if m.deps.WorkspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}
	if m.deps.UserStore == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}

	var payload CreateVeloxDestinationRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		slog.Warn("velox destination: invalid JSON", "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.WorkspaceID <= 0 || payload.PlatformAccountID <= 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: workspace_id and platform_account_id must be positive integers")
		return
	}

	userID := adminIdentityUserID(req)
	if userID == 0 {
		// Middleware should have already rejected unauthenticated
		// callers; this is the defensive fallback when a
		// mis-wired Router exposes the route without auth.
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}

	// Workspace ownership check (403 if not owner).
	ws, err := m.deps.WorkspaceStore.FindByID(payload.WorkspaceID)
	if err != nil {
		slog.Error("velox destination: workspace lookup failed",
			"user_id", userID, "workspace_id", payload.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil || ws.OwnerID != userID {
		// Collapse "no such workspace" + "not yours" to the same
		// 403 so a probing caller can't enumerate workspace ids.
		writeError(w, http.StatusForbidden, "workspace not owned by caller")
		return
	}

	// Platform_account enablement check (422 if missing/disabled).
	pa, err := m.deps.UserStore.FindPlatformAccountByID(payload.PlatformAccountID)
	if err != nil {
		slog.Error("velox destination: platform_account lookup failed",
			"user_id", userID, "platform_account_id", payload.PlatformAccountID, "err", err)
		writeError(w, http.StatusInternalServerError, "platform_account lookup failed")
		return
	}
	if pa == nil {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: platform_account_id not found")
		return
	}
	if pa.Status != "active" || pa.ReauthRequiredAt != nil {
		// Both signals checked defense-in-depth: migration 005
		// added reauth_required_at; the status enum is the
		// canonical signal. Checking both keeps us honest across
		// partial-migration scenarios.
		writeError(w, http.StatusUnprocessableEntity,
			"validation: platform_account is not active (status or reauth_required_at set)")
		return
	}

	// Mint opaque ULID-style id "extdst_01J…"
	destID, err := services.GenerateVeloxDestinationID()
	if err != nil {
		slog.Error("velox destination: id mint failed", "err", err)
		writeError(w, http.StatusInternalServerError, "id mint failed")
		return
	}

	// Normalize empty / missing defaults to "{}" so the jsonb
	// column always contains a parseable JSON object. The repo
	// defends against invalid JSON, but normalising here keeps
	// the wire boundary predictable.
	defaults := payload.Defaults
	if len(strings.TrimSpace(string(defaults))) == 0 {
		defaults = json.RawMessage("{}")
	}

	dest := &models.ExternalDestination{
		ID:                destID,
		SourceSystem:      veloxIntegrationSourceSystem,
		WorkspaceID:       payload.WorkspaceID,
		PlatformAccountID: payload.PlatformAccountID,
		Enabled:           true,
		DefaultMetadata:   defaults,
	}
	if err := m.deps.ExternalDestinationStore.Create(req.Context(), dest); err != nil {
		if errors.Is(err, repository.ErrExternalDestinationAlreadyExists) {
			writeError(w, http.StatusConflict,
				"destination already linked for this (workspace_id, platform_account_id) triple")
			return
		}
		slog.Error("velox destination: create failed",
			"user_id", userID, "workspace_id", payload.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "destination create failed")
		return
	}

	// Audit log: best-effort, do not fail the user-visible insert.
	if m.deps.AuditLogStore != nil {
		if err := m.deps.AuditLogStore.Log(req.Context(),
			"external_destination_created",
			strconv.FormatInt(userID, 10),
			"external_destination",
			destID,
			map[string]interface{}{
				"workspace_id":        payload.WorkspaceID,
				"platform_account_id": payload.PlatformAccountID,
				"source_system":       veloxIntegrationSourceSystem,
			},
		); err != nil {
			slog.Warn("velox destination: audit log failed",
				"external_destination_id", destID, "err", err)
		}
	}

	slog.Info("velox destination: created",
		"external_destination_id", destID,
		"user_id", userID,
		"workspace_id", payload.WorkspaceID,
		"platform_account_id", payload.PlatformAccountID,
	)

	writeJSON(w, http.StatusCreated, CreateVeloxDestinationResponse{
		ExternalDestinationID: destID,
		Status:                "active",
	})
}

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

