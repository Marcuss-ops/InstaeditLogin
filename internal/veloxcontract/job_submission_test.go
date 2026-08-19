package veloxcontract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func validJobSubmission() JobSubmissionRequest {
	return JobSubmissionRequest{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "job-idem-1",
		JobType:         "scene.composite.v1",
		TemplateID:      "documentary.clip-stock.v1",
		TemplateVersion: 1,
		VideoName:       "Five legendary boxers",
		Spec:            json.RawMessage(`{"scenes":[]}`),
		Output:          &JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		DeliveryPlan: DeliveryPlan{Destinations: []DeliveryDestination{{
			ExternalDestinationID: "extdst_01J",
		}}},
	}
}

func TestJobSubmissionValidateCanonical(t *testing.T) {
	if err := validJobSubmission().ValidateCanonical(); err != nil {
		t.Fatalf("valid canonical request rejected: %v", err)
	}
}

func TestJobSubmissionValidateCanonicalRejectsRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*JobSubmissionRequest)
	}{
		{"contract version", func(r *JobSubmissionRequest) { r.ContractVersion = "" }},
		{"idempotency key", func(r *JobSubmissionRequest) { r.IdempotencyKey = "" }},
		{"job type", func(r *JobSubmissionRequest) { r.JobType = "" }},
		{"template", func(r *JobSubmissionRequest) { r.TemplateID = "" }},
		{"template version", func(r *JobSubmissionRequest) { r.TemplateVersion = 0 }},
		{"video name", func(r *JobSubmissionRequest) { r.VideoName = "" }},
		{"spec", func(r *JobSubmissionRequest) { r.Spec = json.RawMessage(`[]`) }},
		{"output", func(r *JobSubmissionRequest) { r.Output = nil }},
		{"destinations", func(r *JobSubmissionRequest) { r.DeliveryPlan.Destinations = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validJobSubmission()
			tt.mutate(&req)
			if err := req.ValidateCanonical(); err == nil {
				t.Fatalf("expected %s validation error", tt.name)
			}
		})
	}
}

func TestJobSubmissionAsCreateJobRequest(t *testing.T) {
	req := validJobSubmission()
	req.PublishAt = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	req.Target = &PublicationTarget{Type: "group", GroupID: 12, GroupName: "Social IT"}
	req.Publications = json.RawMessage(`[{"publication_id":"boxe-it","output_ref":{"variant_id":"it"},"destinations":[{"destination_id":"ext-it"}]}]`)
	adapted := req.AsCreateJobRequest()
	if adapted.JobType != req.JobType || adapted.TemplateID != req.TemplateID || adapted.Output != req.Output {
		t.Fatalf("canonical fields not preserved: adapted=%+v", adapted)
	}
	if adapted.PublishAt != req.PublishAt || adapted.Target != req.Target {
		t.Fatalf("publication fields not preserved: adapted=%+v", adapted)
	}
	if string(adapted.Publications) != string(req.Publications) {
		t.Fatalf("publication bundle not preserved: adapted=%s", adapted.Publications)
	}
	if adapted.ProjectID != "" || len(adapted.RenderSpec) != 0 {
		t.Fatalf("legacy fields must remain empty in canonical adaptation: %+v", adapted)
	}
}

func TestJobSubmissionValidateCanonicalPublicationFields(t *testing.T) {
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	tests := []struct {
		name   string
		mutate func(*JobSubmissionRequest)
	}{
		{"future schedule and group", func(r *JobSubmissionRequest) {
			r.PublishAt = future
			r.Target = &PublicationTarget{Type: "group", GroupID: 12, GroupName: "Social IT"}
		}},
		{"channel target", func(r *JobSubmissionRequest) {
			r.Target = &PublicationTarget{Type: "channel", ChannelID: "UC123", ChannelName: "Canale IT"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validJobSubmission()
			tt.mutate(&req)
			if err := req.ValidateCanonical(); err != nil {
				t.Fatalf("valid publication fields rejected: %v", err)
			}
		})
	}

	invalid := validJobSubmission()
	invalid.PublishAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := invalid.ValidateCanonical(); err == nil {
		t.Fatal("past publish_at must be rejected")
	}
	invalid = validJobSubmission()
	invalid.Target = &PublicationTarget{Type: "group", GroupID: 12, ChannelName: "wrong field"}
	if err := invalid.ValidateCanonical(); err == nil {
		t.Fatal("group target with channel fields must be rejected")
	}
	invalid = validJobSubmission()
	invalid.Publications = json.RawMessage(`{}`)
	if err := invalid.ValidateCanonical(); err == nil {
		t.Fatal("publication bundle must be a non-empty array")
	}
}

func TestJobSubmissionValidateCanonicalRejectsLongIdempotencyKey(t *testing.T) {
	req := validJobSubmission()
	req.IdempotencyKey = strings.Repeat("x", 256)
	if err := req.ValidateCanonical(); err == nil {
		t.Fatal("expected long idempotency key to be rejected")
	}
}

func TestJobSubmissionRejectsNestedUnknownFields(t *testing.T) {
	for _, name := range []string{"output", "delivery_plan.destinations"} {
		t.Run(name, func(t *testing.T) {
			body := `{"contract_version":"velox.job.v1","idempotency_key":"job-idem-1","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4","extra":true},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J","extra":true}]}}`
			if name == "output" {
				body = strings.Replace(body, `,"extra":true},"delivery_plan"`, `,"extra":true},"delivery_plan"`, 1)
			}
			var req JobSubmissionRequest
			if err := json.Unmarshal([]byte(body), &req); err == nil {
				t.Fatal("expected nested unknown field to be rejected")
			}
		})
	}
}

func TestJobSubmissionRejectsTrailingJSON(t *testing.T) {
	var req JobSubmissionRequest
	body := `{"contract_version":"velox.job.v1"} {"extra":true}`
	if err := json.Unmarshal([]byte(body), &req); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}
