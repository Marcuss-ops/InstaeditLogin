// Package velox implements the user-facing Backend-for-Frontend (BFF)
// routes that proxy a bounded subset of Velox operations to the browser.
//
// The package is intentionally a LEAF: it depends only on the standard
// library, chi, and internal/auth. It does NOT import pkg/api — the
// parent Router wires it via Register(mux, Deps{...}) so no import
// cycle is created. The Client interface abstracts the Velox master
// call (the concrete implementation lives in internal/veloxclient,
// created in a separate step, and is injected via Deps.Client).
//
// DESIGN RULES (from the architectural spec):
//   - Expose ONLY explicit endpoints. No generic /api/v1/velox/{anything}
//     catch-all.
//   - user_id and workspace_id NEVER come from the request body. They
//     are read from the session identity (auth.IdentityFromContext)
//     and forwarded to Velox via the signed Client call.
//   - Every read that returns a workspace-scoped resource (job, worker,
//     asset) verifies the returned row's WorkspaceID matches the
//     session's workspace. Mismatch → 404 (no existence leak).
//   - The browser never sees VELOX_API_TOKEN, OAuth tokens, or private
//     Velox URLs. Those live behind the Client.
package velox

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxjobs"
)

// --- Shared contract (re-exported) ----------------------------------------
//
// The wire DTOs, the Client interface, and the sentinel errors moved
// to internal/veloxcontract so the concrete implementation
// (internal/veloxclient) no longer imports this HTTP-layer package
// (the pre-refactor layering inversion). The aliases below keep every
// existing `velox.Job` / `velox.Client` / `velox.ErrNotFound`
// reference in pkg/api compiling unchanged. Code under internal/ MUST
// import internal/veloxcontract directly, never this package.

type (
	// Job is the BFF view of a Velox rendering job.
	Job = veloxcontract.Job
	// Delivery is the BFF view of a social delivery associated with a job.
	Delivery = veloxcontract.Delivery
	// JobDetail is the aggregated response for GET /api/v1/velox/jobs/{id}.
	JobDetail = veloxcontract.JobDetail
	// Worker is the BFF view of a Velox compute worker.
	Worker = veloxcontract.Worker
	// Asset is the BFF view of a Velox artifact.
	Asset = veloxcontract.Asset
	// CreateJobRequest is the client DTO used by the canonical POST /api/v1/jobs route.
	CreateJobRequest = veloxcontract.CreateJobRequest
	// JobOutput is the canonical output block of velox.job.v1.
	JobOutput = veloxcontract.JobOutput
	// JobSubmissionRequest names the canonical velox.job.v1 envelope.
	JobSubmissionRequest = veloxcontract.JobSubmissionRequest
	// DeliveryPlan is the nested delivery_plan block of CreateJobRequest.
	DeliveryPlan = veloxcontract.DeliveryPlan
	// DeliveryDestination references an InstaEdit-managed destination.
	DeliveryDestination = veloxcontract.DeliveryDestination
	// ListJobsFilter carries optional query parameters for GET /api/v1/velox/jobs.
	ListJobsFilter = veloxcontract.ListJobsFilter
	// Client is the contract the BFF handlers depend on.
	Client = veloxcontract.Client
	// JobRegistry resolves canonical technical job types.
	JobRegistry = veloxjobs.Registry
)

var (
	// ErrNotFound is returned by the Client when the upstream Velox
	// resource does not exist.
	ErrNotFound = veloxcontract.ErrNotFound
	// ErrWorkspaceMismatch is returned when the upstream Velox
	// response belongs to a different workspace than the one signed
	// into the control JWT.
	ErrWorkspaceMismatch   = veloxcontract.ErrWorkspaceMismatch
	ErrIdempotencyConflict = veloxcontract.ErrIdempotencyConflict
)

// --- Deps + Register ------------------------------------------------------

// Deps carries the injectable dependencies for the BFF routes. The
// parent Router builds this from its own fields and passes it to
// Register. nil Client = routes not mounted (nil-guard pattern
// matching AdminModule / VeloxModule).
type Deps struct {
	Client         Client
	JobRegistry    *veloxjobs.Registry
	AuthMiddleware func(http.Handler) http.Handler
	CSRFMiddleware func(http.Handler) http.Handler
}

// Register mounts the user-facing BFF Velox routes on mux. No-op when
// deps.Client is nil so a partial deployment surfaces 404 (route not
// mounted) rather than 500.
//
// Route table (explicit only — no catch-all):
//
//	GET    /api/v1/velox/jobs
//	POST   /api/v1/jobs             (canonical velox.job.v1 path)
//	GET    /api/v1/velox/jobs/{id}
//	POST   /api/v1/velox/jobs/{id}/cancel
//	GET    /api/v1/velox/jobs/{id}/deliveries
//	GET    /api/v1/velox/workers
//	GET    /api/v1/velox/workers/{id}
//	GET    /api/v1/velox/assets/{id}
//
// Every route is wrapped with the auth + CSRF chain (auth outermost,
// CSRF inner — matches the IntegrationsModule.Register ordering).
func Register(mux chi.Router, deps Deps) {
	if deps.Client == nil {
		return
	}
	if deps.JobRegistry == nil {
		deps.JobRegistry = veloxjobs.NewDefaultRegistry()
	}
	b := &bff{
		deps:       deps,
		submission: veloxjobs.NewJobSubmissionService(deps.Client, deps.JobRegistry),
	}
	wrap := deps.wrap

	mux.Method(http.MethodGet, "/api/v1/velox/jobs", wrap(b.listJobs))
	mux.Method(http.MethodPost, "/api/v1/jobs", wrap(b.createCanonicalJob))
	mux.Method(http.MethodGet, "/api/v1/velox/jobs/{id}", wrap(b.getJob))
	mux.Method(http.MethodPost, "/api/v1/velox/jobs/{id}/cancel", wrap(b.cancelJob))
	mux.Method(http.MethodGet, "/api/v1/velox/jobs/{id}/deliveries", wrap(b.listJobDeliveries))
	mux.Method(http.MethodGet, "/api/v1/velox/workers", wrap(b.listWorkers))
	mux.Method(http.MethodGet, "/api/v1/velox/workers/{id}", wrap(b.getWorker))
	mux.Method(http.MethodGet, "/api/v1/velox/assets/{id}", wrap(b.getAsset))
}

// wrap composes the CSRF and auth middlewares around a handler.
// CSRF is applied first (innermost), auth second (outermost) so the
// request flows auth → CSRF → handler. Matches the ordering in
// pkg/api/admin_velox_destinations_handlers.go (IntegrationsModule.Register).
func (d Deps) wrap(h http.HandlerFunc) http.Handler {
	var handler http.Handler = h
	if d.CSRFMiddleware != nil {
		handler = d.CSRFMiddleware(handler)
	}
	if d.AuthMiddleware != nil {
		handler = d.AuthMiddleware(handler)
	}
	return handler
}

// bff holds the deps for all handlers. Methods on *bff are the
// handler functions registered by Register.
type bff struct {
	deps       Deps
	submission *veloxjobs.JobSubmissionService
}

// --- Local helpers --------------------------------------------------------
//
// These mirror the unexported helpers in pkg/api/router_helpers.go.
// Duplicated here because this is a leaf package and cannot import
// pkg/api (which would create a cycle when pkg/api imports this
// package to call Register).

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// requireIdentity extracts both workspace_id and user_id from the
// session. Used by all BFF handlers so the real user id is signed
// into the outbound control JWT.
func (b *bff) requireIdentity(w http.ResponseWriter, req *http.Request) (wsID, userID int64, ok bool) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return 0, 0, false
	}
	wsID = id.WorkspaceID()
	userID = id.UserID()
	if wsID <= 0 || userID <= 0 {
		writeError(w, http.StatusForbidden, "session missing workspace or user scope")
		return 0, 0, false
	}
	return wsID, userID, true
}

// verifyOwnership checks that a workspace-scoped resource belongs to
// the session's workspace. Returns true when the resource is safe to
// return to the caller. Writes 404 and returns false on mismatch
// (collapses "not yours" with "does not exist" so the caller cannot
// enumerate by id).
func verifyOwnership(w http.ResponseWriter, resourceWorkspaceID, sessionWorkspaceID int64) bool {
	if resourceWorkspaceID != sessionWorkspaceID {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	return true
}

// mapClientError translates a Client error into an HTTP status + body.
// ErrNotFound and ErrWorkspaceMismatch → 404; idempotency conflicts → 409.
func mapClientError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrWorkspaceMismatch) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, ErrIdempotencyConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error_code": "IDEMPOTENCY_CONFLICT",
			"message":    "idempotency conflict",
		})
		return
	}
	writeError(w, http.StatusInternalServerError, "upstream call failed")
}
