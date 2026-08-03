package velox

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxjobs"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// listJobs implements GET /api/v1/velox/jobs.
//
// Query parameters (all optional):
//
//	?status=<render_status>   filter by Velox render status
//	?limit=<int>              cap on rows (default 100, max 500)
//
// The workspace scope comes from the session identity; the Client
// signs it into the outbound JWT so Velox scopes the query.
func (b *bff) listJobs(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	filter := ListJobsFilter{
		Status: req.URL.Query().Get("status"),
		Limit:  100,
	}
	if l := req.URL.Query().Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > 500 {
			n = 500
		}
		filter.Limit = n
	}
	jobs, err := b.deps.Client.ListJobs(req.Context(), wsID, userID, filter)
	if err != nil {
		slog.Error("velox bff: list jobs failed", "workspace_id", wsID, "err", err)
		writeError(w, http.StatusInternalServerError, "upstream call failed")
		return
	}
	// Defense-in-depth: drop any job whose WorkspaceID does not match
	// the session. Velox should already scope by the signed JWT, but
	// this prevents a misconfigured Velox from leaking cross-tenant
	// rows. Mirrors the same pattern used by listWorkers.
	safe := jobs[:0]
	for _, j := range jobs {
		if j.WorkspaceID == wsID {
			safe = append(safe, j)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs": safe,
	})
}

// createJob implements POST /api/v1/velox/jobs.
//
// The body carries the canonical Velox job contract, project_id,
// render_spec and delivery_plan.
// workspace_id and user_id are read from the session identity and
// forwarded to Velox via the signed Client call — they NEVER come
// from the browser body.
func (b *bff) createJob(w http.ResponseWriter, req *http.Request) {
	outcome := metrics.LegacyJobOutcomeValidation
	defer func() {
		metrics.RecordLegacyJobEndpointUsage(metrics.LegacyJobEndpointVeloxJobs, outcome)
	}()
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		outcome = metrics.LegacyJobOutcomeAuth
		return
	}
	var body CreateJobRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		outcome = metrics.LegacyJobOutcomeBadRequest
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		outcome = metrics.LegacyJobOutcomeBadRequest
		if err == nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: multiple values")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return
	}
	result, err := b.submission.SubmitLegacy(req.Context(), wsID, userID, body)
	if err != nil {
		if errors.Is(err, veloxjobs.ErrInvalidSubmission) {
			outcome = metrics.LegacyJobOutcomeValidation
			writeError(w, http.StatusUnprocessableEntity, "validation: "+err.Error())
			return
		}
		if errors.Is(err, veloxjobs.ErrNilJob) {
			outcome = metrics.LegacyJobOutcomeUpstream
			writeError(w, http.StatusInternalServerError, "upstream call failed")
			return
		}
		if errors.Is(err, ErrWorkspaceMismatch) || errors.Is(err, ErrNotFound) {
			outcome = metrics.LegacyJobOutcomeMismatch
		} else {
			outcome = metrics.LegacyJobOutcomeUpstream
		}
		slog.Error("velox bff: create job failed",
			"workspace_id", wsID, "user_id", userID, "err", err)
		mapClientError(w, err)
		return
	}
	job := result.Job
	// Defense-in-depth: verify the returned job belongs to the
	// caller's workspace before returning 201. A misconfigured Velox
	// could return a job stamped with a different workspace; reject
	// it rather than leak a cross-tenant resource id.
	if !verifyOwnership(w, job.WorkspaceID, wsID) {
		outcome = metrics.LegacyJobOutcomeMismatch
		return
	}
	outcome = metrics.LegacyJobOutcomeAccepted
	slog.Info("velox bff: job created",
		"job_id", job.ID, "workspace_id", wsID, "user_id", userID)
	writeJSON(w, http.StatusAccepted, job)
}

// createCanonicalJob implements POST /api/v1/jobs with the strict
// canonical velox.job.v1 envelope. It reuses the existing client call
// only after adapting the canonical request to the shared client DTO.
func (b *bff) createCanonicalJob(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	var body JobSubmissionRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: multiple values")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		return
	}
	result, err := b.submission.SubmitCanonical(req.Context(), wsID, userID, body)
	if err != nil {
		if errors.Is(err, veloxjobs.ErrInvalidSubmission) {
			message := err.Error()
			if strings.Contains(message, "unknown velox job_type") {
				message = "validation: unknown job_type"
			} else {
				message = "validation: " + message
			}
			writeError(w, http.StatusUnprocessableEntity, message)
			return
		}
		slog.Error("velox bff: canonical job create failed",
			"workspace_id", wsID, "user_id", userID, "err", err)
		mapClientError(w, err)
		return
	}
	job := result.Job
	estimate := result.Estimate
	if !verifyOwnership(w, job.WorkspaceID, wsID) {
		return
	}
	slog.Info("velox bff: canonical job created",
		"job_id", job.ID, "workspace_id", wsID, "user_id", userID,
		"job_type", body.JobType, "template_id", body.TemplateID,
		"render_units", estimate.RenderUnits,
		"estimated_duration_ms", estimate.EstimatedDurationMS)
	writeJSON(w, http.StatusAccepted, job)
}

// getJob implements GET /api/v1/velox/jobs/{id}.
//
// Returns the aggregated JobDetail (job + deliveries) so the
// frontend renders rendering status and publishing status as a
// single unified view. Verifies the job belongs to the session's
// workspace before returning.
func (b *bff) getJob(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	jobID := chi.URLParam(req, "id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}
	detail, err := b.deps.Client.GetJob(req.Context(), wsID, userID, jobID)
	if err != nil {
		slog.Error("velox bff: get job failed", "job_id", jobID, "err", err)
		mapClientError(w, err)
		return
	}
	if !verifyOwnership(w, detail.Job.WorkspaceID, wsID) {
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// cancelJob implements POST /api/v1/velox/jobs/{id}/cancel.
//
// Returns 204 No Content on success. The workspace scope is signed
// into the outbound JWT; Velox rejects a cancel for a job outside
// the caller's workspace.
func (b *bff) cancelJob(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	jobID := chi.URLParam(req, "id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}
	if err := b.deps.Client.CancelJob(req.Context(), wsID, userID, jobID); err != nil {
		slog.Error("velox bff: cancel job failed", "job_id", jobID, "err", err)
		mapClientError(w, err)
		return
	}
	slog.Info("velox bff: job cancelled", "job_id", jobID, "workspace_id", wsID)
	w.WriteHeader(http.StatusNoContent)
}

// listJobDeliveries implements GET /api/v1/velox/jobs/{id}/deliveries.
//
// Returns the deliveries associated with a job so the frontend can
// show per-destination publishing status. Verifies the job belongs
// to the session's workspace via the Client's signed JWT.
func (b *bff) listJobDeliveries(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	jobID := chi.URLParam(req, "id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}
	deliveries, err := b.deps.Client.ListJobDeliveries(req.Context(), wsID, userID, jobID)
	if err != nil {
		slog.Error("velox bff: list job deliveries failed", "job_id", jobID, "err", err)
		mapClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deliveries": deliveries,
	})
}
