package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
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
//
// ActualPrivacy + YouTubeSyncStatus are the YouTube-side projection
// (P0#7). Pointer-to-string so the SPA sees `null` (not empty string)
// when the publish hasn't completed or the read-back errored — the
// SPA treats `null actual_privacy` as "in flight", the same way it
// treats no `editor_url` on a freshly-discovered card grid entry.
type youTubeEditorSessionDetail struct {
	ID                 string     `json:"id"`
	WorkspaceID        int64      `json:"workspace_id"`
	PlatformAccountID  int64      `json:"platform_account_id"`
	YouTubeVideoID     string     `json:"youtube_video_id"`
	VeloxProjectID     string     `json:"velox_project_id"`
	SourceThumbnailURL string     `json:"source_thumbnail_url,omitempty"`
	ThumbnailMediaID   *string    `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy     string     `json:"desired_privacy"`
	PublishAt          *time.Time `json:"publish_at,omitempty"`
	Status             string     `json:"status"`
	LastError          string     `json:"last_error,omitempty"`
	ActualPrivacy      *string    `json:"actual_privacy,omitempty"`
	YouTubeSyncStatus  *string    `json:"youtube_sync_status,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
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
		ActualPrivacy:      edit.ActualPrivacy,
		YouTubeSyncStatus:  edit.YouTubeSyncStatus,
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
// + workspace-ownership checks; this helper handles the side-effects.
//
// Step order (single goroutine, no concurrency hazards):
//  1. idempotency: if status=='published' return 200 + stored URL;
//  2. in-flight guard: if status=='publishing' within the timeout
//     window → 409;
//  3. privacy + publish_at validation (resolved against the payload
//     override OR edit.DesiredPrivacy);
//  4. YouTubePublishOptions.Validate() gate (tag count / char limit /
//     BCP-47 sanity / translations require default_language) —
//     runs BEFORE any side-effect fetch (media + token) so a bad
//     payload fails fast with 400, no API quota consumed;
//  5. media asset + thumbnail bytes fetch from storage;
//  6. token fetch from vault;
//  7. MarkPublishing atomic CAS (status → 'publishing', stamped
//     desired_privacy + publish_at);
//  8. PublishThumbnail: thumbnail.set + single videos.update
//     (part=snippet,status) carrying title + description + tags +
//     default_language + default_audio_language; on the pre-extension
//     path (no tags/langs) it delegates to the byte-identical
//     UpdateVideoPrivacy;
//  9. translations loop: per-language videos.update(part=localizations)
//     call, in sorted order so retries converge. Mid-loop failure
//     flips status → 'failed' + records the failing lang on
//     last_error so a retry picks up at the right point;
// 10. status='published' write + 200 response.
//
// Behaviour parity with handlePublishYouTubeEditorSession:
// the by-project variant inherits the exact same semantics because
// the only thing that varies between the two is the session lookup,
// which the wrappers handle before calling this helper.
func (r *Router) executePublishYouTubeEditorSession(
	ctx context.Context,
	w http.ResponseWriter,
	identity auth.Identity,
	edit *models.YouTubeVideoEdit,
	payload publishYouTubeEditorSessionRequest,
) {
	// Idempotency: published sessions can be replayed without
	// re-running the YouTube API call. The YouTube-side projection
	// (ActualPrivacy + YouTubeSyncStatus) is also cached on the row
	// by MarkPublishedWithActualPrivacy during the FIRST successful
	// publish, so a replay returns the same terminal-state shape.
	if edit.Status == "published" {
		writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
			PublicURL:         "https://www.youtube.com/watch?v=" + edit.YouTubeVideoID,
			VideoID:           edit.YouTubeVideoID,
			PrivacyStatus:     edit.DesiredPrivacy,
			ActualPrivacy:     derefString(edit.ActualPrivacy),
			YouTubeSyncStatus: derefString(edit.YouTubeSyncStatus),
			PublishedAt:       edit.PublishAt,
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

	// Validate the P1 extension fields (tags / default language /
	// default audio language / translations) BEFORE any side effect
	// (media download, token fetch, CAS). YouTubePublishOptions.Validate
	// enforces the YouTube-published bounds (tag count, tag char
	// length, BCP-47 sanity, translations require default_language).
	// Failing fast saves the operator an entire round-trip when the
	// payload is malformed: a 4xx from YouTube would still cost the
	// 1600 quota the snippet+status call would burn if we deferred
	// the check.
	if err := youTubePublishOptionsForRequest(payload).Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	opts := youTubePublishOptionsForRequest(payload)
	publicURL, err := r.youTubeSvc.PublishThumbnail(
		ctx,
		token.AccessToken,
		edit.YouTubeVideoID,
		thumbnailData,
		asset.ContentType,
		privacyStatus,
		payload.PublishAt,
		opts,
	)
	if err != nil {
		edit.Status = "failed"
		edit.LastError = truncateError(err.Error())
		edit.UpdatedAt = time.Now().UTC()
		_ = r.youtubeVideoEditStore.Update(ctx, edit)
		writeError(w, http.StatusBadGateway, "youtube publish failed: "+err.Error())
		return
	}

	// P0#7 actual_privacy read-back. Right after the snippet+status
	// + localizations loop completes (above), call YouTube videos.list
	// to confirm what YouTube ACTUALLY accepted for the privacy.
	//
	// Three terminal outcomes for the sync_status marker:
	//   'confirmed': YouTube accepted exactly the privacy we requested.
	//   'drift': YouTube accepted a DIFFERENT privacy (rare — typically
	//                a YouTube-side fluke on scheduled_publish at the
	//                moment of read-back). The publish is still terminal-
	//                published; the drift_reconciler sweeps the row on
	//                its next tick and attempts to converge.
	//   'pending': The videos.list read-back errored transiently (5xx,
	//                network). The publish is still terminal-published;
	//                the drift_reconciler's partial index sweep on
	//                youtube_sync_status='pending' retries until the
	//                read-back succeeds.
	//
	// Failure policy: the PublishThumbnail YouTube call succeeded,
	// so we never DOWNGRADE to a 5xx from this branch — we always
	// surface 200 + a terminal-published row, deferring read-back
	// success to the reconciler. This is the operator-friendly
	// contract: "you clicked Pubblica, your visibility is set, we'll
	// confirm the precise state with YouTube in a few seconds."
	actualPrivacy := privacyStatus
	syncStatus := "confirmed"
	if video, lookupErr := r.youTubeSvc.GetYouTubeVideo(ctx, token.AccessToken, edit.YouTubeVideoID); lookupErr != nil {
		// Read-back transport error: stamp pending, defer to
		// reconciler. We log the error internally for the
		// dashboard's diagnostics but do NOT surface it to the
		// operator — the publish itself succeeded.
		actualPrivacy = ""
		syncStatus = "pending"
	} else if video == nil {
		// Defensive: videos.list returning empty shouldn't happen
		// (we just successfully updated it) but treat the same as
		// a transport error.
		actualPrivacy = ""
		syncStatus = "pending"
	} else {
		ytPrivacy := strings.ToLower(strings.TrimSpace(video.Privacy))
		if ytPrivacy != privacyStatus {
			syncStatus = "drift"
			actualPrivacy = ytPrivacy
		} else {
			actualPrivacy = ytPrivacy
		}
	}

	// Apply per-language localizations AFTER the snippet+status
	// update succeeds. Each language is a separate
	// videos.update(part=localizations) call — YouTube rejects
	// multi-language requests in a single body. The loop is
	// idempotent: a retry after a mid-loop failure re-applies
	// every translation (YouTube upserts), so an operator replay
	// converges to the same final state without leaving a
	// half-applied set on the video.
	//
	// Order: we use a sorted slice of (lang -> translation) so the
	// iteration order is deterministic across retries — important
	// for test stability + a clean violation trace when a partial
	// failure leaves N translated langs and 1 that still needs to
	// be applied.
	for _, lang := range sortedTranslationKeys(opts.Translations) {
		tr := opts.Translations[lang]
		localErr := r.youTubeSvc.UpsertLocalizations(ctx, token.AccessToken, edit.YouTubeVideoID, lang, tr)
		if localErr != nil {
			// Mid-loop failure: stamp status='failed' + record the
			// failing language on last_error so a retry can
			// pick up where the previous attempt left off (the
			// published flag is NOT set — the operator retries
			// the whole publish flow which is idempotent on the
			// localizations loop).
			edit.Status = "failed"
			edit.LastError = truncateError(fmt.Sprintf("localizations[%s] failed: %v", lang, localErr))
			edit.UpdatedAt = time.Now().UTC()
			_ = r.youtubeVideoEditStore.Update(ctx, edit)
			writeError(w, http.StatusBadGateway, fmt.Sprintf("youtube upsert localizations %s failed: %v", lang, localErr))
			return
		}
	}

	// MarkPublishedWithActualPrivacy (P0#7) atomically flips
	// status='publishing' -> 'published' AND stamps actual_privacy +
	// youtube_sync_status. The CAS guarantees a concurrent reader
	// cannot observe Status='published' with NULL ActualPrivacy
	// (the partial-state bug we fixed).
	edit.LastError = ""
	claimed, err = r.youtubeVideoEditStore.MarkPublishedWithActualPrivacy(
		ctx, edit.ID, actualPrivacy, syncStatus,
	)
	if err != nil {
		if errors.Is(err, repository.ErrYouTubeVideoEditNotFound) {
			writeError(w, http.StatusConflict, "publish already in progress or terminal state")
			return
		}
		writeError(w, http.StatusInternalServerError, "mark published: "+err.Error())
		return
	}
	edit = claimed

	writeJSON(w, http.StatusOK, publishYouTubeEditorSessionResponse{
		PublicURL:         publicURL,
		VideoID:           edit.YouTubeVideoID,
		PrivacyStatus:     privacyStatus,
		ActualPrivacy:     derefString(edit.ActualPrivacy),
		YouTubeSyncStatus: derefString(edit.YouTubeSyncStatus),
		PublishedAt:       payload.PublishAt,
	})
}



// sortedTranslationKeys returns the map keys in a stable,
// deterministic order. The orchestrator's per-language loop uses
// this so the iteration order is reproducible across retries
// (important when the loop fails mid-way — re-running the same
// map with a different iteration order would still arrive at the
// same end state, but a stable order keeps the test failure
// signatures clean).
//
// Empty map → empty slice. Nil map → empty slice. Both callers
// go through the same branch in the orchestrator.
func sortedTranslationKeys(m map[string]models.YouTubeTranslation) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}