package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// MetadataGenerationStore is the narrow read/write slice of the
// metadata_generation_jobs repository the API layer needs: enqueue a
// job (POST) and poll it (GET). The worker uses a wider surface
// (internal/worker.MetadataGenerationJobStore) for claim/lease/mark.
type MetadataGenerationStore interface {
	Create(job *models.MetadataGenerationJob) error
	FindByID(id int64) (*models.MetadataGenerationJob, error)
}

// Compile-time assertion that the SQL repository satisfies the store.
var _ MetadataGenerationStore = (*repository.MetadataGenerationJobRepository)(nil)

// generateMetadataRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata.
// Prompt is optional — when empty, the service uses no additional
// context and NVIDIA generates metadata from its built-in knowledge.
type generateMetadataRequest struct {
	Prompt string `json:"prompt,omitempty"`
}

// generateMetadataJobResponse is returned by the kick-off endpoint
// (HTTP 202): the job was accepted and will be processed in the
// background. Poll GET /api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}.
type generateMetadataJobResponse struct {
	JobID          int64  `json:"job_id"`
	Status         string `json:"status"`
	VeloxProjectID string `json:"velox_project_id"`
}

// generateMetadataJobPollResponse is returned by the poll endpoint.
// When Status == "completed", Result carries the full generated
// metadata (title/description/tags/languages/translations — the same
// shape the old synchronous endpoint returned). When Status ==
// "failed", ErrorMessage explains why.
type generateMetadataJobPollResponse struct {
	JobID          int64           `json:"job_id"`
	Status         string          `json:"status"`
	VeloxProjectID string          `json:"velox_project_id"`
	Result         json.RawMessage `json:"result,omitempty"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty"`
	CompletedAt    string          `json:"completed_at,omitempty"`
}

// handleGenerateNVIDIAMetadata is the HTTP entry point for
// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata.
//
// ASYNC KICK-OFF (migration 113): the handler validates the editor
// session + workspace access, enqueues a metadata_generation_jobs row,
// and returns 202 + {job_id} immediately. The MetadataGenerationWorker
// calls NVIDIA in the background (60-180s+), and the caller polls
// GET /generate-metadata/jobs/{job_id} until completion. The POST
// never blocks on the NVIDIA call.
//
// The operator reviews the generated values (polled from the job),
// optionally edits them, and only THEN submits them through the
// /publish endpoint.
//
// The NVIDIA API key is NEVER included in any response.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when JSON is malformed or {velox_project_id} is empty.
//   - 404 when the editor session is unknown or not accessible.
//   - 503 when the NVIDIA service is not configured (NVIDIA_API_KEY
//     empty) or the job store is not wired.
//   - 500 when the job could not be persisted.
//   - 202 + {job_id, status:"queued"} on success.
func (r *Router) handleGenerateNVIDIAMetadata(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	veloxProjectID := chi.URLParam(req, "velox_project_id")
	if veloxProjectID == "" {
		writeError(w, http.StatusBadRequest, "velox_project_id is required")
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}
	if r.metadataGenerationStore == nil {
		writeError(w, http.StatusServiceUnavailable, "metadata generation store not configured")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace store not configured")
		return
	}
	if r.nvidiaMetadataSvc == nil || !r.nvidiaMetadataSvc.Configured() {
		writeError(w, http.StatusServiceUnavailable, "NVIDIA AI metadata generation is not configured (set NVIDIA_API_KEY). Manual metadata entry is still available.")
		return
	}

	// Verify the session exists and the caller has access.
	edit, err := r.youtubeVideoEditStore.FindByVeloxProjectID(req.Context(), veloxProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	var payload generateMetadataRequest
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			// Empty body is legal (prompt is optional) — io.EOF is
			// what Decode returns for a zero-length stream.
			if !errors.Is(err, io.EOF) {
				writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return
			}
		}
	}

	job := &models.MetadataGenerationJob{
		WorkspaceID:    edit.WorkspaceID,
		VeloxProjectID: veloxProjectID,
		Prompt:         payload.Prompt,
	}
	if err := r.metadataGenerationStore.Create(job); err != nil {
		writeError(w, http.StatusInternalServerError, "enqueue metadata generation job: "+err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, generateMetadataJobResponse{
		JobID:          job.ID,
		Status:         models.MetadataGenJobQueued,
		VeloxProjectID: veloxProjectID,
	})
}

// handleGetMetadataGenerationJob is the poll endpoint:
// GET /api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}.
//
// Returns the job status and — once completed — the generated metadata
// result. Ownership is verified via the job's workspace (the job_id is
// a BIGSERIAL so it is enumerable — a caller without workspace access
// gets 404, no existence leak).
//
// Behaviour:
//   - 401 without identity; 503 when the store is not wired.
//   - 404 when the job does not exist OR belongs to a workspace the
//     caller cannot access.
//   - 200 + poll response otherwise.
func (r *Router) handleGetMetadataGenerationJob(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	if r.metadataGenerationStore == nil {
		writeError(w, http.StatusServiceUnavailable, "metadata generation store not configured")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace store not configured")
		return
	}

	jobID, err := strconv.ParseInt(chi.URLParam(req, "job_id"), 10, 64)
	if err != nil || jobID <= 0 {
		writeError(w, http.StatusBadRequest, "job_id must be a positive integer")
		return
	}

	job, err := r.metadataGenerationStore.FindByID(jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find metadata generation job: "+err.Error())
		return
	}
	if job == nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	// Ownership check — collapse cross-tenant access into 404.
	workspace, err := r.workspaceStore.FindByID(job.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	resp := generateMetadataJobPollResponse{
		JobID:          job.ID,
		Status:         job.Status,
		VeloxProjectID: job.VeloxProjectID,
		Result:         job.Result,
		ErrorMessage:   job.ErrorMessage,
		CreatedAt:      job.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if job.CompletedAt != nil {
		resp.CompletedAt = job.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	writeJSON(w, http.StatusOK, resp)
}
