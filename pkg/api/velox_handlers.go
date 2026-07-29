package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// NOTE — handleCreateInternalDelivery was relocated to
// `pkg/api/deliveries_create.go` (out of `pkg/api/internal/` because
// Go's internal-visibility rule prevents a file in the internal
// subtree from referencing symbols in its parent package; the
// handler shares types like VeloxModule, writeError, sha256HexRegex,
// mimeAllowlist that live in a sibling file). Imports above are
// pruned to the GET/validate handlers + option functions in this
// file only. New types added by the relocation live in
// `deliveries_create.go` (VeloxDeliverContractRequest,
// VeloxDeliverContractResponse, VeloxContractIdempotencyKeyRegex).


// handleGetInternalDelivery implements
// GET /internal/v1/deliveries/{id} for the Velox integration
// contract. Velox uses this for reconciliation/poll when its
// callback channel drops a packet (network blip, peer restarts,
// webhook 5xx storm). Returns a small JSON shape with the
// delivery's authoritative state at lookup time.
//
// 404 is reserved for "id never accepted" — distinct from "we
// accepted then lost it" semantics. We deliberately collapse
// unknown-id and rejected/cancelled rows into 404 so the
// caller cannot use the response to enumerate row ids.
//
// 401 (Bearer missing) AND 403 (token mismatch) AND 503 (token
// not configured) are emitted by the internalVeloxAuth
// middleware BEFORE this handler runs; the spec is satisfied
// via the middleware's existing behaviour, no per-handler code.
func (m *VeloxModule) handleGetInternalDelivery(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "delivery id required")
		return
	}

	delivery, err := m.deps.ExternalDeliveryStore.GetByID(req.Context(), id)
	if err != nil {
		slog.Error("velox get delivery: lookup failed",
			"social_delivery_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "delivery lookup failed")
		return
	}
	if delivery == nil {
		writeError(w, http.StatusNotFound, "delivery not found")
		return
	}

	resp := VeloxGetDeliveryResponse{
		ID:        delivery.ID,
		Status:    string(delivery.Status),
		CreatedAt: delivery.CreatedAt,
		UpdatedAt: delivery.UpdatedAt,
	}
	// Surface LastErrorCode + Message verbatim. omitempty drops
	// the field on rows that haven't seen a failed transition
	// yet (the brand-new accepted row).
	if delivery.LastErrorCode != nil {
		resp.LastErrorCode = *delivery.LastErrorCode
		// retry_wait_reason mirrors last_error_code ONLY when
		// status == retry_wait — the operator's "why is this
		// sitting in retry?" question is answered by this field.
		// In any other state the field is empty.
		if delivery.Status == models.ExternalDeliveryStatusRetryWait {
			resp.RetryWaitReason = *delivery.LastErrorCode
		}
	}
	if delivery.LastErrorMessage != nil {
		resp.LastErrorMessage = *delivery.LastErrorMessage
	}
	if delivery.PlatformMediaID != nil {
		resp.PlatformMediaID = *delivery.PlatformMediaID
	}
	if delivery.PlatformURL != nil {
		resp.PlatformURL = *delivery.PlatformURL
	}
	// published_at is ONLY set when the row reached the published
	// terminal state. For other terminal states (failed,
	// dead_letter) the user spec explicitly maps "published_at"
	// to the success path.
	if delivery.Status == models.ExternalDeliveryStatusPublished &&
		delivery.CompletedAt != nil {
		resp.PublishedAt = delivery.CompletedAt
	}

	slog.Info("velox get delivery",
		"social_delivery_id", delivery.ID,
		"status", delivery.Status,
	)
	writeJSON(w, http.StatusOK, resp)
}

// handleValidateInternalDestination implements
// POST /internal/v1/destinations/{id}/validate for the Velox
// integration contract.
//
// RATIONALE — five server-side checks:
//
//  1. Destination row exists.
//  2. Destination row enabled = TRUE.
//  3. Workspace row exists (workspaces has no archived_at column;
//     "attivo" maps to "row present"; FindByID non-nil == active).
//  4. Platform_account exists.
//  5. Platform_account NOT in reauth_required — both signals
//     (status enum + reauth_required_at timestamp) checked
//     defense-in-depth.
//
// All dependent stores (workspaceStore + userRepo) are read
// from the module's dependency struct (not via a captured config
// struct). This avoids an option-order trap: a RouterOption
// that snapshots r.workspaceStore at option-call time would
// capture nil if the option order is wrong. The typed deps are
// always current at handler-time.
//
// Inconsistency note: a reauth_required destination returns 404
// (not 422) because the canonical Velox contract treats
// non-usable destinations as if they don't exist — the peer's
// only sane response is to drop the destination and reissue
// the URL with a fresh id. Returning a distinct status would
// leak existence.
//
// TOKEN REFRESHABILITY — see VeloxModule.Register for the full
// rationale: /validate is a fast poll that DOES NOT touch the credential
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
func (m *VeloxModule) handleValidateInternalDestination(w http.ResponseWriter, req *http.Request) {
	if m.deps.ExternalDestinationStore == nil {
		writeError(w, http.StatusNotImplemented, "internal velox store not configured")
		return
	}
	id := chi.URLParam(req, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "destination id required")
		return
	}

	// 0. Per-destination rate limit. Runs BEFORE any DB lookup
	// so a Velox hot-loop on a single id is rejected cheaply
	// without saturating the destination / workspace /
	// platform_account downstreams. 429 + Retry-After header
	// signals the peer to spread its retry load.
	if m.deps.VeloxValidateRateLimiter != nil {
		allowed, retryAfter := m.deps.VeloxValidateRateLimiter.take(id)
		if !allowed {
			seconds := int(retryAfter.Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			slog.Warn("velox validate: rate limit exceeded",
				"destination_id", id, "retry_after_seconds", seconds)
			writeError(w, http.StatusTooManyRequests,
				fmt.Sprintf("rate limit exceeded; retry after %d seconds", seconds))
			return
		}
	}

	// 1. Destination lookup.
	dest, err := m.deps.ExternalDestinationStore.GetByID(req.Context(), id)
	if err != nil {
		// Mirror of handleCreateInternalDelivery's sentinel-aware
		// 404: production repos wrap the missing-row case as
		// (nil, ErrExternalDestinationNotFound); the validate-side
		// mock returns (nil, nil) for missing rows, so the L862
		// nil-dest branch covers tests. Real production code
		// hits this branch on missing rows and we MUST map it
		// to 404 (not 500) to keep the validate path consistent
		// with the POST path — a 500 here would let a probe
		// iterate IDs and enumerate which are live.
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
			return
		}
		slog.Error("velox validate: destination lookup failed",
			"id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil || !dest.Enabled {
		// Disabled = 404 (uniform with not-found; doesn't leak
		// existence).
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}

	// 2. Workspace lookup. Read directly from module deps —
	// avoids the option-order trap of capturing values at
	// WithExternalDestinationStore call time.
	if m.deps.WorkspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}
	ws, err := m.deps.WorkspaceStore.FindByID(dest.WorkspaceID)
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
	if m.deps.UserStore == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}
	pa, err := m.deps.UserStore.FindPlatformAccountByID(dest.PlatformAccountID)
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

	// P1 deletion check: refuse explicitly-cancelled accounts
	// (status=AccountStatusRevoked OR AccountStatusDisconnected).
	// These mean the user took an explicit action to terminate
	// the OAuth grant, so keeping the destination
	// enabled-but-unusable would surface as a publish-time
	// blocked_auth. Returning 404 here gives Velox the same
	// "destination not found" signal as a removed row so the
	// worker reissues with a fresh id (matches the
	// reauth_required collapse semantics documented at the
	// file header).
	//
	// The check uses the typed AccountStatus* constants from
	// internal/models/user.go — they ARE the canonical string
	// aliases ("revoked", "disconnected"); checking the model
	// constants instead of bare literals removes the
	// maintenance trap of a literal drifting from the canonical
	// value during a future status-rename migration.
	if pa.Status == models.AccountStatusRevoked ||
		pa.Status == models.AccountStatusDisconnected {
		slog.Warn("velox validate: destination has cancelled channel",
			"destination_id", id, "platform_account_id", pa.ID,
			"status", pa.Status)
		writeError(w, http.StatusNotFound, "destination cancelled")
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

// WithExternalDestinationStore wires
// *repository.ExternalDestinationRepository into the Router.
// Following the WorkspaceStore / PostStore nil-guard pattern:
// when the option is omitted, /internal/v1 routes return 404
// (the helper refuses to register them). Production wiring
// in internal/bootstrap.Wire passes
// repository.NewExternalDestinationRepository(db).
//
// Plus WithVeloxAPIToken AND the user/workspace stores MUST
// be wired for the validate handler's full happy path. Calling
// only this option but not WithVeloxAPIToken leaves the route
// un-registered. cmd/server/main.go is responsible for
// wiring all three (or all four, including WithWorkspaceStore
// + WithUserStore which are normally wired earlier).
func WithExternalDestinationStore(s ExternalDestinationStore) RouterOption {
	return func(r *Router) { r.externalDestinations = s }
}

// WithExternalDeliveryStore wires
// *repository.ExternalDeliveryRepository into the Router for
// POST /internal/v1/deliveries. Mirrors
// WithExternalDestinationStore: when omitted, the deliveries
// route is not registered (VeloxModule.Register nil-guards).
// The validate route is unaffected —
// only the deliveries route depends on this option.
//
// Production wiring in internal/bootstrap.Wire passes the
// SAME *repository.ExternalDeliveryRepository struct that
// repos/backend already uses for handler-side lookups; the
// struct is BOTH an ExternalDestinationStore (its
// GetByID method) AND an ExternalDeliveryStore (its Insert
// method) per the compile-time assertions above.
func WithExternalDeliveryStore(s ExternalDeliveryStore) RouterOption {
	return func(r *Router) { r.externalDeliveries = s }
}
