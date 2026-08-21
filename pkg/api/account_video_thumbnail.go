package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/go-chi/chi/v5"
)

// handlePublishAccountVideoThumbnail is the account-scoped counterpart to
// the group thumbnail endpoint. It exists for YouTube Studio private videos,
// which are not necessarily members of a selected group.
func (r *Router) handlePublishAccountVideoThumbnail(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	accountID, err := parsePositiveQueryInt(strings.TrimSpace(chi.URLParam(req, "account_id")))
	if err != nil || r.userRepo == nil {
		writeError(w, http.StatusNotFound, "video not found on this account")
		return
	}
	account, err := r.userRepo.FindPlatformAccountByID(accountID)
	if err != nil || account == nil || account.UserID != identity.UserID() || account.Platform != models.PlatformYouTube {
		writeError(w, http.StatusNotFound, "video not found on this account")
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
	mediaID := strings.TrimSpace(body.ThumbnailMediaID)
	if mediaID == "" || r.mediaStore == nil || r.storageProvider == nil || r.vault == nil || r.youTubeSvc == nil {
		writeError(w, http.StatusBadRequest, "thumbnail_media_id is required")
		return
	}
	asset, err := r.mediaStore.FindByID(mediaID)
	if err != nil || asset == nil || asset.UserID != identity.UserID() || asset.Status != models.MediaAssetStatusReady {
		writeError(w, http.StatusBadRequest, "invalid or unverified media asset")
		return
	}
	if asset.ContentType != "image/jpeg" && asset.ContentType != "image/png" {
		writeError(w, http.StatusBadRequest, "unsupported thumbnail content type")
		return
	}
	token, err := r.vault.Renew(req.Context(), account.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
		return
	}
	video, err := r.youTubeSvc.GetYouTubeVideo(req.Context(), token.AccessToken, videoID)
	if err != nil {
		if errors.Is(err, services.ErrYouTubeVideoNotFound) {
			writeError(w, http.StatusNotFound, "video not found on this account")
		} else {
			writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
		}
		return
	}
	if video == nil || video.ChannelID != account.PlatformUserID {
		writeError(w, http.StatusNotFound, "video not found on this account")
		return
	}
	downloadURL, err := r.storageProvider.GetObject(req.Context(), asset.UploadKey, 5*time.Minute)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "generate thumbnail download URL: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	data, err := downloadThumbnailBytes(ctx, r.thumbnailDownloadClient, downloadURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "download thumbnail: "+err.Error())
		return
	}
	if err := r.youTubeSvc.SetThumbnail(req.Context(), token.AccessToken, videoID, asset.ContentType, bytes.NewReader(data), int64(len(data))); err != nil {
		writeError(w, http.StatusBadRequest, "impossibile pubblicare la copertina")
		return
	}
	r.invalidateAccountCachedVideos(account)
	writeJSON(w, http.StatusOK, publishGroupVideoThumbnailResponse{Status: "published", YouTubeVideoID: videoID, WatchURL: "https://www.youtube.com/watch?v=" + videoID, ThumbnailMediaID: mediaID, ContentType: asset.ContentType, Bytes: int64(len(data))})
}
