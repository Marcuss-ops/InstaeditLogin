package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// CoverLibraryStore is the API-facing catalog contract. It is separate from
// ThumbnailProjectStore so adding library reads and template versioning does
// not expand every thumbnail-project fake or editor dependency.
type CoverLibraryStore interface {
	ListCoverLibrary(context.Context, int64, string, int) ([]models.CoverLibraryItem, error)
	ListCoverTemplates(context.Context, int64, string, string) ([]models.CoverTemplate, error)
	ListCoverTemplateVersions(context.Context, int64, int64) ([]models.CoverTemplateVersion, error)
	CreateCoverTemplate(context.Context, *models.CoverTemplate, *models.CoverTemplateVersion) error
	CreateCoverTemplateVersion(context.Context, int64, *models.CoverTemplateVersion) error
	ArchiveCoverTemplate(context.Context, int64, int64) error
}

var _ CoverLibraryStore = (*repository.CoverLibraryRepository)(nil)

type coverTemplateCreateRequest struct {
	WorkspaceID     int64           `json:"workspace_id"`
	Name            string          `json:"name"`
	Description     string          `json:"description,omitempty"`
	Category        string          `json:"category,omitempty"`
	Language        string          `json:"language,omitempty"`
	EditorProjectID string          `json:"editor_project_id"`
	PreviewMediaID  *string         `json:"preview_media_id,omitempty"`
	Slots           json.RawMessage `json:"slots,omitempty"`
}

type coverTemplateVersionRequest struct {
	EditorProjectID string          `json:"editor_project_id"`
	PreviewMediaID  *string         `json:"preview_media_id,omitempty"`
	Slots           json.RawMessage `json:"slots,omitempty"`
}

type coverTemplateListResponse struct {
	Items []models.CoverTemplate `json:"items"`
}

type coverTemplateVersionListResponse struct {
	Items []models.CoverTemplateVersion `json:"items"`
}

type coverLibraryListResponse struct {
	Items []models.CoverLibraryItem `json:"items"`
}

func (r *Router) handleListCoverLibrary(w http.ResponseWriter, req *http.Request) {
	if r.coverLibraryStore == nil {
		writeError(w, http.StatusNotImplemented, "cover library is not configured")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleViewer); !ok {
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(req.URL.Query().Get("limit")))
	items, err := r.coverLibraryStore.ListCoverLibrary(req.Context(), workspaceID, req.URL.Query().Get("status"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list cover library: "+err.Error())
		return
	}
	if items == nil {
		items = []models.CoverLibraryItem{}
	}
	writeJSON(w, http.StatusOK, coverLibraryListResponse{Items: items})
}

func (r *Router) handleListCoverTemplates(w http.ResponseWriter, req *http.Request) {
	if r.coverLibraryStore == nil {
		writeError(w, http.StatusNotImplemented, "template library is not configured")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleViewer); !ok {
		return
	}
	items, err := r.coverLibraryStore.ListCoverTemplates(req.Context(), workspaceID, req.URL.Query().Get("language"), req.URL.Query().Get("status"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list template library: "+err.Error())
		return
	}
	if items == nil {
		items = []models.CoverTemplate{}
	}
	writeJSON(w, http.StatusOK, coverTemplateListResponse{Items: items})
}

func coverTemplateID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(req, "template_id")), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "template_id must be positive")
		return 0, false
	}
	return id, true
}

func (r *Router) handleListCoverTemplateVersions(w http.ResponseWriter, req *http.Request) {
	if r.coverLibraryStore == nil {
		writeError(w, http.StatusNotImplemented, "template library is not configured")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleViewer); !ok {
		return
	}
	templateID, ok := coverTemplateID(w, req)
	if !ok {
		return
	}
	items, err := r.coverLibraryStore.ListCoverTemplateVersions(req.Context(), workspaceID, templateID)
	if err != nil {
		if errors.Is(err, repository.ErrThumbnailProjectNotFound) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "list template versions: "+err.Error())
		return
	}
	if items == nil {
		items = []models.CoverTemplateVersion{}
	}
	writeJSON(w, http.StatusOK, coverTemplateVersionListResponse{Items: items})
}

func (r *Router) handleCreateCoverTemplate(w http.ResponseWriter, req *http.Request) {
	if r.coverLibraryStore == nil {
		writeError(w, http.StatusNotImplemented, "template library is not configured")
		return
	}
	var body coverTemplateCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid template body")
		return
	}
	if body.WorkspaceID <= 0 || strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.EditorProjectID) == "" {
		writeError(w, http.StatusBadRequest, "workspace_id, name and editor_project_id are required")
		return
	}
	userID, ok := r.thumbnailProjectWorkspace(w, req, body.WorkspaceID, workspaceRoleEditor)
	if !ok {
		return
	}
	if body.Slots == nil {
		body.Slots = json.RawMessage(`{}`)
	}
	template := &models.CoverTemplate{WorkspaceID: body.WorkspaceID, CreatedBy: userID, Name: strings.TrimSpace(body.Name), Description: body.Description, Category: body.Category, Language: body.Language}
	version := &models.CoverTemplateVersion{EditorProjectID: strings.TrimSpace(body.EditorProjectID), PreviewMediaID: body.PreviewMediaID, Slots: body.Slots, CreatedBy: userID}
	if err := r.coverLibraryStore.CreateCoverTemplate(req.Context(), template, version); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "create template: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": template, "version": version})
}

func (r *Router) handleCreateCoverTemplateVersion(w http.ResponseWriter, req *http.Request) {
	if r.coverLibraryStore == nil {
		writeError(w, http.StatusNotImplemented, "template library is not configured")
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
	templateID, ok := coverTemplateID(w, req)
	if !ok {
		return
	}
	var body coverTemplateVersionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid template version body")
		return
	}
	if strings.TrimSpace(body.EditorProjectID) == "" {
		writeError(w, http.StatusBadRequest, "editor_project_id is required")
		return
	}
	if body.Slots == nil {
		body.Slots = json.RawMessage(`{}`)
	}
	version := &models.CoverTemplateVersion{TemplateID: templateID, EditorProjectID: strings.TrimSpace(body.EditorProjectID), PreviewMediaID: body.PreviewMediaID, Slots: body.Slots, CreatedBy: userID}
	if err := r.coverLibraryStore.CreateCoverTemplateVersion(req.Context(), workspaceID, version); err != nil {
		if errors.Is(err, repository.ErrThumbnailProjectNotFound) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "create template version: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, version)
}

func (r *Router) handleArchiveCoverTemplate(w http.ResponseWriter, req *http.Request) {
	if r.coverLibraryStore == nil {
		writeError(w, http.StatusNotImplemented, "template library is not configured")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID, workspaceRoleEditor); !ok {
		return
	}
	templateID, ok := coverTemplateID(w, req)
	if !ok {
		return
	}
	if err := r.coverLibraryStore.ArchiveCoverTemplate(req.Context(), workspaceID, templateID); err != nil {
		if errors.Is(err, repository.ErrThumbnailProjectNotFound) {
			writeError(w, http.StatusNotFound, "template not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "archive template: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
