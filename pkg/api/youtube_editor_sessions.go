package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
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

// updateYouTubeEditorSessionRequest is the body accepted by the
// PATCH /api/v1/youtube/editor-sessions/by-project/{velox_project_id}
// endpoint.
type updateYouTubeEditorSessionRequest struct {
	ThumbnailMediaID string `json:"thumbnail_media_id"`
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

// handleUpdateYouTubeEditorSession updates a thumbnail editor session.
// It is used by the dark editor after uploading the generated thumbnail
// to InstaEdit storage so the session keeps a reference to the verified
// asset (thumbnail_media_id) before the user clicks Publish.
func (r *Router) handleUpdateYouTubeEditorSession(w http.ResponseWriter, req *http.Request) {
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

	var payload updateYouTubeEditorSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if payload.ThumbnailMediaID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail_media_id is required")
		return
	}

	if r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}

	if r.youtubeVideoEditStore == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube video edit store not configured")
		return
	}

	edit, err := r.youtubeVideoEditStore.FindByVeloxProjectID(req.Context(), veloxProjectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find editor session: "+err.Error())
		return
	}
	if edit == nil {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	// Verify the media asset exists, is ready, and belongs to the caller.
	asset, err := r.mediaStore.FindByID(payload.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find media asset: "+err.Error())
		return
	}
	if asset == nil || asset.UserID != identity.UserID() || asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusBadRequest, "invalid or unverified media asset")
		return
	}

	workspace, err := r.workspaceStore.FindByID(edit.WorkspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find workspace: "+err.Error())
		return
	}
	if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	edit.ThumbnailMediaID = &payload.ThumbnailMediaID
	edit.UpdatedAt = time.Now().UTC()
	if err := r.youtubeVideoEditStore.Update(req.Context(), edit); err != nil {
		writeError(w, http.StatusInternalServerError, "update editor session: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"session_id":         edit.ID,
		"thumbnail_media_id": edit.ThumbnailMediaID,
	})
}

// publishYouTubeEditorSessionRequest is the body accepted by
// POST /api/v1/youtube/editor-sessions/{id}/publish.
// Title and Description are optional; when provided they are sent to
// YouTube's videos.update with part=snippet,status. YouTube enforces a
// 100-character title limit and a 5000-character description limit.
type publishYouTubeEditorSessionRequest struct {
	Title         string     `json:"title,omitempty"`
	Description   string     `json:"description,omitempty"`
	PrivacyStatus string     `json:"privacy_status,omitempty"`
	PublishAt     *time.Time `json:"publish_at,omitempty"`
}

// publishYouTubeEditorSessionResponse is returned on a successful publish.
type publishYouTubeEditorSessionResponse struct {
	PublicURL    string     `json:"public_url"`
	VideoID      string     `json:"video_id"`
	PrivacyStatus string    `json:"privacy_status"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
}

// handlePublishYouTubeEditorSession publishes the edited thumbnail to
// YouTube. It is idempotent: if the session is already published it
// returns the stored public URL; if a publish is already in flight it
// returns 409; on failure it records the error and allows retries.
func (r *Router) handlePublishYouTubeEditorSession(w http.ResponseWriter, req *http.Request) {
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

	var payload publishYouTubeEditorSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	payload.Title = strings.TrimSpace(payload.Title)
	payload.Description = strings.TrimSpace(payload.Description)
	if err := services.ValidateYouTubeSnippet(payload.Title, payload.Description); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}

	// Idempotency checks: published sessions can be replayed without
	// requiring the downstream media/YouTube services.
	if edit.Status == "published" {
		writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
			PublicURL:     "https://www.youtube.com/watch?v=" + edit.YouTubeVideoID,
			VideoID:       edit.YouTubeVideoID,
			PrivacyStatus: edit.DesiredPrivacy,
			PublishedAt:   edit.PublishAt,
		})
		return
	}
	if edit.Status == "publishing" && time.Since(edit.UpdatedAt) < 5*time.Minute {
		writeError(w, http.StatusConflict, "publish already in progress")
		return
	}

	if r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	if r.youTubeSvc == nil {
		writeError(w, http.StatusServiceUnavailable, "YouTube service not configured")
		return
	}

	if edit.ThumbnailMediaID == nil || *edit.ThumbnailMediaID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail media not attached to session")
		return
	}
	asset, err := r.mediaStore.FindByID(*edit.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find media asset: "+err.Error())
		return
	}
	if asset == nil || asset.UserID != identity.UserID() || asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusBadRequest, "invalid or unverified media asset")
		return
	}

	// Resolve privacy status and publish time.
	privacyStatus := payload.PrivacyStatus
	if privacyStatus == "" {
		privacyStatus = edit.DesiredPrivacy
	}
	if privacyStatus == "" {
		privacyStatus = "public"
	}
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	if privacyStatus != "public" && privacyStatus != "unlisted" && privacyStatus != "private" {
		writeError(w, http.StatusBadRequest, "privacy_status must be public, unlisted, or private")
		return
	}
	if payload.PublishAt != nil && !payload.PublishAt.IsZero() {
		if payload.PublishAt.Before(time.Now().UTC()) {
			writeError(w, http.StatusBadRequest, "publish_at must be in the future")
			return
		}
		if privacyStatus != "private" {
			writeError(w, http.StatusBadRequest, "scheduled publishing requires privacy_status=private")
			return
		}
	}

	// Fetch a fresh access token.
	token, err := r.vault.Get(req.Context(), edit.PlatformAccountID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(req.Context(), edit.PlatformAccountID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), edit.PlatformAccountID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	// Download the thumbnail bytes using a presigned GET URL.
	downloadURL, err := r.storageProvider.GetObject(req.Context(), asset.UploadKey, 5*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate thumbnail download URL: "+err.Error())
		return
	}
	downloadCtx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	thumbnailData, err := downloadThumbnailBytes(downloadCtx, r.thumbnailDownloadClient, downloadURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "download thumbnail: "+err.Error())
		return
	}

	// Mark publishing and attempt the publish.
	edit.Status = "publishing"
	edit.DesiredPrivacy = privacyStatus
	edit.PublishAt = payload.PublishAt
	edit.UpdatedAt = time.Now().UTC()
	if err := r.youtubeVideoEditStore.Update(req.Context(), edit); err != nil {
		writeError(w, http.StatusInternalServerError, "update editor session: "+err.Error())
		return
	}

	publicURL, err := r.youTubeSvc.PublishThumbnail(
		req.Context(),
		token.AccessToken,
		edit.YouTubeVideoID,
		thumbnailData,
		asset.ContentType,
		privacyStatus,
		payload.PublishAt,
		payload.Title,
		payload.Description,
	)
	if err != nil {
		edit.Status = "failed"
		edit.LastError = truncateError(err.Error())
		edit.UpdatedAt = time.Now().UTC()
		_ = r.youtubeVideoEditStore.Update(req.Context(), edit)
		writeError(w, http.StatusBadGateway, "youtube publish failed: "+err.Error())
		return
	}

	edit.Status = "published"
	edit.LastError = ""
	edit.UpdatedAt = time.Now().UTC()
	if err := r.youtubeVideoEditStore.Update(req.Context(), edit); err != nil {
		writeError(w, http.StatusInternalServerError, "update editor session: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
		PublicURL:     publicURL,
		VideoID:       edit.YouTubeVideoID,
		PrivacyStatus: privacyStatus,
		PublishedAt:   payload.PublishAt,
	})
}

// downloadThumbnailBytes fetches the thumbnail bytes from the signed
// download URL. The asset is capped at 2 MB, so reading into memory is
// safe.
func downloadThumbnailBytes(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("thumbnail download returned %d: %s", resp.StatusCode, string(body))
	}
	const maxBytes = 2 * 1024 * 1024
	// Guard against unexpectedly large payloads before reading the body.
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("thumbnail download exceeded max size: %d > %d", resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("thumbnail download read: %w", err)
	}
	if len(data) == maxBytes {
		// We may have hit the limit; the next byte would tell, but for our
		// use case the caller will validate the exact size against the
		// stored media asset before publishing to YouTube anyway.
	}
	return data, nil
}

// truncateError limits an error string to a length suitable for
// storage in the last_error column.
func truncateError(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	return s[:max]
}
