package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// Sentinel errors for the shared thumbnail-attach resolver. They keep
// the HTTP mapping identical for both the direct POST
// /{id}/thumbnail and the PATCH /by-project/{project_id} paths.
var (
	errAttachWorkspaceNotAccessible = errors.New("workspace not accessible")
	errAttachAssetNotFound          = errors.New("media asset not found")
	errAttachAssetNotOwned          = errors.New("media asset not owned by caller")
	errAttachAssetNotReady          = errors.New("media asset is not ready")
	errAttachSessionNotEditable     = errors.New("editor session is not in an editable state")
)

// attachThumbnailToSession is the shared resolver used by both the
// PATCH /by-project/{velox_project_id} and the direct POST
// /{id}/thumbnail endpoints. It validates workspace access, asset
// existence/ownership/readiness, and then atomically links the asset
// via AttachThumbnail CAS.
func (r *Router) attachThumbnailToSession(ctx context.Context, identity auth.Identity, edit *models.YouTubeVideoEdit, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		return nil, fmt.Errorf("find workspace: %w", err)
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		return nil, errAttachWorkspaceNotAccessible
	}

	asset, err := r.mediaStore.FindByID(thumbnailMediaID)
	if err != nil {
		return nil, fmt.Errorf("find media asset: %w", err)
	}
	if asset == nil {
		return nil, errAttachAssetNotFound
	}
	if asset.UserID != identity.UserID() {
		return nil, errAttachAssetNotOwned
	}
	if asset.Status != models.MediaAssetStatusReady {
		return nil, errAttachAssetNotReady
	}

	updated, err := r.youtubeVideoEditStore.AttachThumbnail(ctx, edit.ID, thumbnailMediaID)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditNotFound) {
			return nil, errAttachSessionNotEditable
		}
		return nil, fmt.Errorf("attach thumbnail: %w", err)
	}
	return updated, nil
}

// writeAttachThumbnailError maps the shared resolver's sentinel
// errors to HTTP status codes. Both attach-thumbnail entry points
// use this so callers see a uniform contract regardless of how the
// session was resolved.
func (r *Router) writeAttachThumbnailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errAttachWorkspaceNotAccessible):
		writeError(w, http.StatusForbidden, "workspace not accessible")
	case errors.Is(err, errAttachAssetNotFound):
		writeError(w, http.StatusNotFound, "media asset not found")
	case errors.Is(err, errAttachAssetNotOwned):
		writeError(w, http.StatusForbidden, "media asset not owned by caller")
	case errors.Is(err, errAttachAssetNotReady):
		writeError(w, http.StatusConflict, "media asset is not ready")
	case errors.Is(err, errAttachSessionNotEditable):
		writeError(w, http.StatusConflict, "editor session is not in an editable state")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// attachThumbnailRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/{id}/thumbnail. This is the
// "direct" handoff endpoint (Blocco #5 P0 #4): callers (typically the
// dark editor SPA, post-upload) supply a verified media_assets.id
// instead of going through Velox's PATCH-by-project flow. The handler
// validates asset existence + readiness + workspace accessibility, then
// atomically links the asset to the session via a single UPDATE with a
// CAS predicate (status IN ('editing','failed')).
type attachThumbnailRequest struct {
	ThumbnailMediaID string `json:"thumbnail_media_id"`
}

// attachThumbnailResponse is returned on success.
type attachThumbnailResponse struct {
	SessionID        string `json:"session_id"`
	ThumbnailMediaID string `json:"thumbnail_media_id"`
	ThumbnailStatus  string `json:"thumbnail_status"`
}

// handleAttachThumbnailToEditorSession is the direct handoff entry
// point. It accepts a verified thumbnail_media_id, resolves the
// session by id, and delegates the rest to attachThumbnailToSession.
//
// Error branches:
//   - asset not found                                  → 404
//   - asset exists but Status != ready                 → 409
//   - workspace not accessible by the caller           → 403
//   - session not found / CAS-loss (status flipped)    → 404 / 409
//   - missing thumbnail_media_id payload               → 400
//
// The AttachThumbnail call is the atomic CAS that simultaneously
// stamps thumbnail_media_id AND guards against concurrent publishes
// (status must be 'editing' or 'failed' — a session in 'publishing'
// or 'published' state will not match, the 0-rows return maps to 409).
func (r *Router) handleAttachThumbnailToEditorSession(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	sessionID := chi.URLParam(req, "id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var payload attachThumbnailRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.ThumbnailMediaID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail_media_id is required")
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}
	if r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
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

	updated, err := r.attachThumbnailToSession(req.Context(), identity, edit, payload.ThumbnailMediaID)
	if err != nil {
		r.writeAttachThumbnailError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, attachThumbnailResponse{
		SessionID:        updated.ID,
		ThumbnailMediaID: *updated.ThumbnailMediaID,
		ThumbnailStatus:  updated.Status,
	})
}
