package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/jobmaster"
)

type fakeJobMaster struct {
	lastSubmit jobmaster.SubmitRequest
}

func (f *fakeJobMaster) ListTypes(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`{"types":["script.generate"]}`), nil
}

func (f *fakeJobMaster) Submit(_ context.Context, input jobmaster.SubmitRequest) (json.RawMessage, error) {
	f.lastSubmit = input
	return json.RawMessage(`{"job_id":"job_1","status":"queued"}`), nil
}

func (f *fakeJobMaster) PrepareVideo(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"job_1"}`), nil
}

func (f *fakeJobMaster) FinalizeVideo(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"status":"queued"}`), nil
}

func (f *fakeJobMaster) Get(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"job_id":"job_1","status":"completed","result":{"asset_id":"asset_1"}}`), nil
}

func TestJobMasterModuleProxiesAuthenticatedJobLifecycle(t *testing.T) {
	fake := &fakeJobMaster{}
	mux := chi.NewRouter()
	identityMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithIdentity(r.Context(), auth.NewUserIdentity(7, 42, 1))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	NewJobMasterModule(JobMasterModuleDeps{
		Client:         fake,
		AuthMiddleware: identityMiddleware,
	}).Register(mux)

	types := httptest.NewRecorder()
	mux.ServeHTTP(types, httptest.NewRequest(http.MethodGet, "/api/v1/automation/jobs/types", nil))
	if types.Code != http.StatusOK || types.Body.String() != `{"types":["script.generate"]}` {
		t.Fatalf("types response = %d %s", types.Code, types.Body.String())
	}

	submit := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/automation/jobs", strings.NewReader(`{"type":"script.generate","project":"video-01","idempotency_key":"video-01-script-001","payload":{"prompt":"hello"}}`))
	mux.ServeHTTP(submit, request)
	if submit.Code != http.StatusAccepted {
		t.Fatalf("submit status = %d, body = %s", submit.Code, submit.Body.String())
	}
	if fake.lastSubmit.Type != "script.generate" || fake.lastSubmit.Project != "video-01" {
		t.Fatalf("submit = %+v", fake.lastSubmit)
	}

	get := httptest.NewRecorder()
	mux.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/automation/jobs/job_1", nil))
	if get.Code != http.StatusOK || get.Body.String() == "" {
		t.Fatalf("get response = %d %s", get.Code, get.Body.String())
	}
}

func TestJobMasterModuleRequiresIdentity(t *testing.T) {
	mux := chi.NewRouter()
	NewJobMasterModule(JobMasterModuleDeps{Client: &fakeJobMaster{}}).Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/automation/jobs/types", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}
