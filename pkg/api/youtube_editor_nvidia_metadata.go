package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// generateMetadataRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata.
//
// Prompt is optional — when empty, the service uses no additional
// context and NVIDIA generates metadata from its built-in knowledge.
// When supplied, it describes the video content, target audience,
// and desired tone.
type generateMetadataRequest struct {
	Prompt string `json:"prompt,omitempty"`
}

// generateMetadataResponse is returned on a successful generation.
// The shape mirrors services.NVIDIAMetadataResponse with explicit json
// tags so the SPA receives exactly the fields it expects.
type generateMetadataResponse struct {
	Title                string                               `json:"title"`
	Description          string                               `json:"description"`
	Tags                 []string                             `json:"tags"`
	DefaultLanguage      string                               `json:"default_language"`
	DefaultAudioLanguage string                               `json:"default_audio_language"`
	Translations         map[string]models.YouTubeTranslation `json:"translations"`
}

// handleGenerateNVIDIAMetadata is the HTTP entry point for
// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata.
//
// This endpoint is a READ-ONLY metadata generation step that sits
// BEFORE the publish flow. It calls NVIDIA AI (via MetadataGenerator),
// validates every field against YouTube's bounds, and returns the
// structured metadata to the Dark Editor. The operator reviews the
// generated values, optionally edits them, and only THEN submits
// them through the /publish endpoint.
//
// The NVIDIA API key is NEVER included in the response. The service
// is optional — when NVIDIA_API_KEY is empty, the endpoint returns
// 503 and the manual metadata flow remains fully functional.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when JSON is malformed or {velox_project_id} is empty.
//   - 404 when the editor session is unknown or not accessible.
//   - 503 when the NVIDIA service is not configured (NVIDIA_API_KEY
//     empty — manual metadata entry still works).
//   - 502 when the NVIDIA API call fails (network, auth, bad response).
//   - 200 + generateMetadataResponse on success.
func (r *Router) handleGenerateNVIDIAMetadata(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	veloxProjectID := chi.URLParam(req, "velox_project_id")
	if veloxProjectID == "" {
		writeError(w, http.StatusBadRequest, "velox_project_id is required")
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	// Verify the session exists and the caller has access.
	edit, err := r.youtubeVideoEditStore.FindByVeloxProjectID(req.Context(), veloxProjectID)
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
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	if r.nvidiaMetadataSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "NVIDIA AI metadata generation is not configured (set NVIDIA_API_KEY). Manual metadata entry is still available.")
		return
	}

	var payload generateMetadataRequest
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}

	generated, err := r.nvidiaMetadataSvc.Generate(req.Context(), payload.Prompt)
	if err != nil {
		if errors.Is(err, services.ErrNVIDIANotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "NVIDIA AI metadata generation is not configured. Manual metadata entry is still available.")
			return
		}
		if errors.Is(err, services.ErrNVIDIAResponseInvalid) {
			writeError(w, http.StatusBadGateway, "NVIDIA returned an invalid response: "+err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "NVIDIA metadata generation failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, generateMetadataResponse{
		Title:                generated.Title,
		Description:          generated.Description,
		Tags:                 generated.Tags,
		DefaultLanguage:      generated.DefaultLanguage,
		DefaultAudioLanguage: generated.DefaultAudioLanguage,
		Translations:         generated.Translations,
	})
}
