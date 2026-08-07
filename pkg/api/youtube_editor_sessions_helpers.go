package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
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
	CreateEditorSession(context.Context, CreateEditorSessionInput) (*models.YouTubeVideoEdit, error)
} = (*Router)(nil)

// userCanAccessWorkspace reports whether the user owns the workspace.
// For the editor session creation flow, workspace ownership is the
// required authorization gate; future iterations may also accept team
// members via the team store.
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

func (r *Router) userCanAccessWorkspace(userID int64, workspace *models.Workspace) bool {
	if workspace == nil {
		return false
	}
	return workspace.OwnerID == userID
}

// editorURLForProject returns the canonical editor URL for a newly
// created project. When an editor URL is configured explicitly it is
// used; otherwise the frontend URL is used as a fallback.
func (r *Router) editorURLForProject(projectID string) string {
	base := r.editorURL
	if base == "" {
		base = r.frontendURL
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		// Last-resort fallback for test environments.
		base = "https://editor.instaedit.org"
	}
	return fmt.Sprintf("%s/editor/%s", base, projectID)
}
