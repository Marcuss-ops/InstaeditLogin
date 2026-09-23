package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/agenttools"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/go-chi/chi/v5"
)

type fakeVideoIntentStore struct {
	next  int64
	byID  map[int64]*models.Post
	byKey map[string]*models.Post
	due   []models.Post
}

func newFakeVideoIntentStore() *fakeVideoIntentStore {
	return &fakeVideoIntentStore{next: 1, byID: map[int64]*models.Post{}, byKey: map[string]*models.Post{}}
}
func (f *fakeVideoIntentStore) CreateAgentVideoIntent(p *models.Post) error {
	p.ID = f.next
	f.next++
	f.byID[p.ID] = p
	var m map[string]json.RawMessage
	_ = json.Unmarshal(p.Metadata, &m)
	var key string
	_ = json.Unmarshal(m["agent_schedule_key"], &key)
	f.byKey[key] = p
	return nil
}
func (f *fakeVideoIntentStore) FindAgentVideoIntentByKey(_ context.Context, _ int64, key string) (*models.Post, error) {
	return f.byKey[key], nil
}
func (f *fakeVideoIntentStore) FindAgentVideoIntentByID(_ context.Context, _ int64, id int64) (*models.Post, error) {
	return f.byID[id], nil
}
func (f *fakeVideoIntentStore) ListDueAgentVideoIntents(context.Context, time.Time, int) ([]models.Post, error) {
	return append([]models.Post(nil), f.due...), nil
}
func (f *fakeVideoIntentStore) ClaimAgentVideoIntent(context.Context, int64, int64, time.Time) (bool, error) {
	return true, nil
}
func (f *fakeVideoIntentStore) LinkAgentVideoIntent(context.Context, int64, int64, string, string) error {
	return nil
}
func (f *fakeVideoIntentStore) UpdateAgentVideoIntentSchedule(_ context.Context, _ int64, id int64, publishAt, generationAt time.Time, timezone string, payload []byte) error {
	p := f.byID[id]
	p.PublishAt = &publishAt
	var m map[string]any
	_ = json.Unmarshal(p.Metadata, &m)
	m["generation_at"] = generationAt
	m["schedule_timezone"] = timezone
	var workflow any
	_ = json.Unmarshal(payload, &workflow)
	m["generation_payload"] = workflow
	p.Metadata, _ = json.Marshal(m)
	return nil
}
func (f *fakeVideoIntentStore) RunAgentVideoIntentNow(_ context.Context, _ int64, id int64, now time.Time) error {
	var m map[string]any
	_ = json.Unmarshal(f.byID[id].Metadata, &m)
	m["generation_at"] = now
	f.byID[id].Metadata, _ = json.Marshal(m)
	return nil
}
func (f *fakeVideoIntentStore) CancelAgentVideoIntent(_ context.Context, _ int64, id int64) error {
	var m map[string]any
	_ = json.Unmarshal(f.byID[id].Metadata, &m)
	m["generation_status"] = "CANCELLED"
	f.byID[id].Metadata, _ = json.Marshal(m)
	return nil
}
func (f *fakeVideoIntentStore) FailAgentVideoIntent(context.Context, int64, int64, string) error {
	return nil
}

func TestVideoIntentRejectsSchedulingWithoutFullVideoAssembler(t *testing.T) {
	store := newFakeVideoIntentStore()
	module := NewAgentRunsModule(AgentRunsModuleDeps{Store: newFakeAgentRunStore(), Catalog: agenttools.NewCatalog(), JobMaster: fakeAgentJobMaster{}, VideoPublisher: fakeAgentVideoPublisher{}, VideoIntents: store, Protected: func(h http.HandlerFunc) http.HandlerFunc { return h }})
	mux := chi.NewRouter()
	module.Register(mux)
	withID := func(req *http.Request) *http.Request {
		return req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, 7, []string{agenttools.PermissionAutomation})))
	}
	publishAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	body, _ := json.Marshal(map[string]any{"idempotency_key": "calendar-video-1", "timezone": "Europe/Rome", "payload": map[string]any{"pre": map[string]any{"scenes": []any{map[string]any{"scene_id": "s1"}}}, "finalize": map[string]any{}, "publish": map[string]any{"title": "Video", "scheduled_at": publishAt.Format(time.RFC3339), "targets": []any{map[string]any{"platform_account_id": 7}}}}})
	request := func(method, path string, body []byte) *httptest.ResponseRecorder {
		req := withID(httptest.NewRequest(method, path, bytes.NewReader(body)))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	w := request(http.MethodPost, "/api/v1/agent/video-intents", body)
	if w.Code != http.StatusUnprocessableEntity || !bytes.Contains(w.Body.Bytes(), []byte("full-video assembler")) {
		t.Fatalf("create without assembler: %d %s", w.Code, w.Body.String())
	}
	if len(store.byID) != 0 {
		t.Fatalf("unsupported workflow created a calendar intent: %+v", store.byID)
	}
}

type intentWorkspaceStore struct {
	WorkspaceStore
	workspace *models.Workspace
}

func (s intentWorkspaceStore) FindByID(id int64) (*models.Workspace, error) {
	if s.workspace.ID == id {
		return s.workspace, nil
	}
	return nil, nil
}

func TestVideoIntentDispatcherFailsClosedWhenAssemblerIsUnavailable(t *testing.T) {
	store := newFakeAgentRunStore()
	intents := newFakeVideoIntentStore()
	workflow := json.RawMessage(`{"pre":{"scenes":[{"scene_id":"s1"}]},"finalize":{},"publish":{"title":"Scheduled","scheduled_at":"2030-01-01T09:00:00Z","targets":[{"platform_account_id":7}]}}`)
	metadata, _ := json.Marshal(map[string]any{"agent_video_intent": true, "agent_schedule_key": "key-1", "generation_payload": workflow, "generation_status": "DISPATCHING", "generation_at": time.Now().UTC()})
	intents.due = []models.Post{{ID: 1, WorkspaceID: 7, Title: "Scheduled", Metadata: metadata}}
	master := &stagedVideoJobMaster{}
	module := &AgentRunsModule{deps: AgentRunsModuleDeps{Store: store, Catalog: agenttools.NewCatalog(), JobMaster: master, VideoPublisher: fakeAgentVideoPublisher{}, VideoIntents: intents, Workspaces: intentWorkspaceStore{workspace: &models.Workspace{ID: 7, OwnerID: 42}}}}
	if err := module.dispatchDueVideoIntents(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.runs) != 1 || len(store.steps) != 0 {
		t.Fatalf("unsupported dispatch unexpectedly submitted a job: runs=%d steps=%d", len(store.runs), len(store.steps))
	}
	for _, run := range store.runs {
		if run.ActorUserID != 42 || run.WorkspaceID != 7 || run.Status != "failed" {
			t.Fatalf("unexpected run: %+v", run)
		}
	}
}
