package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/editor"
)

type moduleEditorProxy struct {
	called        bool
	expectedScope string
}

func (p *moduleEditorProxy) Proxy(context.Context, string, string, int64, int64, io.Reader, string, []string) (*http.Response, error) {
	panic("unscoped editor proxy must not be used")
}

func (p *moduleEditorProxy) ProxyForProject(_ context.Context, _ string, _ string, _ int64, _ int64, _ string, _ io.Reader, _ string, scopes []string) (*http.Response, error) {
	p.called = true
	if len(scopes) != 1 || scopes[0] != p.expectedScope {
		return nil, context.Canceled
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"accepted":true}`)),
	}, nil
}

func editorModuleFixture(t *testing.T) (*chi.Mux, *moduleEditorProxy, *models.Workspace) {
	t.Helper()
	workspace := &models.Workspace{ID: 42, OwnerID: 1}
	edit := &models.YouTubeVideoEdit{VeloxProjectID: "ve_project_1", WorkspaceID: workspace.ID}
	editStore := &mockYouTubeVideoEditStore{findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
		if projectID != edit.VeloxProjectID {
			return nil, nil
		}
		return edit, nil
	}}
	workspaceStore := &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		if id != workspace.ID {
			return nil, nil
		}
		return workspace, nil
	}}
	teamStore := newFakeTeamStore()
	teamStore.AddMember(workspace.ID, 7, "editor")
	teamStore.AddMember(workspace.ID, 8, "viewer")
	teamStore.AddMember(workspace.ID, 9, "admin")
	proxy := &moduleEditorProxy{}

	module := NewEditorBFFModule(EditorBFFModuleDeps{
		Client:                editor.ProxyClient(proxy),
		YouTubeVideoEditStore: editStore,
		WorkspaceStore:        workspaceStore,
		TeamStore:             teamStore,
	})
	mux := chi.NewRouter()
	module.Register(mux)
	return mux, proxy, workspace
}

func requestWithIdentity(method, path string, userID, workspaceID int64) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(userID, workspaceID, 1)))
}

func TestEditorBFFAllowsEditorMemberWriteButDeniesViewer(t *testing.T) {
	mux, proxy, workspace := editorModuleFixture(t)
	proxy.expectedScope = veloxcontract.ScopeVeloxEditorWrite

	editorResponse := httptest.NewRecorder()
	mux.ServeHTTP(editorResponse, requestWithIdentity(http.MethodPut, "/api/v1/editor/projects/ve_project_1/document", 7, workspace.ID))
	if editorResponse.Code != http.StatusOK {
		t.Fatalf("editor member write status = %d, want %d", editorResponse.Code, http.StatusOK)
	}
	if !proxy.called {
		t.Fatal("authorized editor member did not reach project proxy")
	}

	proxy.called = false
	viewerResponse := httptest.NewRecorder()
	mux.ServeHTTP(viewerResponse, requestWithIdentity(http.MethodPut, "/api/v1/editor/projects/ve_project_1/document", 8, workspace.ID))
	if viewerResponse.Code != http.StatusNotFound {
		t.Fatalf("viewer write status = %d, want %d", viewerResponse.Code, http.StatusNotFound)
	}
	if proxy.called {
		t.Fatal("viewer write reached project proxy")
	}
}

func TestEditorBFFAllowsViewerReadAndAdminWrite(t *testing.T) {
	mux, proxy, workspace := editorModuleFixture(t)
	proxy.expectedScope = veloxcontract.ScopeVeloxEditorRead

	viewerResponse := httptest.NewRecorder()
	mux.ServeHTTP(viewerResponse, requestWithIdentity(http.MethodGet, "/api/v1/editor/projects/ve_project_1", 8, workspace.ID))
	if viewerResponse.Code != http.StatusOK {
		t.Fatalf("viewer read status = %d, want %d", viewerResponse.Code, http.StatusOK)
	}

	proxy.expectedScope = veloxcontract.ScopeVeloxEditorWrite
	proxy.called = false
	adminResponse := httptest.NewRecorder()
	mux.ServeHTTP(adminResponse, requestWithIdentity(http.MethodPatch, "/api/v1/editor/projects/ve_project_1/document", 9, workspace.ID))
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin write status = %d, want %d", adminResponse.Code, http.StatusOK)
	}
	if !proxy.called {
		t.Fatal("authorized admin did not reach project proxy")
	}
}

func TestEditorBFFDeniesProjectWorkspaceMismatchAndMissingProject(t *testing.T) {
	mux, proxy, workspace := editorModuleFixture(t)
	proxy.expectedScope = veloxcontract.ScopeVeloxEditorRead

	mismatchResponse := httptest.NewRecorder()
	mux.ServeHTTP(mismatchResponse, requestWithIdentity(http.MethodGet, "/api/v1/editor/projects/ve_project_1", 8, workspace.ID+1))
	if mismatchResponse.Code != http.StatusNotFound {
		t.Fatalf("workspace mismatch status = %d, want %d", mismatchResponse.Code, http.StatusNotFound)
	}
	if proxy.called {
		t.Fatal("workspace mismatch reached project proxy")
	}

	missingResponse := httptest.NewRecorder()
	mux.ServeHTTP(missingResponse, requestWithIdentity(http.MethodGet, "/api/v1/editor/projects/ve_missing", 8, workspace.ID))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing project status = %d, want %d", missingResponse.Code, http.StatusNotFound)
	}
	if proxy.called {
		t.Fatal("missing project reached project proxy")
	}
}
