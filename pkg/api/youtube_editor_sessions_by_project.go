package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// youTubeEditorSessionDetail is the per-row JSON shape returned by
// GET /api/v1/youtube/editor-sessions/by-project/{velox_project_id}.
//
// Mirrors models.YouTubeVideoEdit but adds explicit json tags (the
// model struct intentionally avoids tags to stay storage-agnostic).
// `last_error` is included as an operator hint for the dashboard's
// "Perché ha fallito?" copy — internal diagnostics only.
//
// ActualPrivacy + YouTubeSyncStatus are the YouTube-side projection
// (P0#7). Pointer-to-string so the SPA sees `null` (not empty string)
// when the publish hasn't completed or the read-back errored — the
// SPA treats `null actual_privacy` as "in flight", the same way it
// treats no `editor_url` on a freshly-discovered card grid entry.
type youTubeEditorSessionDetail struct {
	ID                 string `json:"id"`
	WorkspaceID        int64  `json:"workspace_id"`
	PlatformAccountID  int64  `json:"platform_account_id"`
	ChannelID          string `json:"channel_id,omitempty"`
	YouTubeVideoID     string `json:"youtube_video_id"`
	VeloxProjectID     string `json:"velox_project_id"`
	EditorURL          string `json:"editor_url"`
	SourceThumbnailURL string `json:"source_thumbnail_url,omitempty"`
	// Extended session contract (thumbnail_url, category_id,
	// privacy_status) — the authoritative YouTube projection InstaEditor
	// renders as its initial document on load. thumbnail_url mirrors the
	// persisted source_thumbnail_url under the contract's wire name;
	// category_id is stamped at session creation from videos.list;
	// privacy_status is the live read-back (actual_privacy) when the
	// publish orchestrator stamped it, falling back to desired_privacy
	// for a session that has not published yet.
	ThumbnailURL      string     `json:"thumbnail_url,omitempty"`
	CategoryID        string     `json:"category_id,omitempty"`
	PrivacyStatus     string     `json:"privacy_status"`
	ThumbnailMediaID  *string    `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy    string     `json:"desired_privacy"`
	PublishAt         *time.Time `json:"publish_at,omitempty"`
	Status            string     `json:"status"`
	LastError         string     `json:"last_error,omitempty"`
	ActualPrivacy     *string    `json:"actual_privacy,omitempty"`
	YouTubeSyncStatus *string    `json:"youtube_sync_status,omitempty"`
	// DraftTitle is the operator-typed (or auto-provisioner
	// pre-filled) title the SPA renders on initial load. Added for
	// the GET-by-id endpoint so the Thumbnail Maker SPA can
	// prefill its form after a fresh POST /internal/v1/thumbnail-sessions.
	// Pointer + omitempty so a brand-new row (no draft written)
	// surfaces as a missing field rather than an empty string.
	DraftTitle       *string   `json:"draft_title,omitempty"`
	DraftDescription *string   `json:"draft_description,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// editorDetailWithURL stamps the launcher URL on the session detail DTO
// and reports whether the editor is available. It is the shared gate for
// the by-id and by-project GET handlers so the two endpoints can never
// drift apart.
//
// The 503 branch is the live fail-fast contract: with no INSTAEDITOR_URL
// (or an empty project handle) editorURLForProject returns "" and this
// gate answers 503 "Editor unavailable / misconfigured" — the API never
// redirects to the main InstaEdit frontend or fabricates an editor
// destination. Pinned by TestEditorURLForProject_EmptyWhenUnconfigured.
func (r *Router) editorDetailWithURL(w http.ResponseWriter, detail youTubeEditorSessionDetail) (youTubeEditorSessionDetail, bool) {
	detail.EditorURL = r.editorURLForProject(detail.VeloxProjectID)
	if detail.EditorURL == "" {
		writeError(w, http.StatusServiceUnavailable, "Editor unavailable / misconfigured")
		return detail, false
	}
	return detail, true
}

func toYouTubeEditorSessionDetail(edit *models.YouTubeVideoEdit) youTubeEditorSessionDetail {
	privacyStatus := edit.DesiredPrivacy
	if edit.ActualPrivacy != nil && strings.TrimSpace(*edit.ActualPrivacy) != "" {
		privacyStatus = *edit.ActualPrivacy
	}
	return youTubeEditorSessionDetail{
		ID:                 edit.ID,
		WorkspaceID:        edit.WorkspaceID,
		PlatformAccountID:  edit.PlatformAccountID,
		YouTubeVideoID:     edit.YouTubeVideoID,
		VeloxProjectID:     edit.VeloxProjectID,
		SourceThumbnailURL: edit.SourceThumbnailURL,
		ThumbnailURL:       edit.SourceThumbnailURL,
		CategoryID:         edit.CategoryID,
		PrivacyStatus:      privacyStatus,
		ThumbnailMediaID:   edit.ThumbnailMediaID,
		DesiredPrivacy:     edit.DesiredPrivacy,
		PublishAt:          edit.PublishAt,
		Status:             edit.Status,
		LastError:          edit.LastError,
		ActualPrivacy:      edit.ActualPrivacy,
		YouTubeSyncStatus:  edit.YouTubeSyncStatus,
		DraftTitle:         edit.DraftTitle,
		DraftDescription:   edit.DraftDescription,
		CreatedAt:          edit.CreatedAt,
		UpdatedAt:          edit.UpdatedAt,
	}
}

// hydrateAttachedThumbnailURL makes the editor consume the same image that
// was attached to the session/video. The persisted source_thumbnail_url is
// intentionally kept as the fallback, but must never win over the current
// thumbnail_media_id: otherwise the editor can reopen an older cover while
// YouTube and the Covers preview already show the new one.
func (r *Router) hydrateAttachedThumbnailURL(ctx context.Context, detail *youTubeEditorSessionDetail) {
	if detail == nil || detail.ThumbnailMediaID == nil || strings.TrimSpace(*detail.ThumbnailMediaID) == "" || r.mediaStore == nil || r.storageProvider == nil {
		return
	}
	asset, err := r.mediaStore.FindByID(strings.TrimSpace(*detail.ThumbnailMediaID))
	if err != nil || asset == nil || asset.Status != models.MediaAssetStatusReady || time.Now().After(asset.ExpiresAt) {
		return
	}
	url, err := r.storageProvider.GetObject(ctx, asset.UploadKey, 15*time.Minute)
	if err == nil && strings.TrimSpace(url) != "" {
		detail.ThumbnailURL = url
	}
}

// handleGetYouTubeEditorSessionByProject is the HTTP entry point for
// GET /api/v1/youtube/editor-sessions/by-project/{velox_project_id}.
//
// The InstaEditor reaches this endpoint with the velox_project_id
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
		// Standalone thumbnail projects do not have a YouTube editor-session
		// row. The editor gate still needs a workspace-scoped session-shaped
		// response so it can distinguish an editable bridge from a missing
		// project. The actual canvas remains owned by the project-scoped editor
		// BFF; this compatibility detail intentionally contains no YouTube data.
		bridgeStore, bridgeOK := r.thumbnailProjectStore.(thumbnailExternalProjectBridgeStore)
		if !bridgeOK {
			writeError(w, http.StatusNotFound, "editor session not found")
			return
		}
		bridge, bridgeErr := bridgeStore.FindVeloxProjectBridgeByExternalProjectID(req.Context(), identity.WorkspaceID(), veloxProjectID)
		if bridgeErr != nil || bridge == nil {
			writeError(w, http.StatusNotFound, "editor session not found")
			return
		}
		workspace, workspaceErr := r.workspaceStore.FindByID(bridge.WorkspaceID)
		if workspaceErr != nil {
			writeError(w, http.StatusInternalServerError, "find workspace: "+workspaceErr.Error())
			return
		}
		if workspace == nil || !r.userCanAccessWorkspace(identity.UserID(), workspace) {
			writeError(w, http.StatusNotFound, "editor session not found")
			return
		}
		now := time.Now()
		detail, ok := r.editorDetailWithURL(w, youTubeEditorSessionDetail{
			ID:             bridge.ExternalProjectID,
			WorkspaceID:    bridge.WorkspaceID,
			VeloxProjectID: bridge.ExternalProjectID,
			Status:         "editing",
			CreatedAt:      now,
			UpdatedAt:      now,
		})
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, detail)
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

	detail, ok := r.editorDetailWithURL(w, toYouTubeEditorSessionDetail(edit))
	if !ok {
		return
	}
	r.hydrateAttachedThumbnailURL(req.Context(), &detail)
	if r.userRepo != nil {
		if account, accountErr := r.userRepo.FindPlatformAccountByID(edit.PlatformAccountID); accountErr == nil && account != nil {
			if channelID, ok := account.Metadata["channel_id"].(string); ok && channelID != "" {
				detail.ChannelID = channelID
			} else {
				detail.ChannelID = account.PlatformUserID
			}
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

// handlePublishYouTubeEditorSessionByProject is the HTTP entry point
// for POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish.
//
// Mirror of handlePublishYouTubeEditorSession keyed by velox_project_id
// rather than session_id. The InstaEditor never knows the session_id
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
	if workspace == nil || !r.userCanEditWorkspace(identity.UserID(), workspace) {
		writeError(w, http.StatusNotFound, "editor session not found")
		return
	}

	r.executePublishYouTubeEditorSession(req.Context(), w, identity, edit, payload)
}
