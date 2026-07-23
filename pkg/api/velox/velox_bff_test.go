package velox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// --- Mock Client ----------------------------------------------------------

type mockClient struct {
	// Configurable return values per method.
	listJobsFn       func(ctx context.Context, wsID, uid int64, f ListJobsFilter) ([]Job, error)
	createJobFn      func(ctx context.Context, wsID, uid int64, r CreateJobRequest) (*Job, error)
	getJobFn         func(ctx context.Context, wsID, uid int64, id string) (*JobDetail, error)
	cancelJobFn      func(ctx context.Context, wsID, uid int64, id string) error
	listDeliveriesFn func(ctx context.Context, wsID, uid int64, id string) ([]Delivery, error)
	listWorkersFn    func(ctx context.Context, wsID, uid int64) ([]Worker, error)
	getWorkerFn      func(ctx context.Context, wsID, uid int64, id string) (*Worker, error)
	getAssetFn       func(ctx context.Context, wsID, uid int64, id string) (*Asset, error)
}

func (m *mockClient) ListJobs(ctx context.Context, wsID, uid int64, f ListJobsFilter) ([]Job, error) {
	return m.listJobsFn(ctx, wsID, uid, f)
}
func (m *mockClient) CreateJob(ctx context.Context, wsID, uid int64, r CreateJobRequest) (*Job, error) {
	return m.createJobFn(ctx, wsID, uid, r)
}
func (m *mockClient) GetJob(ctx context.Context, wsID, uid int64, id string) (*JobDetail, error) {
	return m.getJobFn(ctx, wsID, uid, id)
}
func (m *mockClient) CancelJob(ctx context.Context, wsID, uid int64, id string) error {
	return m.cancelJobFn(ctx, wsID, uid, id)
}
func (m *mockClient) ListJobDeliveries(ctx context.Context, wsID, uid int64, id string) ([]Delivery, error) {
	return m.listDeliveriesFn(ctx, wsID, uid, id)
}
func (m *mockClient) ListWorkers(ctx context.Context, wsID, uid int64) ([]Worker, error) {
	return m.listWorkersFn(ctx, wsID, uid)
}
func (m *mockClient) GetWorker(ctx context.Context, wsID, uid int64, id string) (*Worker, error) {
	return m.getWorkerFn(ctx, wsID, uid, id)
}
func (m *mockClient) GetAsset(ctx context.Context, wsID, uid int64, id string) (*Asset, error) {
	return m.getAssetFn(ctx, wsID, uid, id)
}

// --- Test harness ---------------------------------------------------------

const (
	testWSID = 42
	testUID  = 7
)

// stubAuth stamps a test identity into the request context, mirroring
// what auth.Manager.Middleware does in production.
func stubAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(testUID, testWSID, 1))
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// stubAuthNoWorkspace stamps an identity with workspace_id=0 to test
// the "no workspace scope" 403 path.
func stubAuthNoWorkspace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(testUID, 0, 1))
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

// newMux builds a chi mux with the BFF routes registered against the
// given mock client and the stub auth middleware.
func newMux(t *testing.T, mc *mockClient, authMw func(http.Handler) http.Handler) *chi.Mux {
	t.Helper()
	mux := chi.NewRouter()
	Register(mux, Deps{
		Client:         mc,
		AuthMiddleware: authMw,
	})
	return mux
}

func do(t *testing.T, mux *chi.Mux, method, path string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v (body=%q)", err, w.Body.String())
	}
	return m
}

// --- Tests ----------------------------------------------------------------

func TestRegister_NilClient_NoRoutes(t *testing.T) {
	mux := chi.NewRouter()
	Register(mux, Deps{Client: nil})
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs", "")
	// chi returns 404 for unregistered routes
	if w.Code != http.StatusNotFound {
		t.Fatalf("nil client should not mount routes; got %d", w.Code)
	}
}

func TestListJobs_HappyPath(t *testing.T) {
	mc := &mockClient{listJobsFn: func(_ context.Context, wsID, uid int64, _ ListJobsFilter) ([]Job, error) {
		if wsID != testWSID {
			t.Fatalf("workspace_id mismatch: got %d", wsID)
		}
		return []Job{{ID: "job_1", WorkspaceID: wsID, RenderStatus: "RUNNING"}}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	jobs, ok := body["jobs"].([]interface{})
	if !ok || len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %v", body)
	}
}

func TestListJobs_NoWorkspace_403(t *testing.T) {
	mc := &mockClient{listJobsFn: func(context.Context, int64, int64, ListJobsFilter) ([]Job, error) {
		t.Fatal("client should not be called without workspace")
		return nil, nil
	}}
	mux := newMux(t, mc, stubAuthNoWorkspace)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestListJobs_FiltersCrossTenant(t *testing.T) {
	mc := &mockClient{listJobsFn: func(_ context.Context, wsID, uid int64, _ ListJobsFilter) ([]Job, error) {
		return []Job{
			{ID: "job_ours", WorkspaceID: wsID, RenderStatus: "RUNNING"},
			{ID: "job_theirs", WorkspaceID: 999, RenderStatus: "RUNNING"},
		}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	jobs := body["jobs"].([]interface{})
	if len(jobs) != 1 {
		t.Fatalf("cross-tenant job should be filtered; got %d jobs", len(jobs))
	}
}

func TestListJobs_ClientError_500(t *testing.T) {
	mc := &mockClient{listJobsFn: func(context.Context, int64, int64, ListJobsFilter) ([]Job, error) {
		return nil, errors.New("upstream down")
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs", "")
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestCreateJob_HappyPath(t *testing.T) {
	mc := &mockClient{createJobFn: func(_ context.Context, wsID, uid int64, r CreateJobRequest) (*Job, error) {
		if wsID != testWSID || uid != testUID {
			t.Fatalf("identity not forwarded: ws=%d uid=%d", wsID, uid)
		}
		if r.ProjectID != "project_123" {
			t.Fatalf("project_id mismatch: %s", r.ProjectID)
		}
		return &Job{ID: "job_new", WorkspaceID: wsID, RenderStatus: "QUEUED"}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"project_id":"project_123","render_spec":{"template":"news"},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J","metadata":{"title":"Hi"}}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateJob_MissingProjectID_422(t *testing.T) {
	mc := &mockClient{createJobFn: func(context.Context, int64, int64, CreateJobRequest) (*Job, error) {
		t.Fatal("client should not be called on validation failure")
		return nil, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"render_spec":{},"delivery_plan":{"destinations":[{"external_destination_id":"x"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestCreateJob_MissingDestinations_422(t *testing.T) {
	mc := &mockClient{}
	mux := newMux(t, mc, stubAuth)
	body := `{"project_id":"p1","render_spec":{}}`
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", body)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", w.Code)
	}
}

func TestCreateJob_WorkspaceMismatch_404(t *testing.T) {
	mc := &mockClient{createJobFn: func(_ context.Context, _, _ int64, _ CreateJobRequest) (*Job, error) {
		// Misconfigured Velox returns a job stamped with a different workspace.
		return &Job{ID: "job_x", WorkspaceID: 999}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	body := `{"project_id":"p1","render_spec":{},"delivery_plan":{"destinations":[{"external_destination_id":"extdst_01J","metadata":{"title":"Hi"}}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for workspace mismatch, got %d", w.Code)
	}
}

func TestCreateJob_InvalidJSON_400(t *testing.T) {
	mc := &mockClient{}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", "{not json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetJob_HappyPath(t *testing.T) {
	mc := &mockClient{getJobFn: func(_ context.Context, wsID, uid int64, id string) (*JobDetail, error) {
		if id != "job_1" {
			t.Fatalf("job id mismatch: %s", id)
		}
		return &JobDetail{
			Job:        Job{ID: id, WorkspaceID: wsID, RenderStatus: "SUCCEEDED"},
			Deliveries: []Delivery{{ExternalDestinationID: "extdst_01J", Status: "published"}},
		}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs/job_1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetJob_NotFound_404(t *testing.T) {
	mc := &mockClient{getJobFn: func(context.Context, int64, int64, string) (*JobDetail, error) {
		return nil, ErrNotFound
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs/missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetJob_WorkspaceMismatch_404(t *testing.T) {
	mc := &mockClient{getJobFn: func(context.Context, int64, int64, string) (*JobDetail, error) {
		return &JobDetail{Job: Job{ID: "job_x", WorkspaceID: 999}}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs/job_x", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for workspace mismatch, got %d", w.Code)
	}
}

func TestCancelJob_HappyPath(t *testing.T) {
	mc := &mockClient{cancelJobFn: func(_ context.Context, wsID, uid int64, id string) error {
		if id != "job_1" || wsID != testWSID {
			t.Fatalf("unexpected args: ws=%d id=%s", wsID, id)
		}
		return nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs/job_1/cancel", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
}

func TestCancelJob_NotFound_404(t *testing.T) {
	mc := &mockClient{cancelJobFn: func(context.Context, int64, int64, string) error {
		return ErrNotFound
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs/missing/cancel", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestListJobDeliveries_HappyPath(t *testing.T) {
	mc := &mockClient{listDeliveriesFn: func(_ context.Context, wsID, uid int64, id string) ([]Delivery, error) {
		return []Delivery{{ExternalDestinationID: "extdst_1", SocialDeliveryID: "sdel_1", Status: "published"}}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs/job_1/deliveries", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestListWorkers_HappyPath(t *testing.T) {
	mc := &mockClient{listWorkersFn: func(_ context.Context, wsID, uid int64) ([]Worker, error) {
		return []Worker{{ID: "w1", WorkspaceID: wsID, Status: "idle"}}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/workers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestListWorkers_FiltersCrossTenant(t *testing.T) {
	mc := &mockClient{listWorkersFn: func(_ context.Context, wsID, uid int64) ([]Worker, error) {
		return []Worker{
			{ID: "w_ours", WorkspaceID: wsID, Status: "idle"},
			{ID: "w_theirs", WorkspaceID: 999, Status: "idle"},
		}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/workers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := decodeBody(t, w)
	workers := body["workers"].([]interface{})
	if len(workers) != 1 {
		t.Fatalf("cross-tenant worker should be filtered; got %d workers", len(workers))
	}
}

func TestGetWorker_HappyPath(t *testing.T) {
	mc := &mockClient{getWorkerFn: func(_ context.Context, wsID, uid int64, id string) (*Worker, error) {
		return &Worker{ID: id, WorkspaceID: wsID, Status: "busy"}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/workers/w1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetWorker_NotFound_404(t *testing.T) {
	mc := &mockClient{getWorkerFn: func(context.Context, int64, int64, string) (*Worker, error) {
		return nil, ErrNotFound
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/workers/missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetWorker_WorkspaceMismatch_404(t *testing.T) {
	mc := &mockClient{getWorkerFn: func(context.Context, int64, int64, string) (*Worker, error) {
		return &Worker{ID: "w_x", WorkspaceID: 999}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/workers/w_x", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatch, got %d", w.Code)
	}
}

func TestGetAsset_HappyPath(t *testing.T) {
	mc := &mockClient{getAssetFn: func(_ context.Context, wsID, uid int64, id string) (*Asset, error) {
		return &Asset{ID: id, WorkspaceID: wsID, SHA256: "abc", SizeBytes: 123, MimeType: "video/mp4"}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/assets/a1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestGetAsset_NotFound_404(t *testing.T) {
	mc := &mockClient{getAssetFn: func(context.Context, int64, int64, string) (*Asset, error) {
		return nil, ErrNotFound
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/assets/missing", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetAsset_WorkspaceMismatch_404(t *testing.T) {
	mc := &mockClient{getAssetFn: func(context.Context, int64, int64, string) (*Asset, error) {
		return &Asset{ID: "a_x", WorkspaceID: 999}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/assets/a_x", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for mismatch, got %d", w.Code)
	}
}
