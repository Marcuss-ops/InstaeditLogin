package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// fakeAgentRunStore is an in-memory AgentRunStore for handler tests.
type fakeAgentRunStore struct {
	runs  map[string]*repository.AgentRun
	steps map[string]*repository.AgentRunStep
	seq   int
}

func newFakeAgentRunStore() *fakeAgentRunStore {
	return &fakeAgentRunStore{runs: map[string]*repository.AgentRun{}, steps: map[string]*repository.AgentRunStep{}}
}

func (f *fakeAgentRunStore) CreateRun(ctx context.Context, run *repository.AgentRun) error {
	// Idempotency: same workspace + key reuses the row.
	for _, existing := range f.runs {
		if existing.WorkspaceID == run.WorkspaceID && existing.IdempotencyKey == run.IdempotencyKey {
			run.ID = existing.ID
			run.CreatedAt = existing.CreatedAt
			run.UpdatedAt = time.Now()
			return nil
		}
	}
	f.seq++
	run.ID = "run_" + string(rune('a'+f.seq-1))
	run.CreatedAt = time.Now()
	run.UpdatedAt = time.Now()
	f.runs[run.ID] = run
	return nil
}

func (f *fakeAgentRunStore) AppendStep(ctx context.Context, step *repository.AgentRunStep) error {
	f.seq++
	step.ID = "step_" + string(rune('a'+f.seq-1))
	step.StartedAt = time.Now()
	f.steps[step.ID] = step
	return nil
}

func (f *fakeAgentRunStore) CompleteStep(ctx context.Context, step *repository.AgentRunStep) error {
	if existing, ok := f.steps[step.ID]; ok {
		existing.Status = step.Status
		existing.OutputJSON = step.OutputJSON
		existing.ErrorCode = step.ErrorCode
		existing.ErrorMessage = step.ErrorMessage
		now := time.Now()
		existing.CompletedAt = &now
	}
	return nil
}

func (f *fakeAgentRunStore) UpdateRun(ctx context.Context, runID, status, currentStep string, completedAt *time.Time) error {
	if existing, ok := f.runs[runID]; ok {
		existing.Status = status
		existing.CurrentStep = currentStep
		existing.CompletedAt = completedAt
		existing.UpdatedAt = time.Now()
	}
	return nil
}

// runAgentRunsRequest mounts the module with a passthrough protect and a
// workspace-anchored API-key identity, then issues the request.
func runAgentRunsRequest(t *testing.T, store AgentRunStore, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	module := NewAgentRunsModule(AgentRunsModuleDeps{
		Store: store,
		Protected: func(h http.HandlerFunc) http.HandlerFunc {
			return h
		},
	})
	mux := chi.NewRouter()
	module.Register(mux)

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	// Workspace 7, created_by 42, key 9 — the API-key identity shape.
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, 7, nil)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestAgentRuns_CreateRun(t *testing.T) {
	store := newFakeAgentRunStore()
	w := runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs", []byte(`{
		"goal":"Crea una copertina per il video abc123 e pubblicalo",
		"idempotency_key":"key_1",
		"youtube_video_id":"abc123"
	}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp createRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.RunID == "" {
		t.Fatal("run_id empty")
	}
	if resp.WorkspaceID != 7 {
		t.Fatalf("workspace_id = %d, want 7 (from identity, not body)", resp.WorkspaceID)
	}
	// The run must be anchored to the identity's workspace + key.
	run := store.runs[resp.RunID]
	if run == nil {
		t.Fatal("run not persisted")
	}
	if run.WorkspaceID != 7 || run.ActorKeyID == nil || *run.ActorKeyID != 9 {
		t.Fatalf("run anchored to ws=%d key=%v, want ws=7 key=9", run.WorkspaceID, run.ActorKeyID)
	}
}

func TestAgentRuns_CreateRun_Idempotent(t *testing.T) {
	store := newFakeAgentRunStore()
	body := []byte(`{"goal":"g","idempotency_key":"same_key"}`)
	w1 := runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs", body)
	w2 := runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs", body)
	if w1.Code != http.StatusCreated || w2.Code != http.StatusCreated {
		t.Fatalf("codes = %d,%d", w1.Code, w2.Code)
	}
	var r1, r2 createRunResponse
	_ = json.Unmarshal(w1.Body.Bytes(), &r1)
	_ = json.Unmarshal(w2.Body.Bytes(), &r2)
	if r1.RunID != r2.RunID {
		t.Fatalf("idempotent replays must reuse the same run: %s vs %s", r1.RunID, r2.RunID)
	}
	if len(store.runs) != 1 {
		t.Fatalf("expected 1 run row, got %d", len(store.runs))
	}
}

func TestAgentRuns_CreateRun_RequiresGoalAndKey(t *testing.T) {
	store := newFakeAgentRunStore()
	for _, body := range []string{`{}`, `{"goal":"g"}`, `{"idempotency_key":"k"}`} {
		w := runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs", []byte(body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, w.Code)
		}
	}
}

func TestAgentRuns_AppendAndCompleteStep(t *testing.T) {
	store := newFakeAgentRunStore()
	w := runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs", []byte(`{"goal":"g","idempotency_key":"k2"}`))
	var run createRunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &run)

	// Append a step.
	w = runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs/"+run.RunID+"/steps", []byte(`{
		"tool_name":"attach_thumbnail",
		"input_json":{"session_id":"sess_77","media_id":"media_A"}
	}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("append status = %d, body=%s", w.Code, w.Body.String())
	}
	var step appendStepResponse
	if err := json.Unmarshal(w.Body.Bytes(), &step); err != nil {
		t.Fatalf("unmarshal step: %v", err)
	}
	if step.StepID == "" {
		t.Fatal("step_id empty")
	}

	// Complete the step with a reference-bearing output.
	w = runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs/"+run.RunID+"/steps/"+step.StepID+"/complete", []byte(`{
		"status":"completed",
		"output_json":{"attached":true,"media_id":"media_A"}
	}`))
	if w.Code != http.StatusOK {
		t.Fatalf("complete status = %d, body=%s", w.Code, w.Body.String())
	}
	persisted := store.steps[step.StepID]
	if persisted == nil || persisted.Status != "completed" {
		t.Fatalf("step not completed: %+v", persisted)
	}
}

func TestAgentRuns_UpdateRunStatus(t *testing.T) {
	store := newFakeAgentRunStore()
	w := runAgentRunsRequest(t, store, http.MethodPost, "/api/v1/agent/runs", []byte(`{"goal":"g","idempotency_key":"k3"}`))
	var run createRunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &run)

	w = runAgentRunsRequest(t, store, http.MethodPatch, "/api/v1/agent/runs/"+run.RunID, []byte(`{
		"status":"completed","current_step":"publish_video"
	}`))
	if w.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body=%s", w.Code, w.Body.String())
	}
	if store.runs[run.RunID].Status != "completed" {
		t.Fatalf("run status = %q, want completed", store.runs[run.RunID].Status)
	}
	// Invalid status fails closed.
	w = runAgentRunsRequest(t, store, http.MethodPatch, "/api/v1/agent/runs/"+run.RunID, []byte(`{"status":"bogus"}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid status: code = %d, want 400", w.Code)
	}
}
