package veloxjobs

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

func TestSpecSceneAdapterToJobSubmissionService(t *testing.T) {
	client := &submissionClient{job: &veloxcontract.Job{ID: "job-from-spec-scene", WorkspaceID: 42}}
	input := veloxcontract.SpecSceneSubmission{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "spec-scene-integration-1",
		JobType:         "scene.composite.v1",
		TemplateID:      "template",
		TemplateVersion: 1,
		VideoName:       "SpecScene integration",
		Output:          &veloxcontract.JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		DeliveryPlan:    veloxcontract.DeliveryPlan{Destinations: []veloxcontract.DeliveryDestination{{ExternalDestinationID: "dest-1"}}},
		Scenes: []veloxcontract.SpecScene{{
			ID:       "scene-1",
			Bindings: veloxcontract.SpecSceneBindings{Clip: &veloxcontract.SpecSceneAsset{AssetID: "clip-1"}},
		}},
	}
	canonical, err := veloxcontract.AdaptSpecSceneSubmission(input)
	if err != nil {
		t.Fatalf("adapt: %v", err)
	}
	definition, err := NewDefaultRegistry().Resolve(canonical.JobType)
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.Validator.Validate(canonical.Spec); err != nil {
		t.Fatalf("registry validator rejected adapted SpecScene: %v; spec=%s", err, canonical.Spec)
	}
	service := NewJobSubmissionService(client, NewDefaultRegistry())
	result, err := service.SubmitCanonical(context.Background(), 42, 7, canonical)
	if err != nil {
		t.Fatalf("submit canonical: %v", err)
	}
	if result.Job.ID != "job-from-spec-scene" || result.JobType != canonical.JobType {
		t.Fatalf("unexpected result: %+v", result)
	}
	var forwarded map[string]json.RawMessage
	if err := json.Unmarshal(client.received.Spec, &forwarded); err != nil {
		t.Fatalf("forwarded spec invalid: %v", err)
	}
	if _, ok := forwarded["scenes"]; !ok {
		t.Fatalf("forwarded canonical spec missing scenes: %s", client.received.Spec)
	}
	if client.received.IdempotencyKey != input.IdempotencyKey {
		t.Fatalf("idempotency key = %q, want %q", client.received.IdempotencyKey, input.IdempotencyKey)
	}
}
