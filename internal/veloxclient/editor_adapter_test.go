package veloxclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestVeloxAdapterLifecycleAndRenderOnlyContract(t *testing.T) {
	const projectID = "ve_adapter_test"
	var documentWritten bool
	var renderRequested bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := parseBearer(t, r.Header.Get("Authorization"))
		if r.URL.Path == veloxAPIPrefix+"/editor/projects/"+projectID+"/document" {
			if got, _ := claims["project_id"].(string); got != projectID {
				t.Errorf("project_id claim = %q; want %q", got, projectID)
			}
			if r.Method != http.MethodPut {
				t.Errorf("document method = %s; want PUT", r.Method)
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read document: %v", err)
			}
			if string(raw) != `{"objects":[]}` {
				t.Errorf("document body = %s", raw)
			}
			documentWritten = true
			_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true})
			return
		}
		if r.URL.Path == veloxAPIPrefix+"/editor/projects/"+projectID {
			if got, _ := claims["project_id"].(string); got != projectID {
				t.Errorf("project_id claim = %q; want %q", got, projectID)
			}
			switch r.Method {
			case http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]any{"project_id": projectID, "workspace_id": 7})
				return
			default:
				t.Errorf("unexpected project method %s", r.Method)
			}
		}
		if r.URL.Path == veloxAPIPrefix+"/jobs" && r.Method == http.MethodPost {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode render request: %v", err)
			}
			var renderOnly bool
			if err := json.Unmarshal(body["render_only"], &renderOnly); err != nil || !renderOnly {
				t.Errorf("render_only = %s; want true", body["render_only"])
			}
			var bodyProject string
			if err := json.Unmarshal(body["project_id"], &bodyProject); err != nil || bodyProject != projectID {
				t.Errorf("project_id body = %q; want %q", bodyProject, projectID)
			}
			renderRequested = true
			_ = json.NewEncoder(w).Encode(jobResponse{ID: "job_adapter_render", WorkspaceID: 7, ProjectID: projectID, RenderStatus: "QUEUED"})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	adapter := NewVeloxAdapter(client)
	ctx := context.Background()

	created, err := adapter.CreateProject(ctx, services.CreateEditorProjectRequest{
		UserID:               11,
		WorkspaceID:          7,
		ApplicationProjectID: "thumb_1",
		ExternalProjectID:    projectID,
		InitialDocument:      json.RawMessage(`{"objects":[]}`),
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if created.ExternalProjectID != projectID || !documentWritten {
		t.Fatalf("created=%+v documentWritten=%v", created, documentWritten)
	}

	opened, err := adapter.OpenProject(ctx, services.EditorProject{
		UserID: 11, WorkspaceID: 7, ExternalProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if opened.State != "ready" {
		t.Errorf("opened state = %q; want ready", opened.State)
	}

	status, err := adapter.GetProjectStatus(ctx, services.EditorProject{
		UserID: 11, WorkspaceID: 7, ExternalProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("GetProjectStatus: %v", err)
	}
	if status.State != "ready" || status.ExternalProjectID != projectID {
		t.Errorf("status = %+v", status)
	}

	render, err := adapter.RequestRender(ctx, services.EditorProject{
		UserID: 11, WorkspaceID: 7, ExternalProjectID: projectID,
	}, services.RequestEditorRenderRequest{
		RenderSpec:     json.RawMessage(`{"scenes":[]}`),
		IdempotencyKey: "render-idem-1",
	})
	if err != nil {
		t.Fatalf("RequestRender: %v", err)
	}
	if render.JobID != "job_adapter_render" || !renderRequested {
		t.Fatalf("render=%+v renderRequested=%v", render, renderRequested)
	}

}
