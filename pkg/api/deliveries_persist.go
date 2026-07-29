// Package api — shared persistence helper for POST /internal/v1/deliveries.
//
// Part of the split of deliveries_create.go (audit T3: versioned contract).
// Used by both the contract path and the legacy path after validation.
//
// Performs Insert under pg_advisory_xact_lock and emits the 3-way outcome
// (fresh / same-SHA replay / different-SHA 409).
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// persistInternalDelivery is the shared post-validation insert used by
// both contract and legacy paths. Performs:
//
//  1. Mint a social_delivery_id (sdel_01J... ULID-shaped).
//  2. Run ExternalDeliveryStore.Insert under pg_advisory_xact_lock.
//     The 3-way outcome (fresh / same-SHA replay / different-SHA
//     ErrIdempotencyConflict) is owned by the repo. The Insert is
//     bounded by a 5s ctx timeout; > 300ms triggers a WARN log.
//  3. Detects fresh-vs-replay via mintedID != inserted.ID, emits the
//     schema-appropriate 202 response.
//  4. On ErrIdempotencyConflict: emit a structured 409 with the
//     conflict body (the same across both paths so callers can
//     pattern-match on it).
func (m *VeloxModule) persistInternalDelivery(w http.ResponseWriter, ctx context.Context, body []byte, veloxReq *VeloxDeliverArtifactRequest, isContractPath bool) {
	mintedID, err := services.GenerateVeloxDeliveryID()
	if err != nil {
		slog.Error("velox deliver: id mint failed", "err", err)
		writeError(w, http.StatusInternalServerError, "id mint failed")
		return
	}

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

	t0 := time.Now()
	inserted, err := m.deps.ExternalDeliveryStore.Insert(ctx, delivery, body)
	elapsed := time.Since(t0)
	if elapsed > 300*time.Millisecond {
		slog.Warn("velox deliver: insert slow",
			"elapsed_ms", elapsed.Milliseconds(),
			"idempotency_key", veloxReq.IdempotencyKey,
			"is_contract_path", isContractPath)
	}

	if err != nil {
		if errors.Is(err, repository.ErrIdempotencyConflict) {
			var existingID string
			if inserted != nil {
				existingID = inserted.ID
			}
			slog.Info("velox deliver: replay with different sha rejected",
				"idempotency_key", veloxReq.IdempotencyKey,
				"existing_social_delivery_id", existingID,
				"is_contract_path", isContractPath,
			)
			writeJSON(w, http.StatusConflict, VeloxDeliverArtifactConflictResponse{
				Error:          "idempotency_key_conflict",
				Code:           "idempotency_key_conflict",
				IdempotencyKey: veloxReq.IdempotencyKey,
			})
			return
		}
		slog.Error("velox deliver: insert failed",
			"err", err,
			"idempotency_key", veloxReq.IdempotencyKey,
			"is_contract_path", isContractPath)
		writeError(w, http.StatusInternalServerError, "delivery persist failed")
		return
	}

	alreadyExists := inserted.ID != mintedID
	slog.Info("velox deliver: accepted",
		"social_delivery_id", inserted.ID,
		"idempotency_key", veloxReq.IdempotencyKey,
		"already_exists", alreadyExists,
		"elapsed_ms", elapsed.Milliseconds(),
		"is_contract_path", isContractPath,
	)

	if isContractPath {
		writeJSON(w, http.StatusAccepted, VeloxDeliverContractResponse{
			DeliveryID: inserted.ID,
			Status:     "accepted",
			Duplicate:  alreadyExists,
		})
		return
	}
	writeJSON(w, http.StatusAccepted, VeloxDeliverArtifactResponse{
		SocialDeliveryID: inserted.ID,
		Status:           "accepted",
		AlreadyExists:    alreadyExists,
	})
}
