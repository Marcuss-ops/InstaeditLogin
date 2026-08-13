package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// youtubeVideoMetadataPatchRequest is the PATCH body for
// /api/v1/groups/{group_id}/youtube/videos/{video_id}.
//
// platform_account_id is REQUIRED: it identifies the group channel
// that owns the video so the handler can mint the correct OAuth token
// — a bare video id is ambiguous across the group's channels. The
// frontend already carries it on every GroupYouTubeVideo row.
//
// title / description / category_id are pointers so the merge can
// distinguish "omitted" (nil — keep the current value) from
// "explicitly cleared" (empty string — clear it).
type youtubeVideoMetadataPatchRequest struct {
	PlatformAccountID int64   `json:"platform_account_id"`
	Title             *string `json:"title"`
	Description       *string `json:"description"`
	CategoryID        *string `json:"category_id"`
}

// groupYouTubeVideoMetadataResponse echoes the effective (merged)
// snippet values after a successful update, so the SPA can refresh
// its card without a follow-up list fetch.
type groupYouTubeVideoMetadataResponse struct {
	YoutubeVideoID string `json:"youtube_video_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	CategoryID     string `json:"category_id"`
}

// handlePatchGroupYouTubeVideoMetadata is the HTTP entry point for
// PATCH /api/v1/groups/{group_id}/youtube/videos/{video_id} — the
// single metadata update endpoint for a group's YouTube video
// (title / description / category).
//
// Behaviour:
//   - 401 without a JWT identity.
//   - 400 for an invalid group_id / empty video_id / malformed body /
//     missing platform_account_id / empty patch / empty title / unknown
//     category_id.
//   - 404 when the group is unknown or not owned by the caller, when
//     the account is not part of the group, or when the video does not
//     exist on the account's channel (collapsed: a cross-tenant probe
//     cannot distinguish "no such group" from "no such video").
//   - 403 when the video is not owned by the requested channel or the
//     grant lacks permission.
//   - 502 when the token cannot be renewed or YouTube errors out
//     (transient upstream failure); 429 surfaces rate limits.
//   - 200 + the merged { youtube_video_id, title, description,
//     category_id } on success. The account's cached editable-videos
//     entries are invalidated so the next group list reflects the new
//     metadata.
//
// The merge itself lives in the YouTube service (UpdateVideoMetadata):
// videos.update REPLACES the snippet, so the service reads the current
// canonical snippet and re-sends tags / default languages verbatim
// alongside the patched title / description / categoryId.
func (r *Router) handlePatchGroupYouTubeVideoMetadata(w http.ResponseWriter, req *http.Request) {
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

	var body youtubeVideoMetadataPatchRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.PlatformAccountID <= 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
		return
	}
	if body.Title == nil && body.Description == nil && body.CategoryID == nil {
		writeError(w, http.StatusBadRequest, "at least one of title, description, category_id must be provided")
		return
	}
	if body.Title != nil && strings.TrimSpace(*body.Title) == "" {
		writeError(w, http.StatusBadRequest, "title cannot be empty")
		return
	}
	if body.CategoryID != nil {
		categoryID := strings.TrimSpace(*body.CategoryID)
		if categoryID == "" || !models.ValidLivestreamCategory(categoryID) {
			// ValidLivestreamCategory is the canonical known-id gate
			// (videoCategories.list); the livestream scoping of the
			// helper name is incidental — the category set is shared
			// across all YouTube snippet writes.
			writeError(w, http.StatusBadRequest, "category_id must be a known YouTube category id")
			return
		}
	}

	if r.youTubeSvc == nil || r.vault == nil {
		writeError(w, http.StatusServiceUnavailable, "youtube service not configured")
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

	token, err := r.vault.Renew(req.Context(), acc.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
		return
	}

	patch := models.YouTubeMetadataPatch{
		Title:       body.Title,
		Description: body.Description,
		CategoryID:  body.CategoryID,
	}
	result, err := r.youTubeSvc.UpdateVideoMetadata(req.Context(), token.AccessToken, videoID, acc.PlatformUserID, patch)
	if err != nil {
		if errors.Is(err, services.ErrYouTubeVideoNotFound) {
			writeError(w, http.StatusNotFound, "video not found on this account")
			return
		}
		var apiErr *services.YouTubeAPIError
		if errors.As(err, &apiErr) {
			switch {
			case apiErr.StatusCode == http.StatusForbidden:
				writeError(w, http.StatusForbidden, "video non gestibile dall'account selezionato")
				return
			case apiErr.StatusCode == http.StatusTooManyRequests:
				writeError(w, http.StatusTooManyRequests, "YouTube rate limit raggiunto. Riprova tra poco.")
				return
			case apiErr.StatusCode >= 500 || apiErr.StatusCode == 0:
				writeError(w, http.StatusBadGateway, "YouTube non risponde temporaneamente. Riprova tra poco.")
				return
			}
		}
		writeError(w, http.StatusBadRequest, "impossibile aggiornare i metadati del video")
		return
	}

	// The group list is served from a short-lived per-account cache;
	// drop the entries so the next list reflects the new metadata.
	r.invalidateAccountCachedVideos(acc)

	writeJSON(w, http.StatusOK, groupYouTubeVideoMetadataResponse{
		YoutubeVideoID: result.VideoID,
		Title:          result.Title,
		Description:    result.Description,
		CategoryID:     result.CategoryID,
	})
}
