package velox

import (
	"context"
	"net/http"
	"testing"
)

func TestCreateCanonicalJob_HappyPath(t *testing.T) {
	var received CreateJobRequest
	mc := &mockClient{createJobFn: func(_ context.Context, wsID, uid int64, req CreateJobRequest) (*Job, error) {
		if wsID != testWSID || uid != testUID {
			t.Fatalf("identity not forwarded: workspace=%d user=%d", wsID, uid)
		}
		received = req
		return &Job{ID: "job_canonical", WorkspaceID: wsID, RenderStatus: "QUEUED"}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"canonical-1","job_type":"scene.composite.v1","template_id":"documentary.clip-stock.v1","template_version":1,"video_name":"Five legendary boxers","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if received.JobType != "scene.composite.v1" || received.TemplateID != "documentary.clip-stock.v1" {
		t.Fatalf("canonical fields not forwarded: %+v", received)
	}
	if received.ProjectID != "" || len(received.RenderSpec) != 0 {
		t.Fatalf("legacy fields must not be synthesized: %+v", received)
	}
}

func TestCreateCanonicalJob_IdempotencyConflict409(t *testing.T) {
	calls := 0
	mc := &mockClient{createJobFn: func(_ context.Context, _, _ int64, req CreateJobRequest) (*Job, error) {
		calls++
		if req.IdempotencyKey != "canonical-idempotency-1" {
			t.Fatalf("idempotency key = %q", req.IdempotencyKey)
		}
		return nil, ErrIdempotencyConflict
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"canonical-idempotency-1","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	for i := 0; i < 2; i++ {
		w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
		if w.Code != http.StatusConflict {
			t.Fatalf("attempt %d: expected 409, got %d: %s", i+1, w.Code, w.Body.String())
		}
		decoded := decodeBody(t, w)
		if decoded["error_code"] != "IDEMPOTENCY_CONFLICT" {
			t.Fatalf("attempt %d: unexpected conflict body: %s", i+1, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("client calls = %d, want 2 with the same forwarded idempotency key", calls)
	}
}

func TestCreateCanonicalJob_UnknownField400(t *testing.T) {
	mc := &mockClient{}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"canonical-1","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]},"unexpected":true}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown field, got %d", w.Code)
	}
}

func TestCreateCanonicalJob_InvalidContract422(t *testing.T) {
	mc := &mockClient{}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"canonical-1","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":0,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for semantic validation, got %d", w.Code)
	}
}

func TestCreateCanonicalJob_WorkspaceMismatch404(t *testing.T) {
	mc := &mockClient{createJobFn: func(context.Context, int64, int64, CreateJobRequest) (*Job, error) {
		return &Job{ID: "job_other", WorkspaceID: 999}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"contract_version":"velox.job.v1","idempotency_key":"canonical-1","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for workspace mismatch, got %d", w.Code)
	}
}
