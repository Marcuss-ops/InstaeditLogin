package api

import (
	"encoding/json"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListIntegrationVeloxDestinations_Happy(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JAAA", 12, 345, true)
	seedDestination(destStore, "extdst_01JBBB", 12, 346, true)
	seedDestination(destStore, "extdst_01JCCC", 12, 347, false) // disabled
	seedDestination(destStore, "extdst_01JDDD", 99, 348, true)  // different workspace

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations?workspace_id=12", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Destinations []VeloxDestinationResponse `json:"destinations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Should return 2 (enabled only, same workspace; disabled +
	// cross-workspace rows excluded).
	if len(resp.Destinations) != 2 {
		t.Errorf("len(destinations) = %d; want 2 (enabled only, ws=12)", len(resp.Destinations))
	}
}

func TestListIntegrationVeloxDestinations_IncludeDisabled(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JAAA", 12, 345, true)
	seedDestination(destStore, "extdst_01JBBB", 12, 346, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations?workspace_id=12&include_disabled=true", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var resp struct {
		Destinations []VeloxDestinationResponse `json:"destinations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Destinations) != 2 {
		t.Errorf("len = %d; want 2 (include_disabled)", len(resp.Destinations))
	}
}

func TestListIntegrationVeloxDestinations_403_NotOwned(t *testing.T) {
	r, _, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 999}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations?workspace_id=12", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", w.Code)
	}
}

func TestListIntegrationVeloxDestinations_Empty(t *testing.T) {
	r, _, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations?workspace_id=12", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", w.Code)
	}
	var resp struct {
		Destinations []VeloxDestinationResponse `json:"destinations"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Destinations) != 0 {
		t.Errorf("len = %d; want 0", len(resp.Destinations))
	}
}

func TestListIntegrationVeloxDestinations_400_NoWorkspaceID(t *testing.T) {
	r, _, _, _, _ := setupRouterForDestinations()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", w.Code)
	}
}

func TestGetIntegrationVeloxDestination_Happy(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JABC", 12, 345, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations/extdst_01JABC", nil)
	req = reqWithUser(req, 123)
	// chi needs the route to be registered with {id} for URLParam to
	// work — IntegrationsModule.Register already mounted it.
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ExternalDestinationID != "extdst_01JABC" {
		t.Errorf("id = %q; want extdst_01JABC", resp.ExternalDestinationID)
	}
	if resp.Status != "active" {
		t.Errorf("status = %q; want active", resp.Status)
	}
	// WorkspaceID must NOT appear in the JSON (json:"-").
	if strings.Contains(w.Body.String(), "workspace_id") {
		t.Error("workspace_id should not be serialized to the browser")
	}
}

func TestGetIntegrationVeloxDestination_404_NotFound(t *testing.T) {
	r, _, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations/extdst_UNKNOWN", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
}

func TestGetIntegrationVeloxDestination_404_NotOwned(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	// The destination belongs to workspace 99, but the caller owns 12.
	// wsStore returns OwnerID=123 for ANY id (it's a single-result fake),
	// so we need to make the destination's WorkspaceID not match the
	// workspace the caller owns. We set wsStore to return ws=99 owned by
	// 123, and the destination belongs to ws=99 — but the caller (123)
	// does own ws=99. To test the not-owned path, we need ws.OwnerID !=
	// userID. Set wsStore to return a workspace owned by a different user.
	wsStore.FindByIDResult = &models.Workspace{ID: 99, OwnerID: 999}
	seedDestination(destStore, "extdst_01JXYZ", 99, 345, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/velox/destinations/extdst_01JXYZ", nil)
	req = reqWithUser(req, 123) // caller is 123, workspace owner is 999
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (not owned collapses to not found)", w.Code)
	}
}
