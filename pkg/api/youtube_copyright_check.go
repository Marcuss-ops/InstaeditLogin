package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// youtubeCopyrightChecker is kept narrower than YouTubeOAuthService so the
// existing validation/test doubles do not need to implement the optional
// copyright capability.
type youtubeCopyrightChecker interface {
	CheckCopyright(ctx context.Context, accessToken, videoID string) (*services.YouTubeCopyrightCheck, error)
}

func copyrightBlocksPublicPublish(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "claim", "blocked", "error":
		return true
	default:
		return false
	}
}

// handleCheckYouTubeCopyright runs the same YouTube-side check used by the
// background worker. It is intentionally account-scoped: before checking a
// video we verify that the video belongs to the connected channel.
func (r *Router) handleCheckYouTubeCopyright(w http.ResponseWriter, req *http.Request) {
	accountID, err := parsePositiveQueryInt(req.URL.Query().Get("account_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "account_id must be a positive integer")
		return
	}
	videoID := strings.TrimSpace(req.URL.Query().Get("video_id"))
	if videoID == "" {
		writeError(w, http.StatusBadRequest, "video_id is required")
		return
	}

	account, _, ok := r.loadOwnAccountByID(w, req, accountID)
	if !ok {
		return
	}
	if account.Platform != "youtube" {
		writeError(w, http.StatusBadRequest, "copyright checks are available only for YouTube accounts")
		return
	}
	checker, ok := r.youTubeSvc.(youtubeCopyrightChecker)
	if !ok || r.vault == nil {
		writeError(w, http.StatusNotImplemented, "youtube copyright checks are not configured")
		return
	}

	token, err := r.vault.Renew(req.Context(), account.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if errors.Is(err, credentials.ErrModernGrantMissing) {
		token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
		if errors.Is(err, credentials.ErrModernGrantMissing) {
			token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
		}
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no valid token found for this account")
		return
	}

	video, err := r.youTubeSvc.GetYouTubeVideo(req.Context(), token.AccessToken, videoID)
	if err != nil || video == nil || video.ChannelID != account.PlatformUserID {
		writeError(w, http.StatusNotFound, "video not found on this YouTube channel")
		return
	}
	check, err := checker.CheckCopyright(req.Context(), token.AccessToken, videoID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "youtube copyright check failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, check)
}
