package veloxjobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
// Legacy submissions are mapped to the compatibility job type
// legacy.render.v1 before forwarding; their original render_spec remains
// unchanged on the legacy wire DTO.
type SubmissionResult struct {
	Job      *veloxcontract.Job
	Estimate CostEstimate
	JobType  string
	Legacy   bool
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

// SubmitLegacy validates the migration-era CreateJobRequest and adapts it
// into the common submission representation before forwarding it. The
// canonical metadata is intentionally compatibility-only (`legacy.render.v1`):
// old producers do not provide a technical job_type/template/output yet.
// The derived client DTO retains project_id/render_spec so the existing
// downstream endpoint remains wire-compatible during migration.
func (s *JobSubmissionService) SubmitLegacy(ctx context.Context, workspaceID, userID int64, req veloxcontract.CreateJobRequest) (*SubmissionResult, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("%w: submission client is not configured", ErrInvalidSubmission)
	}
	if err := validateLegacyRequest(req); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSubmission, err)
	}
	canonical, clientRequest := adaptLegacyRequest(req)
	definition, err := s.registry.Resolve(canonical.JobType)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSubmission, err)
	}
	if err := definition.Validator.Validate(canonical.Spec); err != nil {
		return nil, fmt.Errorf("%w: spec: %v", ErrInvalidSubmission, err)
	}
	compiled, err := definition.Compiler.Compile(canonical.Spec)
	if err != nil {
		return nil, fmt.Errorf("%w: compile spec: %v", ErrInvalidSubmission, err)
	}
	canonical.Spec = compiled.Spec
	estimate, err := s.registry.Resolve(canonical.JobType)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSubmission, err)
	}
	cost, err := estimate.CostEstimator.Estimate(canonical.Spec, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: estimate spec: %v", ErrInvalidSubmission, err)
	}
	job, err := s.submitClientRequest(ctx, workspaceID, userID, clientRequest)
	if err != nil {
		return nil, err
	}
	return &SubmissionResult{Job: job, Estimate: cost, JobType: canonical.JobType, Legacy: true}, nil
}

// adaptLegacyRequest is the migration seam. It creates the canonical
// envelope used by the common service while retaining legacy fields in the
// client DTO until the downstream contract is fully migrated.
func adaptLegacyRequest(req veloxcontract.CreateJobRequest) (veloxcontract.JobSubmissionRequest, veloxcontract.CreateJobRequest) {
	canonical := veloxcontract.JobSubmissionRequest{
		ContractVersion: req.ContractVersion,
		IdempotencyKey:  req.IdempotencyKey,
		JobType:         "legacy.render.v1",
		TemplateID:      "legacy." + req.ProjectID,
		TemplateVersion: 1,
		VideoName:       req.ProjectID,
		Spec:            json.RawMessage(`{"legacy_render_spec":` + string(req.RenderSpec) + `}`),
		DeliveryPlan:    req.DeliveryPlan,
	}
	// Keep the outbound legacy DTO unchanged until the downstream endpoint
	// explicitly accepts the canonical envelope. The canonical value is used
	// by the shared validation/compiler boundary above, not leaked into the
	// legacy wire shape.
	return canonical, req
}

func (s *JobSubmissionService) submitClientRequest(ctx context.Context, workspaceID, userID int64, clientRequest veloxcontract.CreateJobRequest) (*veloxcontract.Job, error) {
	job, err := s.client.CreateJob(ctx, workspaceID, userID, clientRequest)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, ErrNilJob
	}
	return job, nil
}

// SubmissionService is kept as a compatibility alias for callers that used
// the shorter name during the migration.
type SubmissionService = JobSubmissionService

// NewSubmissionService is the compatibility constructor for the shorter name.
func NewSubmissionService(client veloxcontract.Client, registry *Registry) *JobSubmissionService {
	return NewJobSubmissionService(client, registry)
}

func validateLegacyRequest(req veloxcontract.CreateJobRequest) error {
	if strings.TrimSpace(req.ContractVersion) == "" {
		return errors.New("contract_version is required")
	}
	if req.ContractVersion != "velox.job.v1" {
		return errors.New("unsupported contract_version")
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return errors.New("idempotency_key is required")
	}
	if req.ProjectID == "" {
		return errors.New("project_id is required")
	}
	if len(req.RenderSpec) == 0 || !json.Valid(req.RenderSpec) {
		return errors.New("render_spec is required")
	}
	if len(req.DeliveryPlan.Destinations) == 0 {
		return errors.New("delivery_plan.destinations must be non-empty")
	}
	for i, destination := range req.DeliveryPlan.Destinations {
		if destination.ExternalDestinationID == "" {
			return fmt.Errorf("delivery_plan.destinations[%d].external_destination_id is required", i)
		}
	}
	return nil
}
