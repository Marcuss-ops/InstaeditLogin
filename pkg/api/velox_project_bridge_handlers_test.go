package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func bridgeTestRouter(t *testing.T, store *thumbnailProjectTestStore, ownerID int64) *Router {
	t.Helper()
	return thumbnailProjectRouter(t, store, &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		return &models.Workspace{ID: id, OwnerID: ownerID}, nil
	}})
}

func bridgeRequest(t *testing.T, method, path, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	return w, req
}

func TestVeloxProjectBridge_CreateAndReplayIsIdempotent(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 1)
	body := `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`

	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", body)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", body)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same bridge replay want 200, got %d: %s", w.Code, w.Body.String())
	}
	if store.bridge == nil || store.bridge.ExternalProjectID != "vx_1" {
		t.Fatalf("bridge not persisted in store: %+v", store.bridge)
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

func TestVeloxProjectBridge_CrossWorkspaceIs404(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 99)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace want 404, got %d", w.Code)
	}
}

func TestVeloxProjectBridge_UnknownContextFieldsAreRejected(t *testing.T) {
	store := &thumbnailProjectTestStore{project: &models.ThumbnailProject{ID: "thumbproj_1", WorkspaceID: 7, Status: models.ThumbnailProjectStatusDraft}}
	r := bridgeTestRouter(t, store, 1)
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1","channel_id":"UC123"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown context field want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestVeloxProjectBridge_GetAndDelete(t *testing.T) {
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
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_1/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_1"}`)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("conflict want 409, got %d: %s", w.Code, w.Body.String())
	}
}
