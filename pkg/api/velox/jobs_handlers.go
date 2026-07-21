package velox

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
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
	wsID, ok := b.requireWorkspace(w, req)
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
	jobs, err := b.deps.Client.ListJobs(req.Context(), wsID, filter)
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
// The body carries project_id, render_spec and delivery_plan only.
// workspace_id and user_id are read from the session identity and
// forwarded to Velox via the signed Client call — they NEVER come
// from the browser body.
func (b *bff) createJob(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	var body CreateJobRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.ProjectID == "" {
		writeError(w, http.StatusUnprocessableEntity, "validation: project_id is required")
		return
	}
	if len(body.RenderSpec) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation: render_spec is required")
		return
	}
	if len(body.DeliveryPlan.Destinations) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation: delivery_plan.destinations must be non-empty")
		return
	}
	for i, d := range body.DeliveryPlan.Destinations {
		if d.ExternalDestinationID == "" {
			writeError(w, http.StatusUnprocessableEntity,
				"validation: delivery_plan.destinations["+strconv.Itoa(i)+"].external_destination_id is required")
			return
		}
	}
	job, err := b.deps.Client.CreateJob(req.Context(), wsID, userID, body)
	if err != nil {
		slog.Error("velox bff: create job failed",
			"workspace_id", wsID, "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "upstream call failed")
		return
	}
	// Defense-in-depth: verify the returned job belongs to the
	// caller's workspace before returning 201. A misconfigured Velox
	// could return a job stamped with a different workspace; reject
	// it rather than leak a cross-tenant resource id.
	if !verifyOwnership(w, job.WorkspaceID, wsID) {
		return
	}
	slog.Info("velox bff: job created",
		"job_id", job.ID, "workspace_id", wsID, "user_id", userID)
	writeJSON(w, http.StatusCreated, job)
}

// getJob implements GET /api/v1/velox/jobs/{id}.
//
// Returns the aggregated JobDetail (job + deliveries) so the
// frontend renders rendering status and publishing status as a
// single unified view. Verifies the job belongs to the session's
// workspace before returning.
func (b *bff) getJob(w http.ResponseWriter, req *http.Request) {
	wsID, ok := b.requireWorkspace(w, req)
	if !ok {
		return
	}
	jobID := chi.URLParam(req, "id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}
	detail, err := b.deps.Client.GetJob(req.Context(), wsID, jobID)
	if err != nil {
		slog.Error("velox bff: get job failed", "job_id", jobID, "err", err)
		mapClientError(w, err, ErrJobNotFound)
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
	wsID, ok := b.requireWorkspace(w, req)
	if !ok {
		return
	}
	jobID := chi.URLParam(req, "id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}
	if err := b.deps.Client.CancelJob(req.Context(), wsID, jobID); err != nil {
		slog.Error("velox bff: cancel job failed", "job_id", jobID, "err", err)
		mapClientError(w, err, ErrJobNotFound)
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
	wsID, ok := b.requireWorkspace(w, req)
	if !ok {
		return
	}
	jobID := chi.URLParam(req, "id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "job id required")
		return
	}
	deliveries, err := b.deps.Client.ListJobDeliveries(req.Context(), wsID, jobID)
	if err != nil {
		slog.Error("velox bff: list job deliveries failed", "job_id", jobID, "err", err)
		mapClientError(w, err, ErrJobNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"deliveries": deliveries,
	})
}

