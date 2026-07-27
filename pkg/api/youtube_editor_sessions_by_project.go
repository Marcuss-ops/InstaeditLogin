package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// youTubeEditorSessionDetail is the per-row JSON shape returned by
// GET /api/v1/youtube/editor-sessions/by-project/{velox_project_id}.
//
// Mirrors models.YouTubeVideoEdit but adds explicit json tags (the
// model struct intentionally avoids tags to stay storage-agnostic).
// `last_error` is included as an operator hint for the dashboard's
// "Perché ha fallito?" copy — internal diagnostics only.
type youTubeEditorSessionDetail struct {
	ID                string     `json:"id"`
	WorkspaceID       int64      `json:"workspace_id"`
	PlatformAccountID int64      `json:"platform_account_id"`
	YouTubeVideoID    string     `json:"youtube_video_id"`
	VeloxProjectID    string     `json:"velox_project_id"`
	SourceThumbnailURL string    `json:"source_thumbnail_url,omitempty"`
	ThumbnailMediaID  *string    `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy    string     `json:"desired_privacy"`
	PublishAt         *time.Time `json:"publish_at,omitempty"`
	Status            string     `json:"status"`
	LastError         string     `json:"last_error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func toYouTubeEditorSessionDetail(edit *models.YouTubeVideoEdit) youTubeEditorSessionDetail {
	return youTubeEditorSessionDetail{
		ID:                 edit.ID,
		WorkspaceID:        edit.WorkspaceID,
		PlatformAccountID:  edit.PlatformAccountID,
		YouTubeVideoID:     edit.YouTubeVideoID,
		VeloxProjectID:     edit.VeloxProjectID,
		SourceThumbnailURL: edit.SourceThumbnailURL,
		ThumbnailMediaID:   edit.ThumbnailMediaID,
		DesiredPrivacy:     edit.DesiredPrivacy,
		PublishAt:          edit.PublishAt,
		Status:             edit.Status,
		LastError:          edit.LastError,
		CreatedAt:          edit.CreatedAt,
		UpdatedAt:          edit.UpdatedAt,
	}
}

// handleGetYouTubeEditorSessionByProject is the HTTP entry point for
// GET /api/v1/youtube/editor-sessions/by-project/{velox_project_id}.
//
// The Dark Editor reaches this endpoint with the velox_project_id
// it already has in the URL (/editor/{velox_project_id}) and
// receives the full session row (status, desired_privacy,
// thumbnail_media_id, youTubeVideoID) so it can render the form
// without first POSTing /editor-sessions to discover the session_id.
//
// Behaviour:
//   - 401 when no JWT identity is on the context.
//   - 400 when {velox_project_id} is empty (defence; chi's URLParam
//     would already return "" for a missing segment).
//   - 404 when the session is unknown OR the caller does not have
//     access to its workspace. Both branches return the SAME 404 +
//     message so a cross-tenant probe cannot distinguish "no such
//     session" from "session exists but not yours" (defence-in-depth
//     on top of the SQL `WHERE id = $1` guard).
//   - 503 when the youtube video edit store is not configured.
//   - 500 on a real repository error.
//   - 200 + the detail DTO otherwise.
func (r *Router) handleGetYouTubeEditorSessionByProject(w http.ResponseWriter, req *http.Request) {
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
		// Same 404-as-foreign pattern as the other endpoints: the
		// caller cannot tell "not found" from "not yours".
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	writeJSON(w, http.StatusOK, toYouTubeEditorSessionDetail(edit))
}

// handlePublishYouTubeEditorSessionByProject is the HTTP entry point
// for POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish.
//
// Mirror of handlePublishYouTubeEditorSession keyed by velox_project_id
// rather than session_id. The Dark Editor never knows the session_id
// — the project_id is the only handle it has. The two handlers
// converge on the same `executePublishYouTubeEditorSession` helper so
// the publish path (idempotency / in-flight / privacy resolution /
// media download / CAS / YouTube API) lives in exactly one place.
//
// Behaviour parity with handlePublishYouTubeEditorSession:
//   - 401 when no JWT identity.
//   - 400 when JSON is malformed, {velox_project_id} is empty,
//     title/description fail ValidateYouTubeSnippet.
//   - 404 when the session is unknown / not yours (same combined
//     404-as-foreign response as the GET above).
//   - 409 on in-flight / terminal CAS-loss.
//   - 502 on YouTube API failure.
//   - 200 + publishYouTubeEditorSessionResponse on success.
func (r *Router) handlePublishYouTubeEditorSessionByProject(w http.ResponseWriter, req *http.Request) {
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

	r.executePublishYouTubeEditorSession(req.Context(), w, identity, edit, payload)
}

// executePublishYouTubeEditorSession is the shared core of the two
// publish endpoints (handlePublishYouTubeEditorSession keyed by
// session_id, handlePublishYouTubeEditorSessionByProject keyed by
// velox_project_id). Both wrappers perform identity + payload + lookup
// + workspace-ownership checks; this helper handles the side-effects
// (idempotency / in-flight / privacy resolution / media / token /
// CAS / YouTube API / write response).
//
// Behaviour — see the long-form handler comments for the by-session
// variant. The by-project variant inherits the exact same semantics
// because the only thing that varies between the two is the session
// lookup, which the wrappers handle before calling this helper.
func (r *Router) executePublishYouTubeEditorSession(
	ctx context.Context,
	w http.ResponseWriter,
	identity auth.Identity,
	edit *models.YouTubeVideoEdit,
	payload publishYouTubeEditorSessionRequest,
) {
	// Idempotency: published sessions can be replayed without
	// re-running the YouTube API call.
	if edit.Status == "published" {
		writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
			PublicURL:     "https://www.youtube.com/watch?v=" + edit.YouTubeVideoID,
			VideoID:       edit.YouTubeVideoID,
			PrivacyStatus: edit.DesiredPrivacy,
			PublishedAt:   edit.PublishAt,
		})
		return
	}

	inFlightTimeout := r.publishingInFlightTimeout
	if inFlightTimeout <= 0 {
		inFlightTimeout = DefaultPublishingInFlightTimeout
	}
	if edit.Status == "publishing" && time.Since(edit.UpdatedAt) < inFlightTimeout {
		writeError(w, http.StatusConflict, "publish already in progress")
		return
	}

	// Resolve privacy status: payload override → session default → public.
	privacyStatus := payload.PrivacyStatus
	if privacyStatus == "" {
		privacyStatus = edit.DesiredPrivacy
	}
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	if privacyStatus == "" {
		privacyStatus = "public"
	}
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

	token, err := r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	downloadURL, err := r.storageProvider.GetObject(ctx, asset.UploadKey, 5*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate thumbnail download URL: "+err.Error())
		return
	}
	downloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	thumbnailData, err := downloadThumbnailBytes(downloadCtx, r.thumbnailDownloadClient, downloadURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "download thumbnail: "+err.Error())
		return
	}

	claimed, err := r.youtubeVideoEditStore.MarkPublishing(
		ctx, edit.ID, privacyStatus, payload.PublishAt, inFlightTimeout,
	)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditNotFound) {
			writeError(w, http.StatusConflict, "publish already in progress or terminal state")
			return
		}
		writeError(w, http.StatusInternalServerError, "mark publishing: "+err.Error())
		return
	}
	edit = claimed

	publicURL, err := r.youTubeSvc.PublishThumbnail(
		ctx,
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
		_ = r.youtubeVideoEditStore.Update(ctx, edit)
		writeError(w, http.StatusBadGateway, "youtube publish failed: "+err.Error())
		return
	}

	edit.Status = "published"
	edit.LastError = ""
	edit.UpdatedAt = time.Now().UTC()
	if err := r.youtubeVideoEditStore.Update(ctx, edit); err != nil {
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