package api

import (
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AdminQueueResponse is the JSON body for GET /admin/queue. Includes
// queue depth by status (counts), stuck-job count (D3.c ∪ D3.a
// combined), and per-worker in-flight breakdown.
type AdminQueueResponse struct {
	Counts    repository.AdminQueueCounts   `json:"counts"`
	InFlight  []repository.AdminInFlightRow `json:"in_flight_per_worker"`
	Generated int64                         `json:"generated_at_unix"`
}

// AdminDeadLetterJobsResponse (Task 10/10) is the JSON body for
// GET /admin/upload_jobs/dead_letter. Lists upload_jobs rows
// whose retry budget has been exhausted (status='dead_letter'),
// ordered by completed_at DESC. The dashboard renders this list
// so operators can triage retry-budget exhaustions and decide
// between manual retry / cancel / ignore per row. Bounded by 500
// rows per the repo's hard cap; subsequent pages are a future
// cursor-based follow-up.
type AdminDeadLetterJobsResponse struct {
	Jobs      []repository.AdminDeadLetterJobRow `json:"jobs"`
	Count     int                                `json:"count"`
	Generated int64                              `json:"generated_at_unix"`
}

// handleAdminQueue (GET /admin/queue) returns the queue depth +
// stuck count + in-flight per worker. The query is a single SELECT
// (counts) + one GROUP BY (in-flight) + one COUNT (stuck); no
// per-row pagination so the dashboard always has the freshest
// snapshot.
func (m *AdminModule) handleAdminQueue(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	counts, err := m.deps.AdminStore.QueueCounts(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load queue counts: "+err.Error())
		return
	}
	inFlight, err := m.deps.AdminStore.InFlightPerWorker(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load in-flight per worker: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, AdminQueueResponse{
		Counts:    counts,
		InFlight:  inFlight,
		Generated: nowUnix(),
	})
}

// handleAdminQueueCSV (GET /admin/queue.csv) streams the STUCK-JOB
// rows (not the full queue). The full queue CSV would dominate the
// file (hundreds of pending rows that aren't actionable); stuck
// rows are the operator's actual to-do. Combined D3.c + D3.a
// classification via the `stuck_reason` column.
func (m *AdminModule) handleAdminQueueCSV(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	stuck, err := m.deps.AdminStore.ListStuckJobs(req.Context(), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list stuck jobs: "+err.Error())
		return
	}

	_, csvw, flush, err := writeAdminCSV(w, "queue-stuck")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "csv writer init failed")
		return
	}
	_ = csvw.Write([]string{
		"job_id", "user_id", "workspace_id", "source_type", "source_id", "title",
		"status", "attempt_count", "lease_owner",
		"heartbeat_at", "lease_expires_at", "started_at", "stuck_reason",
	})
	for _, j := range stuck {
		_ = csvw.Write([]string{
			itoa(j.JobID),
			itoa(j.UserID),
			itoa(j.WorkspaceID),
			j.SourceType,
			j.SourceID,
			j.Title,
			j.Status,
			itoa(int64(j.AttemptCount)),
			j.LeaseOwner,
			formatTimePtr(j.HeartbeatAt),
			formatTimePtr(j.LeaseExpiresAt),
			formatTimePtr(j.StartedAt),
			j.StuckReason,
		})
	}
	if err := flush(); err != nil {
		slogCSVStreamError("queue-stuck", err)
	}
}

// handleAdminUploadJobsDeadLetter (Task 10/10 —
// GET /admin/upload_jobs/dead_letter) returns up to 500
// dead-lettered upload_jobs in JSON form. Sibling CSV variant
// handleAdminUploadJobsDeadLetterCSV is the stream-out shape for
// the same data. The handler is the operator-triage endpoint
// defined in the Definition of Done: every retry-exhausted row
// surfaces here so an operator can decide retry / cancel / ignore
// per row.
func (m *AdminModule) handleAdminUploadJobsDeadLetter(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	jobs, err := m.deps.AdminStore.ListDeadLetterJobs(req.Context(), 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list dead-letter jobs: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, AdminDeadLetterJobsResponse{
		Jobs:      jobs,
		Count:     len(jobs),
		Generated: nowUnix(),
	})
}

// handleAdminUploadJobsDeadLetterCSV (Task 10/10 —
// GET /admin/upload_jobs/dead_letter.csv) streams the dead-lettered
// rows as CSV. Same row shape as the JSON variant but columns
// are first-row-stable so a spreadsheet import "just works".
// Columns: job_id, user_id, workspace_id, source_type, source_id,
// title, status, attempt_count, error_code, error_message,
// dead_lettered_at.
func (m *AdminModule) handleAdminUploadJobsDeadLetterCSV(w http.ResponseWriter, req *http.Request) {
	if m.deps.AdminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	jobs, err := m.deps.AdminStore.ListDeadLetterJobs(req.Context(), 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list dead-letter jobs: "+err.Error())
		return
	}

	_, csvw, flush, err := writeAdminCSV(w, "upload-jobs-dead_letter")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "csv writer init failed")
		return
	}
	_ = csvw.Write([]string{
		"job_id", "user_id", "workspace_id", "source_type", "source_id", "title",
		"status", "attempt_count", "error_code", "error_message", "dead_lettered_at",
	})
	for _, j := range jobs {
		_ = csvw.Write([]string{
			itoa(j.JobID),
			itoa(j.UserID),
			itoa(j.WorkspaceID),
			j.SourceType,
			j.SourceID,
			j.Title,
			j.Status,
			itoa(int64(j.AttemptCount)),
			j.ErrorCode,
			j.ErrorMessage,
			formatTimePtr(j.DeadLetteredAt),
		})
	}
	if err := flush(); err != nil {
		slogCSVStreamError("upload-jobs-dead_letter", err)
	}
}
