package api

import (
	"bytes"
	"encoding/json"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateIntegrationVeloxDestination_Happy_Defaults(t *testing.T) {
	r, destStore, wsStore, _, auditStore := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JUPD", 12, 345, true)

	body := []byte(`{"defaults": {"privacy_status": "unlisted", "language": "en", "timezone": "Europe/Rome"}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_01JUPD", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ExternalDestinationID != "extdst_01JUPD" {
		t.Errorf("id = %q; want extdst_01JUPD", resp.ExternalDestinationID)
	}
	// Stored row should reflect the new defaults (round-trip check).
	if !strings.Contains(string(destStore.ByIDMap["extdst_01JUPD"].DefaultMetadata), "unlisted") {
		t.Errorf("store row defaults = %q; want contains unlisted",
			string(destStore.ByIDMap["extdst_01JUPD"].DefaultMetadata))
	}
	if auditStore.LastEvent != "external_destination_updated" {
		t.Errorf("audit event = %q; want external_destination_updated", auditStore.LastEvent)
	}
	// Schema pin: keys are exactly {enabled, defaults_changed} per the
	// VeloxDestinationUpdateAuditDeltas struct. enabled surfaces as
	// nil here (the body omitted it); defaults_changed = true
	// (the body supplied defaults).
	if v, ok := auditStore.LastMetadata["defaults_changed"]; !ok {
		t.Error("audit metadata missing `defaults_changed` delta")
	} else if v != true {
		t.Errorf("audit metadata defaults_changed = %v; want true", v)
	}
	if v, ok := auditStore.LastMetadata["enabled"]; !ok {
		t.Error("audit metadata missing `enabled` key (should be JSON null when PATCH body omitted it)")
	} else if v != nil {
		t.Errorf("audit metadata enabled = %v; want nil", v)
	}
}

func TestUpdateIntegrationVeloxDestination_Happy_Enabled(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JUPE", 12, 345, true)

	disable := func() VeloxDestinationResponse {
		body := []byte(`{"enabled": false}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_01JUPE", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = reqWithUser(req, 123)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
		}
		var resp VeloxDestinationResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}
	if got := disable(); got.Status != "disabled" {
		t.Errorf("after disable status = %q; want disabled", got.Status)
	}
	if destStore.ByIDMap["extdst_01JUPE"].Enabled {
		t.Error("store row Enabled should be false after disable PATCH")
	}

	enable := func() VeloxDestinationResponse {
		body := []byte(`{"enabled": true}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_01JUPE", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = reqWithUser(req, 123)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
		}
		var resp VeloxDestinationResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp
	}
	if got := enable(); got.Status != "active" {
		t.Errorf("after re-enable status = %q; want active", got.Status)
	}
}

func TestUpdateIntegrationVeloxDestination_Happy_Both(t *testing.T) {
	r, destStore, wsStore, _, auditStore := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JUPB", 12, 345, true)

	body := []byte(`{"enabled": false, "defaults": {"privacy_status": "private", "language": "it"}}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_01JUPB", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	if v, ok := auditStore.LastMetadata["enabled"]; !ok {
		t.Error("audit metadata missing `enabled` delta")
	} else if v != false {
		t.Errorf("audit metadata enabled = %v; want false", v)
	}
	if v, ok := auditStore.LastMetadata["defaults_changed"]; !ok {
		t.Error("audit metadata missing `defaults_changed` delta")
	} else if v != true {
		t.Errorf("audit metadata defaults_changed = %v; want true", v)
	}
}

func TestUpdateIntegrationVeloxDestination_400_Empty(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JUPX", 12, 345, true)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_01JUPX", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateIntegrationVeloxDestination_404_NotFound(t *testing.T) {
	r, _, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_UNKNOWN", strings.NewReader(`{"enabled": true}`))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateIntegrationVeloxDestination_404_NotOwned(t *testing.T) {
	r, destStore, wsStore, _, _ := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 99, OwnerID: 999}
	seedDestination(destStore, "extdst_01JUPN", 99, 345, true)

	body := []byte(`{"enabled": false}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/integrations/velox/destinations/extdst_01JUPN", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123) // caller ≠ owner of the row's workspace
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (not owned collapses to not found)", w.Code)
	}
	// Row state must be preserved verbatim — the fake defaults
	// Enabled=true so the stall tells us we made the right call.
	if !destStore.ByIDMap["extdst_01JUPN"].Enabled {
		t.Error("destination should NOT have been mutated when not owned")
	}
}

func TestUpdateIntegrationVeloxDestination_Happy_DefaultsNull(t *testing.T) {
	t.Run("literal null stores 'null' on row", func(t *testing.T) {
		r, destStore, wsStore, _, _ := setupRouterForDestinations()
		wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
		seedDestination(destStore, "extdst_01JNULL", 12, 345, true)

		body := []byte(`{"defaults": null}`)
		req := httptest.NewRequest(http.MethodPatch,
			"/api/v1/integrations/velox/destinations/extdst_01JNULL",
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = reqWithUser(req, 123)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("null literal: status = %d; want 200; body=%s", w.Code, w.Body.String())
		}
		got := strings.TrimSpace(string(destStore.ByIDMap["extdst_01JNULL"].DefaultMetadata))
		if got != "null" {
			t.Errorf("null-literal PATCH: row defaults = %q; want literal \"null\"", got)
		}
	})

	t.Run("absent field does not touch row", func(t *testing.T) {
		r, destStore, wsStore, _, _ := setupRouterForDestinations()
		wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
		dest := seedDestination(destStore, "extdst_01JABS", 12, 345, true)
		// Pin a fresh sentinel so the post-PATCH assertion is
		// robust against any incidental mutation elsewhere.
		seededBytes := `{"seeded_baseline":"true"}`
		dest.DefaultMetadata = json.RawMessage(seededBytes)

		body := []byte(`{"enabled": false}`)
		req := httptest.NewRequest(http.MethodPatch,
			"/api/v1/integrations/velox/destinations/extdst_01JABS",
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = reqWithUser(req, 123)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("absent defaults: status = %d; want 200; body=%s", w.Code, w.Body.String())
		}
		got := strings.TrimSpace(string(destStore.ByIDMap["extdst_01JABS"].DefaultMetadata))
		if got != seededBytes {
			t.Errorf("defaults changed despite absent field; got %q, want %q", got, seededBytes)
		}
		if destStore.ByIDMap["extdst_01JABS"].Enabled {
			t.Error("enabled should be false after PATCH with enabled=false")
		}
	})
}

func TestUpdateIntegrationVeloxDestination_AuditDeltas(t *testing.T) {
	cases := []struct {
		name                string
		body                string
		wantEnabled         interface{} // bool OR nil
		wantDefaultsChanged bool
	}{
		{
			name:                "enabled only (true)",
			body:                `{"enabled": true}`,
			wantEnabled:         true,
			wantDefaultsChanged: false,
		},
		{
			name:                "enabled only (false)",
			body:                `{"enabled": false}`,
			wantEnabled:         false,
			wantDefaultsChanged: false,
		},
		{
			name:                "defaults only",
			body:                `{"defaults": {"privacy_status": "private"}}`,
			wantEnabled:         nil,
			wantDefaultsChanged: true,
		},
		{
			name:                "both fields",
			body:                `{"enabled": true, "defaults": {"language": "it"}}`,
			wantEnabled:         true,
			wantDefaultsChanged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "extdst_audit_" + strings.ReplaceAll(tc.name, " ", "_")
			r, destStore, wsStore, _, auditStore := setupRouterForDestinations()
			wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
			seedDestination(destStore, id, 12, 345, true)

			req := httptest.NewRequest(http.MethodPatch,
				"/api/v1/integrations/velox/destinations/"+id,
				bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			req = reqWithUser(req, 123)
			w := httptest.NewRecorder()
			r.mux.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
			}
			if auditStore.LastEvent != "external_destination_updated" {
				t.Fatalf("audit event = %q; want external_destination_updated", auditStore.LastEvent)
			}

			// Schema: keys are EXACTLY {enabled, defaults_changed}.
			gotKeys := make(map[string]struct{})
			for k := range auditStore.LastMetadata {
				gotKeys[k] = struct{}{}
			}
			if _, ok := gotKeys["enabled"]; !ok {
				t.Errorf("audit metadata missing `enabled` key; got keys = %v", gotKeys)
			}
			if _, ok := gotKeys["defaults_changed"]; !ok {
				t.Errorf("audit metadata missing `defaults_changed` key; got keys = %v", gotKeys)
			}
			if len(gotKeys) != 2 {
				t.Errorf("audit metadata has %d keys; want exactly 2; got = %v",
					len(gotKeys), gotKeys)
			}

			// Values: `enabled` is bool(true|false) when supplied, nil when JSON null.
			got := auditStore.LastMetadata["enabled"]
			if got != tc.wantEnabled {
				t.Errorf("audit metadata enabled = %v (%T); want %v (%T)",
					got, got, tc.wantEnabled, tc.wantEnabled)
			}
			// `defaults_changed` is always bool.
			dv, ok := auditStore.LastMetadata["defaults_changed"].(bool)
			if !ok {
				t.Errorf("audit metadata defaults_changed not bool: got %T = %v",
					auditStore.LastMetadata["defaults_changed"],
					auditStore.LastMetadata["defaults_changed"])
			} else if dv != tc.wantDefaultsChanged {
				t.Errorf("audit metadata defaults_changed = %v; want %v",
					dv, tc.wantDefaultsChanged)
			}
		})
	}
}

func TestUpdateIntegrationVeloxDestination_CombinedUpdate(t *testing.T) {
	r, destStore, wsStore, _, auditStore := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JUPC", 12, 345, false)

	body := []byte(`{"enabled": true, "defaults": {"k": "v", "n": 42}}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/integrations/velox/destinations/extdst_01JUPC",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var resp VeloxDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ExternalDestinationID != "extdst_01JUPC" {
		t.Errorf("id = %q; want extdst_01JUPC", resp.ExternalDestinationID)
	}
	if resp.Status != "active" {
		t.Errorf("status = %q; want active (Enabled flipped to true)", resp.Status)
	}

	row, ok := destStore.ByIDMap["extdst_01JUPC"]
	if !ok {
		t.Fatal("seeded destination vanished from ByIDMap")
	}
	// Combined-verb invariants: BOTH columns persisted in ONE round-trip.
	if !row.Enabled {
		t.Errorf("After PATCH {enabled:true, defaults:...}, row.Enabled = false; want true")
	}
	if !strings.Contains(string(row.DefaultMetadata), `"k": "v"`) ||
		!strings.Contains(string(row.DefaultMetadata), `"n": 42`) {
		t.Errorf("After PATCH, row.DefaultMetadata = %q; want contains \"k\": \"v\" and \"n\": 42",
			string(row.DefaultMetadata))
	}
	// Single-call invariant: handler issued exactly ONE
	// UpdateEnabledAndDefaults call. Re-introducing the two-call
	// sequence would break this counter and re-open the
	// partial-write window a concurrent DELETE could exploit.
	if destStore.updateEnabledAndDefaultsCalls != 1 {
		t.Errorf("UpdateEnabledAndDefaults called %d times; want exactly 1 (proves single-statement UPDATE)",
			destStore.updateEnabledAndDefaultsCalls)
	}

	// Audit shape stays exactly the pinned contract regardless of
	// single-vs-two-call internals.
	if auditStore.LastEvent != "external_destination_updated" {
		t.Errorf("audit event = %q; want external_destination_updated", auditStore.LastEvent)
	}
	if v, ok := auditStore.LastMetadata["enabled"]; !ok {
		t.Error("audit metadata missing `enabled` key")
	} else if v != true {
		t.Errorf("audit metadata enabled = %v; want true", v)
	}
	if v, ok := auditStore.LastMetadata["defaults_changed"]; !ok {
		t.Error("audit metadata missing `defaults_changed` key")
	} else if v != true {
		t.Errorf("audit metadata defaults_changed = %v; want true", v)
	}
}

func TestUpdateIntegrationVeloxDestination_CombinedUpdate_NotFoundRaceMapping(t *testing.T) {
	r, destStore, wsStore, _, auditStore := setupRouterForDestinations()
	wsStore.FindByIDResult = &models.Workspace{ID: 12, OwnerID: 123}
	seedDestination(destStore, "extdst_01JUPCRACE", 12, 345, false)
	// Simulate concurrent DELETE between authz and UPDATE.
	destStore.UpdateEnabledAndDefaultsErr = repository.ErrExternalDestinationNotFound

	body := []byte(`{"enabled": true, "defaults": {"k": "v"}}`)
	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/integrations/velox/destinations/extdst_01JUPCRACE",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = reqWithUser(req, 123)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (ErrExternalDestinationNotFound mapping); body=%s",
			w.Code, w.Body.String())
	}
	if destStore.updateEnabledAndDefaultsCalls != 1 {
		t.Errorf("UpdateEnabledAndDefaults called %d times; want exactly 1",
			destStore.updateEnabledAndDefaultsCalls)
	}
	if auditStore.LastEvent != "" {
		t.Errorf("audit event = %q; want empty (handler must abort before audit on 404)",
			auditStore.LastEvent)
	}
	// Even though the stub returned ErrExternalDestinationNotFound,
	// the row's in-memory state must be UNCHANGED (the stub
	// short-circuits before mutating).
	row := destStore.ByIDMap["extdst_01JUPCRACE"]
	if row.Enabled {
		t.Error("After 404-mapping PATCH, row.Enabled flipped to true; want unchanged (false)")
	}
}
