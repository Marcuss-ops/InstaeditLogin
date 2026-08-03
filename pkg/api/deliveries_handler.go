// Package api — handler for POST /internal/v1/deliveries.
//
// The endpoint accepts one wire contract only: the flat
// VeloxDeliverArtifactRequest with contract_version=velox.delivery.v1.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/deliveries"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// handleCreateInternalDelivery implements POST /internal/v1/deliveries
// for the Velox integration contract. The request is decoded and
// validated once, then persisted through the single canonical path.
func (m *VeloxModule) handleCreateInternalDelivery(w http.ResponseWriter, req *http.Request) {
	if m.deps.ExternalDeliveryStore == nil {
		writeError(w, http.StatusNotImplemented, "internal velox delivery store not configured")
		return
	}
	if m.deps.ExternalDestinationStore == nil {
		writeError(w, http.StatusInternalServerError, "external destination store not configured")
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	// Step 1 — body cap via http.MaxBytesReader (8 MB envelope).
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxDeliveryBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			slog.Warn("velox deliver: body too large", "limit_bytes", maxDeliveryBodyBytes)
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

	// Step 2 — decode and require the sole supported wire version.
	var veloxReq VeloxDeliverArtifactRequest
	if err := json.Unmarshal(body, &veloxReq); err != nil {
		slog.Warn("velox deliver: json unmarshal failed", "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if veloxReq.ContractVersion != ContractVersionDelivery {
		writeError(w, http.StatusUnprocessableEntity, "unsupported contract_version")
		return
	}

	// Step 3 — idempotency_key presence + max length.
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

	// Step 4 — artifact.sha256 lowercase hex regex.
	if !sha256HexRegex.MatchString(veloxReq.Artifact.SHA256) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.sha256 must be 64 lowercase hex characters")
		return
	}

	// Step 5 — artifact.size_bytes positive.
	if veloxReq.Artifact.SizeBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: artifact.size_bytes must be > 0")
		return
	}

	// Step 6 — mime allowlist.
	if !mimeAllowlist[veloxReq.Artifact.MimeType] {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("validation: artifact.mime_type %q not supported (allowed: video/mp4, video/quicktime, video/webm, video/x-matroska)",
				veloxReq.Artifact.MimeType))
		return
	}

	// Step 7 — metadata must be a non-empty JSON object.
	if !services.IsNonEmptyJSONObject(veloxReq.Metadata) {
		writeError(w, http.StatusUnprocessableEntity,
			"validation: metadata must be a non-empty JSON object")
		return
	}

	// Step 8 — external_destination_id lookup.
	dest, err := m.deps.ExternalDestinationStore.GetByID(ctx, veloxReq.ExternalDestinationID)
	if err != nil {
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
		writeError(w, http.StatusNotFound, veloxDestinationNotFoundBody)
		return
	}

	// Re-resolve the opaque destination at acceptance time. Discovery and
	// job-submit checks can be minutes old by the time rendering finishes;
	// this closes the window where an operator disables a channel after job
	// creation but before the resulting artifact is accepted for publishing.
	if m.deps.WorkspaceStore != nil && m.deps.UserStore != nil {
		resolved, resolveErr := m.resolver().Resolve(ctx, deliveries.ResolveRequest{DestID: veloxReq.ExternalDestinationID})
		if resolveErr != nil || resolved == nil || !resolved.Valid {
			if resolveErr != nil {
				slog.Error("velox deliver: destination validation failed",
					"external_destination_id", veloxReq.ExternalDestinationID, "err", resolveErr)
			}
			writeError(w, http.StatusUnprocessableEntity, "validation: destination is not publishable")
			return
		}
	}
	veloxReq.Metadata = services.MergeVeloxDestinationMetadata(dest, veloxReq.Metadata)

	// Step 9 — parse + validate metadata once at HTTP boundary.
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

	m.persistInternalDelivery(w, ctx, body, &veloxReq)
}
