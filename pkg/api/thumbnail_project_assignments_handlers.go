package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// maxAssignmentTargets caps how many videos an export can be linked to in
// one request. Assigning one export to many videos is legitimate (DoD:
// "molti video"), but unbounded batches would be a trivial amplification
// vector for the sequential INSERT loop.
const maxAssignmentTargets = 50

// createThumbnailAssignmentsRequest is the body of
// POST /api/v1/thumbnail-exports/{export_id}/assignments.
type createThumbnailAssignmentsRequest struct {
	Targets []thumbnailAssignmentTarget `json:"targets"`
}

// thumbnailAssignmentTarget describes one YouTube destination for an
// existing export. The platform is always youtube in this module; the
// account must be a workspace channel (enforced by composite FK in the
// repository).
type thumbnailAssignmentTarget struct {
	PlatformAccountID int64   `json:"platform_account_id"`
	YouTubeVideoID    string  `json:"youtube_video_id"`
	TargetLanguage    *string `json:"target_language,omitempty"`
}

type thumbnailAssignmentListResponse struct {
	Items []models.ThumbnailAssignment `json:"items"`
}

// mapThumbnailAssignmentError maps repository errors for the assignment
// endpoints. Duplicate links surface as 409 (recoverable — the client can
// inspect existing assignments); missing exports are 404; invalid payloads
// or foreign accounts are 422.
func mapThumbnailAssignmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrThumbnailAssignmentConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"code": "ASSIGNMENT_ALREADY_EXISTS", "error": err.Error()})
	case errors.Is(err, repository.ErrThumbnailExportNotFound):
		writeError(w, http.StatusNotFound, "thumbnail export not found")
	case errors.Is(err, repository.ErrThumbnailProjectInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleListThumbnailAssignments returns the workspace-scoped destination
// assignments of a project (GET /api/v1/thumbnail-projects/{id}/assignments).
// Empty array when the project has no assignments yet — the library uses
// this to classify a project as "Collegata" (≥1 row) vs unlinked.
func (r *Router) handleListThumbnailAssignments(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID); !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	items, err := r.thumbnailProjectStore.ListAssignments(req.Context(), workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list thumbnail assignments: "+err.Error())
		return
	}
	if items == nil {
		items = []models.ThumbnailAssignment{}
	}
	writeJSON(w, http.StatusOK, thumbnailAssignmentListResponse{Items: items})
}

// handleCreateThumbnailAssignments links an EXISTING ready export to one or
// more YouTube videos. The export must be ready and visible in the caller's
// workspace; each platform account must be linked to that workspace as a
// channel (repository composite FK). The original project is never
// modified — assignments are pure destinations on top of the export.
//
// Targets are applied sequentially and the request fails fast on the first
// error, so a duplicate target surfaces as a clear 409 instead of a partial
// silent success. On a multi-target failure earlier targets remain
// persisted — clients should re-fetch assignments to see what was created.
func (r *Router) handleCreateThumbnailAssignments(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID); !ok {
		return
	}
	exportID := strings.TrimSpace(chi.URLParam(req, "export_id"))
	if exportID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail export id is required")
		return
	}
	var body createThumbnailAssignmentsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail assignments body")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusBadRequest, "targets must contain at least one video")
		return
	}
	if len(body.Targets) > maxAssignmentTargets {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("targets must not exceed %d videos", maxAssignmentTargets))
		return
	}
	export, err := r.thumbnailProjectStore.FindExport(req.Context(), workspaceID, exportID)
	if err != nil {
		mapThumbnailAssignmentError(w, err)
		return
	}
	if export == nil {
		writeError(w, http.StatusNotFound, "thumbnail export not found")
		return
	}
	if export.Status != models.ThumbnailProjectExportStatusReady {
		writeError(w, http.StatusUnprocessableEntity, "only ready exports can be assigned to a video")
		return
	}
	created := make([]models.ThumbnailAssignment, 0, len(body.Targets))
	for i := range body.Targets {
		target := &body.Targets[i]
		assignment := &models.ThumbnailAssignment{
			WorkspaceID:       workspaceID,
			ProjectID:         export.ProjectID,
			ExportID:          exportID,
			PlatformAccountID: target.PlatformAccountID,
			Platform:          "youtube",
			YouTubeVideoID:    target.YouTubeVideoID,
			TargetLanguage:    target.TargetLanguage,
		}
		if err := r.thumbnailProjectStore.CreateAssignment(req.Context(), assignment); err != nil {
			mapThumbnailAssignmentError(w, err)
			return
		}
		created = append(created, *assignment)
	}
	writeJSON(w, http.StatusCreated, thumbnailAssignmentListResponse{Items: created})
}
