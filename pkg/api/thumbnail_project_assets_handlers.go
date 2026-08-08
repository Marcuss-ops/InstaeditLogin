package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// mapThumbnailAssetError maps repository errors for the asset endpoints.
// Domain conflicts (duplicate links) surface as 409 so the editor can
// treat "already linked" as a recoverable state; invalid payloads are
// 422; ownership/absence misses are 404 (cross-workspace rows are
// indistinguishable from missing rows, so nothing leaks).
func mapThumbnailAssetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrThumbnailDomainConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "ASSET_ALREADY_LINKED", "error": err.Error()})
	case errors.Is(err, repository.ErrThumbnailProjectNotFound):
		writeError(w, http.StatusNotFound, "thumbnail project or media asset not found")
	case errors.Is(err, repository.ErrThumbnailProjectAssetNotFound):
		writeError(w, http.StatusNotFound, "thumbnail project asset not found")
	case errors.Is(err, repository.ErrThumbnailProjectInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleAddThumbnailProjectAsset (POST /api/v1/thumbnail-projects/{id}/assets)
// links a ready media asset to the project. No YouTube prerequisite: the
// media row must simply be owned by (or shared with) the workspace; the
// repository enforces that at SQL level.
func (r *Router) handleAddThumbnailProjectAsset(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleEditor); !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	var body createThumbnailProjectAssetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail project asset body")
		return
	}
	asset := &models.ThumbnailProjectAsset{
		ProjectID: projectID,
		MediaID:   body.MediaID,
		Role:      body.Role,
		ObjectID:  body.ObjectID,
	}
	if err := r.thumbnailProjectStore.CreateAsset(req.Context(), workspaceID, asset); err != nil {
		mapThumbnailAssetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, asset)
}

// handleListThumbnailProjectAssets (GET /api/v1/thumbnail-projects/{id}/assets)
// returns the project's media links ordered by creation. Empty array when
// the project has no assets.
func (r *Router) handleListThumbnailProjectAssets(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleViewer); !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	items, err := r.thumbnailProjectStore.ListAssets(req.Context(), workspaceID, projectID)
	if err != nil {
		mapThumbnailAssetError(w, err)
		return
	}
	if items == nil {
		items = []models.ThumbnailProjectAsset{}
	}
	writeJSON(w, http.StatusOK, thumbnailProjectAssetListResponse{Items: items})
}

// handleDeleteThumbnailProjectAsset (DELETE .../assets/{media_id}) removes a
// single (project, media_id, role) link. The role query parameter is
// required because the primary key is the (project_id, media_id, role)
// triple.
func (r *Router) handleDeleteThumbnailProjectAsset(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleEditor); !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	mediaID := strings.TrimSpace(chi.URLParam(req, "media_id"))
	if mediaID == "" {
		writeError(w, http.StatusBadRequest, "media_id is required")
		return
	}
	role := strings.TrimSpace(req.URL.Query().Get("role"))
	if role == "" {
		writeError(w, http.StatusBadRequest, "role query parameter is required")
		return
	}
	if err := r.thumbnailProjectStore.DeleteAsset(req.Context(), workspaceID, projectID, mediaID, role); err != nil {
		mapThumbnailAssetError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
