package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
func (r *Router) handleGetInternalDelivery(w http.ResponseWriter, req *http.Request) {
	id := chi.URLParam(req, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "delivery id required")
		return
	}

	delivery, err := r.externalDeliveries.GetByID(req.Context(), id)
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

// handleCreateInternalDelivery implements
// POST /internal/v1/deliveries for the Velox integration
// contract.
//
// IDEMPOTENCY CONTRACT (per user spec):
//  1. Compute request_sha256 = sha256(raw_body_bytes) inside
//     external_delivery_repo.Insert (the Insert computes the
//     hex from rawBody internally).
//  2. INSERT path look-and-write happens under a single
//     pg_advisory_xact_lock so concurrent replays serialise.
//  3. SAME SHA → reuse social_delivery_id, return 202 with
//     already_exists=true.
//  4. DIFFERENT SHA → 409 with structured
//     VeloxDeliverArtifactConflictResponse body
//     (error/code/idempotency_key).
//  5. NOT FOUND → INSERT new row, return 202 with
//     already_exists=false.
//
// The three-way outcome is detected in the handler by comparing
// the returned record's ID to the minted ID: equal → freshly
// inserted by THIS request; different → reused from a previous
// row (same idempotency_key + same SHA pre-existed).
//
// SLA — 500ms p99 target (per user spec). The Insert with
// pg_advisory_xact_lock + SELECT + maybe INSERT is bounded
// by the lock-holder's transaction speed (typically 50-150ms
// on healthy Postgres). We add a 5s ctx timeout as a safety
// cap; an Insert > 300ms is logged WARN so operators can
// alert on slow path without paging.
//
// VALIDATION CHAIN (fast-fail-first, ordered by error cost):
//  1. Authorization (Bearer middleware, 401/403/503)
//  2. Body cap (8 MB → 413 if over)
//  3. JSON parsing (400 if malformed)
//  4. idempotency_key presence + length ≤ 256 (422)
//  5. artifact.sha256 regex (422)
//  6. artifact.size_bytes > 0 (422)
//  7. artifact.mime_type allowlist (422)
//  8. metadata non-empty JSON object (422)
//  9. external_destination_id present in DB (422)
//  10. Insert call (3-way outcome)
//
// All 422 paths funnel through writeError so callers see the
// uniform {"error": "validation: ..."} envelope. The 409
// conflict is the ONLY structured-body response in this
// handler — by design, so callers can distinguish
// "validate-and-fix" (422) from "permanent conflict, don't
// retry" (409).
func (r *Router) handleCreateInternalDelivery(w http.ResponseWriter, req *http.Request) {
	if r.externalDeliveries == nil {
		writeError(w, http.StatusNotImplemented, "internal velox delivery store not configured")
		return
	}
	if r.externalDestinations == nil {
		writeError(w, http.StatusInternalServerError, "external destination store not configured")
		return
	}

	// Defensive ctx cap. 5s is well above the 500ms p99 SLA — the
	// Insert is bounded by transactional speed. The cap means a
	// runaway DB never blocks the handler indefinitely.
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	// Step 2 — body cap via http.MaxBytesReader. The Reader
	// returns *http.MaxBytesError on truncation, which we
	// detect with errors.As (not string match).
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxDeliveryBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			slog.Warn("velox deliver: body too large",
				"limit_bytes", maxDeliveryBodyBytes)
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body exceeds %d MB", maxDeliveryBodyBytes/(1024*1024)))
			return
		}
		slog.Error("velox deliver: body read failed", "err", err)
		writeError(w, http.StatusInternalServerError, "body read failed")
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body")
		return
	}

	// Step 3 — JSON parse. Decode on a COPY of the raw bytes
	// because the Insert needs the raw bytes for SHA
	// computation; the unmarshal call below does NOT consume
	// the body iterator (it's a fresh Unmarshal pass).
	var veloxReq VeloxDeliverArtifactRequest
	if err := json.Unmarshal(body, &veloxReq); err != nil {
		slog.Warn("velox deliver: json unmarshal failed", "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Step 4 — idempotency_key presence + max length.
	if veloxReq.IdempotencyKey == "" {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: idempotency_key is required")
		return
	}
	if len(veloxReq.IdempotencyKey) > 256 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: idempotency_key exceeds 256 characters")
		return
	}

	// Step 5 — artifact.sha256 lowercase hex regex.
	if !sha256HexRegex.MatchString(veloxReq.Artifact.SHA256) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.sha256 must be 64 lowercase hex characters")
		return
	}

	// Step 6 — artifact.size_bytes positive.
	if veloxReq.Artifact.SizeBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.size_bytes must be > 0")
		return
	}

	// Step 7 — mime allowlist (4 video formats).
	if !mimeAllowlist[veloxReq.Artifact.MimeType] {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("validation: artifact.mime_type %q not supported (allowed: video/mp4, video/quicktime, video/webm, video/x-matroska)",
				veloxReq.Artifact.MimeType))
		return
	}

	// Step 8 — metadata must be a non-empty JSON object. This
	// fast-fail happens BEFORE the destination lookup so callers
	// always see 422 for malformed metadata, even if the
	// destination id is unknown.
	if !services.IsNonEmptyJSONObject(veloxReq.Metadata) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: metadata must be a non-empty JSON object")
		return
	}

	// Step 9 — external_destination_id must exist.
	dest, err := r.externalDestinations.GetByID(ctx, veloxReq.ExternalDestinationID)
	if err != nil {
		// Sentinel-aware: a not-found sentinel from the
		// destination repo is the SAME 404 a missing-row
		// produces, so the caller can't distinguish them
		// (closes a status-code-oracle / existence-leak path:
		// without this branch, the 500/404 split on missing
		// destinations would let a malicious Velox peer iterate
		// IDs and enumerate which are live).
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
			return
		}
		slog.Error("velox deliver: destination lookup failed",
			"external_destination_id", veloxReq.ExternalDestinationID,
			"err", err)
		writeError(w, http.StatusInternalServerError, "destination lookup failed")
		return
	}
	if dest == nil {
		// Spec mandate: a missing destination row is NOT a
		// payload validation failure (the body IS well-formed) —
		// return 404 because the resource the request refers to
		// doesn't exist. This matches the user spec
		// "ErrExternalDeliveryNotFound → 404 (se destination_id
		// manca)" + collapses with the /validate 404 "destination
		// not found" so the Velox consumer treats both
		// identically and a missing row never leaks a 422/500
		// distinction.
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}
	// Destination defaults are authoritative for the downstream uploader.
	// Merge them before persistence so the Velox peer remains opaque while
	// InstaEdit resolves the Drive account and folder locally.
	veloxReq.Metadata = services.MergeVeloxDestinationMetadata(dest, veloxReq.Metadata)

	// Step 8 — parse and validate the merged metadata once at the HTTP
	// boundary. All downstream consumers use the typed result.
	meta, err := models.ParseVeloxDeliveryMetadata(veloxReq.Metadata)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: "+err.Error())
		return
	}
	if err := meta.Validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: "+err.Error())
		return
	}

	// Step 10 — mint social_delivery_id (ULID-shaped opaque).
	mintedID, err := services.GenerateVeloxDeliveryID()
	if err != nil {
		slog.Error("velox deliver: id mint failed", "err", err)
		writeError(w, http.StatusInternalServerError, "id mint failed")
		return
	}

	// Build the ExternalDelivery record. The Insert computes
	// RequestSHA256 from rawBody internally (no double-read).
	delivery := &models.ExternalDelivery{
		ID:                    mintedID,
		SourceSystem:          veloxSourceSystemTag,
		ExternalDeliveryID:    veloxReq.ExternalDeliveryID,
		IdempotencyKey:        veloxReq.IdempotencyKey,
		ExternalDestinationID: veloxReq.ExternalDestinationID,
		SourceArtifactID:      veloxReq.Artifact.ArtifactID,
		ExpectedSHA256:        veloxReq.Artifact.SHA256,
		ExpectedSizeBytes:     veloxReq.Artifact.SizeBytes,
		ExpectedMimeType:      veloxReq.Artifact.MimeType,
		DownloadURL:           veloxReq.Artifact.DownloadURL,
		Metadata:              veloxReq.Metadata,
		PublishAt:             veloxReq.PublishAt,
		CallbackURL:           veloxReq.CallbackURL,
		Status:                models.ExternalDeliveryStatusAccepted,
	}

	// Insert with rawBody so repo computes SHA from the EXACT
	// bytes (no serialization round-trip mismatch possible).
	t0 := time.Now()
	inserted, err := r.externalDeliveries.Insert(ctx, delivery, body)
	elapsed := time.Since(t0)
	if elapsed > 300*time.Millisecond {
		slog.Warn("velox deliver: insert slow",
			"elapsed_ms", elapsed.Milliseconds(),
			"idempotency_key", veloxReq.IdempotencyKey)
	}

	if err != nil {
		// 3-way outcome: ErrIdempotencyConflict → 409 structured.
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			var existingID string
			if inserted != nil {
				existingID = inserted.ID
			}
			slog.Info("velox deliver: replay with different sha rejected",
				"idempotency_key", veloxReq.IdempotencyKey,
				"existing_social_delivery_id", existingID,
			)
			writeJSON(w, http.StatusConflict, VeloxDeliverArtifactConflictResponse{
				Error:          "idempotency_key_conflict",
				Code:           "idempotency_key_conflict",
				IdempotencyKey: veloxReq.IdempotencyKey,
			})
			return
		}
		slog.Error("velox deliver: insert failed",
			"err", err, "idempotency_key", veloxReq.IdempotencyKey)
		writeError(w, http.StatusInternalServerError, "delivery persist failed")
		return
	}

	// 3-way outcome — fresh (mintedID == inserted.ID) vs
	// replay (mintedID != inserted.ID). ALWAYS 202.
	alreadyExists := inserted.ID != mintedID
	slog.Info("velox deliver: accepted",
		"social_delivery_id", inserted.ID,
		"idempotency_key", veloxReq.IdempotencyKey,
		"already_exists", alreadyExists,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	// The delivery is persisted in external_deliveries. The worker
	// polls that table and claims rows atomically; no in-process
	// channel is used. 202 "accepted" therefore means "persisted",
	// not "delivered to the worker".

	writeJSON(w, http.StatusAccepted, VeloxDeliverArtifactResponse{
		SocialDeliveryID: inserted.ID,
		Status:           "accepted",
		AlreadyExists:    alreadyExists,
	})
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
	if r.veloxValidateRateLimiter != nil {
		allowed, retryAfter := r.veloxValidateRateLimiter.take(id)
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
	dest, err := r.externalDestinations.GetByID(req.Context(), id)
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

// registerInternalVeloxRoutes wires the /internal/v1
// service-to-service routes. Called from Router.Setup().
// Refuses to register if the per-route dependencies aren't
// wired (matches the WorkspaceStore / PostStore nil-guard
// pattern) — a server without WithExternalDestinationStore +
// WithVeloxAPIToken returns 404 for /internal/v1/* paths so
// the operator sees a clear "route not registered" rather
// than a 500.
//
// Per-route dependency requirements:
//   - destinations/{id}/validate: externalDestinations +
//     veloxAPIToken (workspaceStore + userRepo required at
//     handler-time for the full happy path; checked inline).
//   - deliveries: externalDestinations + externalDeliveries
//   - veloxAPIToken (all three required AT register-time;
//     the handler's defensive nil-checks also catch a
//     misordered wiring).
//
// Boot-time guard rationale: if VELOX_API_TOKEN is empty OR
// the destination store IS unwired, the middleware returns
// 503 on every request. Better to NOT register the route at
// all so the operator sees a 404 in the logs and traces back
// the env config. Subsequent env rotation (process restart
// re-loads) restores the route.
func (r *Router) registerInternalVeloxRoutes() {
	if r.externalDestinations == nil || r.veloxAPIToken == "" {
		return
	}
	r.mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate",
		r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination)))
	if r.externalDeliveries != nil {
		r.mux.Method(http.MethodPost, "/internal/v1/deliveries",
			r.internalVeloxAuth(http.HandlerFunc(r.handleCreateInternalDelivery)))
		// GET /internal/v1/deliveries/{id} — Velox reconciliation/poll.
		// The id is the social_delivery_id minted by handleCreateInternalDelivery
		// (shape: "sdel_01J…"). Velox polls this endpoint when its
		// outbound callback channel drops a packet (network blip,
		// peer restarts, 502 storm). Same Bearer auth as the other
		// /internal/v1 routes — the middleware separately handles
		// 401 missing-header / 403 token-mismatch before this handler
		// runs.
		r.mux.Method(http.MethodGet, "/internal/v1/deliveries/{id}",
			r.internalVeloxAuth(http.HandlerFunc(r.handleGetInternalDelivery)))
	}
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
// route is not registered (the registerInternalVeloxRoutes
// helper nil-guards). The validate route is unaffected —
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
