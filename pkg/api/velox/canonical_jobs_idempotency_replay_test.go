package velox

import (
	"context"
	"net/http"
	"testing"
)

func TestCreateCanonicalJob_IdempotentReplayReturnsSameJob(t *testing.T) {
	const body = `{"contract_version":"velox.job.v1","idempotency_key":"replay-1","job_type":"scene.composite.v1","template_id":"template","template_version":1,"video_name":"name","spec":{"scenes":[{"id":"scene-1"}]},"output":{"width":1920,"height":1080,"fps":30,"format":"mp4"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J"}]}}`
	seen := make(map[string]*Job)
	calls := 0
	mc := &mockClient{createJobFn: func(_ context.Context, _, _ int64, req CreateJobRequest) (*Job, error) {
		calls++
		if existing := seen[req.IdempotencyKey]; existing != nil {
			return existing, nil
		}
		job := &Job{ID: "job-replay-1", WorkspaceID: testWSID, RenderStatus: "QUEUED"}
		seen[req.IdempotencyKey] = job
		return job, nil
	}}
	mux := newMux(t, mc, stubAuth)
	for attempt := 0; attempt < 2; attempt++ {
		w := do(t, mux, http.MethodPost, "/api/v1/jobs", body)
		if w.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: expected 202, got %d: %s", attempt+1, w.Code, w.Body.String())
		}
		if body := decodeBody(t, w); body["id"] != "job-replay-1" {
			t.Fatalf("attempt %d: response = %v", attempt+1, body)
		}
	}
	if calls != 2 {
		t.Fatalf("client calls = %d, want 2 idempotent service invocations", calls)
	}
	if len(seen) != 1 {
		t.Fatalf("stored idempotency keys = %d, want 1", len(seen))
	}
}
