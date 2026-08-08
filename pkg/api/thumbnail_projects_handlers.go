package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func (r *Router) thumbnailProjectWorkspace(w http.ResponseWriter, req *http.Request, workspaceID int64, requiredRole string) (int64, bool) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusServiceUnavailable, "workspace store not configured")
		return 0, false
	}
	workspace, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return 0, false
	}
	if !workspaceRoleAllowed(identity.UserID(), workspace, r.teamStore, requiredRole) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return 0, false
	}
	return identity.UserID(), true
}

func parseThumbnailWorkspaceQuery(w http.ResponseWriter, req *http.Request) (int64, bool) {
	workspaceID, err := strconv.ParseInt(strings.TrimSpace(req.URL.Query().Get("workspace_id")), 10, 64)
	if err != nil || workspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id query parameter is required and must be positive")
		return 0, false
	}
	return workspaceID, true
}

func parseThumbnailProjectID(w http.ResponseWriter, req *http.Request) (string, bool) {
	id := strings.TrimSpace(chi.URLParam(req, "id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "thumbnail project id is required")
		return "", false
	}
	return id, true
}

func parseThumbnailRevisionID(w http.ResponseWriter, req *http.Request) (string, bool) {
	id := strings.TrimSpace(chi.URLParam(req, "revision_id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "thumbnail project revision id is required")
		return "", false
	}
	return id, true
}

// parseVersionPrecondition accepts both the JSON base_version contract and
// If-Match values emitted by browser clients: \"version-7\", \"7\", or 7.
func parseVersionPrecondition(w http.ResponseWriter, req *http.Request, bodyVersion int64) (int64, bool) {
	header := strings.TrimSpace(req.Header.Get("If-Match"))
	if header == "" {
		if bodyVersion <= 0 {
			writeError(w, http.StatusBadRequest, "base_version is required and must be positive")
			return 0, false
		}
		return bodyVersion, true
	}
	header = strings.Trim(header, "\\\"")
	header = strings.TrimPrefix(header, "version-")
	version, err := strconv.ParseInt(header, 10, 64)
	if err != nil || version <= 0 {
		writeError(w, http.StatusBadRequest, "If-Match must contain a positive project version")
		return 0, false
	}
	if bodyVersion > 0 && bodyVersion != version {
		writeError(w, http.StatusBadRequest, "If-Match and base_version must match")
		return 0, false
	}
	return version, true
}

// conflictCurrentVersionRe extracts the live project version from
// repository conflict errors of the form
// "thumbnail project version conflict: expected=N current=M".
var conflictCurrentVersionRe = regexp.MustCompile(`current=(\d+)`)

// thumbnailConflictBody renders the 409 PROJECT_VERSION_CONFLICT body.
// The live version is surfaced as structured `current_version` so the
// editor can offer "reload latest version"; lifecycle CAS paths whose
// errors do not report the current version omit the field rather than
// sending a wrong value.
func thumbnailConflictBody(err error) map[string]any {
	body := map[string]any{"code": "PROJECT_VERSION_CONFLICT", "error": err.Error()}
	if m := conflictCurrentVersionRe.FindStringSubmatch(err.Error()); len(m) == 2 {
		if v, parseErr := strconv.ParseInt(m[1], 10, 64); parseErr == nil {
			body["current_version"] = v
		}
	}
	return body
}

func mapThumbnailRevisionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrThumbnailProjectConflict):
		writeJSON(w, http.StatusConflict, thumbnailConflictBody(err))
	case errors.Is(err, repository.ErrThumbnailProjectNotFound), errors.Is(err, repository.ErrThumbnailProjectRevisionNotFound):
		writeError(w, http.StatusNotFound, "thumbnail project revision not found")
	case errors.Is(err, repository.ErrThumbnailProjectInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (r *Router) handleCreateThumbnailProject(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	var body createThumbnailProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail project body")
		return
	}
	if body.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id is required and must be positive")
		return
	}
	userID, ok := r.thumbnailProjectWorkspace(w, req, body.WorkspaceID, workspaceRoleEditor)
	if !ok {
		return
	}
	project := &models.ThumbnailProject{
		WorkspaceID:  body.WorkspaceID,
		CreatedBy:    userID,
		Name:         body.Name,
		Description:  body.Description,
		CanvasWidth:  body.CanvasWidth,
		CanvasHeight: body.CanvasHeight,
		Status:       models.ThumbnailProjectStatusDraft,
	}
	if err := r.thumbnailProjectStore.Create(req.Context(), project); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (r *Router) handleListThumbnailProjects(w http.ResponseWriter, req *http.Request) {
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
	items, err := r.thumbnailProjectStore.ListByWorkspace(req.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list thumbnail projects: "+err.Error())
		return
	}
	if items == nil {
		items = []models.ThumbnailProject{}
	}
	writeJSON(w, http.StatusOK, thumbnailProjectListResponse{Items: items})
}

func (r *Router) handleGetThumbnailProject(w http.ResponseWriter, req *http.Request) {
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
	id, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	project, err := r.thumbnailProjectStore.FindByID(req.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find thumbnail project: "+err.Error())
		return
	}
	if project == nil || project.Status == models.ThumbnailProjectStatusDeleted {
		writeError(w, http.StatusNotFound, "thumbnail project not found")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (r *Router) handleUpdateThumbnailProject(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	userID, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleEditor)
	if !ok {
		return
	}
	id, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	var body updateThumbnailProjectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail project body")
		return
	}
	if body.Version <= 0 {
		writeError(w, http.StatusBadRequest, "version is required and must be positive")
		return
	}
	project, err := r.thumbnailProjectStore.FindByID(req.Context(), workspaceID, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find thumbnail project: "+err.Error())
		return
	}
	if project == nil || project.Status == models.ThumbnailProjectStatusDeleted {
		writeError(w, http.StatusNotFound, "thumbnail project not found")
		return
	}
	if body.Name != nil {
		project.Name = *body.Name
	}
	if body.Description != nil {
		project.Description = *body.Description
	}
	if body.CanvasWidth != nil {
		project.CanvasWidth = *body.CanvasWidth
	}
	if body.CanvasHeight != nil {
		project.CanvasHeight = *body.CanvasHeight
	}
	if body.Status != nil {
		project.Status = models.ThumbnailProjectStatus(strings.TrimSpace(*body.Status))
		if project.Status != models.ThumbnailProjectStatusDraft && project.Status != models.ThumbnailProjectStatusReady && project.Status != models.ThumbnailProjectStatusArchived {
			writeError(w, http.StatusBadRequest, "status must be draft, ready, or archived")
			return
		}
	}
	project.WorkspaceID = workspaceID
	project.CreatedBy = userID
	if err := r.thumbnailProjectStore.UpdateCAS(req.Context(), project, body.Version); err != nil {
		switch {
		case errors.Is(err, repository.ErrThumbnailProjectConflict):
			writeJSON(w, http.StatusConflict, thumbnailConflictBody(err))
		default:
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (r *Router) handleSaveThumbnailProjectSnapshot(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	userID, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleEditor)
	if !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	var body thumbnailProjectSnapshotRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail snapshot body")
		return
	}
	baseVersion, ok := parseVersionPrecondition(w, req, body.BaseVersion)
	if !ok {
		return
	}
	result, err := r.thumbnailProjectStore.SaveSnapshot(req.Context(), workspaceID, projectID, models.ThumbnailProjectSnapshot{
		SchemaVersion: body.SchemaVersion, SnapshotJSON: body.Snapshot,
		RendererVersion: body.RendererVersion, BaseVersion: baseVersion,
	}, userID)
	if err != nil {
		mapThumbnailRevisionError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"version-%d\"", result.Version))
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleListThumbnailProjectRevisions(w http.ResponseWriter, req *http.Request) {
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
	items, err := r.thumbnailProjectStore.ListRevisions(req.Context(), workspaceID, projectID)
	if err != nil {
		mapThumbnailRevisionError(w, err)
		return
	}
	if items == nil {
		items = []models.ThumbnailProjectRevision{}
	}
	writeJSON(w, http.StatusOK, thumbnailProjectRevisionListResponse{Items: items})
}

func (r *Router) handleGetThumbnailProjectRevision(w http.ResponseWriter, req *http.Request) {
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
	revisionID, ok := parseThumbnailRevisionID(w, req)
	if !ok {
		return
	}
	revision, err := r.thumbnailProjectStore.FindRevision(req.Context(), workspaceID, projectID, revisionID)
	if err != nil {
		mapThumbnailRevisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, thumbnailProjectRevisionDetailResponse{Revision: *revision})
}

func (r *Router) handleRestoreThumbnailProjectRevision(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	userID, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleEditor)
	if !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	revisionID, ok := parseThumbnailRevisionID(w, req)
	if !ok {
		return
	}
	var body thumbnailProjectRestoreRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail restore body")
		return
	}
	baseVersion, ok := parseVersionPrecondition(w, req, body.BaseVersion)
	if !ok {
		return
	}
	result, err := r.thumbnailProjectStore.RestoreRevision(req.Context(), workspaceID, projectID, revisionID, baseVersion, userID, body.RendererVersion)
	if err != nil {
		mapThumbnailRevisionError(w, err)
		return
	}
	w.Header().Set("ETag", fmt.Sprintf("\"version-%d\"", result.Version))
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleArchiveThumbnailProject(w http.ResponseWriter, req *http.Request) {
	r.changeThumbnailProjectStatus(w, req, models.ThumbnailProjectStatusArchived)
}

func (r *Router) handleDeleteThumbnailProject(w http.ResponseWriter, req *http.Request) {
	r.changeThumbnailProjectStatus(w, req, models.ThumbnailProjectStatusDeleted)
}

func (r *Router) changeThumbnailProjectStatus(w http.ResponseWriter, req *http.Request, status models.ThumbnailProjectStatus) {
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
	id, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(strings.TrimSpace(req.URL.Query().Get("version")), 10, 64)
	if err != nil || version <= 0 {
		writeError(w, http.StatusBadRequest, "version query parameter is required and must be positive")
		return
	}
	if err := r.thumbnailProjectStore.UpdateStatusCAS(req.Context(), workspaceID, id, status, version); err != nil {
		switch {
		case errors.Is(err, repository.ErrThumbnailProjectConflict):
			writeJSON(w, http.StatusConflict, thumbnailConflictBody(err))
		case errors.Is(err, repository.ErrThumbnailProjectNotFound):
			writeError(w, http.StatusNotFound, "thumbnail project not found")
		case errors.Is(err, repository.ErrThumbnailProjectInvalid):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "update thumbnail project status: "+err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
