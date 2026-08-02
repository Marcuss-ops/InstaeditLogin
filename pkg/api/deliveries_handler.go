// Package api — handler for POST /internal/v1/deliveries.
//
// Part of the split of deliveries_create.go (audit T3: versioned contract).
// Dispatches between the versioned contract path and the legacy path using
// the explicit contract_version discriminator — no auto-detection heuristics.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/deliveries"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// handleCreateInternalDelivery implements POST /internal/v1/deliveries
// for the Velox integration contract.
//
// Relocated from pkg/api/velox_handlers.go. Extended with explicit
// contract_version dispatch (audit T3):
//
//  1. Body is parsed as VeloxDeliverContractRequest.
//  2. If contract_version == "velox-instaedit.v1" → CONTRACT path.
//     Strict validation per validateContractRequest; the contract path
//     synthesises the legacy row fields so the existing Insert path is
//     reused unchanged.
//  3. Otherwise → LEGACY path (existing validation chain verbatim,
//     destination lookup + MergeVeloxDestinationMetadata + Parse+Validate).
//
// Both paths converge on persistInternalDelivery which mints the
// social_delivery_id, runs Insert under pg_advisory_xact_lock, and
// emits the 3-way outcome (fresh / replay / different-SHA 409).
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

	// Step 2 — DISPATCH: contract_version discriminator (NO auto-detection).
	var contractReq VeloxDeliverContractRequest
	if jerr := json.Unmarshal(body, &contractReq); jerr == nil && hasContractVersion(&contractReq) {
		// CONTRACT PATH — explicit contract_version == "velox-instaedit.v1".
		headerIdempotencyKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
		if verr := validateContractRequest(&contractReq, headerIdempotencyKey); verr != nil {
			writeError(w, http.StatusUnprocessableEntity, verr.Error())
			return
		}
		// Synthesise legacy row fields from the contract body so
		// the existing Insert path is reused unchanged.
		metadata := map[string]any{
			"title":              contractReq.Publication.Title,
			"description":        contractReq.Publication.Description,
			"privacy_status":     contractReq.Publication.InitialPrivacy,
			"final_privacy":      contractReq.Publication.FinalPrivacy,
			"require_thumbnail":  contractReq.Publication.RequireThumbnail,
			"target_account_ids": []int64{},
			"language":           "",
			"duration_seconds":   contractReq.Media.DurationSeconds,
			"platform":           contractReq.Destination.Platform,
			"target_type":        contractReq.Destination.TargetType,
		}
		if len(contractReq.Publication.Tags) > 0 {
			metadata["tags"] = contractReq.Publication.Tags
		}
		if contractReq.Destination.PlatformAccountID > 0 {
			metadata["target_account_ids"] = []int64{contractReq.Destination.PlatformAccountID}
		}
		if contractReq.Destination.GroupID > 0 {
			metadata["group_id"] = contractReq.Destination.GroupID
		}
		metaBytes, mErr := json.Marshal(metadata)
		if mErr != nil {
			slog.Error("velox deliver contract: metadata marshal failed", "err", mErr)
			writeError(w, http.StatusInternalServerError, "metadata marshal failed")
			return
		}
		downloadURL := contractReq.Media.DownloadURL
		veloxReq := &VeloxDeliverArtifactRequest{
			ExternalDeliveryID:    synthesizeContractDeliveryID(&contractReq),
			IdempotencyKey:        headerIdempotencyKey,
			ExternalDestinationID: synthesizeContractDestinationID(&contractReq.Destination),
			Artifact: VeloxArtifactRef{
				ArtifactID:  contractReq.Source.ArtifactID,
				SHA256:      contractReq.Media.SHA256,
				SizeBytes:   contractReq.Media.SizeBytes,
				MimeType:    contractReq.Media.MimeType,
				DownloadURL: &downloadURL,
			},
			Metadata:  metaBytes,
			PublishAt: contractReq.Publication.PublishAt,
		}
		// The versioned contract has no opaque destination ID in its wire
		// shape, so validate its direct target before accepting the durable
		// delivery. This keeps the contract path fail-closed just like the
		// legacy opaque-destination path below.
		if m.deps.WorkspaceStore != nil && m.deps.UserStore != nil {
			resolved, resolveErr := m.resolver().Resolve(ctx, deliveries.ResolveRequest{
				WorkspaceID: contractReq.Destination.WorkspaceID,
				Platform:    contractReq.Destination.Platform,
				Target: deliveries.TargetDescriptor{
					Type:              contractReq.Destination.TargetType,
					PlatformAccountID: contractReq.Destination.PlatformAccountID,
					GroupID:           contractReq.Destination.GroupID,
				},
			})
			if resolveErr != nil || resolved == nil || !resolved.Valid {
				if resolveErr != nil {
					slog.Error("velox deliver contract: destination validation failed", "err", resolveErr)
				}
				writeError(w, http.StatusUnprocessableEntity, "validation: destination is not publishable")
				return
			}
		}
		m.persistInternalDelivery(w, ctx, body, veloxReq, true /* isContractPath */)
		return
	}

	// LEGACY PATH — verbatim validation chain from the original
	// pkg/api/velox_handlers.go::handleCreateInternalDelivery.
	var veloxReq VeloxDeliverArtifactRequest
	if err := json.Unmarshal(body, &veloxReq); err != nil {
		slog.Warn("velox deliver: json unmarshal failed", "err", err)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
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

	// Re-resolve the opaque destination at acceptance time.  Discovery and
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

	m.persistInternalDelivery(w, ctx, body, &veloxReq, false /* isContractPath */)
}
