package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// handleGetYouTubeEditorSessionByID is the HTTP entry point for
// GET /api/v1/youtube/editor-sessions/{id}.
//
// Companion to handleGetYouTubeEditorSessionByProject
// (pkg/api/youtube_editor_sessions_by_project.go) — same DTO
// shape (youTubeEditorSessionDetail), keyed by session_id rather
// than velox_project_id. The Thumbnail Maker (Velox dark-editor
// SPA) and any caller that already has an editor_session_id
// (typically returned by the auto-provisioner
// POST /internal/v1/thumbnail-sessions) reach the session via
// this endpoint without first POSTing /editor-sessions to
// discover a session_id.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when {id} is empty.
//   - 404 when the session is unknown OR the caller does not
//     have access to its workspace. Both branches return the
//     SAME 404 + message so a cross-tenant probe cannot
//     distinguish "no such session" from "session exists but not
//     yours" (defence-in-depth on top of the SQL WHERE id=$1
//     guard — the workspace ownership check rejects the request
//     before the SQL even runs, but a 404 here collapses the two
//     outcomes so an attacker probing IDs gets a uniform
//     response shape).
//   - 503 when the youtube video edit store is not configured.
//   - 500 on a real repository error.
//   - 200 + youTubeEditorSessionDetail otherwise.
//
// The session_id format is opaque to this handler — both
// auto-provisioned (ytedit_<uuid>) and manually-created (bare
// uuid) sessions resolve through the same SELECT id=$1 path.
func (r *Router) handleGetYouTubeEditorSessionByID(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	sessionID := chi.URLParam(req, "id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	edit, err := r.youtubeVideoEditStore.FindByID(req.Context(), sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		// Same 404-as-foreign pattern as the other endpoints: the
		// caller cannot tell "not found" from "not yours".
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	writeJSON(w, http.StatusOK, toYouTubeEditorSessionDetail(edit))
}