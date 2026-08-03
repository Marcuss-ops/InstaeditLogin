package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

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
//
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
			Status:            edit.Status,
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

	// Resolve privacy status: payload override → session default → private.
	privacyStatus := payload.PrivacyStatus
	if privacyStatus == "" {
		privacyStatus = edit.DesiredPrivacy
	}
	privacyStatus = strings.ToLower(strings.TrimSpace(privacyStatus))
	if privacyStatus == "" {
		privacyStatus = "private"
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
	if asset.ContentType != "image/jpeg" && asset.ContentType != "image/png" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported thumbnail content type %q (only image/jpeg and image/png are allowed)", asset.ContentType))
		return
	}

	// Renew first (P0): an expired access token is refreshed
	// automatically from the stored grant (r.youTubeSvc is guaranteed
	// non-nil here — the 503 guard above). The Get bearer → long_lived
	// → short_lived fallback remains only for historical tokens written
	// by older releases / migrations.
	token, err := r.vault.Renew(ctx, edit.PlatformAccountID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
	if err != nil {
		token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeBearer)
	}
	if err != nil {
		token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeLongLived)
	}
	if err != nil {
		token, err = r.vault.Get(ctx, edit.PlatformAccountID, models.TokenTypeShortLived)
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no valid token found for this account")
		return
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
		Status:            edit.Status,
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
// Empty map → empty slice. Nil map → empty slice. Both cases
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
