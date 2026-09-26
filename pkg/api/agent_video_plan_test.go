package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/agenttools"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/jobmaster"
	"github.com/go-chi/chi/v5"
)

type videoPlanMaster struct {
	jobmaster.API
	searchCalls int
}

func (m *videoPlanMaster) SearchMedia(context.Context, string, int) (json.RawMessage, error) {
	m.searchCalls++
	return json.RawMessage(`{"items":[]}`), nil
}

func TestAgentVideoPlanBuildsAutonomousVideoCreatePayload(t *testing.T) {
	master := &videoPlanMaster{}
	module := NewAgentRunsModule(AgentRunsModuleDeps{
		Store: newFakeAgentRunStore(), Catalog: agenttools.NewCatalog(), JobMaster: master,
		Protected:               func(h http.HandlerFunc) http.HandlerFunc { return h },
		ProtectedWithPermission: func(_ string, h http.HandlerFunc) http.HandlerFunc { return h },
	})
	mux := chi.NewRouter()
	module.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/video-plan", bytes.NewBufferString(`{"topic":"Mike Tyson training","title":"Tyson","target_duration_seconds":480}`))
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, 7, []string{agenttools.PermissionAutomation})))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("video plan: status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Generation map[string]any `json:"generation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Generation["topic"] != "Mike Tyson training" || body.Generation["duration_seconds"] != float64(480) {
		t.Fatalf("generation request=%+v", body.Generation)
	}
	if master.searchCalls != 0 {
		t.Fatalf("control plane did media discovery %d times; video.create owns discovery", master.searchCalls)
	}
}
