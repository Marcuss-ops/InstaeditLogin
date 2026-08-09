package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
	WorkspaceID       int64 `json:"workspace_id"`
	PlatformAccountID int64 `json:"platform_account_id"`
	// FolderID is required for Google Drive destinations and is stored
	// as destination-owned metadata. It is deliberately not read from
	// the Velox request or from the smoke-test environment.
	FolderID string          `json:"folder_id,omitempty"`
	Defaults json.RawMessage `json:"defaults"`
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

	if pa.Platform == models.PlatformGoogleDrive {
		if strings.TrimSpace(payload.FolderID) == "" {
			writeError(w, http.StatusUnprocessableEntity,
				"validation: folder_id is required for Google Drive destinations")
			return
		}
	} else {
		// Social destinations must be explicitly bound to the workspace.
		// Drive accounts are workspace-owned OAuth resources, not channels.
		binding, bindingErr := m.deps.WorkspaceStore.FindChannel(req.Context(), payload.WorkspaceID, payload.PlatformAccountID)
		if bindingErr != nil {
			slog.Error("velox destination: workspace channel lookup failed",
				"user_id", userID, "workspace_id", payload.WorkspaceID,
				"platform_account_id", payload.PlatformAccountID, "err", bindingErr)
			writeError(w, http.StatusInternalServerError, "workspace channel lookup failed")
			return
		}
		if binding == nil || !binding.Enabled {
			writeError(w, http.StatusUnprocessableEntity,
				"validation: platform_account_id is not enabled in this workspace")
			return
		}
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
	if pa.Platform == models.PlatformGoogleDrive {
		var defaultsMap map[string]json.RawMessage
		if err := json.Unmarshal(defaults, &defaultsMap); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "validation: defaults must be a JSON object")
			return
		}
		folderJSON, _ := json.Marshal(strings.TrimSpace(payload.FolderID))
		defaultsMap["folder_id"] = folderJSON
		defaultsMap["provider"] = json.RawMessage(`"google_drive"`)
		defaults, _ = json.Marshal(defaultsMap)
	} else {
		// These keys are reserved for destination-owned Drive routing;
		// never let a social destination opt out of its channel binding.
		var defaultsMap map[string]json.RawMessage
		if json.Unmarshal(defaults, &defaultsMap) == nil {
			delete(defaultsMap, "provider")
			delete(defaultsMap, "folder_id")
			defaults, _ = json.Marshal(defaultsMap)
		}
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
