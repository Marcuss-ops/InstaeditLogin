package velox

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxjobs"
)

func TestCreateCanonicalJob_UnknownJobType422(t *testing.T) {
	mc := &mockClient{createJobFn: func(context.Context, int64, int64, CreateJobRequest) (*Job, error) {
		t.Fatal("client should not be called for an unknown job type")
		return nil, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"unknown-type","job_type":"unknown.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateCanonicalJob_RegistryValidatesSpec422(t *testing.T) {
	registry := veloxjobs.NewDefaultRegistry()
	mc := &mockClient{createJobFn: func(context.Context, int64, int64, CreateJobRequest) (*Job, error) {
		t.Fatal("client should not be called for an invalid job-type spec")
		return nil, nil
	}}
	mux := chi.NewRouter()
	Register(mux, Deps{
		Client:      mc,
		JobRegistry: registry,
		AuthMiddleware: func(next http.Handler) http.Handler {
			return stubAuth(next)
		},
	})
	body := `{"contract_version":"velox.job.v1","idempotency_key":"bad-spec","job_type":"slideshow.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateCanonicalJob_CompilerNormalizesForwardedSpec(t *testing.T) {
	var received CreateJobRequest
	mc := &mockClient{createJobFn: func(_ context.Context, _, _ int64, req CreateJobRequest) (*Job, error) {
		received = req
		return &Job{ID: "job_compiled", WorkspaceID: testWSID}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"compile-spec","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[{"id":"one"}],"z":1},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	var normalized map[string]json.RawMessage
	if err := json.Unmarshal(received.Spec, &normalized); err != nil {
		t.Fatalf("compiled spec is invalid JSON: %v", err)
	}
	if string(normalized["z"]) != "1" {
		t.Fatalf("compiled spec lost fields: %s", received.Spec)
	}
}
