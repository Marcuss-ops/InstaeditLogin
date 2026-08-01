package api

import (
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteIntegrationVeloxDestination_Happy(t *testing.T) {
	r, destStore, wsStore, _, auditStore := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JDEL", 12, 345, true)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/velox/destinations/extdst_01JDEL", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d; want 204; body=%s", w.Code, w.Body.String())
	}
	if _, ok := destStore.ByIDMap["extdst_01JDEL"]; ok {
		t.Error("destination should have been deleted from the store")
	}
	if auditStore.LastEvent != "external_destination_deleted" {
		t.Errorf("audit event = %q; want external_destination_deleted", auditStore.LastEvent)
	}
}

func TestDeleteIntegrationVeloxDestination_404_NotFound(t *testing.T) {
	r, _, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/velox/destinations/extdst_UNKNOWN", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", w.Code)
	}
}

func TestDeleteIntegrationVeloxDestination_404_NotOwned(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 99, OwnerID: 999}
	seedDestination(destStore, "extdst_01JXYZ", 99, 345, true)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/velox/destinations/extdst_01JXYZ", nil)
	req = reqWithUser(req, 123) // caller 123, owner 999
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (not owned)", w.Code)
	}
	if _, ok := destStore.ByIDMap["extdst_01JXYZ"]; !ok {
		t.Error("destination should NOT have been deleted (not owned)")
	}
}

func TestDeleteIntegrationVeloxDestination_409_Dependents(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JDEP", 12, 345, true)
	destStore.DeleteErr = repository.ErrExternalDestinationHasDependents

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/integrations/velox/destinations/extdst_01JDEP", nil)
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d; want 409; body=%s", w.Code, w.Body.String())
	}
}
