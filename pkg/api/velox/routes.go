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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// --- Wire types -----------------------------------------------------------
//
// These mirror the architectural spec's response shapes. WorkspaceID
// is tagged `json:"-"` so it is never serialized to the browser; it is
// only used server-side for the ownership check.

// Job is the BFF view of a Velox rendering job.
type Job struct {
	ID           string    `json:"id"`
	WorkspaceID  int64     `json:"-"`
	ProjectID    string    `json:"project_id,omitempty"`
	RenderStatus string    `json:"render_status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Delivery is the BFF view of a social delivery associated with a job.
// It merges Velox's delivery row with the InstaEdit social_delivery
// state so the frontend renders one unified status.
type Delivery struct {
	ExternalDestinationID string `json:"external_destination_id"`
	SocialDeliveryID      string `json:"social_delivery_id"`
	Status                string `json:"status"`
	PlatformMediaID       string `json:"platform_media_id,omitempty"`
	PlatformURL           string `json:"platform_url,omitempty"`
}

// JobDetail is the aggregated response for GET /api/v1/velox/jobs/{id}.
// It pairs the Velox job with its deliveries so the frontend shows
// rendering + publishing status as a single view.
type JobDetail struct {
	Job        Job        `json:"job"`
	Deliveries []Delivery `json:"deliveries"`
}

// Worker is the BFF view of a Velox compute worker.
type Worker struct {
	ID          string `json:"id"`
	WorkspaceID int64  `json:"-"`
	Status      string `json:"status"`
	CPU         int    `json:"cpu,omitempty"`
	RAMMB       int    `json:"ram_mb,omitempty"`
	GPU         string `json:"gpu,omitempty"`
	DiskGB      int    `json:"disk_gb,omitempty"`
}

// Asset is the BFF view of a Velox artifact.
type Asset struct {
	ID          string `json:"id"`
	WorkspaceID int64  `json:"-"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	MimeType    string `json:"mime_type"`
	DownloadURL string `json:"download_url,omitempty"`
}

// CreateJobRequest is the body for POST /api/v1/velox/jobs. The
// workspace_id and user_id are NOT in this body; the handler reads
// them from the session identity.
type CreateJobRequest struct {
	ProjectID    string          `json:"project_id"`
	RenderSpec   json.RawMessage `json:"render_spec"`
	DeliveryPlan DeliveryPlan    `json:"delivery_plan"`
}

// DeliveryPlan is the nested delivery_plan block of CreateJobRequest.
type DeliveryPlan struct {
	Destinations []DeliveryDestination `json:"destinations"`
}

// DeliveryDestination references an InstaEdit-managed destination by
// its opaque external_destination_id plus per-delivery metadata.
type DeliveryDestination struct {
	ExternalDestinationID string          `json:"external_destination_id"`
	Metadata              json.RawMessage `json:"metadata"`
}

// ListJobsFilter carries optional query parameters for GET /api/v1/velox/jobs.
type ListJobsFilter struct {
	Status string
	Limit  int
}

// --- Client interface -----------------------------------------------------
//
// Client abstracts the Velox master call. The concrete implementation
// (internal/veloxclient) signs a short-lived JWT with
// VELOX_CONTROL_JWT_SECRET and forwards workspace_id + user_id from
// the session. Implementations MUST scope every call by workspaceID
// so a signed-JWT tampering cannot cross tenants.

// Client is the contract the BFF handlers depend on. Every method
// takes workspaceID so the implementation can sign it into the
// outbound JWT; the returned rows carry WorkspaceID so the handler
// can double-check ownership (defense-in-depth).
type Client interface {
	ListJobs(ctx context.Context, workspaceID int64, filter ListJobsFilter) ([]Job, error)
	CreateJob(ctx context.Context, workspaceID, userID int64, req CreateJobRequest) (*Job, error)
	GetJob(ctx context.Context, workspaceID int64, jobID string) (*JobDetail, error)
	CancelJob(ctx context.Context, workspaceID int64, jobID string) error
	ListJobDeliveries(ctx context.Context, workspaceID int64, jobID string) ([]Delivery, error)
	ListWorkers(ctx context.Context, workspaceID int64) ([]Worker, error)
	GetWorker(ctx context.Context, workspaceID int64, workerID string) (*Worker, error)
	GetAsset(ctx context.Context, workspaceID int64, assetID string) (*Asset, error)
}

// --- Sentinel errors ------------------------------------------------------
//
// Mapped to HTTP status codes by the handlers. Implementations of
// Client should wrap these via %w so errors.Is works.

var (
	ErrJobNotFound     = errors.New("velox: job not found")
	ErrWorkerNotFound  = errors.New("velox: worker not found")
	ErrAssetNotFound   = errors.New("velox: asset not found")
	ErrWorkspaceMismatch = errors.New("velox: workspace mismatch")
)

// --- Deps + Register ------------------------------------------------------

// Deps carries the injectable dependencies for the BFF routes. The
// parent Router builds this from its own fields and passes it to
// Register. nil Client = routes not mounted (nil-guard pattern
// matching AdminModule / VeloxModule).
type Deps struct {
	Client         Client
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
//	POST   /api/v1/velox/jobs
//	GET    /api/v1/velox/jobs/{id}
//	POST   /api/v1/velox/jobs/{id}/cancel
//	GET    /api/v1/velox/jobs/{id}/deliveries
//	GET    /api/v1/velox/workers
//	GET    /api/v1/velox/workers/{id}
//	GET    /api/v1/velox/assets/{id}
//
// Every route is wrapped with the auth + CSRF chain (auth outermost,
// CSRF inner — matches the registerUserVeloxDestinations ordering).
func Register(mux chi.Router, deps Deps) {
	if deps.Client == nil {
		return
	}
	b := &bff{deps: deps}
	wrap := deps.wrap

	mux.Method(http.MethodGet, "/api/v1/velox/jobs", wrap(b.listJobs))
	mux.Method(http.MethodPost, "/api/v1/velox/jobs", wrap(b.createJob))
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
// pkg/api/admin_velox_destinations_handlers.go::registerUserVeloxDestinations.
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
	deps Deps
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

// requireWorkspace extracts the session workspace_id. Returns
// (workspaceID, true) on success; writes 401/403 and returns
// (0, false) on failure.
func (b *bff) requireWorkspace(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return 0, false
	}
	wsID := id.WorkspaceID()
	if wsID <= 0 {
		writeError(w, http.StatusForbidden, "session has no workspace scope")
		return 0, false
	}
	return wsID, true
}

// requireIdentity extracts both workspace_id and user_id from the
// session. Used by POST handlers that need to forward user_id to
// Velox (e.g. CreateJob).
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
// Sentinels (ErrJobNotFound etc.) → 404; anything else → 500.
func mapClientError(w http.ResponseWriter, err error, notFoundSentinel error) {
	if errors.Is(err, notFoundSentinel) || errors.Is(err, ErrWorkspaceMismatch) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "upstream call failed")
}
