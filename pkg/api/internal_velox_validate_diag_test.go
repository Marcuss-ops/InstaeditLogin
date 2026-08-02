package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/go-chi/chi/v5"
)

func TestValidate_DiagnosticQueryParam(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:       345,
			Platform: "youtube",
			Status:   "active",
		},
	}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "diagnostic=true")
	if w.Code != http.StatusOK {
		t.Fatalf("diagnostic query: want 200, got %d", w.Code)
	}
	var resp VeloxValidateDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
	}
	if !resp.Valid {
		t.Errorf("Valid: want true, got false")
	}
	if resp.DestinationID != "extdst_01JABC" {
		t.Errorf("DestinationID: want extdst_01JABC, got %s", resp.DestinationID)
	}
	if resp.Status != "active" {
		t.Errorf("Status: want active, got %s", resp.Status)
	}
	if resp.Platform != "youtube" {
		t.Errorf("Platform: want youtube, got %s", resp.Platform)
	}
}

// TestValidate_DiagnosticHeader verifies the X-Velox-Diagnostic:
// true header trigger ALSO returns 200 with JSON.
func TestValidate_DiagnosticHeader(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:       345,
			Platform: "youtube",
			Status:   "active",
		},
	}
	// Custom httptest invocation with header.
	r := buildVeloxTestRouter(dst, ws, user, testVeloxAPIToken)
	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate", handler)
	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/destinations/extdst_01JABC/validate", nil)
	req.Header.Set("Authorization", "Bearer "+testVeloxAPIToken)
	req.Header.Set("X-Velox-Diagnostic", "true")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("diagnostic header: want 200, got %d", w.Code)
	}
	var resp VeloxValidateDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Valid || resp.DestinationID != "extdst_01JABC" {
		t.Errorf("response mismatch: %+v", resp)
	}
}

// TestValidate_DiagnosticDisabled verifies the diagnostic mode
// ALSO short-circuits on disabled destination — no JSON
// leak even when ?diagnostic=true is requested.
func TestValidate_DiagnosticDisabled(t *testing.T) {
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           false,
		},
	}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "diagnostic=true")
	if w.Code != http.StatusNotFound {
		t.Fatalf("diagnostic + disabled: want 404, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestValidate_WorkspaceMissing pins the workspace-not-found
// branch — should return 404 even with diagnostic=true.
