package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func mapEditorServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrEditorProjectNotFound):
		writeError(w, http.StatusNotFound, "project bridge not found")
	case errors.Is(err, services.ErrEditorProjectInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, services.ErrEditorProjectConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, services.ErrEditorServiceNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "Editor unavailable / misconfigured")
	default:
		// Unknown errors are local (persistence, validation, wiring)
		// failures, not provider errors. 500 lets operators distinguish
		// them from an editor-provider outage.
		writeError(w, http.StatusInternalServerError, "editor service failed")
	}
}

func mapVeloxProjectBridgeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrVeloxProjectBridgeConflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":  "VELOX_PROJECT_BRIDGE_ALREADY_EXISTS",
			"error": err.Error(),
		})
	case errors.Is(err, repository.ErrVeloxProjectBridgeNotFound), errors.Is(err, repository.ErrThumbnailProjectNotFound):
		writeError(w, http.StatusNotFound, "project bridge not found")
	case errors.Is(err, repository.ErrVeloxProjectBridgeInvalid):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (r *Router) authorizeThumbnailProjectBridge(w http.ResponseWriter, req *http.Request, workspaceID int64, projectID string, requiredRole string) (int64, *models.ThumbnailProject, bool) {
	userID, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, requiredRole)
	if !ok {
		return 0, nil, false
	}
	project, err := r.thumbnailProjectStore.FindByID(req.Context(), workspaceID, strings.TrimSpace(projectID))
	if err != nil {
		mapVeloxProjectBridgeError(w, err)
		return 0, nil, false
	}
	if project == nil || project.Status == models.ThumbnailProjectStatusDeleted {
		writeError(w, http.StatusNotFound, "project bridge not found")
		return 0, nil, false
	}
	return userID, project, true
}

func bridgeContextMatches(a, b *models.VeloxProjectBridge) bool {
	return a != nil && b != nil &&
		a.ProjectID == b.ProjectID &&
		a.WorkspaceID == b.WorkspaceID &&
		a.ExternalProjectID == b.ExternalProjectID
}

// handleCreateVeloxProjectBridge accepts a velox_project_id that has already
// been created/resolved by the trusted editor handoff. It is not an endpoint
// for inventing Velox projects; the opaque ID is only persisted after
// InstaEdit authorizes the application project in its workspace.
func (r *Router) handleCreateVeloxProjectBridge(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	projectID := strings.TrimSpace(chi.URLParam(req, "id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail project id is required")
		return
	}
	var body createVeloxProjectBridgeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid Velox project bridge body")
		return
	}
	if body.ContractVersion != models.ProjectBridgeContractVersion {
		writeError(w, http.StatusUnprocessableEntity, "unsupported project bridge contract_version")
		return
	}
	if body.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id is required and must be positive")
		return
	}
	if _, _, ok := r.authorizeThumbnailProjectBridge(w, req, body.WorkspaceID, projectID, workspaceRoleEditor); !ok {
		return
	}
	if r.editorService == nil {
		writeError(w, http.StatusServiceUnavailable, "Editor unavailable / misconfigured")
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	// A browser-supplied external handle is never authoritative for a new
	// mapping: accepting it would let a caller bind an InstaEdit project to
	// an arbitrary provider project. It is used only as a conflict hint when
	// an existing InstaEdit-owned bridge is being replayed. New projects get
	// their provider handle from the adapter (deterministically, when the
	// provider supports lazy project materialization).
	requestedExternalID := strings.TrimSpace(body.ExternalProjectID)
	existing, err := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), body.WorkspaceID, projectID)
	if err != nil {
		mapVeloxProjectBridgeError(w, err)
		return
	}
	if existing == nil && requestedExternalID != "" {
		writeError(w, http.StatusUnprocessableEntity, "external_project_id is server-issued and must be omitted when creating a bridge")
		return
	}
	created, err := r.editorService.CreateProject(req.Context(), services.CreateEditorProjectRequest{
		UserID:               identity.UserID(),
		WorkspaceID:          body.WorkspaceID,
		ApplicationProjectID: projectID,
		ExternalProjectID:    requestedExternalID,
	})
	if err != nil {
		mapEditorServiceError(w, err)
		return
	}
	bridge, bridgeErr := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), body.WorkspaceID, projectID)
	expected := &models.VeloxProjectBridge{
		ProjectID:         projectID,
		WorkspaceID:       body.WorkspaceID,
		ExternalProjectID: created.ExternalProjectID,
	}
	if bridgeErr != nil || bridge == nil || !bridgeContextMatches(bridge, expected) {
		if bridgeErr != nil {
			mapVeloxProjectBridgeError(w, bridgeErr)
		} else {
			writeError(w, http.StatusInternalServerError, "editor project bridge was not persisted")
		}
		return
	}
	status := http.StatusOK
	if created.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *bridge, EditorURL: r.editorURLForProject(bridge.ExternalProjectID)})
}

func (r *Router) handleGetVeloxProjectBridge(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	if _, _, ok := r.authorizeThumbnailProjectBridge(w, req, workspaceID, projectID, workspaceRoleViewer); !ok {
		return
	}
	bridge, err := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), workspaceID, projectID)
	if err != nil {
		mapVeloxProjectBridgeError(w, err)
		return
	}
	if bridge == nil {
		writeError(w, http.StatusNotFound, "project bridge not found")
		return
	}
	writeJSON(w, http.StatusOK, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *bridge, EditorURL: r.editorURLForProject(bridge.ExternalProjectID)})
}

func (r *Router) handleDeleteVeloxProjectBridge(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	if _, _, ok := r.authorizeThumbnailProjectBridge(w, req, workspaceID, projectID, workspaceRoleEditor); !ok {
		return
	}
	if err := r.thumbnailProjectStore.DeleteVeloxProjectBridge(req.Context(), workspaceID, projectID); err != nil {
		mapVeloxProjectBridgeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
