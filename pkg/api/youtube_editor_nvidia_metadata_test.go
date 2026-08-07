package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeMetadataGenStore is the in-memory MetadataGenerationStore for
// the handler tests. Jobs are stored FIFO with auto-increment ids;
// the prompt and workspace are recorded for assertions.
type fakeMetadataGenStore struct {
	mu   sync.Mutex
	jobs []*models.MetadataGenerationJob
	next int64
}

func newFakeMetadataGenStore() *fakeMetadataGenStore {
	return &fakeMetadataGenStore{next: 1}
}

func (f *fakeMetadataGenStore) Create(job *models.MetadataGenerationJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	job.ID = f.next
	f.next++
	job.Status = models.MetadataGenJobQueued
	job.CreatedAt = time.Now().UTC()
	job.UpdatedAt = job.CreatedAt
	f.jobs = append(f.jobs, job)
	return nil
}

func (f *fakeMetadataGenStore) FindByID(id int64) (*models.MetadataGenerationJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.ID == id {
			return j, nil
		}
	}
	return nil, nil
}

// runMetadataGenPOST mounts the kick-off handler on a bare chi mux and
// issues a POST against /by-project/{velox_project_id}/generate-metadata.
// identity nil → no identity on the context (401 path).
func runMetadataGenPOST(
	t *testing.T,
	r *Router,
	identity auth.Identity,
	projectID string,
	body string,
) *httptest.ResponseRecorder {
	t.Helper()
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata",
		http.HandlerFunc(r.handleGenerateNVIDIAMetadata))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/"+projectID+"/generate-metadata", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if identity != nil {
		req = req.WithContext(withIdentity(req.Context(), identity))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// runMetadataGenGET mounts the poll handler and issues a GET against
// /generate-metadata/jobs/{job_id}.
func runMetadataGenGET(t *testing.T, r *Router, identity auth.Identity, jobID string) *httptest.ResponseRecorder {
	t.Helper()
	mux := chi.NewRouter()
	mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}",
		http.HandlerFunc(r.handleGetMetadataGenerationJob))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions/generate-metadata/jobs/"+jobID, nil)
	if identity != nil {
		req = req.WithContext(withIdentity(req.Context(), identity))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// metadataGenTestRouter wires a Router with a session in workspace 12
// (owned by user 42), a metadata generation store, and a (disabled-key)
// NVIDIA service. Individual deps can be overridden after the call.
func metadataGenTestRouter(t *testing.T) (*Router, *fakeMetadataGenStore) {
	t.Helper()
	store := newFakeYouTubeVideoEditStore()
	row := &models.YouTubeVideoEdit{
		ID:                "ytedit_test",
		WorkspaceID:       12,
		PlatformAccountID: 381,
		VeloxProjectID:    "ve_test",
		Status:            "editing",
		CreatedAt:         time.Now().UTC(),
		UpdatedAt:         time.Now().UTC(),
	}
	store.rows[yveTripleKey(12, 381, "vid")] = row

	ws := &fakeWorkspaceStoreForSessionGet{rows: map[int64]*models.Workspace{
		12: {ID: 12, OwnerID: 42, Name: "ws"},
	}}

	genStore := newFakeMetadataGenStore()
	r := &Router{
		youtubeVideoEditStore:   store,
		workspaceStore:          ws,
		metadataGenerationStore: genStore,
		// A non-empty dummy key exercises the enqueue path without making
		// any NVIDIA network request; the handler only checks configuration
		// before persisting the async job.
		nvidiaMetadataSvc:       services.NewMetadataGenerator("test-nvidia-key"),
	}
	return r, genStore
}

// ---------------------------------------------------------------------------
// POST kick-off
// ---------------------------------------------------------------------------

// TestHandleGenerateNVIDIAMetadata_HappyPath: a valid session + prompt
// → 202 + {job_id, status:"queued"} and the job is persisted with the
// workspace + prompt (NOT the caller's identity, NOT the API key).
func TestHandleGenerateNVIDIAMetadata_HappyPath(t *testing.T) {
	r, genStore := metadataGenTestRouter(t)
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", `{"prompt":"boxing tutorial"}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d body=%s", w.Code, w.Body.String())
	}
	var resp generateMetadataJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.JobID != 1 {
		t.Errorf("job_id: want 1, got %d", resp.JobID)
	}
	if resp.Status != models.MetadataGenJobQueued {
		t.Errorf("status: want queued, got %q", resp.Status)
	}
	if resp.VeloxProjectID != "ve_test" {
		t.Errorf("velox_project_id: want ve_test, got %q", resp.VeloxProjectID)
	}

	job, _ := genStore.FindByID(1)
	if job == nil {
		t.Fatal("job not persisted")
	}
	if job.WorkspaceID != 12 {
		t.Errorf("job workspace: want 12, got %d", job.WorkspaceID)
	}
	if job.Prompt != "boxing tutorial" {
		t.Errorf("job prompt: want %q, got %q", "boxing tutorial", job.Prompt)
	}
	if strings.Contains(w.Body.String(), "apiKey") || strings.Contains(w.Body.String(), "NVIDIA_API_KEY") {
		t.Error("response must never leak the API key")
	}
}

// TestHandleGenerateNVIDIAMetadata_PromptOptional: no body → the job is
// still enqueued with an empty prompt (the model generates from its
// own knowledge).
func TestHandleGenerateNVIDIAMetadata_PromptOptional(t *testing.T) {
	r, genStore := metadataGenTestRouter(t)
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", ``)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d body=%s", w.Code, w.Body.String())
	}
	job, _ := genStore.FindByID(1)
	if job == nil || job.Prompt != "" {
		t.Errorf("job prompt should default to empty, got %+v", job)
	}
}

// TestHandleGenerateNVIDIAMetadata_MalformedJSON: bad body → 400.
func TestHandleGenerateNVIDIAMetadata_MalformedJSON(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", `{"prompt":`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGenerateNVIDIAMetadata_MissingIdentity: no JWT → 401.
func TestHandleGenerateNVIDIAMetadata_MissingIdentity(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	w := runMetadataGenPOST(t, r, nil, "ve_test", `{}`)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGenerateNVIDIAMetadata_SessionNotFound: unknown project → 404.
func TestHandleGenerateNVIDIAMetadata_SessionNotFound(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_unknown", `{}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGenerateNVIDIAMetadata_CrossTenant: session in a workspace
// the caller does not own → 404 (no existence leak).
func TestHandleGenerateNVIDIAMetadata_CrossTenant(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 9999}, "ve_test", `{}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404 (cross-tenant), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGenerateNVIDIAMetadata_ServiceUnconfigured: nil NVIDIA svc
// → 503 with a hint that manual metadata entry still works.
func TestHandleGenerateNVIDIAMetadata_ServiceUnconfigured(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	r.nvidiaMetadataSvc = nil
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", `{}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Manual metadata entry is still available") {
		t.Errorf("503 body should mention the manual fallback, got %s", w.Body.String())
	}
}

// TestHandleGenerateNVIDIAMetadata_StoreUnconfigured: nil job store →
// 503.
func TestHandleGenerateNVIDIAMetadata_StoreUnconfigured(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	r.metadataGenerationStore = nil
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", `{}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGenerateNVIDIAMetadata_StoreError: the job store fails →
// 500, the failure is surfaced (no silent accept).
func TestHandleGenerateNVIDIAMetadata_StoreError(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	r.metadataGenerationStore = &failingMetadataGenStore{}
	w := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", `{}`)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d body=%s", w.Code, w.Body.String())
	}
}

type failingMetadataGenStore struct{}

func (f *failingMetadataGenStore) Create(job *models.MetadataGenerationJob) error {
	return errors.New("db down")
}
func (f *failingMetadataGenStore) FindByID(id int64) (*models.MetadataGenerationJob, error) {
	return nil, errors.New("db down")
}

// ---------------------------------------------------------------------------
// GET poll
// ---------------------------------------------------------------------------

// TestHandleGetMetadataGenerationJob_Queued: a fresh job polls as
// queued with created_at.
func TestHandleGetMetadataGenerationJob_Queued(t *testing.T) {
	r, genStore := metadataGenTestRouter(t)
	w0 := runMetadataGenPOST(t, r, &fakeAuthIdentity{uid: 42}, "ve_test", `{}`)
	if w0.Code != http.StatusAccepted {
		t.Fatalf("kick-off: want 202, got %d", w0.Code)
	}

	w := runMetadataGenGET(t, r, &fakeAuthIdentity{uid: 42}, "1")
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp generateMetadataJobPollResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.Status != models.MetadataGenJobQueued {
		t.Errorf("status: want queued, got %q", resp.Status)
	}
	if resp.VeloxProjectID != "ve_test" {
		t.Errorf("velox_project_id: want ve_test, got %q", resp.VeloxProjectID)
	}
	if resp.CreatedAt == "" {
		t.Error("created_at must be present")
	}
	if len(resp.Result) != 0 || resp.ErrorMessage != "" {
		t.Errorf("queued job must carry no result/error, got result=%s error=%q", resp.Result, resp.ErrorMessage)
	}
	_ = genStore
}

// TestHandleGetMetadataGenerationJob_Completed: a completed job returns
// the stored result verbatim.
func TestHandleGetMetadataGenerationJob_Completed(t *testing.T) {
	r, genStore := metadataGenTestRouter(t)
	if err := genStore.Create(&models.MetadataGenerationJob{
		WorkspaceID:    12,
		VeloxProjectID: "ve_test",
		Status:         models.MetadataGenJobCompleted,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	job, _ := genStore.FindByID(1)
	job.Status = models.MetadataGenJobCompleted
	result := []byte(`{"title":"T","description":"D","tags":["a"],"default_language":"it","default_audio_language":"it","translations":{}}`)
	job.Result = result
	now := time.Now().UTC()
	job.CompletedAt = &now

	w := runMetadataGenGET(t, r, &fakeAuthIdentity{uid: 42}, "1")
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp generateMetadataJobPollResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if resp.Status != models.MetadataGenJobCompleted {
		t.Errorf("status: want completed, got %q", resp.Status)
	}
	if string(resp.Result) != string(result) {
		t.Errorf("result: want %s, got %s", result, resp.Result)
	}
	if resp.CompletedAt == "" {
		t.Error("completed_at must be present for a completed job")
	}
}

// TestHandleGetMetadataGenerationJob_NotFound: unknown job_id → 404.
func TestHandleGetMetadataGenerationJob_NotFound(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	w := runMetadataGenGET(t, r, &fakeAuthIdentity{uid: 42}, "999")
	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGetMetadataGenerationJob_BadID: non-numeric job_id → 400.
func TestHandleGetMetadataGenerationJob_BadID(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	for _, id := range []string{"abc", "-1", "0"} {
		w := runMetadataGenGET(t, r, &fakeAuthIdentity{uid: 42}, id)
		if w.Code != http.StatusBadRequest {
			t.Errorf("job_id=%q: want 400, got %d body=%s", id, w.Code, w.Body.String())
		}
	}
}

// TestHandleGetMetadataGenerationJob_CrossTenant: a job whose workspace
// the caller cannot access → 404 (enumeration guard).
func TestHandleGetMetadataGenerationJob_CrossTenant(t *testing.T) {
	r, genStore := metadataGenTestRouter(t)
	if err := genStore.Create(&models.MetadataGenerationJob{
		WorkspaceID:    12,
		VeloxProjectID: "ve_test",
		Status:         models.MetadataGenJobQueued,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := runMetadataGenGET(t, r, &fakeAuthIdentity{uid: 9999}, "1")
	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404 (cross-tenant), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGetMetadataGenerationJob_MissingIdentity: no JWT → 401.
func TestHandleGetMetadataGenerationJob_MissingIdentity(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	w := runMetadataGenGET(t, r, nil, "1")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandleGetMetadataGenerationJob_StoreUnconfigured: nil store → 503.
func TestHandleGetMetadataGenerationJob_StoreUnconfigured(t *testing.T) {
	r, _ := metadataGenTestRouter(t)
	r.metadataGenerationStore = nil
	w := runMetadataGenGET(t, r, &fakeAuthIdentity{uid: 42}, "1")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d body=%s", w.Code, w.Body.String())
	}
}
