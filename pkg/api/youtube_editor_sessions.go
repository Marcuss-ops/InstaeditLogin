package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// createYouTubeEditorSessionRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions.
type createYouTubeEditorSessionRequest struct {
	WorkspaceID        int64  `json:"workspace_id"`
	PlatformAccountID  int64  `json:"platform_account_id"`
	YouTubeVideoID     string `json:"youtube_video_id"`
	SourceThumbnailURL string `json:"source_thumbnail_url,omitempty"`
}

// createYouTubeEditorSessionResponse is returned on a successful creation.
type createYouTubeEditorSessionResponse struct {
	SessionID      string `json:"session_id"`
	VeloxProjectID string `json:"velox_project_id"`
	EditorURL      string `json:"editor_url"`
}

// handleCreateYouTubeEditorSession creates a YouTube thumbnail editor
// session. It verifies that the caller owns the workspace and the
// YouTube account, that the video belongs to the channel, and that the
// video is editable. On success it persists a youtube_video_edits row
// and returns the session id, velox project id, and editor URL.
func (r *Router) handleCreateYouTubeEditorSession(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	var payload createYouTubeEditorSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if payload.WorkspaceID <= 0 {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if payload.PlatformAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
		return
	}
	if payload.YouTubeVideoID == "" {
		writeError(w, http.StatusBadRequest, "youtube_video_id is required")
		return
	}

	// 1. Workspace must exist and belong to the caller.
	workspace, err := r.workspaceStore.FindByID(payload.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// 2. Platform account must belong to the caller and be linked to
	// the requested workspace.
	account, err := r.userRepo.FindPlatformAccountByID(payload.PlatformAccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find account: "+err.Error())
		return
	}
	if account == nil || account.UserID != identity.UserID() || account.Platform != models.PlatformYouTube {
		writeError(w, http.StatusNotFound, "account not found")
		return
	}
	channel, err := r.workspaceStore.FindChannel(req.Context(), payload.WorkspaceID, payload.PlatformAccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find channel binding: "+err.Error())
		return
	}
	if channel == nil {
		writeError(w, http.StatusNotFound, "account not linked to workspace")
		return
	}

	// 3. The caller must have a valid token for the account.
	token, err := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	// 4. Verify the video exists, belongs to the channel, and is
	// editable (private or unlisted videos may be edited; public
	// videos are also editable, but the typical use case is flipping
	// a private upload to public).
	if r.youTubeSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "YouTube service not configured")
		return
	}
	video, err := r.youTubeSvc.GetYouTubeVideo(req.Context(), token.AccessToken, payload.YouTubeVideoID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "youtube video: "+err.Error())
		return
	}
	if video.ChannelID != account.PlatformUserID {
		writeError(w, http.StatusBadRequest, "video does not belong to the selected channel")
		return
	}
	if video.UploadStatus != "processed" {
		writeError(w, http.StatusBadRequest, "video is not ready for thumbnail editing")
		return
	}
	if video.Privacy == "public" {
		writeError(w, http.StatusBadRequest, "video is already public; thumbnail editing is allowed only for private or unlisted videos")
		return
	}

	// 5. Create the session row.
	now := time.Now().UTC()
	sessionID := uuid.NewString()
	projectID := "ve_" + uuid.NewString()
	edit := &models.YouTubeVideoEdit{
		ID:                 sessionID,
		WorkspaceID:        payload.WorkspaceID,
		PlatformAccountID:  payload.PlatformAccountID,
		YouTubeVideoID:     payload.YouTubeVideoID,
		VeloxProjectID:     projectID,
		SourceThumbnailURL: payload.SourceThumbnailURL,
		DesiredPrivacy:     "public",
		Status:             "editing",
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}
	if err := r.youtubeVideoEditStore.Create(req.Context(), edit); err != nil {
		writeError(w, http.StatusInternalServerError, "create editor session: "+err.Error())
		return
	}

	editorURL := r.editorURLForProject(projectID)
	writeJSON(w, http.StatusCreated, createYouTubeEditorSessionResponse{
		SessionID:      sessionID,
		VeloxProjectID: projectID,
		EditorURL:      editorURL,
	})
}

// userCanAccessWorkspace reports whether the user owns the workspace.
// For the editor session creation flow, workspace ownership is the
// required authorization gate; future iterations may also accept team
// members via the team store.
func (r *Router) userCanAccessWorkspace(userID int64, workspace *models.Workspace) bool {
	if workspace == nil {
		return false
	}
	return workspace.OwnerID == userID
}

// editorURLForProject returns the canonical editor URL for a newly
// created project. When an editor URL is configured explicitly it is
// used; otherwise the frontend URL is used as a fallback.
func (r *Router) editorURLForProject(projectID string) string {
	base := r.editorURL
	if base == "" {
		base = r.frontendURL
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		// Last-resort fallback for test environments.
		base = "https://editor.instaedit.org"
	}
	return fmt.Sprintf("%s/editor/%s", base, projectID)
}
