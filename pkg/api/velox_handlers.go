package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/deliveries"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// NOTE — handleCreateInternalDelivery lives in deliveries_handler.go.
// This file contains the GET delivery and destination-validation handlers.
//
// The GET delivery response is canonical-only. Legacy v0 aliases are
// intentionally not emitted so every Velox consumer reads one contract.
//
// The package-level helpers resolveDeliveryTarget /
// mapExternalDeliveryStatusToPublishStatus /
// mapExternalDeliveryStatusToThumbnailStatus /
// privacyStatusFromMetadata / strFromPtr live in this file
// (next to handleGetInternalDelivery) — no separate helper
// file is needed for the 4 small mapping functions.

// handleGetInternalDelivery implements
// GET /internal/v1/deliveries/{id} for the Velox integration
// contract. Velox uses this for reconciliation/poll when its
// callback channel drops a packet (network blip, peer restarts,
// webhook 5xx storm).
//
// Wire shape — see VeloxGetDeliveryResponse in velox_types.go.
// The handler populates the canonical fields (delivery_id|velox_job_id|target{
// platform_account_id,channel_id,channel_name,enabled}|
// publish_status|thumbnail_status|youtube_video_id|privacy).
//
// Velox clients read `delivery_id` + `publish_status` + `thumbnail_status`
// and the canonical `youtube_video_id` when publication has produced one.
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

	youtubeVideoID := strFromPtr(delivery.PlatformMediaID)
	resp := VeloxGetDeliveryResponse{
		DeliveryID:      delivery.ID,
		VeloxJobID:      delivery.ExternalDeliveryID,
		Target:          m.resolveDeliveryTarget(req.Context(), delivery),
		PublishStatus:   mapExternalDeliveryStatusToPublishStatus(delivery.Status),
		ThumbnailStatus: mapExternalDeliveryStatusToThumbnailStatus(delivery.Status),
		YouTubeVideoID:  youtubeVideoID,
		Privacy:         privacyStatusFromMetadata(delivery.Metadata),
		CreatedAt:       delivery.CreatedAt,
		UpdatedAt:       delivery.UpdatedAt,
	}
	// Surface LastErrorCode + Message verbatim. omitempty drops
	// the field on rows that haven't seen a failed transition
	// yet (the brand-new accepted row).
	if delivery.LastErrorCode != nil {
		resp.LastErrorCode = *delivery.LastErrorCode
	}
	if delivery.LastErrorMessage != nil {
		resp.LastErrorMessage = *delivery.LastErrorMessage
	}
	slog.Info("velox get delivery",
		"social_delivery_id", delivery.ID,
		"status", delivery.Status,
	)
	writeJSON(w, http.StatusOK, resp)
}

// resolveDeliveryTarget walks the 3-step FK chain
// (external_destinations → platform_accounts → workspace_channels)
// to populate the spec §8 target block. Missing-row tolerance:
// any single step that fails (channel unbound / account removed /
// workspace deleted) is logged at warn level and the corresponding
// fields remain zero — the handler still returns 200 with
// partial info, and the operator dashboard surfaces partial
// targets as "binding missing; reconcile needed".
//
// Each lookup is short-circuited when its predecessor returned
// nil/empty so a missing external_destination doesn't cascade
// into a pointless FindPlatformAccountByID call.
func (m *VeloxModule) resolveDeliveryTarget(ctx context.Context, delivery *models.ExternalDelivery) VeloxGetDeliveryTarget {
	target := VeloxGetDeliveryTarget{}
	if m.deps.ExternalDestinationStore == nil {
		return target
	}
	destination, err := m.deps.ExternalDestinationStore.GetByID(ctx, delivery.ExternalDestinationID)
	if err != nil {
		// INFRA failure — logged at Error so alerting can distinguish
		// "DB problem" from the tolerated "row missing" degradation
		// below. The GET contract still degrades gracefully.
		slog.Error("velox get delivery: destination lookup failed (infra)",
			"social_delivery_id", delivery.ID,
			"external_destination_id", delivery.ExternalDestinationID,
			"err", err)
		return target
	}
	if destination == nil {
		slog.Warn("velox get delivery: destination row missing",
			"social_delivery_id", delivery.ID,
			"external_destination_id", delivery.ExternalDestinationID)
		return target
	}
	target.PlatformAccountID = destination.PlatformAccountID
	if m.deps.UserStore == nil {
		return target
	}
	pa, err := m.deps.UserStore.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		slog.Error("velox get delivery: platform_account lookup failed (infra)",
			"social_delivery_id", delivery.ID,
			"platform_account_id", target.PlatformAccountID,
			"err", err)
		return target
	}
	if pa == nil {
		slog.Warn("velox get delivery: platform_account row missing",
			"social_delivery_id", delivery.ID,
			"platform_account_id", target.PlatformAccountID)
		return target
	}
	target.ChannelID = pa.PlatformUserID
	target.ChannelName = pa.Username
	if m.deps.WorkspaceStore == nil {
		return target
	}
	binding, err := m.deps.WorkspaceStore.FindChannel(ctx, destination.WorkspaceID, target.PlatformAccountID)
	if err != nil {
		// Previously swallowed with no log at all: an Enabled=false
		// projection here was indistinguishable from a healthy but
		// unbound channel.
		slog.Error("velox get delivery: workspace channel lookup failed (infra)",
			"social_delivery_id", delivery.ID,
			"workspace_id", destination.WorkspaceID,
			"platform_account_id", target.PlatformAccountID,
			"err", err)
	} else if binding != nil {
		target.Enabled = binding.Enabled
	}
	return target
}

// mapExternalDeliveryStatusToPublishStatus translates the
// 11-value ExternalDeliveryStatus enum into the spec §8's
// publish_status enum (waiting_thumbnail | ready_to_publish |
// scheduled | published | failed | blocked | retry_wait).
//
// Mapping rules:
//   - published → published
//   - failed | dead_letter → failed (dead_letter collapses into
//     failed for §8 — the operator runbook treats both the same)
//   - blocked_auth → blocked
//   - retry_wait → retry_wait
//   - queued → scheduled (publish_at-elapsed pre-state; spec §10.1)
//   - accepted | downloading | artifact_verified | ingest_completed |
//     publishing → waiting_thumbnail
//
// The "waiting_thumbnail" bucket collapses the 5-state
// happy-path forward into one operator-facing word because
// dashboards care about "still working" vs "needs reauth"
// vs "done" — fine-grained state is in the parallel `status`
// field (mirrors the 11-value enum verbatim).
func mapExternalDeliveryStatusToPublishStatus(s models.ExternalDeliveryStatus) string {
	switch s {
	case models.ExternalDeliveryStatusPublished:
		return "published"
	case models.ExternalDeliveryStatusFailed,
		models.ExternalDeliveryStatusDeadLetter:
		return "failed"
	case models.ExternalDeliveryStatusBlockedAuth:
		return "blocked"
	case models.ExternalDeliveryStatusRetryWait:
		return "retry_wait"
	case models.ExternalDeliveryStatusQueued:
		return "scheduled"
	case models.ExternalDeliveryStatusAccepted,
		models.ExternalDeliveryStatusDownloading,
		models.ExternalDeliveryStatusArtifactVerified,
		models.ExternalDeliveryStatusIngestCompleted,
		models.ExternalDeliveryStatusPublishing:
		return "waiting_thumbnail"
	}
	return ""
}

// mapExternalDeliveryStatusToThumbnailStatus translates the
// 11-value status enum into spec §9's 4-value enum (pending |
// applied | failed).
//
// Mapping rules:
//   - published → applied
//   - failed | dead_letter | blocked_auth → failed
//   - everything else → pending
//
// The "skipped" branch is reserved for future use when
// metadata.require_thumbnail=false starts routing through this
// handler. Today every row's thumbnail_status is one of
// pending | applied | failed; never omitted.
func mapExternalDeliveryStatusToThumbnailStatus(s models.ExternalDeliveryStatus) string {
	switch s {
	case models.ExternalDeliveryStatusPublished:
		return "applied"
	case models.ExternalDeliveryStatusFailed,
		models.ExternalDeliveryStatusDeadLetter,
		models.ExternalDeliveryStatusBlockedAuth:
		return "failed"
	}
	return "pending"
}

// privacyStatusFromMetadata parses external_deliveries.metadata
// (JSONB) for the publisher-side privacy_status string. Returns
// "" when the blob is empty or malformed.
//
// Lenient by design — GET must NEVER 500 on a metadata parse
// miss; it just omits the field and lets the operator reconcile.
func privacyStatusFromMetadata(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var meta models.VeloxDeliveryMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		slog.Warn("velox get delivery: metadata unmarshal partial", "err", err)
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(meta.PrivacyStatus)) {
	case "private", "public", "unlisted":
		return strings.ToLower(meta.PrivacyStatus)
	}
	return ""
}

// strFromPtr is a tiny helper that returns the dereferenced
// string from a *string pointer, or "" when the pointer is nil.
// Centralised so the canonical YouTubeVideoID is populated consistently
// from the source delivery column.
func strFromPtr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// handleValidateInternalDestination implements
// POST /internal/v1/destinations/{id}/validate for the Velox
// integration contract.
//
// Thin adapter: delegates to the unified TargetResolver
// (internal/deliveries/target_resolver.go). This eliminates the
// duplicated workspace/account/binding/eligibility checks that
// previously lived inline — the resolver consolidates both the
// SavedDestination path (this handler) and the DirectTarget path
// (destinations_resolve_target.go) into a single use case.
//
// Rate-limit + diagnostic mode remain in the handler layer.
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

	// 0. Per-destination rate limit (unchanged).
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

	// 1. Delegate to the unified TargetResolver (SavedDestination path).
	result, err := m.resolver().Resolve(req.Context(), deliveries.ResolveRequest{
		DestID: id,
	})
	if err != nil {
		// A typed missing-row miss from the destination store (wrapped
		// by the resolver) maps to the canonical 404 body — identical
		// to the nil-dest domain result below. A real infra failure
		// keeps the redacted 500 instead of being collapsed into
		// "not found".
		if errors.Is(err, repository.ErrExternalDestinationNotFound) {
			writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
			return
		}
		slog.Error("velox validate: resolver failed",
			"destination_id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "validation failed")
		return
	}

	// 2. Map resolver result to HTTP response.
	if !result.Valid {
		// Collapse all non-valid results to 404 — the canonical
		// Velox contract treats non-usable destinations as if they
		// don't exist (no existence leak).
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}

	// 3. Diagnostic JSON trigger (explicit operator opt-in only).
	diagnostic := req.URL.Query().Get("diagnostic") == "true" ||
		req.Header.Get("X-Velox-Diagnostic") == "true"

	if diagnostic && result.Diagnostic != nil {
		writeJSON(w, http.StatusOK, VeloxValidateDestinationResponse{
			Valid:         true,
			DestinationID: result.DestinationID,
			Status:        result.Diagnostic.Status,
			Platform:      result.Diagnostic.Platform,
		})
		return
	}

	// 4. Happy path: 204 No Content.
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
// un-registered. the canonical bootstrap is responsible for
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
