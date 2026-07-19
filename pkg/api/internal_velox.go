package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ExternalDestinationStore is the persistence contract for
// external_destinations. Mirrors the WorkspaceStore / PostStore
// pattern declared in handlers.go: a local interface so tests
// can supply an in-memory fake without dragging the *sql.DB-bound
// *repository.ExternalDestinationRepository into the test fixture.
// The production wiring in cmd/server/main.go passes
// repository.NewExternalDestinationRepository(db) which satisfies
// this contract.
//
// Method scope is intentionally narrow — only the read methods
// the Velox validate path needs. Mutators (Create / Update /
// Delete) live on the SAME repository struct but are NOT in
// this interface because the API surface for service-to-service
// integrations today only reads; mutations go through the
// user-facing admin endpoint POST
// /api/v1/integrations/velox/destinations which uses a different
// (yet unwritten) writer path.
type ExternalDestinationStore interface {
	GetByID(ctx context.Context, id string) (*models.ExternalDestination, error)
}

// Compile-time assertion the production repository satisfies
// this interface. Mirrors the handlers.go convention for
// WorkspaceStore (var _ WorkspaceStore = ...
// (*repository.WorkspaceRepository)(nil)). Catches schema drift
// at go vet time, not at runtime.
var _ ExternalDestinationStore = (*repository.ExternalDestinationRepository)(nil)

// handleValidateInternalDestination implements
// POST /internal/v1/destinations/{id}/validate for the Velox
// integration contract.
//
// RATIONALE — five server-side checks:
//
//   1. Destination row exists.
//   2. Destination row enabled = TRUE.
//   3. Workspace row exists (workspaces has no archived_at column;
//      "attivo" maps to "row present"; FindByID non-nil == active).
//   4. Platform_account exists.
//   5. Platform_account NOT in reauth_required — both signals
//      (status enum + reauth_required_at timestamp) checked
//      defense-in-depth.
//
// All dependent stores (workspaceStore + userRepo) are read
// from Router fields DIRECTLY (not via a captured config
// struct). This avoids an option-order trap: a RouterOption
// that snapshots r.workspaceStore at option-call time would
// capture nil if the option order is wrong. The Router fields
// are always current at handler-time.
//
// Inconsistency note: a reauth_required destination returns 404
// (not 422) because the canonical Velox contract treats
// non-usable destinations as if they don't exist — the peer's
// only sane response is to drop the destination and reissue
// the URL with a fresh id. Returning a distinct status would
// leak existence.
//
// TOKEN REFRESHABILITY — see the file-level doc-comment at the
// registerInternalVeloxRoutes helper for the full rationale:
// /validate is a fast poll that DOES NOT touch the credential
// vault. Trust chain:
//   - platform_account.status = 'active'
//   - platform_account.reauth_required_at IS NULL
//
// A stale active-but-revoked-by-provider grant surfaces at
// publish time (publish_worker decrypts, refreshes, gets a 4xx,
// propagates to external_deliveries.status='blocked_auth').
// Phase-1 trust this near-miss rate; a future Taglio can add
// oauth_connections.last_validated_at as a freshness probe.
//
// RESPONSE — Velox consumes only the HTTP status code per
// spec; diagnostic JSON is OPT-IN via:
//
//   - ?diagnostic=true query parameter
//   - X-Velox-Diagnostic: true request header
//
// Both must be explicit "true" so a peer misconfiguration
// doesn't accidentally trigger the body variant (Velox's
// request layer forwards all headers by default; the explicit
// true gate avoids accidental triggering).
func (r *Router) handleValidateInternalDestination(w http.ResponseWriter, req *http.Request) {
	if r.externalDestinations == nil {
		writeError(w, http.StatusNotImplemented, "internal velox store not configured")
		return
	}
	id := req.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "destination id required")
		return
	}

	// 1. Destination lookup.
	dest, err := r.externalDestinations.GetByID(req.Context(), id)
	if err != nil {
		slog.Error("velox validate: destination lookup failed",
			"id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil || !dest.Enabled {
		// Disabled = 404 (uniform with not-found; doesn't leak
		// existence).
		writeError(w, http.StatusNotFound, "destination not found")
		return
	}

	// 2. Workspace lookup. Read directly from Router field —
	// avoids the option-order trap of capturing values at
	// WithExternalDestinationStore call time.
	if r.workspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}
	ws, err := r.workspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		slog.Error("velox validate: workspace lookup failed",
			"workspace_id", dest.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// 3. Platform_account lookup. Same direct-from-Router pattern.
	if r.userRepo == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}
	pa, err := r.userRepo.FindPlatformAccountByID(dest.PlatformAccountID)
	if err != nil {
		slog.Error("velox validate: platform_account lookup failed",
			"platform_account_id", dest.PlatformAccountID, "err", err)
		writeError(w, http.StatusInternalServerError, "platform_account lookup failed")
		return
	}
	if pa == nil {
		writeError(w, http.StatusNotFound, "platform_account not found")
		return
	}
	// Both reauth signals must be checked (migration 005
	// added reauth_required_at; status enum is the canonical
	// signal). They are redundant by design — checking both
	// ensures a partial migration that updates one without
	// the other still surfaces here.
	if pa.Status == "reauth_required" || pa.ReauthRequiredAt != nil {
		slog.Warn("velox validate: destination has reauth_required channel",
			"destination_id", id, "platform_account_id", pa.ID)
		writeError(w, http.StatusNotFound, "destination requires reauth")
		return
	}

	// 4. Diagnostic JSON trigger (explicit operator opt-in only).
	diagnostic := req.URL.Query().Get("diagnostic") == "true" ||
		req.Header.Get("X-Velox-Diagnostic") == "true"

	if diagnostic {
		writeJSON(w, http.StatusOK, VeloxValidateDestinationResponse{
			Valid:         true,
			DestinationID: dest.ID,
			Status:        "active",
			Platform:      pa.Platform,
		})
		return
	}

	// 5. Happy path: 204 No Content. Velox consumes only the
	// status code per spec.
	w.WriteHeader(http.StatusNoContent)
}

// VeloxValidateDestinationResponse is the diagnostic-mode body
// returned when ?diagnostic=true OR X-Velox-Diagnostic: true is
// set on the request. Stable shape — operators monitoring
// pattern-match on the values. Mirrors the user's spec tuple
// `{valid, destination_id, status, platform}` verbatim.
type VeloxValidateDestinationResponse struct {
	Valid         bool   `json:"valid"`
	DestinationID string `json:"destination_id"`
	Status        string `json:"status"`
	Platform      string `json:"platform"`
}

// registerInternalVeloxRoutes wires the /internal/v1/destination
// validate route. Called from Router.Setup(). Refuses to
// register if dependencies aren't wired (matches the
// WorkspaceStore / PostStore nil-guard pattern) — a server
// without WithExternalDestinationStore + WithVeloxAPIToken
// returns 404 for /internal/v1/* paths so the operator sees
// a clear "route not registered" rather than a 500.
//
// Boot-time guard rationale: if VELOX_API_TOKEN is empty AND
// destination store IS wired, the middleware returns 503 on
// every request. Better to NOT register the route at all so
// the operator sees a 404 in the logs and traces back the
// env config. Subsequent env rotation (process restart
// re-loads) restores the route.
func (r *Router) registerInternalVeloxRoutes() {
	if r.externalDestinations == nil || r.veloxAPIToken == "" {
		return
	}
	r.mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate",
		r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination)))
}

// WithExternalDestinationStore wires
// *repository.ExternalDestinationRepository into the Router.
// Following the WorkspaceStore / PostStore nil-guard pattern:
// when the option is omitted, /internal/v1 routes return 404
// (the helper refuses to register them). Production wiring
// in internal/bootstrap.Wire passes
// repository.NewExternalDestinationRepository(db).
//
// Plus WithVeloxAPIToken AND the user/workspace stores MUST
// be wired for the handler's full happy path. Calling only
// this option but not WithVeloxAPIToken leaves the route
// un-registered. cmd/server/main.go is responsible for
// wiring all three (or all four, including WithWorkspaceStore
// + WithUserStore which are normally wired earlier).
func WithExternalDestinationStore(s ExternalDestinationStore) RouterOption {
	return func(r *Router) { r.externalDestinations = s }
}
