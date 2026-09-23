package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/agenttools"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/jobmaster"
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
	// Idempotency: same request reuses the row; a changed request conflicts.
	for _, existing := range f.runs {
		if existing.WorkspaceID == run.WorkspaceID && existing.IdempotencyKey == run.IdempotencyKey {
			if existing.Goal != run.Goal || existing.YouTubeVideoID != run.YouTubeVideoID || existing.EditorSessionID != run.EditorSessionID {
				return repository.ErrAgentRunIdempotencyConflict
			}
			*run = *existing
			run.ID = existing.ID
			run.CreatedAt = existing.CreatedAt
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

func (f *fakeAgentRunStore) GetRun(ctx context.Context, workspaceID int64, runID string) (*repository.AgentRun, error) {
	run, ok := f.runs[runID]
	if !ok || run.WorkspaceID != workspaceID {
		return nil, repository.ErrAgentRunNotFound
	}
	return run, nil
}
func (f *fakeAgentRunStore) ListRecoverableRuns(context.Context, int) ([]*repository.AgentRun, error) {
	return nil, nil
}
func (f *fakeAgentRunStore) ListSteps(ctx context.Context, workspaceID int64, runID string) ([]repository.AgentRunStep, error) {
	if _, err := f.GetRun(ctx, workspaceID, runID); err != nil {
		return nil, err
	}
	result := []repository.AgentRunStep{}
	for _, step := range f.steps {
		if step.RunID == runID {
			result = append(result, *step)
		}
	}
	return result, nil
}
func (f *fakeAgentRunStore) AppendStepOwned(ctx context.Context, workspaceID int64, step *repository.AgentRunStep) error {
	if _, err := f.GetRun(ctx, workspaceID, step.RunID); err != nil {
		return err
	}
	return f.AppendStep(ctx, step)
}
func (f *fakeAgentRunStore) CompleteStepOwned(ctx context.Context, workspaceID int64, runID string, step *repository.AgentRunStep) error {
	if _, err := f.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	existing, ok := f.steps[step.ID]
	if !ok || existing.RunID != runID {
		return repository.ErrAgentRunNotFound
	}
	return f.CompleteStep(ctx, step)
}
func (f *fakeAgentRunStore) SetStepRemoteJob(ctx context.Context, workspaceID int64, runID, stepID, remoteJobID, idempotencyKey string) error {
	if _, err := f.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	step, ok := f.steps[stepID]
	if !ok || step.RunID != runID {
		return repository.ErrAgentRunNotFound
	}
	step.RemoteJobID, step.IdempotencyKey = remoteJobID, idempotencyKey
	return nil
}
func (f *fakeAgentRunStore) UpdateStepProgressOwned(ctx context.Context, workspaceID int64, runID, stepID, remoteStatus string, remoteProgress *int, progressJSON []byte) error {
	if _, err := f.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	step, ok := f.steps[stepID]
	if !ok || step.RunID != runID {
		return repository.ErrAgentRunNotFound
	}
	step.RemoteStatus, step.RemoteProgress, step.ProgressJSON = remoteStatus, remoteProgress, progressJSON
	return nil
}
func (f *fakeAgentRunStore) UpdateRunOwned(ctx context.Context, workspaceID int64, runID, status, currentStep string, completedAt *time.Time) error {
	if _, err := f.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	return f.UpdateRun(ctx, runID, status, currentStep, completedAt)
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

type fakeAgentJobMaster struct{}

func (fakeAgentJobMaster) ListTypes(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"types":["script.generate","clip.render"]}`), nil
}
func (fakeAgentJobMaster) SearchMedia(context.Context, string, int) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}
func (fakeAgentJobMaster) GetMediaAsset(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

type stagedVideoJobMaster struct {
	prePayload      json.RawMessage
	finalizePayload json.RawMessage
	finalizeJobID   string
}

type fakeAgentVideoPublisher struct{}

func (fakeAgentVideoPublisher) Validate(context.Context, auth.Identity, int64, json.RawMessage) error {
	return nil
}
func (fakeAgentVideoPublisher) Reserve(context.Context, auth.Identity, int64, string, repository.AgentRunStep) (json.RawMessage, error) {
	return json.RawMessage(`{"post_id":1}`), nil
}
func (fakeAgentVideoPublisher) Publish(context.Context, auth.Identity, int64, string, repository.AgentRunStep, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"post_id":1}`), nil
}

func (s *stagedVideoJobMaster) ListTypes(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"types":["script.generate","clip.render"]}`), nil
}
func (s *stagedVideoJobMaster) SearchMedia(context.Context, string, int) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}
func (s *stagedVideoJobMaster) GetMediaAsset(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (s *stagedVideoJobMaster) Submit(context.Context, jobmaster.SubmitRequest) (json.RawMessage, error) {
	return nil, errors.New("generic submit should not be used for complete video")
}
func (s *stagedVideoJobMaster) PrepareVideo(_ context.Context, body json.RawMessage) (json.RawMessage, error) {
	s.prePayload = body
	return json.RawMessage(`{"job_id":"remote-video"}`), nil
}
func (s *stagedVideoJobMaster) FinalizeVideo(_ context.Context, id string, body json.RawMessage) (json.RawMessage, error) {
	s.finalizeJobID, s.finalizePayload = id, body
	return json.RawMessage(`{"status":"queued"}`), nil
}
func (s *stagedVideoJobMaster) Get(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-video","status":"RUNNING"}`), nil
}

func TestAgentRuns_CreateVideoPreparesAndFinalizesRemoteRender(t *testing.T) {
	store := newFakeAgentRunStore()
	master := &stagedVideoJobMaster{}
	module := NewAgentRunsModule(AgentRunsModuleDeps{Store: store, Catalog: agenttools.NewCatalog(), JobMaster: master, VideoPublisher: fakeAgentVideoPublisher{}, Protected: func(h http.HandlerFunc) http.HandlerFunc { return h }})
	mux := chi.NewRouter()
	module.Register(mux)
	withIdentity := func(req *http.Request) *http.Request {
		return req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, 7, []string{agenttools.PermissionAutomation})))
	}
	create := withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs", bytes.NewBufferString(`{"goal":"create and schedule video","idempotency_key":"video-run"}`)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create run: %d %s", w.Code, w.Body.String())
	}
	var run createRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	body := `{"project":"creator-51","idempotency_key":"video-51","payload":{"pre":{"job_type":"scene.composite.v1","copy_only":true,"script_text":"script","scenes":[{"scene_id":"s1","text":"scene"}],"output":{"format":"mp4"},"delivery_plan":[{"destination_id":"drive-production"}]},"finalize":{"overlays":[],"runtime_assets":[]},"publish":{"title":"Generated","caption":"caption","language":"it","scheduled_at":"2099-01-01T12:00:00Z","privacy":"unlisted","targets":[{"platform_account_id":51}]}}}`
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs/"+run.RunID+"/tools/content.create_video", strings.NewReader(body)))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("invoke video: %d %s", w.Code, w.Body.String())
	}
	if master.finalizeJobID != "remote-video" {
		t.Fatalf("finalized job %q", master.finalizeJobID)
	}
	var pre, finalize map[string]json.RawMessage
	_ = json.Unmarshal(master.prePayload, &pre)
	_ = json.Unmarshal(master.finalizePayload, &finalize)
	var preKey, finalizeKey string
	_ = json.Unmarshal(pre["idempotency_key"], &preKey)
	_ = json.Unmarshal(finalize["idempotency_key"], &finalizeKey)
	if preKey != "video-51-prepare" || finalizeKey != "video-51-finalize" {
		t.Fatalf("phase keys = %q, %q", preKey, finalizeKey)
	}
	if len(store.steps) != 1 {
		t.Fatalf("expected one durable video step, got %d", len(store.steps))
	}
	for _, step := range store.steps {
		if step.RemoteJobID != "remote-video" || step.RemoteStatus != "FINALIZE_QUEUED" {
			t.Fatalf("video stage not persisted: %+v", step)
		}
	}
}
func (fakeAgentJobMaster) Submit(context.Context, jobmaster.SubmitRequest) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-1","status":"queued"}`), nil
}
func (fakeAgentJobMaster) PrepareVideo(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-video"}`), nil
}
func (fakeAgentJobMaster) FinalizeVideo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"queued"}`), nil
}
func (fakeAgentJobMaster) Get(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-1","status":"completed"}`), nil
}

type progressAgentJobMaster struct{}

func (progressAgentJobMaster) ListTypes(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"types":["script.generate"]}`), nil
}
func (progressAgentJobMaster) SearchMedia(context.Context, string, int) (json.RawMessage, error) {
	return json.RawMessage(`{"items":[]}`), nil
}
func (progressAgentJobMaster) GetMediaAsset(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (progressAgentJobMaster) Submit(context.Context, jobmaster.SubmitRequest) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-progress","status":"queued"}`), nil
}
func (progressAgentJobMaster) PrepareVideo(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-progress"}`), nil
}
func (progressAgentJobMaster) FinalizeVideo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"queued"}`), nil
}
func (progressAgentJobMaster) Get(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"remote-progress","status":"RUNNING","progress":42,"stage_progress":{"script":{"status":"running","completed":2,"total":5}}}`), nil
}

func TestAgentRuns_TypedToolPersistsRemoteReferenceAndRecoveryIsOwned(t *testing.T) {
	store := newFakeAgentRunStore()
	module := NewAgentRunsModule(AgentRunsModuleDeps{Store: store, Catalog: agenttools.NewCatalog(), JobMaster: fakeAgentJobMaster{}, Protected: func(h http.HandlerFunc) http.HandlerFunc { return h }})
	mux := chi.NewRouter()
	module.Register(mux)
	ctx := func(req *http.Request, ws int64) *http.Request {
		return req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, ws, []string{agenttools.PermissionAutomation})))
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs", bytes.NewBufferString(`{"goal":"g","idempotency_key":"workflow-1"}`))
	req = ctx(req, 7)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var run createRunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs/"+run.RunID+"/tools/content.generate_script", bytes.NewBufferString(`{"project":"p","idempotency_key":"workflow-1-script","payload":{}}`))
	req = ctx(req, 7)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("invoke: %d %s", w.Code, w.Body.String())
	}
	if len(store.steps) != 1 {
		t.Fatalf("steps=%d", len(store.steps))
	}
	for _, step := range store.steps {
		if step.RemoteJobID != "remote-1" || step.IdempotencyKey != "workflow-1-script" {
			t.Fatalf("remote ref not persisted: %+v", step)
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/runs/"+run.RunID+"/recovery", nil)
	req = ctx(req, 99)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace recovery = %d, want 404", w.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agent/runs/"+run.RunID+"/recovery", nil)
	req = ctx(req, 7)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "completed") {
		t.Fatalf("recovery: %d %s", w.Code, w.Body.String())
	}
	stepCompleted := false
	for _, persistedStep := range store.steps {
		stepCompleted = stepCompleted || persistedStep.Status == "completed"
	}
	if store.runs[run.RunID].Status != "completed" || !stepCompleted {
		t.Fatalf("recovery did not persist terminal status: run=%q steps=%+v", store.runs[run.RunID].Status, store.steps)
	}
}

func TestAgentRuns_RecoveryPersistsIntermediateProgress(t *testing.T) {
	store := newFakeAgentRunStore()
	module := NewAgentRunsModule(AgentRunsModuleDeps{Store: store, Catalog: agenttools.NewCatalog(), JobMaster: progressAgentJobMaster{}, Protected: func(h http.HandlerFunc) http.HandlerFunc { return h }})
	mux := chi.NewRouter()
	module.Register(mux)
	withIdentity := func(req *http.Request) *http.Request {
		return req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, 7, []string{agenttools.PermissionAutomation})))
	}
	req := withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs", bytes.NewBufferString(`{"goal":"g","idempotency_key":"progress-run"}`)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var run createRunResponse
	_ = json.Unmarshal(w.Body.Bytes(), &run)
	req = withIdentity(httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs/"+run.RunID+"/tools/content.generate_script", bytes.NewBufferString(`{"project":"p","idempotency_key":"progress-script","payload":{}}`)))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("invoke: %d %s", w.Code, w.Body.String())
	}
	req = withIdentity(httptest.NewRequest(http.MethodGet, "/api/v1/agent/runs/"+run.RunID+"/recovery", nil))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("recovery: %d %s", w.Code, w.Body.String())
	}
	for _, step := range store.steps {
		if step.RemoteStatus != "RUNNING" || step.RemoteProgress == nil || *step.RemoteProgress != 42 || !strings.Contains(string(step.ProgressJSON), "stage_progress") {
			t.Fatalf("progress not persisted: %+v", step)
		}
	}
	if store.runs[run.RunID].Status != "running" {
		t.Fatalf("run status=%q, want running", store.runs[run.RunID].Status)
	}
}
