package veloxjobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

type submissionClient struct {
	received veloxcontract.CreateJobRequest
	job      *veloxcontract.Job
	err      error
}

func (c *submissionClient) ListJobs(context.Context, int64, int64, veloxcontract.ListJobsFilter) ([]veloxcontract.Job, error) {
	return nil, nil
}
func (c *submissionClient) CreateJob(_ context.Context, _, _ int64, req veloxcontract.CreateJobRequest) (*veloxcontract.Job, error) {
	c.received = req
	return c.job, c.err
}
func (c *submissionClient) GetJob(context.Context, int64, int64, string) (*veloxcontract.JobDetail, error) {
	return nil, nil
}
func (c *submissionClient) CancelJob(context.Context, int64, int64, string) error { return nil }
func (c *submissionClient) ListJobDeliveries(context.Context, int64, int64, string) ([]veloxcontract.Delivery, error) {
	return nil, nil
}
func (c *submissionClient) ListWorkers(context.Context, int64, int64) ([]veloxcontract.Worker, error) {
	return nil, nil
}
func (c *submissionClient) GetWorker(context.Context, int64, int64, string) (*veloxcontract.Worker, error) {
	return nil, nil
}
func (c *submissionClient) GetAsset(context.Context, int64, int64, string) (*veloxcontract.Asset, error) {
	return nil, nil
}

func TestSubmitLegacyPreservesRenderSpecAndForwardsThroughClient(t *testing.T) {
	client := &submissionClient{job: &veloxcontract.Job{ID: "job_1", WorkspaceID: 42}}
	service := NewSubmissionService(client, NewDefaultRegistry())
	req := veloxcontract.CreateJobRequest{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "legacy-1",
		ProjectID:       "project-1",
		RenderSpec:      json.RawMessage(`{"scenes":[{"text":"hello"}],"voiceover_paths":["asset://voice"]}`),
		DeliveryPlan:    veloxcontract.DeliveryPlan{Destinations: []veloxcontract.DeliveryDestination{{ExternalDestinationID: "dest-1"}}},
	}
	result, err := service.SubmitLegacy(context.Background(), 42, 7, req)
	if err != nil {
		t.Fatalf("SubmitLegacy: %v", err)
	}
	if result == nil || !result.Legacy || result.Job.ID != "job_1" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if string(client.received.RenderSpec) != string(req.RenderSpec) {
		t.Fatalf("legacy render_spec changed: got %s want %s", client.received.RenderSpec, req.RenderSpec)
	}
	if result.JobType != "legacy.render.v1" || result.Estimate.RenderUnits <= 0 {
		t.Fatalf("legacy canonical pipeline metadata missing: %+v", result)
	}
}

func TestSubmitLegacyRejectsInvalidRequestBeforeClient(t *testing.T) {
	client := &submissionClient{job: &veloxcontract.Job{ID: "should-not-happen", WorkspaceID: 42}}
	service := NewSubmissionService(client, NewDefaultRegistry())
	_, err := service.SubmitLegacy(context.Background(), 42, 7, veloxcontract.CreateJobRequest{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "legacy-invalid",
		ProjectID:       "project-1",
		RenderSpec:      json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrInvalidSubmission) {
		t.Fatalf("error = %v, want ErrInvalidSubmission", err)
	}
	if client.received.ProjectID != "" {
		t.Fatal("client should not receive invalid legacy request")
	}
}
