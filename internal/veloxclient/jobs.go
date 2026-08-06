package veloxclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// ListJobs implements veloxapi.Client.ListJobs. The workspace scope
// is signed into the JWT; Velox scopes the query. The BFF handler
// additionally filters the returned rows by WorkspaceID as
// defense-in-depth.
//
// Permission: jobs.read.
func (c *Client) ListJobs(ctx context.Context, workspaceID, userID int64, filter veloxapi.ListJobsFilter) ([]veloxapi.Job, error) {
	page, err := c.ListJobsPage(ctx, workspaceID, userID, filter)
	if err != nil {
		return nil, err
	}
	return page.Jobs, nil
}

// ListJobsPage is the additive cursor-aware jobs read. The upstream
// envelope remains backward-compatible because next_cursor/has_more are
// optional, while the BFF can use them whenever the provider supports it.
func (c *Client) ListJobsPage(ctx context.Context, workspaceID, userID int64, filter veloxapi.ListJobsFilter) (veloxapi.JobsPage, error) {
	q := url.Values{}
	if filter.Status != "" {
		q.Set("status", filter.Status)
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	if filter.Cursor != "" {
		q.Set("cursor", filter.Cursor)
	}
	path := "/api/v1/instaedit/jobs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	// For list/read calls the user_id in the JWT is informational
	// (Velox scopes by workspace_id); we pass workspaceID as both
	// sub and workspace_id so the verifier has a non-zero subject.
	var resp listJobsResponse
	if err := c.do(ctx, "GET", path, userID, workspaceID, []string{ScopeVeloxJobsRead}, nil, &resp); err != nil {
		return veloxapi.JobsPage{}, err
	}
	jobs := make([]veloxapi.Job, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		jobs = append(jobs, veloxapi.Job{
			ID:                j.ID,
			WorkspaceID:       j.WorkspaceID,
			ProjectID:         j.ProjectID,
			RenderStatus:      j.RenderStatus,
			PublicationStatus: j.PublicationStatus,
			OverallStatus:     j.OverallStatus,
			CreatedAt:         j.CreatedAt,
			UpdatedAt:         j.UpdatedAt,
		})
	}
	return veloxapi.JobsPage{Jobs: jobs, NextCursor: resp.NextCursor, HasMore: resp.HasMore}, nil
}

// CreateJob implements veloxapi.Client.CreateJob. The body carries the
// canonical Velox job contract; workspace_id and user_id are signed into
// the control JWT and are not duplicated in the strict body.
//
// Permission: jobs.write.
func (c *Client) CreateJob(ctx context.Context, workspaceID, userID int64, req veloxapi.CreateJobRequest) (*veloxapi.Job, error) {
	body := createJobRequest{
		ContractVersion: req.ContractVersion,
		IdempotencyKey:  req.IdempotencyKey,
		JobType:         req.JobType,
		TemplateID:      req.TemplateID,
		TemplateVersion: req.TemplateVersion,
		VideoName:       req.VideoName,
		Spec:            json.RawMessage(req.Spec),
		Output:          req.Output,
		ProjectID:       req.ProjectID,
		RenderSpec:      json.RawMessage(req.RenderSpec),
		DeliveryPlan: deliveryPlanReq{
			Destinations: make([]deliveryDestinationReq, 0, len(req.DeliveryPlan.Destinations)),
		},
	}
	for _, d := range req.DeliveryPlan.Destinations {
		body.DeliveryPlan.Destinations = append(body.DeliveryPlan.Destinations, deliveryDestinationReq{
			ExternalDestinationID: d.ExternalDestinationID,
			PublicationID:         d.PublicationID,
			Metadata:              json.RawMessage(d.Metadata),
		})
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("veloxclient: marshal create job: %w", err)
	}
	var resp jobResponse
	if err := c.do(ctx, "POST", "/api/v1/instaedit/jobs", userID, workspaceID, []string{ScopeVeloxJobsWrite}, bytes.NewReader(payload), &resp); err != nil {
		return nil, err
	}
	return &veloxapi.Job{
		ID:           resp.ID,
		WorkspaceID:  resp.WorkspaceID,
		ProjectID:    resp.ProjectID,
		RenderStatus: resp.RenderStatus,
		CreatedAt:    resp.CreatedAt,
		UpdatedAt:    resp.UpdatedAt,
	}, nil
}

// GetJob implements veloxapi.Client.GetJob. Returns the aggregated
// JobDetail (job + deliveries) so the BFF renders rendering +
// publishing status as a single view.
//
// Permission: jobs.read.
func (c *Client) GetJob(ctx context.Context, workspaceID, userID int64, jobID string) (*veloxapi.JobDetail, error) {
	var resp jobDetailResponse
	path := fmt.Sprintf("/api/v1/instaedit/jobs/%s", url.PathEscape(jobID))
	if err := c.do(ctx, "GET", path, userID, workspaceID, []string{ScopeVeloxJobsRead}, nil, &resp); err != nil {
		return nil, err
	}
	detail := &veloxapi.JobDetail{
		Job: veloxapi.Job{
			ID:           resp.Job.ID,
			WorkspaceID:  resp.Job.WorkspaceID,
			ProjectID:    resp.Job.ProjectID,
			RenderStatus: resp.Job.RenderStatus,
			CreatedAt:    resp.Job.CreatedAt,
			UpdatedAt:    resp.Job.UpdatedAt,
		},
		Deliveries: make([]veloxapi.Delivery, 0, len(resp.Deliveries)),
	}
	for _, d := range resp.Deliveries {
		detail.Deliveries = append(detail.Deliveries, veloxapi.Delivery{
			ExternalDestinationID: d.ExternalDestinationID,
			SocialDeliveryID:      d.SocialDeliveryID,
			Status:                d.Status,
			PlatformMediaID:       d.PlatformMediaID,
			PlatformURL:           d.PlatformURL,
		})
	}
	return detail, nil
}

// CancelJob implements veloxapi.Client.CancelJob. Returns nil on
// success (Velox responds 204 No Content).
//
// Permission: jobs.write.
func (c *Client) CancelJob(ctx context.Context, workspaceID, userID int64, jobID string) error {
	path := fmt.Sprintf("/api/v1/instaedit/jobs/%s/cancel", url.PathEscape(jobID))
	return c.doNoBody(ctx, "POST", path, userID, workspaceID, []string{ScopeVeloxJobsWrite})
}

// ListJobDeliveries implements veloxapi.Client.ListJobDeliveries.
//
// Permission: jobs.read.
func (c *Client) ListJobDeliveries(ctx context.Context, workspaceID, userID int64, jobID string) ([]veloxapi.Delivery, error) {
	var resp listDeliveriesResponse
	path := fmt.Sprintf("/api/v1/instaedit/jobs/%s/deliveries", url.PathEscape(jobID))
	if err := c.do(ctx, "GET", path, userID, workspaceID, []string{ScopeVeloxJobsRead}, nil, &resp); err != nil {
		return nil, err
	}
	deliveries := make([]veloxapi.Delivery, 0, len(resp.Deliveries))
	for _, d := range resp.Deliveries {
		deliveries = append(deliveries, veloxapi.Delivery{
			ExternalDestinationID: d.ExternalDestinationID,
			SocialDeliveryID:      d.SocialDeliveryID,
			Status:                d.Status,
			PlatformMediaID:       d.PlatformMediaID,
			PlatformURL:           d.PlatformURL,
		})
	}
	return deliveries, nil
}
