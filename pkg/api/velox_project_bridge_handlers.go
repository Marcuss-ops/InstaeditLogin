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

func (r *Router) authorizeThumbnailProjectBridge(w http.ResponseWriter, req *http.Request, workspaceID int64, projectID string) (int64, *models.ThumbnailProject, bool) {
	userID, ok := r.thumbnailProjectWorkspace(w, req, workspaceID)
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
	if a == nil || b == nil || a.ProjectID != b.ProjectID || a.WorkspaceID != b.WorkspaceID || a.VeloxProjectID != b.VeloxProjectID || a.Platform != b.Platform {
		return false
	}
	if !equalInt64Ptr(a.PlatformAccountID, b.PlatformAccountID) ||
		!equalStringPtr(a.ChannelID, b.ChannelID) ||
		!equalStringPtr(a.VideoID, b.VideoID) ||
		!equalStringPtr(a.Language, b.Language) {
		return false
	}
	return true
}

func equalInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// handleCreateVeloxProjectBridge accepts a velox_project_id that has already
// been created/resolved by the trusted editor handoff. It is not an endpoint
// for inventing Velox projects; the opaque ID is only persisted after
// InstaEdit authorizes the application project and optional channel context.
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
	if _, _, ok := r.authorizeThumbnailProjectBridge(w, req, body.WorkspaceID, projectID); !ok {
		return
	}
	if r.editorService != nil {
		if existing, err := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), body.WorkspaceID, projectID); err != nil {
			mapVeloxProjectBridgeError(w, err)
			return
		} else if existing == nil {
			identityUserID := auth.IdentityFromContext(req.Context()).UserID()
			created, err := r.editorService.CreateProject(req.Context(), services.CreateEditorProjectRequest{
				UserID:               identityUserID,
				WorkspaceID:          body.WorkspaceID,
				ApplicationProjectID: projectID,
				ExternalProjectID:    strings.TrimSpace(body.VeloxProjectID),
				Platform:             body.Platform,
				PlatformAccountID:    body.PlatformAccountID,
				ChannelID:            body.ChannelID,
				VideoID:              body.VideoID,
				Language:             body.Language,
			})
			if err != nil {
				mapEditorServiceError(w, err)
				return
			}
			bridge, bridgeErr := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), body.WorkspaceID, projectID)
			if bridgeErr != nil || bridge == nil || bridge.VeloxProjectID != created.ExternalProjectID {
				if bridgeErr != nil {
					mapVeloxProjectBridgeError(w, bridgeErr)
				} else {
					writeError(w, http.StatusInternalServerError, "editor project bridge was not persisted")
				}
				return
			}
			writeJSON(w, http.StatusCreated, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *bridge, EditorURL: r.editorURLForProject(bridge.VeloxProjectID)})
			return
		}
	}
	bridge := &models.VeloxProjectBridge{
		ProjectID:         projectID,
		WorkspaceID:       body.WorkspaceID,
		VeloxProjectID:    strings.TrimSpace(body.VeloxProjectID),
		Platform:          body.Platform,
		PlatformAccountID: body.PlatformAccountID,
		ChannelID:         body.ChannelID,
		VideoID:           body.VideoID,
		Language:          body.Language,
		ContractVersion:   models.ProjectBridgeContractVersion,
	}
	if err := bridge.NormalizeAndValidate(); err != nil {
		mapVeloxProjectBridgeError(w, errors.Join(repository.ErrVeloxProjectBridgeInvalid, err))
		return
	}
	if existing, err := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), body.WorkspaceID, projectID); err != nil {
		mapVeloxProjectBridgeError(w, err)
		return
	} else if existing != nil {
		if bridgeContextMatches(existing, bridge) {
			writeJSON(w, http.StatusOK, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *existing, EditorURL: r.editorURLForProject(existing.VeloxProjectID)})
			return
		}
		mapVeloxProjectBridgeError(w, repository.ErrVeloxProjectBridgeConflict)
		return
	}
	if err := r.thumbnailProjectStore.CreateVeloxProjectBridge(req.Context(), bridge); err != nil {
		// A concurrent identical request may win the unique constraint
		// race. Re-read the authoritative row and return it as an
		// idempotent success when its context matches this request.
		if errors.Is(err, repository.ErrVeloxProjectBridgeConflict) {
			if existing, findErr := r.thumbnailProjectStore.FindVeloxProjectBridge(req.Context(), body.WorkspaceID, projectID); findErr == nil && bridgeContextMatches(existing, bridge) {
				writeJSON(w, http.StatusOK, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *existing, EditorURL: r.editorURLForProject(existing.VeloxProjectID)})
				return
			}
		}
		mapVeloxProjectBridgeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *bridge, EditorURL: r.editorURLForProject(bridge.VeloxProjectID)})
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
	if _, _, ok := r.authorizeThumbnailProjectBridge(w, req, workspaceID, projectID); !ok {
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
	writeJSON(w, http.StatusOK, veloxProjectBridgeResponse{ContractVersion: models.ProjectBridgeContractVersion, Bridge: *bridge, EditorURL: r.editorURLForProject(bridge.VeloxProjectID)})
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
	if _, _, ok := r.authorizeThumbnailProjectBridge(w, req, workspaceID, projectID); !ok {
		return
	}
	if err := r.thumbnailProjectStore.DeleteVeloxProjectBridge(req.Context(), workspaceID, projectID); err != nil {
		mapVeloxProjectBridgeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
