package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/editor"
)

// EditorBFFModuleDeps contains only the project-scoped editor bridge.
type EditorBFFModuleDeps struct {
	Client                editor.ProxyClient
	AuthMiddleware        func(http.Handler) http.Handler
	CSRFMiddleware        func(http.Handler) http.Handler
	YouTubeVideoEditStore YouTubeVideoEditStore
	ThumbnailProjectStore interface {
		FindVeloxProjectBridge(context.Context, int64, string) (*models.VeloxProjectBridge, error)
	}
	WorkspaceStore    WorkspaceStore
	TeamStore         TeamStore
	LaunchTokenIssuer editor.LaunchTokenIssuer
}

type editorBFFModule struct {
	deps EditorBFFModuleDeps
}

type thumbnailExternalProjectBridgeStore interface {
	FindVeloxProjectBridgeByExternalProjectID(context.Context, int64, string) (*models.VeloxProjectBridge, error)
}

func NewEditorBFFModule(deps EditorBFFModuleDeps) RouteModule {
	return &editorBFFModule{deps: deps}
}

func (m *editorBFFModule) Register(mux chi.Router) {
	if m.deps.Client == nil {
		return
	}
	editor.NewEditorBFFModule(editor.Deps{
		Client:            m.deps.Client,
		AuthMiddleware:    m.deps.AuthMiddleware,
		CSRFMiddleware:    m.deps.CSRFMiddleware,
		LaunchTokenIssuer: m.deps.LaunchTokenIssuer,
		AuthorizeProject: func(ctx context.Context, userID, workspaceID int64, projectID string, write bool) error {
			if m.deps.WorkspaceStore == nil {
				return errors.New("editor project authorization stores are not configured")
			}

			// YouTube editor sessions use the external project id directly.
			projectWorkspaceID := int64(0)
			if m.deps.YouTubeVideoEditStore != nil {
				edit, err := m.deps.YouTubeVideoEditStore.FindByVeloxProjectID(ctx, projectID)
				if err == nil && edit != nil {
					projectWorkspaceID = edit.WorkspaceID
				}
			}

			// Standalone thumbnail drafts use an application project id plus a
			// persisted bridge to the opaque Velox external project id. Resolve
			// those ids here as well so the launcher and editor proxy enforce the
			// same workspace boundary for both project types.
			if projectWorkspaceID == 0 {
				bridgeStore, ok := m.deps.ThumbnailProjectStore.(thumbnailExternalProjectBridgeStore)
				if !ok {
					return errors.New("editor project not found")
				}
				bridge, err := bridgeStore.FindVeloxProjectBridgeByExternalProjectID(ctx, workspaceID, projectID)
				if err == nil && bridge != nil && bridge.ExternalProjectID == projectID {
					projectWorkspaceID = bridge.WorkspaceID
				}
			}
			if projectWorkspaceID == 0 || projectWorkspaceID != workspaceID {
				return errors.New("editor project not found")
			}
			workspace, err := m.deps.WorkspaceStore.FindByID(projectWorkspaceID)
			if err != nil || workspace == nil {
				return errors.New("editor project not found")
			}
			minimumRole := workspaceRoleViewer
			if write {
				minimumRole = workspaceRoleEditor
			}
			if !workspaceRoleAllowed(userID, workspace, m.deps.TeamStore, minimumRole) {
				return errors.New("editor project not found")
			}
			return nil
		},
	}).Register(mux)
}

// WithEditorBFFClient wires the project-scoped editor bridge. When absent,
// /api/v1/editor/* is not mounted.
func WithEditorLaunchTokenIssuer(issuer editor.LaunchTokenIssuer) RouterOption {
	return func(r *Router) { r.editorLaunchTokenIssuer = issuer }
}

func WithEditorBFFClient(client editor.ProxyClient) RouterOption {
	return func(r *Router) { r.editorBFFClient = client }
}

func WithEditorBFFAuthMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.editorBFFAuthMiddleware = mw }
}

func WithEditorBFFCSRFMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.editorBFFCSRFMiddleware = mw }
}
