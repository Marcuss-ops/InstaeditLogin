package veloxjobs

import (
	"context"
	"encoding/json"
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

func TestSubmitCanonicalForwardsCompiledSpec(t *testing.T) {
	client := &submissionClient{job: &veloxcontract.Job{ID: "job_1", WorkspaceID: 42}}
	service := NewJobSubmissionService(client, NewDefaultRegistry())
	req := veloxcontract.JobSubmissionRequest{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "canonical-1",
		JobType:         "scene.composite.v1",
		TemplateID:      "template-1",
		TemplateVersion: 1,
		VideoName:       "Canonical video",
		Spec:            json.RawMessage(`{"scenes":[{"id":"one","text":"hello"}]}`),
		Output:          &veloxcontract.JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		DeliveryPlan:    veloxcontract.DeliveryPlan{Destinations: []veloxcontract.DeliveryDestination{{ExternalDestinationID: "dest-1"}}},
	}
	result, err := service.SubmitCanonical(context.Background(), 42, 7, req)
	if err != nil {
		t.Fatalf("SubmitCanonical: %v", err)
	}
	if result == nil || result.Job.ID != "job_1" || result.JobType != req.JobType {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(client.received.Spec) == 0 || string(client.received.Spec) != string(req.Spec) {
		t.Fatalf("canonical spec was not forwarded as expected: got %s want %s", client.received.Spec, req.Spec)
	}
}
