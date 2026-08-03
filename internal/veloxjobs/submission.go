package veloxjobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

var (
	// ErrInvalidSubmission classifies request validation failures before the
	// downstream Velox client is called. HTTP adapters map it to 422.
	ErrInvalidSubmission = errors.New("invalid velox job submission")
	// ErrNilJob protects callers from a malformed successful client response.
	ErrNilJob = errors.New("velox client returned a nil job")
)

// SubmissionResult contains the accepted job and advisory technical metadata.
type SubmissionResult struct {
	Job      *veloxcontract.Job
	Estimate CostEstimate
	JobType  string
}

// JobSubmissionService is the common application boundary for job creation.
// HTTP handlers only decode/authenticate and map errors; validation, registry
// resolution, compilation and client forwarding stay here.
type JobSubmissionService struct {
	client   veloxcontract.Client
	registry *Registry
}

// NewJobSubmissionService constructs the shared service. A nil registry uses the
// built-in technical registry, while a nil client is retained so miswiring is
// returned as an error rather than causing a handler panic.
func NewJobSubmissionService(client veloxcontract.Client, registry *Registry) *JobSubmissionService {
	if registry == nil {
		registry = NewDefaultRegistry()
	}
	return &JobSubmissionService{client: client, registry: registry}
}

// SubmitCanonical validates, resolves, compiles, estimates and forwards a
// velox.job.v1 request through the existing Velox client DTO.
func (s *JobSubmissionService) SubmitCanonical(ctx context.Context, workspaceID, userID int64, req veloxcontract.JobSubmissionRequest) (*SubmissionResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("%w: submission client is not configured", ErrInvalidSubmission)
	}
	if err := req.ValidateCanonical(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSubmission, err)
	}
	definition, err := s.registry.Resolve(req.JobType)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSubmission, err)
	}
	if err := definition.Validator.Validate(req.Spec); err != nil {
		return nil, fmt.Errorf("%w: spec: %v", ErrInvalidSubmission, err)
	}
	compiled, err := definition.Compiler.Compile(req.Spec)
	if err != nil {
		return nil, fmt.Errorf("%w: compile spec: %v", ErrInvalidSubmission, err)
	}
	estimate, err := definition.CostEstimator.Estimate(compiled.Spec, req.Output)
	if err != nil {
		return nil, fmt.Errorf("%w: estimate spec: %v", ErrInvalidSubmission, err)
	}
	adapted := req.AsCreateJobRequest()
	adapted.Spec = compiled.Spec
	job, err := s.client.CreateJob(ctx, workspaceID, userID, adapted)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNilJob
	}
	return &SubmissionResult{Job: job, Estimate: estimate, JobType: req.JobType}, nil
}

// SubmissionService is kept as a compatibility alias for callers that used
// the shorter name during the migration.
type SubmissionService = JobSubmissionService

// NewSubmissionService is the compatibility constructor for the shorter name.
func NewSubmissionService(client veloxcontract.Client, registry *Registry) *JobSubmissionService {
	return NewJobSubmissionService(client, registry)
}
