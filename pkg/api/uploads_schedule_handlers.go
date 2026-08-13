package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

type editScheduledUploadRequest struct {
	Title    *string         `json:"title"`
	Caption  *string         `json:"caption"`
	Metadata json.RawMessage `json:"metadata"`
}

// handleEditScheduledUpload updates the current draft used by the deferred
// Drive worker. It is intentionally limited to pending jobs: once preparation
// creates a post, the normal post PATCH endpoint is the authoritative draft.
func (r *Router) handleEditScheduledUpload(w http.ResponseWriter, req *http.Request) {
	editor, ok := r.uploadJobStore.(ScheduledUploadEditor)
	if r.uploadJobStore == nil || !ok {
		writeError(w, http.StatusNotImplemented, "scheduled upload editing not configured")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	jobID, err := parseInt64PathParam(req, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var body editScheduledUploadRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Title == nil && body.Caption == nil && body.Metadata == nil {
		writeError(w, http.StatusBadRequest, "at least one metadata field is required")
		return
	}
	job, err := editor.UpdateScheduledContent(req.Context(), jobID, identity.UserID(), body.Title, body.Caption, body.Metadata, body.Metadata != nil)
	if err != nil {
		if errors.Is(err, repository.ErrUploadJobNotFound) {
			writeError(w, http.StatusNotFound, "upload job not found or no longer pending")
			return
		}
		slog.Warn("scheduled upload metadata update failed", "job_id", jobID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update scheduled upload")
		return
	}
	writeJSON(w, http.StatusOK, toUploadJobDTO(&job))
}

// rescheduleUploadRequest is the body for PATCH /api/v1/uploads/{id}/reschedule.
// We accept only the new publish_at — title, caption, and targets
// remain unchanged (a future "edit" endpoint can fan those out).
//
// P1#4 — publish_at is canonical; scheduled_at is the legacy alias
// kept for one-minor-version back-compat. If both keys are present
// and parseable, publish_at wins (consistent with
// CreatePostRequest.ResolvePublishAt).
type rescheduleUploadRequest struct {
	// publish_at is canonical.
	PublishAt *time.Time `json:"publish_at,omitempty"`
	// scheduled_at is the legacy alias.
	ScheduledAt time.Time `json:"scheduled_at"`
}

// resolvePublishAt returns the canonical cursor with publish_at
// precedence.
func (r rescheduleUploadRequest) resolvePublishAt() time.Time {
	if r.PublishAt != nil && !r.PublishAt.IsZero() {
		return *r.PublishAt
	}
	return r.ScheduledAt
}

// handleRescheduleUpload moves a pending upload_job to a new
// publish_at. The dashboard calendar uses this on drag-drop. Returns
// 200 with the updated row.
func (r *Router) handleRescheduleUpload(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	jobID, err := parseInt64PathParam(req, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var body rescheduleUploadRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// P1#4 — canonical publish_at wins; scheduled_at falls back.
	newPublishAt := body.resolvePublishAt()
	if newPublishAt.IsZero() {
		writeError(w, http.StatusBadRequest, "publish_at (or scheduled_at alias) is required (RFC3339)")
		return
	}
	if newPublishAt.Before(time.Now().Add(-1 * time.Minute)) {
		// Past dates collapse the anti-pattern-detection jitter: a
		// video "scheduled for yesterday" publishes immediately on
		// the next worker tick. The publish-flow ALREADY supports
		// that (manual "Publish now" path), so dashboard reschedule
		// intentionally rejects past dates and forces the user to
		// use Publish-now if that's what they want.
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("publish_at must be in the future (use /api/v1/posts/{id}/publish to publish immediately instead) [got %s]", newPublishAt.Format(time.RFC3339)))
		return
	}
	// 5-second floor: a drag-drop that resolves to "literally now"
	// would race the publish_worker's next tick and surface as a
	// "completed before the SPA optimistic update" race. Require a
	// few seconds of headroom so the optimistic UI sees the chip in
	// its new bucket before the worker picks it up.
	if newPublishAt.Before(time.Now().Add(5 * time.Second)) {
		writeError(w, http.StatusBadRequest, "publish_at must be at least 5 seconds in the future")
		return
	}
	// Blocco #2 P0 — horizon is config-driven (env
	// PUBLISH_HORIZON_DAYS, default 30). The single-account reschedule
	// endpoint mirrors the batch V2 producer's horizon so a user can't
	// drag-drop a single video beyond the same cap the batch enforces.
	maxHorizonDays := r.publishHorizonDays()
	maxHorizon := time.Now().Add(time.Duration(maxHorizonDays) * 24 * time.Hour)
	if newPublishAt.After(maxHorizon) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("publish_at must be within %d days from now", maxHorizonDays),
		)
		return
	}

	job, err := r.uploadJobStore.Reschedule(jobID, identity.UserID(), newPublishAt)
	if err != nil {
		if errors.Is(err, repository.ErrUploadJobNotFound) {
			writeError(w, http.StatusNotFound,
				"upload job not found or no longer pending (the worker may have already started publishing — refresh and try again)",
			)
			return
		}
		slog.Warn("uploads reschedule failed",
			"user_id", identity.UserID(),
			"job_id", jobID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not reschedule upload")
		return
	}
	writeJSON(w, http.StatusOK, toUploadJobDTO(&job))
}

// handleCancelUpload deletes a pending upload_job. The dashboard
// calendar uses this on the "trash" button. Returns 204.
//
// State-machine contract mirrors Reschedule: only pending rows can be
// cancelled. Once the worker has claimed the row (processing) or it's
// terminal (completed/failed), the DELETE matches zero rows and the
// handler returns 404 — same UX surface as Reschedule (no info leak).
func (r *Router) handleCancelUpload(w http.ResponseWriter, req *http.Request) {
	if r.uploadJobStore == nil {
		writeError(w, http.StatusNotImplemented, "upload jobs not configured on this server")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	jobID, err := parseInt64PathParam(req, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	err = r.uploadJobStore.Cancel(jobID, identity.UserID())
	if err != nil {
		if errors.Is(err, repository.ErrUploadJobNotFound) {
			writeError(w, http.StatusNotFound, "upload job not found or no longer pending")
			return
		}
		slog.Warn("uploads cancel failed",
			"user_id", identity.UserID(),
			"job_id", jobID,
			"error", err,
		)
		writeError(w, http.StatusInternalServerError, "could not cancel upload")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
