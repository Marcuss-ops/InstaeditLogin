// Package veloxcontract holds the shared InstaEdit⇄Velox BFF contract:
// the wire DTOs, the Client interface, the sentinel errors, and the
// control-JWT scope taxonomy.
//
// It exists to break the layering inversion where the concrete client
// implementation (internal/veloxclient) imported the HTTP layer
// (pkg/api/velox) just to reach these types. The package is a strict
// LEAF: standard library only. Both sides depend on it:
//
//	pkg/api/velox        (BFF handlers)  ──┐
//	                                       ├──▶ internal/veloxcontract
//	internal/veloxclient (implementation) ─┘
//
// pkg/api/velox re-exports every name below via type/const/var
// aliases so its existing consumers (pkg/api router + modules) keep
// compiling unchanged; new code may import either package, but code
// under internal/ MUST import this one (never pkg/api/velox) to keep
// the dependency arrow pointing inward.
package veloxcontract

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// --- Wire types -----------------------------------------------------------
//
// These mirror the architectural spec's response shapes. WorkspaceID
// is tagged `json:"-"` so it is never serialized to the browser; it is
// only used server-side for the ownership check.

// Job is the BFF view of a Velox rendering job.
type Job struct {
	ID                string    `json:"id"`
	WorkspaceID       int64     `json:"workspace_id"`
	ProjectID         string    `json:"project_id,omitempty"`
	RenderStatus      string    `json:"render_status"`
	PublicationStatus string    `json:"publication_status"`
	OverallStatus     string    `json:"overall_status"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Delivery is the BFF view of a social delivery associated with a job.
// It merges Velox's delivery row with the InstaEdit social_delivery
// state so the frontend renders one unified status.
type Delivery struct {
	PublicationID         string `json:"publication_id,omitempty"`
	ExternalDestinationID string `json:"external_destination_id"`
	SocialDeliveryID      string `json:"social_delivery_id"`
	Status                string `json:"status"`
	Phase                 string `json:"phase,omitempty"`
	Attempt               int    `json:"attempt,omitempty"`
	NextRetryAt           string `json:"next_retry_at,omitempty"`
	LastErrorCode         string `json:"last_error_code,omitempty"`
	LastErrorMessage      string `json:"last_error_message,omitempty"`
	RetryFrom             string `json:"retry_from,omitempty"`
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

// CreateJobRequest is the shared client DTO for canonical job creation.
// The canonical route adapts JobSubmissionRequest into this DTO after
// strict validation.
// workspace_id and user_id are NOT in this body; the handler reads
// them from the session identity.
type CreateJobRequest struct {
	ContractVersion string `json:"contract_version"`
	IdempotencyKey  string `json:"idempotency_key"`

	// Canonical velox.job.v1 fields. They are pointers/raw JSON so the
	// client can preserve exact presence and nested JSON semantics.
	JobType         string          `json:"job_type,omitempty"`
	TemplateID      string          `json:"template_id,omitempty"`
	TemplateVersion int             `json:"template_version,omitempty"`
	VideoName       string          `json:"video_name,omitempty"`
	Spec            json.RawMessage `json:"spec,omitempty"`
	Output          *JobOutput      `json:"output,omitempty"`

	// Downstream response-compatible fields retained by the shared client
	// DTO until the Velox InstaEdit handler consumes the canonical envelope
	// directly.
	ProjectID    string             `json:"project_id,omitempty"`
	RenderSpec   json.RawMessage    `json:"render_spec,omitempty"`
	DeliveryPlan DeliveryPlan       `json:"delivery_plan"`
	PublishAt    string             `json:"publish_at,omitempty"`
	Target       *PublicationTarget `json:"target,omitempty"`
	Publications json.RawMessage    `json:"publications,omitempty"`
	RenderOnly   bool               `json:"render_only,omitempty"`
}

// PublicationTarget identifies the logical destination selected in
// InstaEdit Social. It never replaces opaque external destination IDs.
type PublicationTarget struct {
	Type        string   `json:"type"`
	ChannelID   string   `json:"channel_id,omitempty"`
	ChannelName string   `json:"channel_name,omitempty"`
	GroupID     int64    `json:"group_id,omitempty"`
	GroupName   string   `json:"group_name,omitempty"`
	ChannelIDs  []string `json:"channel_ids,omitempty"`
}

// UnmarshalJSON keeps the canonical client DTO strict and prevents
// accidental mixing of envelope shapes at the client boundary.
func (r *CreateJobRequest) UnmarshalJSON(data []byte) error {
	type plain CreateJobRequest
	var envelope struct {
		*plain
		JobType         json.RawMessage `json:"job_type"`
		TemplateID      json.RawMessage `json:"template_id"`
		TemplateVersion json.RawMessage `json:"template_version"`
		VideoName       json.RawMessage `json:"video_name"`
		Spec            json.RawMessage `json:"spec"`
		Output          json.RawMessage `json:"output"`
	}
	envelope.plain = new(plain)
	if err := decodeStrict(data, &envelope); err != nil {
		return err
	}
	for name, value := range map[string]json.RawMessage{
		"job_type":         envelope.JobType,
		"template_id":      envelope.TemplateID,
		"template_version": envelope.TemplateVersion,
		"video_name":       envelope.VideoName,
		"spec":             envelope.Spec,
		"output":           envelope.Output,
	} {
		if len(value) != 0 {
			return errors.New("canonical field " + name + " cannot be decoded into the client DTO")
		}
	}
	*r = CreateJobRequest(*envelope.plain)
	return nil
}

// DeliveryPlan is the nested delivery_plan block of CreateJobRequest.
type DeliveryPlan struct {
	Destinations []DeliveryDestination `json:"destinations"`
}

// UnmarshalJSON keeps nested legacy payloads strict as well as the
// top-level envelope. This prevents silently dropping misspelled delivery
// fields during the migration.
func (p *DeliveryPlan) UnmarshalJSON(data []byte) error {
	type plain DeliveryPlan
	var decoded plain
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	*p = DeliveryPlan(decoded)
	return nil
}

// DeliveryDestination references an InstaEdit-managed destination by
// its opaque external_destination_id plus per-delivery metadata.
type DeliveryDestination struct {
	ExternalDestinationID string          `json:"external_destination_id"`
	PublicationID         string          `json:"publication_id,omitempty"`
	Metadata              json.RawMessage `json:"metadata"`
}

// UnmarshalJSON applies the same unknown-field policy to each delivery
// destination object.
func (d *DeliveryDestination) UnmarshalJSON(data []byte) error {
	type plain DeliveryDestination
	var decoded plain
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	*d = DeliveryDestination(decoded)
	return nil
}

// ListJobsFilter carries optional query parameters for GET /api/v1/velox/jobs.
type ListJobsFilter struct {
	Status string
	Limit  int
	Cursor string
}

// JobsPage is the cursor-paginated Velox jobs response exposed by the
// optional paging capability. The legacy ListJobs method remains available
// for callers that only need a bounded first page.
type JobsPage struct {
	Jobs       []Job
	NextCursor string
	HasMore    bool
}

// JobsPager is an additive capability: old Client implementations can keep
// implementing ListJobs, while the concrete client can expose upstream
// cursor metadata without changing the legacy method's return type.
type JobsPager interface {
	ListJobsPage(ctx context.Context, workspaceID, userID int64, filter ListJobsFilter) (JobsPage, error)
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
	ListJobs(ctx context.Context, workspaceID, userID int64, filter ListJobsFilter) ([]Job, error)
	CreateJob(ctx context.Context, workspaceID, userID int64, req CreateJobRequest) (*Job, error)
	GetJob(ctx context.Context, workspaceID, userID int64, jobID string) (*JobDetail, error)
	CancelJob(ctx context.Context, workspaceID, userID int64, jobID string) error
	ListJobDeliveries(ctx context.Context, workspaceID, userID int64, jobID string) ([]Delivery, error)
	ListWorkers(ctx context.Context, workspaceID, userID int64) ([]Worker, error)
	GetWorker(ctx context.Context, workspaceID, userID int64, workerID string) (*Worker, error)
	GetAsset(ctx context.Context, workspaceID, userID int64, assetID string) (*Asset, error)
}

// --- Sentinel errors ------------------------------------------------------
//
// Mapped to HTTP status codes by the handlers. Implementations of
// Client should wrap these via %w so errors.Is works.

var (
	// ErrNotFound is returned by the Client when the upstream Velox
	// resource does not exist. It is used for jobs, workers, and
	// assets so the BFF maps every 404 to the same 404 response
	// without leaking which resource type was requested.
	ErrNotFound = errors.New("velox: not found")
	// ErrWorkspaceMismatch is returned when the upstream Velox
	// response belongs to a different workspace than the one signed
	// into the control JWT.
	ErrWorkspaceMismatch   = errors.New("velox: workspace mismatch")
	ErrIdempotencyConflict = errors.New("velox: idempotency conflict")
)

// --- Control-JWT scope taxonomy --------------------------------------------
//
// Each BFF API call signs a JWT containing ONLY the scope(s) that the
// operation needs (per-call, not all-scopes): the Velox middleware
// MUST see exactly the scope it requires on the route being called;
// extra scopes are accepted but a missing scope is a hard 403
// ("insufficient scope").
//
// Naming matches VeloxEditiingg/internal/instaeditauth/scopes.go
// (declared as the authoritative source on the Velox side). The
// values are duplicated here so a drift between the two repos
// surfaces as a 403 at the first call, not at deploy time.
//
//	jobs.read    : read rendering jobs and their deliveries (Velox
//	                 GET /api/v1/instaedit/jobs,
//	                 GET .../jobs/{id}, GET .../jobs/{id}/deliveries)
//	jobs.write   : mutate the job lifecycle (Velox POST .../jobs,
//	                 POST .../jobs/{id}/cancel)
//	workers.read : read compute workers (Velox GET .../workers,
//	                 GET .../workers/{id})
//	assets.read  : read render assets (Velox GET .../assets/{id})
//	assets.write : upload a render asset (Velox
//	                 PUT/POST .../assets/*)
const (
	ScopeVeloxJobsRead    = "jobs.read"
	ScopeVeloxJobsWrite   = "jobs.write"
	ScopeVeloxWorkersRead = "workers.read"
	ScopeVeloxAssetsRead  = "assets.read"
	ScopeVeloxAssetsWrite = "assets.write"
	// Editor scopes are reserved for the project-scoped bridge. They
	// must never be used for catalog or workspace-wide operations.
	ScopeVeloxEditorRead  = "editor.read"
	ScopeVeloxEditorWrite = "editor.write"
)
