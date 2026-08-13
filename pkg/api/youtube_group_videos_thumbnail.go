package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// publishGroupVideoThumbnailRequest is the POST body for
// /api/v1/groups/{group_id}/youtube/videos/{video_id}/thumbnail — the
// cover-save flow. The client sends the session RESULT: the media asset
// (thumbnail_media_id) holding the rendered cover exported by
// InstaEditor, plus the owning channel (platform_account_id) so the
// handler mints the correct OAuth token.
type publishGroupVideoThumbnailRequest struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	ThumbnailMediaID  string `json:"thumbnail_media_id"`
}

// publishGroupVideoThumbnailResponse echoes the published projection so
// the SPA can refresh its card without a follow-up list fetch.
type publishGroupVideoThumbnailResponse struct {
	Status           string `json:"status"`
	YouTubeVideoID   string `json:"youtube_video_id"`
	WatchURL         string `json:"watch_url"`
	ThumbnailMediaID string `json:"thumbnail_media_id"`
	ContentType      string `json:"content_type"`
	Bytes            int64  `json:"bytes"`
}

// handlePublishGroupVideoThumbnail is the HTTP entry point for
// POST /api/v1/groups/{group_id}/youtube/videos/{video_id}/thumbnail —
// the authorized InstaEdit backend publishes a cover to YouTube via
// thumbnails.set (PNG/JPEG, ≤ 2 MB). This is the THUMBNAIL-ONLY save
// flow: it never touches privacy or snippet metadata — that is the full
// publish pipeline's job (editor-sessions /publish).
//
// Step order:
//  1. identity (401) + path/body validation (400);
//  2. group lookup + workspace ownership + per-account resolution,
//     collapsed 404 so a cross-tenant probe cannot tell "no group"
//     from "not yours";
//  3. media asset resolution: must exist, belong to the caller, be
//     ready, and be image/jpeg or image/png (400 otherwise);
//  4. token renew from the vault (502 on failure);
//  5. owner/video verification: the video must exist AND belong to the
//     account's channel (GetYouTubeVideo + ChannelID gate, 404 when
//     missing or cross-channel);
//  6. thumbnail bytes download from storage, guarded at 2 MB;
//  7. thumbnails.set (403/429/404/502 mapped);
//  8. the account's cached editable videos are invalidated so the next
//     group list reflects the new thumbnail.
func (r *Router) handlePublishGroupVideoThumbnail(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}

	groupIDRaw := strings.TrimSpace(chi.URLParam(req, "group_id"))
	groupID, err := parsePositiveQueryInt(groupIDRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "group_id path parameter must be a positive integer")
		return
	}
	videoID := strings.TrimSpace(chi.URLParam(req, "video_id"))
	if videoID == "" {
		writeError(w, http.StatusBadRequest, "video_id path parameter is required")
		return
	}

	var body publishGroupVideoThumbnailRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.PlatformAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
		return
	}
	body.ThumbnailMediaID = strings.TrimSpace(body.ThumbnailMediaID)
	if body.ThumbnailMediaID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail_media_id is required")
		return
	}

	if r.youTubeSvc == nil || r.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube service not configured")
		return
	}
	if r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}

	// Group lookup + workspace ownership + per-account resolution.
	// Reuses the same collapsed-404 resolution as the group videos
	// list; include_subgroups=true so videos living on sub-group
	// channels are reachable through their parent group.
	_, accountLookup, done := r.resolveGroupYouTubeAccounts(w, identity.UserID(), groupID, true, r.youtubeGroupVideosConfig.normalized())
	if done {
		return
	}
	entry, ok := accountLookup[body.PlatformAccountID]
	if !ok {
		writeError(w, http.StatusNotFound, "video not found in this group")
		return
	}
	acc := entry.account

	// The thumbnail is the session result: a ready media asset owned by
	// the caller. Content type is checked here (PNG/JPEG only) and the
	// ≤ 2 MB bound is enforced both at download time and again inside
	// the SetThumbnail service call.
	asset, err := r.mediaStore.FindByID(body.ThumbnailMediaID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find media asset: "+err.Error())
		return
	}
	if asset == nil || asset.UserID != identity.UserID() || asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusBadRequest, "invalid or unverified media asset")
		return
	}
	if asset.ContentType != "image/jpeg" && asset.ContentType != "image/png" {
		writeError(w, http.StatusBadRequest, "unsupported thumbnail content type (only image/jpeg and image/png are allowed)")
		return
	}

	token, err := r.vault.Renew(req.Context(), acc.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
		return
	}

	// Owner/video verification: the video must exist on the channel the
	// grant was minted for. Missing video and cross-channel rows both
	// collapse to 404 so the endpoint never leaks channel membership.
	video, err := r.youTubeSvc.GetYouTubeVideo(req.Context(), token.AccessToken, videoID)
	if err != nil {
		if errors.Is(err, services.ErrYouTubeVideoNotFound) {
			writeError(w, http.StatusNotFound, "video not found on this account")
			return
		}
		writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
		return
	}
	if video == nil || video.ChannelID != acc.PlatformUserID {
		writeError(w, http.StatusNotFound, "video not found on this account")
		return
	}

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

	if err := r.youTubeSvc.SetThumbnail(req.Context(), token.AccessToken, videoID, asset.ContentType, bytes.NewReader(thumbnailData), int64(len(thumbnailData))); err != nil {
		var apiErr *services.YouTubeAPIError
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.StatusCode == http.StatusForbidden:
				writeError(w, http.StatusForbidden, "video non gestibile dall'account selezionato")
				return
			case apiErr.StatusCode == http.StatusTooManyRequests:
				writeError(w, http.StatusTooManyRequests, "YouTube rate limit raggiunto. Riprova tra poco.")
				return
			case apiErr.StatusCode == http.StatusNotFound:
				writeError(w, http.StatusNotFound, "video not found on this account")
				return
			case apiErr.StatusCode == http.StatusUnauthorized:
				writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
				return
			case apiErr.StatusCode >= 500 || apiErr.StatusCode == 0:
				writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
				return
			}
		}
		writeError(w, http.StatusBadRequest, "impossibile pubblicare la copertina")
		return
	}

	// Refresh the video data: drop the account's cached editable-videos
	// entries so the next group list reflects the new thumbnail without
	// waiting out the cache TTL.
	r.invalidateAccountCachedVideos(acc)

	writeJSON(w, http.StatusOK, publishGroupVideoThumbnailResponse{
		Status:           "published",
		YouTubeVideoID:   videoID,
		WatchURL:         "https://www.youtube.com/watch?v=" + videoID,
		ThumbnailMediaID: body.ThumbnailMediaID,
		ContentType:      asset.ContentType,
		Bytes:            int64(len(thumbnailData)),
	})
}
