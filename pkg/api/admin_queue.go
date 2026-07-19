package api

import (
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AdminQueueResponse is the JSON body for GET /admin/queue. Includes
// queue depth by status (counts), stuck-job count (D3.c ∪ D3.a
// combined), and per-worker in-flight breakdown.
type AdminQueueResponse struct {
	Counts        repository.AdminQueueCounts      `json:"counts"`
	InFlight      []repository.AdminInFlightRow    `json:"in_flight_per_worker"`
	Generated     int64                            `json:"generated_at_unix"`
}

// handleAdminQueue (GET /admin/queue) returns the queue depth +
// stuck count + in-flight per worker. The query is a single SELECT
// (counts) + one GROUP BY (in-flight) + one COUNT (stuck); no
// per-row pagination so the dashboard always has the freshest
// snapshot.
func (r *Router) handleAdminQueue(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	counts, err := r.adminStore.QueueCounts(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load queue counts: "+err.Error())
		return
	}
	inFlight, err := r.adminStore.InFlightPerWorker(req.Context())
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
func (r *Router) handleAdminQueueCSV(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	stuck, err := r.adminStore.ListStuckJobs(req.Context(), 200)
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
