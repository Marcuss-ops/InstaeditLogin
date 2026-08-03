package veloxclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

func TestCreateJobForwardsCanonicalFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != veloxAPIPrefix+"/jobs" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		for _, field := range []string{"contract_version", "idempotency_key", "job_type", "template_id", "template_version", "video_name", "spec", "output", "delivery_plan"} {
			if _, ok := body[field]; !ok {
				t.Errorf("canonical field %q missing from upstream body", field)
			}
		}
		for _, field := range []string{"workspace_id", "user_id", "project_id", "render_spec"} {
			if _, ok := body[field]; ok {
				t.Errorf("field %q must not be present in canonical upstream body", field)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(jobResponse{ID: "job_canonical", WorkspaceID: 42, RenderStatus: "QUEUED"})
	}))
	defer server.Close()

	client := New(server.URL, testSecret)
	job, err := client.CreateJob(context.Background(), 42, 99, veloxapi.CreateJobRequest{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "canonical-1",
		JobType:         "scene.composite.v1",
		TemplateID:      "documentary.clip-stock.v1",
		TemplateVersion: 1,
		VideoName:       "Five legendary boxers",
		Spec:            json.RawMessage(`{"scenes":[]}`),
		Output:          &veloxapi.JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		DeliveryPlan:    veloxapi.DeliveryPlan{Destinations: []veloxapi.DeliveryDestination{{ExternalDestinationID: "extdst_01J"}}},
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.ID != "job_canonical" {
		t.Fatalf("job id = %q; want job_canonical", job.ID)
	}
}
