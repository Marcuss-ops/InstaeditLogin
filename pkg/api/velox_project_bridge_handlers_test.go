package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func bridgeTestRouter(t *testing.T, store *thumbnailProjectTestStore, ownerID int64) *Router {
	t.Helper()
	r := thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		return &models.Workspace{ID: id, OwnerID: ownerID}, nil
	}})
	editorService := &fakeEditorService{}
	editorService.createProjectFn = func(_ context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
		if store.bridge != nil {
			if store.bridge.WorkspaceID != req.WorkspaceID || store.bridge.ProjectID != req.ApplicationProjectID {
				return nil, services.ErrEditorProjectNotFound
			}
			if store.bridge.ExternalProjectID != req.ExternalProjectID {
				return nil, services.ErrEditorProjectConflict
			}
			return &services.EditorProject{ApplicationProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: store.bridge.ExternalProjectID}, nil
		}
		if store.createBridgeErr != nil {
			return nil, services.ErrEditorProjectConflict
		}
		externalID := req.ExternalProjectID
		if externalID == "" {
			externalID = "vx_1"
		}
		store.bridge = &models.VeloxProjectBridge{ProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: externalID, EditorProvider: "velox"}
		return &services.EditorProject{ApplicationProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: externalID, Created: len(editorService.calls) == 1}, nil
	}
	r.editorService = editorService
	return r
}

func bridgeRequest(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	return w, req
}

func TestVeloxProjectBridge_CreateWithoutEditorServiceFailsClosed(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 1)
	r.editorService = nil
	body := `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7}`
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", body)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing editor service want 503, got %d: %s", w.Code, w.Body.String())
	}
	if store.createBridgeCalls != 0 || store.bridge != nil {
		t.Fatalf("missing editor service must not persist bridge: calls=%d bridge=%+v", store.createBridgeCalls, store.bridge)
	}
}

func TestVeloxProjectBridge_CreateAndReplayIsIdempotent(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 1)
	body := `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7}`
	replayBody := `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`

	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", body)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", replayBody)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same bridge replay want 200, got %d: %s", w.Code, w.Body.String())
	}
	if store.createBridgeCalls != 0 {
		t.Fatalf("service-backed replay must not persist through legacy store, got %d create calls", store.createBridgeCalls)
	}
	if store.bridge == nil || store.bridge.ExternalProjectID != "vx_1" {
		t.Fatalf("bridge not persisted in service fake: %+v", store.bridge)
	}
}

func TestVeloxProjectBridge_FirstCreateUsesEditorServiceWhenConfigured(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	editorService := &fakeEditorService{createProjectFn: func(_ context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
		externalID := req.ExternalProjectID
		if externalID == "" {
			externalID = "vx_1"
		}
		store.bridge = &models.VeloxProjectBridge{ProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: externalID}
		return &services.EditorProject{ApplicationProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: externalID, Created: true}, nil
	}}
	r := bridgeTestRouter(t, store, 1)
	r.editorService = editorService
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("service-backed create want 201, got %d: %s", w.Code, w.Body.String())
	}
	if len(editorService.calls) != 1 || store.createBridgeCalls != 0 {
		t.Fatalf("service-backed create calls=%d store creates=%d; want service=1 and legacy store=0", len(editorService.calls), store.createBridgeCalls)
	}
	call := editorService.calls[0]
	if call.UserID != 1 || call.WorkspaceID != 7 || call.ApplicationProjectID != "thumbproj_1" || call.ExternalProjectID != "" {
		t.Fatalf("new bridge must not trust browser external_project_id: %+v", call)
	}
}

func TestVeloxProjectBridge_ReplayUsesEditorServiceWhenConfigured(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft},
		bridge:  &models.VeloxProjectBridge{ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_1"},
	}
	editorService := &fakeEditorService{createProjectFn: func(_ context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
		return &services.EditorProject{ApplicationProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: req.ExternalProjectID, Created: false}, nil
	}}
	r := bridgeTestRouter(t, store, 1)
	r.editorService = editorService
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("service-backed replay want 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(editorService.calls) != 1 {
		t.Fatalf("service-backed replay must call EditorService once, got %d", len(editorService.calls))
	}
	call := editorService.calls[0]
	if call.UserID != 1 || call.WorkspaceID != 7 || call.ApplicationProjectID != "thumbproj_1" || call.ExternalProjectID != "vx_1" {
		t.Fatalf("unexpected replay service request: %+v", call)
	}
	if store.createBridgeCalls != 0 {
		t.Fatalf("service-backed replay must not use legacy repository create, got %d calls", store.createBridgeCalls)
	}
}

func TestVeloxProjectBridge_ChangedReplayIs409(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft},
		bridge:  &models.VeloxProjectBridge{ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_old"},
	}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_new"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("changed bridge want 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVeloxProjectBridge_CrossWorkspaceIs404WithoutPersistence(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 99)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace want 404, got %d", w.Code)
	}
	if store.createBridgeCalls != 0 || store.bridge != nil {
		t.Fatalf("foreign workspace request was persisted: calls=%d bridge=%+v", store.createBridgeCalls, store.bridge)
	}
}

func TestVeloxProjectBridge_LegacyOwnershipFieldsAreRejectedWithoutPersistence(t *testing.T) {
	legacyFields := []string{
		`"group_id":9`,
		`"group_ids":[9]`,
		`"channel_id":"UC123"`,
		`"channel_ids":["UC123"]`,
		`"member_ids":[1]`,
		`"platform_account_id":42`,
		`"video_id":"video-1"`,
		`"language":"it"`,
	}
	for _, field := range legacyFields {
		t.Run(field, func(t *testing.T) {
			store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
			r := bridgeTestRouter(t, store, 1)
			body := fmt.Sprintf(`{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1",%s}`, field)
			w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", body)
			r.Setup().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("legacy field %s: want 400, got %d: %s", field, w.Code, w.Body.String())
			}
			if store.createBridgeCalls != 0 || store.bridge != nil {
				t.Fatalf("legacy field %s was persisted: calls=%d bridge=%+v", field, store.createBridgeCalls, store.bridge)
			}
		})
	}
}

func TestVeloxProjectBridge_GetAndDeleteIsWorkspaceScoped(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft},
		bridge:  &models.VeloxProjectBridge{ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_1"},
	}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodGet, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge?workspace_id=7", "")
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response veloxProjectBridgeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Bridge.ExternalProjectID != "vx_1" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
	w, req = bridgeRequest(t, http.MethodDelete, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge?workspace_id=7", "")
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete want 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVeloxProjectBridge_ConcurrentEquivalentConflictReReadsExisting(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project:         &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft},
		bridge:          &models.VeloxProjectBridge{ProjectID: "thumbproj_1", WorkspaceID: 7, ExternalProjectID: "vx_1"},
		createBridgeErr: repository.ErrVeloxProjectBridgeConflict,
	}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("equivalent concurrent conflict want 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVeloxProjectBridge_UnknownFieldsAreRejected(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1","group_id":9}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("forbidden group_id want 400, got %d", w.Code)
	}
}

func TestVeloxProjectBridge_UnknownContractVersionIs422(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"future.v9","workspace_id":7,"external_project_id":"vx_1"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown contract version want 422, got %d", w.Code)
	}
}

func TestVeloxProjectBridge_StoreConflictMaps409(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project:         &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft},
		createBridgeErr: fmt.Errorf("%w: duplicate", repository.ErrVeloxProjectBridgeConflict),
	}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("conflict want 409, got %d: %s", w.Code, w.Body.String())
	}
}
