package veloxcontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// JobOutput describes the requested render output for a canonical job.
type JobOutput struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
	Format string `json:"format"`
}

// JobSubmissionRequest is the strict canonical velox.job.v1 envelope used
// by POST /api/v1/jobs. It deliberately has no legacy project_id or
// render_spec fields; the existing /api/v1/velox/jobs route keeps using
// CreateJobRequest during migration.
type JobSubmissionRequest struct {
	ContractVersion string          `json:"contract_version"`
	IdempotencyKey  string          `json:"idempotency_key"`
	JobType         string          `json:"job_type"`
	TemplateID      string          `json:"template_id"`
	TemplateVersion int             `json:"template_version"`
	VideoName       string          `json:"video_name"`
	Spec            json.RawMessage `json:"spec"`
	Output          *JobOutput      `json:"output"`
	DeliveryPlan    DeliveryPlan    `json:"delivery_plan"`
}

// UnmarshalJSON keeps strictness at the canonical boundary. The outer
// decoder rejects unknown envelope fields; the nested output and delivery
// plan objects are decoded with the same rule. Spec remains a raw
// job-type-specific JSON object at this envelope layer; the central registry
// applies the typed validator after envelope decoding.
func (r *JobSubmissionRequest) UnmarshalJSON(data []byte) error {
	type plain JobSubmissionRequest
	var envelope plain
	if err := decodeStrict(data, &envelope); err != nil {
		return err
	}

	var raw struct {
		Output       json.RawMessage `json:"output"`
		DeliveryPlan json.RawMessage `json:"delivery_plan"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Output) > 0 && !bytes.Equal(bytes.TrimSpace(raw.Output), []byte("null")) {
		var output JobOutput
		if err := decodeStrict(raw.Output, &output); err != nil {
			return fmt.Errorf("output: %w", err)
		}
		envelope.Output = &output
	}
	if len(raw.DeliveryPlan) > 0 && !bytes.Equal(bytes.TrimSpace(raw.DeliveryPlan), []byte("null")) {
		var plan canonicalDeliveryPlan
		if err := decodeStrict(raw.DeliveryPlan, &plan); err != nil {
			return fmt.Errorf("delivery_plan: %w", err)
		}
		envelope.DeliveryPlan = DeliveryPlan{Destinations: make([]DeliveryDestination, 0, len(plan.Destinations))}
		for _, destination := range plan.Destinations {
			envelope.DeliveryPlan.Destinations = append(envelope.DeliveryPlan.Destinations, DeliveryDestination{
				ExternalDestinationID: destination.ExternalDestinationID,
				PublicationID:         destination.PublicationID,
				Metadata:              destination.Metadata,
			})
		}
	}
	*r = JobSubmissionRequest(envelope)
	return nil
}

type canonicalDeliveryPlan struct {
	Destinations []canonicalDeliveryDestination `json:"destinations"`
}

func (p *canonicalDeliveryPlan) UnmarshalJSON(data []byte) error {
	type plain canonicalDeliveryPlan
	var decoded plain
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	*p = canonicalDeliveryPlan(decoded)
	return nil
}

type canonicalDeliveryDestination struct {
	ExternalDestinationID string          `json:"external_destination_id"`
	PublicationID         string          `json:"publication_id,omitempty"`
	Metadata              json.RawMessage `json:"metadata"`
}

func (d *canonicalDeliveryDestination) UnmarshalJSON(data []byte) error {
	type plain canonicalDeliveryDestination
	var decoded plain
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	*d = canonicalDeliveryDestination(decoded)
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return fmt.Errorf("unexpected trailing JSON: %w", err)
	}
	return nil
}

// AsCreateJobRequest adapts the canonical envelope to the existing
// Velox client interface without exposing legacy fields at the HTTP
// boundary. The client serializes the canonical fields when present.
func (r JobSubmissionRequest) AsCreateJobRequest() CreateJobRequest {
	return CreateJobRequest{
		ContractVersion: r.ContractVersion,
		IdempotencyKey:  r.IdempotencyKey,
		JobType:         r.JobType,
		TemplateID:      r.TemplateID,
		TemplateVersion: r.TemplateVersion,
		VideoName:       r.VideoName,
		Spec:            r.Spec,
		Output:          r.Output,
		DeliveryPlan:    r.DeliveryPlan,
	}
}

// ValidateCanonical validates the strict top-level velox.job.v1 contract.
// The nested Spec is validated by the central job-type registry after this
// envelope-level validation completes.
func (r JobSubmissionRequest) ValidateCanonical() error {
	if r.ContractVersion != "velox.job.v1" {
		return fmt.Errorf("contract_version must be %q", "velox.job.v1")
	}
	if strings.TrimSpace(r.IdempotencyKey) == "" {
		return fmt.Errorf("idempotency_key is required")
	}
	if len(r.IdempotencyKey) > 255 {
		return fmt.Errorf("idempotency_key exceeds 255 characters")
	}
	if strings.TrimSpace(r.JobType) == "" {
		return fmt.Errorf("job_type is required")
	}
	if strings.TrimSpace(r.TemplateID) == "" {
		return fmt.Errorf("template_id is required")
	}
	if r.TemplateVersion <= 0 {
		return fmt.Errorf("template_version must be a positive integer")
	}
	if strings.TrimSpace(r.VideoName) == "" {
		return fmt.Errorf("video_name is required")
	}
	if len(r.Spec) == 0 || !json.Valid(r.Spec) {
		return fmt.Errorf("spec must be valid JSON")
	}
	trimmedSpec := bytes.TrimSpace(r.Spec)
	if len(trimmedSpec) == 0 || trimmedSpec[0] != '{' || bytes.Equal(trimmedSpec, []byte("null")) {
		return fmt.Errorf("spec must be a JSON object")
	}
	if r.Output == nil {
		return fmt.Errorf("output is required")
	}
	if r.Output.Width <= 0 || r.Output.Height <= 0 {
		return fmt.Errorf("output.width and output.height must be positive integers")
	}
	if r.Output.FPS <= 0 {
		return fmt.Errorf("output.fps must be a positive integer")
	}
	if strings.TrimSpace(r.Output.Format) == "" {
		return fmt.Errorf("output.format is required")
	}
	if len(r.DeliveryPlan.Destinations) == 0 {
		return fmt.Errorf("delivery_plan.destinations must be non-empty")
	}
	for i, destination := range r.DeliveryPlan.Destinations {
		if strings.TrimSpace(destination.ExternalDestinationID) == "" {
			return fmt.Errorf("delivery_plan.destinations[%d].external_destination_id is required", i)
		}
	}
	return nil
}
