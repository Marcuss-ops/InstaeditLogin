package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/editor"
)

// EditorBFFModuleDeps contains only the project-scoped editor bridge.
type EditorBFFModuleDeps struct {
	Client                editor.ProxyClient
	AuthMiddleware        func(http.Handler) http.Handler
	CSRFMiddleware        func(http.Handler) http.Handler
	YouTubeVideoEditStore YouTubeVideoEditStore
	WorkspaceStore        WorkspaceStore
	TeamStore             TeamStore
}

type editorBFFModule struct {
	deps EditorBFFModuleDeps
}

func NewEditorBFFModule(deps EditorBFFModuleDeps) RouteModule {
	return &editorBFFModule{deps: deps}
}

func (m *editorBFFModule) Register(mux chi.Router) {
	if m.deps.Client == nil {
		return
	}
	editor.NewEditorBFFModule(editor.Deps{
		Client:         m.deps.Client,
		AuthMiddleware: m.deps.AuthMiddleware,
		CSRFMiddleware: m.deps.CSRFMiddleware,
		AuthorizeProject: func(ctx context.Context, userID, workspaceID int64, projectID string, write bool) error {
			if m.deps.YouTubeVideoEditStore == nil || m.deps.WorkspaceStore == nil {
				return errors.New("editor project authorization stores are not configured")
			}
			edit, err := m.deps.YouTubeVideoEditStore.FindByVeloxProjectID(ctx, projectID)
			if err != nil || edit == nil || edit.WorkspaceID != workspaceID {
				return errors.New("editor project not found")
			}
			workspace, err := m.deps.WorkspaceStore.FindByID(edit.WorkspaceID)
			if err != nil || workspace == nil {
				return errors.New("editor project not found")
			}
			if workspace.OwnerID == userID {
				return nil
			}
			if m.deps.TeamStore == nil {
				return errors.New("editor project not found")
			}
			role, err := m.deps.TeamStore.GetRole(workspaceID, userID)
			if err != nil {
				return errors.New("editor project not found")
			}
			if write && role != repository.RoleEditor && role != repository.RoleAdmin {
				return errors.New("editor project not found")
			}
			if !write && role != repository.RoleViewer && role != repository.RoleEditor && role != repository.RoleAdmin {
				return errors.New("editor project not found")
			}
			return nil
		},
	}).Register(mux)
}

// WithEditorBFFClient wires the project-scoped editor bridge. When absent,
// /api/v1/editor/* is not mounted.
func WithEditorBFFClient(client editor.ProxyClient) RouterOption {
	return func(r *Router) { r.editorBFFClient = client }
}

func WithEditorBFFAuthMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.editorBFFAuthMiddleware = mw }
}

func WithEditorBFFCSRFMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.editorBFFCSRFMiddleware = mw }
}
