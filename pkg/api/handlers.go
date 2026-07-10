package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// WorkspaceStore abstracts the workspace persistence layer so the API
// handlers can be wired against the production WorkspaceRepository or
// against a mock in tests. Mirrors `repository.WorkspaceRepository`
// (excludes Db Tx methods that don't belong at the HTTP boundary).
type WorkspaceStore interface {
	Create(w *models.Workspace) error
	FindByID(id int64) (*models.Workspace, error)
	ListByOwner(ownerID int64) ([]models.Workspace, error)
}

// PostStore abstracts the post + post_targets persistence layer. Mirrors
// the `repository.PostRepository` methods used by handlers (excludes
// ListScheduled/ListPending which belong to the publishing worker).
type PostStore interface {
	Create(post *models.Post, targets []*models.PostTarget) error
	FindByID(id int64) (*models.Post, error)
	ListByWorkspace(workspaceID int64) ([]models.Post, error)
	ListByPost(postID int64) ([]models.PostTarget, error)
}

// RouterOption mutates the Router at construction time so callers can
// inject optional dependencies without breaking the NewRouter signature.
// Functional-options pattern: each new persistence layer (workspaces,
// posts, future rate limiters, etc.) gets its own WithXxxStore opt.
type RouterOption func(*Router)

// WithWorkspaceStore injects the workspace persistence layer. Without it,
// the workspace handlers return 501 Not Implemented — useful for partial
// rollouts where the workspace domain isn't wired yet.
func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) {
		r.workspaceStore = repo
	}
}

// WithPostStore injects the post + post_targets persistence layer.
// Without it, the post handlers return 501 Not Implemented.
func WithPostStore(repo PostStore) RouterOption {
	return func(r *Router) {
		r.postStore = repo
	}
}

// parsePathIDAsInt64 extracts a numeric path parameter (e.g. `{id}`) and
// writes a 400 Bad Request if it's missing or not a positive int64.
// Returns (id, true) on success; the caller should bail if ok is false —
// the response is already written.
func parsePathIDAsInt64(w http.ResponseWriter, req *http.Request, paramName string) (int64, bool) {
	s := req.PathValue(paramName)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+paramName+": "+s)
		return 0, false
	}
	return n, true
}

// requireUserID resolves the caller's user_id from JWT context (or the
// fallback in non-strict mode) and writes 401 if absent. Returns
// (userID, true) on success.
func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	return userID, true
}

// logAndError logs err with structured context and writes a 500 JSON
// response. Centralizes the slog+writeError pair so handlers stay short.
func logAndError(w http.ResponseWriter, msg string, err error, kv ...any) {
	slog.Error(msg, append([]any{"error", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, msg+": "+err.Error())
}
