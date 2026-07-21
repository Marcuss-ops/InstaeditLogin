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
// the row can later be flipped to disabled via PUT/DELETE that
// this endpoint does not expose yet.
type CreateVeloxDestinationResponse struct {
	ExternalDestinationID string `json:"external_destination_id"`
	Status                string `json:"status"`
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
func (r *Router) handleCreateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	if r.externalDestinations == nil {
		writeError(w, http.StatusNotImplemented, "external destinations store not configured")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}
	if r.userRepo == nil {
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
	ws, err := r.workspaceStore.FindByID(payload.WorkspaceID)
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
	pa, err := r.userRepo.FindPlatformAccountByID(payload.PlatformAccountID)
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
	if err := r.externalDestinations.Create(req.Context(), dest); err != nil {
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
	if r.auditLogStore != nil {
		if err := r.auditLogStore.Log(req.Context(),
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

// registerUserVeloxDestinations mounts the user-facing Velox
// integration routes on r.mux. Called from Router.Setup() (or
// equivalent per-feature registration loop). Refuses to register
// when the required dependencies are unwired so a partial
// production deployment surfaces 404 (route not mounted) rather
// than 500.
//
// The handler is wrapped in the standard user JWT + CSRF chain
// (authMiddleware → csrfMiddleware). Note that r.auth /
// r.csrfMiddleware / r.authMiddleware field names depend on the
// Router struct definition; this file expects the helper-mnemonic
// naming already established by the team. If the field naming
// differs in handlers.go, adjust here.
func (r *Router) registerUserVeloxDestinations() {
	if r.mux == nil {
		return
	}
	if r.externalDestinations == nil || r.workspaceStore == nil ||
		r.userRepo == nil || r.auditLogStore == nil {
		// Missing dep = unmounted route. Operator sees 404 chi,
		// not 500 server-error. Matches the nil-guard pattern
		// used by the rest of pkg/api/.
		return
	}
	// handler is typed http.Handler (NOT http.HandlerFunc) so the
	// subsequent r.csrfMiddleware / r.authMiddleware assignments
	// compile — both middleware funcs return http.Handler. The
	// underlying value is still an http.HandlerFunc (a func literal
	// that implements http.Handler) so the dispatch semantics are
	// identical to a HandlerFunc-only declaration.
	var handler http.Handler = http.HandlerFunc(r.handleCreateIntegrationVeloxDestination)
	// Wrap with the project's canonical user-auth + CSRF chain.
	// Order: CSRF first (rejects bad-cookie callers BEFORE we do
	// any DB work), then auth (extracts JWT identity for handlers
	// downstream). Adjust to match the actual field names exposed
	// on Router in handlers.go if they differ.
	if r.csrfMiddleware != nil {
		handler = r.csrfMiddleware(handler)
	}
	if r.authMiddleware != nil {
		handler = r.authMiddleware(handler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/integrations/velox/destinations", handler)
}
