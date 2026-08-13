package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// writeEditorSessionError maps the helper's typed sentinel errors to
// HTTP status codes via errors.Is. Extracted so the handler body
// stays readable and the sentinel → status mapping is testable in
// isolation in a future PR.
func (r *Router) writeEditorSessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrEditorSessionWorkspaceNotFound):
		writeError(w, http.StatusNotFound, "workspace not found")
	case errors.Is(err, ErrEditorSessionAccountNotFound):
		writeError(w, http.StatusNotFound, "account not found")
	case errors.Is(err, ErrEditorSessionChannelUnlinked):
		writeError(w, http.StatusNotFound, "account not linked to workspace")
	case errors.Is(err, ErrEditorSessionNoValidToken):
		writeError(w, http.StatusUnauthorized, "no valid token found for this account")
	case errors.Is(err, ErrEditorSessionYTServiceUnconfigured),
		errors.Is(err, ErrEditorSessionEditStoreUnconfigured):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, ErrEditorSessionVideoWrongChannel):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrEditorSessionVideoNotReady),
		errors.Is(err, ErrEditorSessionVideoAlreadyPub):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, services.ErrEditorProjectInvalid):
		// The session row was persisted but the editor project mapping
		// could not be created from the session's opaque handle. The
		// operator can re-trigger the click: the REUSE path re-runs the
		// same idempotent resolution.
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// Compile-time assertion that *api.Router satisfies the narrow
// interface the reconciler worker depends on (internal/worker/youtube_processing_reconciler.go
// declares this interface; pkg/api must see this assertion signature-
// compatible).
//
// The reconciler passes the *Router pointer as the EditorSessionCreator
// implementation; duck typing via the interface satisfies the contract.
// Without this assertion, a future signature drift on Router.CreateEditorSession
// would surface at runtime in production rather than at go vet time.
var _ interface {
	CreateEditorSession(context.Context, CreateEditorSessionInput) (*models.YouTubeVideoEdit, *models.YouTubeVideoDetails, error)
} = (*Router)(nil)

const (
	workspaceRoleViewer = repository.RoleViewer
	workspaceRoleEditor = repository.RoleEditor
)

// workspaceRoleAllowed is the single InstaEdit authorization primitive for
// workspace-scoped application data. Workspace ownership is authoritative
// and grants the highest role; non-owners must be present in
// workspace_members. Velox/editor services are deliberately not consulted.
func workspaceRoleAllowed(userID int64, workspace *models.Workspace, teamStore TeamStore, minimumRole string) bool {
	if workspace == nil || userID <= 0 {
		return false
	}
	if workspace.OwnerID == userID {
		return true
	}
	if teamStore == nil {
		return false
	}
	role, err := teamStore.GetRole(workspace.ID, userID)
	if err != nil {
		return false
	}
	return roleRank[role] >= roleRank[minimumRole] && roleRank[minimumRole] > 0
}

func (r *Router) userOwnsWorkspace(userID int64, workspace *models.Workspace) bool {
	return workspace != nil && workspace.OwnerID == userID
}

// userCanAccessWorkspace is the read gate for workspace-scoped InstaEdit
// data. Write callers must use userCanEditWorkspace explicitly.
func (r *Router) userCanAccessWorkspace(userID int64, workspace *models.Workspace) bool {
	return workspaceRoleAllowed(userID, workspace, r.teamStore, workspaceRoleViewer)
}

func (r *Router) userCanEditWorkspace(userID int64, workspace *models.Workspace) bool {
	return workspaceRoleAllowed(userID, workspace, r.teamStore, workspaceRoleEditor)
}

// safeEditorAssetURL accepts only browser-fetchable absolute HTTP(S) URLs.
// Local browser URLs such as file://, blob://, and data:// must never be
// persisted as editor thumbnails because the server-side publisher cannot
// fetch them.
func safeEditorAssetURL(raw string) string {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return ""
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return candidate
}

// editorURLForProject returns the server-issued URL for the separately
// deployed InstaEditor. It intentionally returns an empty string when
// INSTAEDITOR_URL is missing: FRONTEND_URL, EDITOR_URL, and hardcoded
// hostnames must never become silent editor destinations.
func (r *Router) editorURLForProject(projectID string) string {
	base := strings.TrimRight(strings.TrimSpace(r.editorURL), "/")
	projectID = strings.TrimSpace(projectID)
	if base == "" || projectID == "" {
		return ""
	}
	return fmt.Sprintf("%s/editor/%s", base, url.PathEscape(projectID))
}
