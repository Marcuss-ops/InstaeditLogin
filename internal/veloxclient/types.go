// Package veloxclient is the concrete implementation of the
// veloxapi.Client interface (pkg/api/velox/routes.go). It signs a
// short-lived HS256 JWT with VELOX_CONTROL_JWT_SECRET (the
// InstaEdit→Velox internal control secret, distinct from the
// reverse-direction VELOX_API_TOKEN) and calls the Velox master at
// VELOX_CONTROL_URL.
//
// DESIGN (from the architectural spec):
//   - Zero assumptions about Velox's public API shape beyond the
//     endpoints listed below; every call is scoped by workspace_id
//     signed into the JWT so Velox can enforce tenant isolation.
//   - The JWT carries iss=instaedit, aud=velox, sub=<userID>,
//     workspace_id=<int>, scopes=[velox:jobs:read, velox:jobs:write,
//     velox:workers:read, velox:assets:read], exp (3 minutes), jti
//     (random). Velox's instaeditauth.Verifier checks signature,
//     issuer, audience, expiry, and scope.
//   - user_id and workspace_id NEVER come from the request body.
//     They are signed into the JWT so Velox trusts the signature,
//     not caller-supplied headers.
//
// Velox endpoint contract (expected by this client):
//
//	GET    /api/v1/jobs?status=&limit=
//	POST   /api/v1/jobs
//	GET    /api/v1/jobs/{id}
//	POST   /api/v1/jobs/{id}/cancel
//	GET    /api/v1/jobs/{id}/deliveries
//	GET    /api/v1/workers
//	GET    /api/v1/workers/{id}
//	GET    /api/v1/assets/{id}
//
// The client maps Velox's JSON responses into the veloxapi wire types.
// WorkspaceID is parsed from the Velox response and tagged json:"-"
// so it is never re-serialized to the browser (the BFF handler uses
// it only for the ownership check).
package veloxclient

import (
	"encoding/json"
	"time"
)

// jobResponse is the Velox master's representation of a job. The
// client converts it to veloxapi.Job (defined in pkg/api/velox) before
// returning to the BFF handler. WorkspaceID is included so the BFF
// can verify ownership (defense-in-depth on top of the signed JWT).
type jobResponse struct {
	ID           string    `json:"id"`
	WorkspaceID  int64     `json:"workspace_id"`
	ProjectID    string    `json:"project_id,omitempty"`
	RenderStatus string    `json:"render_status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// jobDetailResponse is the aggregated Velox response for GET
// /api/v1/jobs/{id}: the job plus its deliveries so the BFF can
// render rendering + publishing status as a single view.
type jobDetailResponse struct {
	Job        jobResponse        `json:"job"`
	Deliveries []deliveryResponse `json:"deliveries"`
}

// deliveryResponse is the Velox master's representation of a social
// delivery associated with a job. Velox retains the relationship with
// social_delivery_id; InstaEdit retains the social state.
type deliveryResponse struct {
	ExternalDestinationID string `json:"external_destination_id"`
	SocialDeliveryID      string `json:"social_delivery_id"`
	Status                string `json:"status"`
	PlatformMediaID       string `json:"platform_media_id,omitempty"`
	PlatformURL           string `json:"platform_url,omitempty"`
}

// workerResponse is the Velox master's representation of a compute
// worker. WorkspaceID scopes visibility (a worker belongs to the
// workspace that provisioned it).
type workerResponse struct {
	ID          string `json:"id"`
	WorkspaceID int64  `json:"workspace_id"`
	Status      string `json:"status"`
	CPU         int    `json:"cpu,omitempty"`
	RAMMB       int    `json:"ram_mb,omitempty"`
	GPU         string `json:"gpu,omitempty"`
	DiskGB      int    `json:"disk_gb,omitempty"`
}

// assetResponse is the Velox master's representation of a rendered
// artifact. The download_url is included so the BFF could, if needed,
// proxy a time-limited fetch; WorkspaceID scopes ownership.
type assetResponse struct {
	ID          string `json:"id"`
	WorkspaceID int64  `json:"workspace_id"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	MimeType    string `json:"mime_type"`
	DownloadURL string `json:"download_url,omitempty"`
}

// listJobsResponse wraps the Velox list endpoint envelope.
type listJobsResponse struct {
	Jobs []jobResponse `json:"jobs"`
}

// listWorkersResponse wraps the Velox list endpoint envelope.
type listWorkersResponse struct {
	Workers []workerResponse `json:"workers"`
}

// listDeliveriesResponse wraps the Velox deliveries endpoint envelope.
type listDeliveriesResponse struct {
	Deliveries []deliveryResponse `json:"deliveries"`
}

// createJobRequest is the body sent TO Velox. workspace_id and
// user_id are NOT in this body — they are signed into the JWT.
type createJobRequest struct {
	ProjectID    string          `json:"project_id"`
	RenderSpec   json.RawMessage `json:"render_spec"`
	DeliveryPlan deliveryPlanReq `json:"delivery_plan"`
}

// deliveryPlanReq mirrors veloxapi.DeliveryPlan for the outbound body.
type deliveryPlanReq struct {
	Destinations []deliveryDestinationReq `json:"destinations"`
}

// deliveryDestinationReq mirrors veloxapi.DeliveryDestination.
type deliveryDestinationReq struct {
	ExternalDestinationID string          `json:"external_destination_id"`
	Metadata              json.RawMessage `json:"metadata"`
}
