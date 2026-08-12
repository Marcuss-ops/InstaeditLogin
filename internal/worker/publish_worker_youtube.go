package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func (w *PublishWorker) publishYouTubePhase2(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, post *models.Post, oauthToken *models.OAuthToken, payload models.PublishPayload) (handled bool, err error) {
	// PHASE 2 BYPASS (Blocco #1 followup — Migration 077 contract):
	// When Phase 1 (upload_worker.MarkYouTubeUploaded) has stamped a
	// youtube_target_publications row with youtube_upload_status
	// "youtube_uploaded" + a non-empty youtube_video_id, skip the
	// fresh videos.insert and re-use the existing video via
	// YouTubePrivacyUpdater.UpdateVideoPrivacy (videos.update).
	// Eliminates the double-upload bug documented in
	// internal/worker/publish_worker_phase1_video_reuse_test.go's
	// header comment.
	//
	// Placement rationale: AFTER throttle Wait wouldn't help
	// (UpdateVideoPrivacy is a metadata-only POST that doesn't
	// benefit from throttle shaping for chunked uploads) BUT after
	// payload build means payload.PrivacyLevel / Title / Text are
	// already cascade-resolved (no need to duplicate the cascade
	// logic inline). When the cast into YouTubePrivacyUpdater
	// fails (older test fixtures / a future non-YouTube platform
	// that registers under the youtube name), we fall through
	// with a warn log — the existing publisher.Publish path is
	// unaffected. Nil ytPubLookup (older test fixtures not yet
	// wired) is also a graceful fall-through — matches the
	// canonicalCanaryUploader nil-safe pattern documented above.
	if account.Platform == models.PlatformYouTube && w.ytPubLookup != nil {
		ytPub, lookupErr := w.ytPubLookup.FindByPostTargetID(ctx, target.ID)
		switch {
		case lookupErr != nil:
			// PR-1 fix — transient yt_pub lookup error must NOT
			// hard-fail the target. The pre-fix branch called
			// w.markFailed(target, ...) which transitioned the row
			// to status='failed' (terminal); the comment previously
			// claimed "the next tick retries" but that was a lie —
			// ListPending filters on status='queued' so a terminal-
			// failed row never gets re-picked up by the driver.
			//
			// Correct semantics: roll back our atomic claim
			// (publishing → queued) so the next tick's ListPending
			// re-picks this target and retries the lookup. We
			// still return the wrapped lookup error so that
			// tick() counts this attempt as a failure (per-target
			// error counter increments, operator sees the cause in
			// the tick-failed log line) — but the STATE is
			// recoverable, not terminal.
			//
			// Two non-fatal edge cases:
			//   1. UpdateStatus itself errors (DB blip during
			//      the rollback): logged at warn, target stays in
			//      'publishing' until lease expiry. Operator still
			//      sees the lookup_err via tick log.
			//   2. The claim rollback races with another replica's
			//      concurrent tick that picked up the same row from
			//      ListPending's transient overlap window: rejected
			//      by the WHERE-clause guard in UpdateStatus
			//      (RowsAffected==0 → ErrPostTargetNotFound). Same
			//      warning-shape handling.
			target.Status = models.PostStatusQueued
			if rbErr := w.updateTargetStatus(ctx, target); rbErr != nil {
				w.logger.Warn(
					"publish worker: yt-pub lookup transient — could not roll back claim to queued",
					"target_id", target.ID, "post_id", target.PostID,
					"lookup_error", lookupErr, "rollback_error", rbErr,
				)
			}
			return false, fmt.Errorf("youtube target publication lookup: %w", lookupErr)
		case ytPub != nil &&
			ytPub.YouTubeUploadStatus == "youtube_uploaded" &&
			ytPub.YouTubeVideoID != nil &&
			*ytPub.YouTubeVideoID != "":
			raw, hasRaw := w.router.Get(account.Platform)
			if hasRaw {
				if updater, ok := raw.(services.YouTubePrivacyUpdater); ok {
					// Blocco #1 followup — Finding #2: coerce privacy +
					// publishAt here so the values the worker LOGS
					// (and the values UpdateVideoPrivacy sees) are
					// both YouTube-API-legal. UpdateVideoPrivacy also
					// calls the helper internally (defense in depth);
					// the helper is idempotent so the two calls produce
					// the same result. time.Now() is passed inline to
					// avoid clashing with the outer `now := time.Now()`
					// already in publishTarget's platform-update block
					// (which would surface as a vet `no new variables
					// on left side of :=` error).
					effectivePrivacy, effectivePublishAt := services.CoercePrivacyForUpdate(payload.PrivacyLevel, post.PublishAt, time.Now())
					if err := updater.UpdateVideoPrivacy(
						ctx,
						oauthToken.AccessToken,
						*ytPub.YouTubeVideoID,
						effectivePrivacy,
						effectivePublishAt,
						post.Title,
						post.Caption,
					); err != nil {
						// Blocco #1 followup — Finding #4 (Phase-1
						// orphan-video recovery): if videos.update returned
						// 404 on OUR yt_pub's video_id (user manually deleted
						// the Phase-1 orphan, moderator takedown, etc.),
						// DON'T markFailed (terminal). Instead: log a
						// warning, clear the stale yt_pub row via
						// ClearYouTubeUpload so the next tick doesn't re-take
						// the bypass with a dead video_id, and synchronously
						// fall through to the publisher.Publish path below.
						//
						// Classification is defense-in-depth: typed sentinel
						// errors.Is on services.ErrYouTubeVideoNotFound is
						// the primary signal; substring fallback covers any
						// future code path that hasn't been re-wired to the
						// sentinel yet.
						if isOrphanedYouTubeVideo(err, *ytPub.YouTubeVideoID) {
							w.logger.Warn(
								"publish worker: Phase-1 YouTube video orphaned (404 from videos.update); clearing yt-pub row + falling through to fresh publisher.Publish",
								"yt_pub_id", ytPub.ID,
								"target_id", target.ID,
								"stale_video_id", *ytPub.YouTubeVideoID,
								"update_privacy_error", err,
							)
							if clearErr := w.ytPubLookup.ClearYouTubeUpload(ctx, ytPub.ID); clearErr != nil {
								w.logger.Warn(
									"publish worker: yt-pub ClearYouTubeUpload failed (non-fatal; fresh publisher.Publish will overwrite on success)",
									"yt_pub_id", ytPub.ID, "target_id", target.ID,
									"error", clearErr,
								)
							}
						} else {
							return false, w.markFailed(target, "UpdateVideoPrivacy: "+err.Error())
						}
					} else {
						// YouTube privacy transition succeeded — stamp
						// published_at on the YT pub row. MarkPublished
						// is an Upsert-shaped stamped-once helper
						// (youtube_target_publication_repo.go::422),
						// 0 rows affected is treated as transient /
						// already-published and we continue downstream.
						if err := w.ytPubLookup.MarkPublished(ctx, ytPub.ID); err != nil {
							w.logger.Warn(
								"publish worker: yt-pub MarkPublished failed (non-fatal; post_target publish transition continues)",
								"yt_pub_id", ytPub.ID, "target_id", target.ID, "error", err,
							)
						}
						// Mirror the SYNC-PUBLISH branch's full target
						// stamp shape (publish_worker.go SYNC block):
						// status + PlatformPostID + PublishedAt — so the
						// dashboard's "published" filter renders correctly
						// and the Published Video link points at the
						// Phase-1 reused video_id (NOT a phantom).
						now := time.Now()
						target.Status = models.PostStatusPublished
						// Composite-shape fix (Blocco #1 followup — Finding #1):
						// stamp `channelID:videoID` so this branch produces the
						// SAME PlatformPostID shape the async-publish branch
						// already stamps via
						// services.EncodeYouTubePublishID(platformUserID,
						// videoID) inside StartPublish. decodeYouTubePublishID
						// (in ReconcileWorker) only accepts the composite shape,
						// so a plain video_id stamp here would surface as
						// `invalid youtube publish id` on every reconciler tick
						// and never transition publishing → published.
						target.PlatformPostID = services.EncodeYouTubePublishID(account.PlatformUserID, *ytPub.YouTubeVideoID)
						target.PublishedAt = &now
						if err := w.updateTargetStatus(ctx, target); err != nil {
							return false, fmt.Errorf("publish worker: update target after YouTube reuse: %w", err)
						}
						w.syncContentPackageState(ctx, post)
						// Post-completion dispatch is best-effort. Resolve a fresh
						// URL for the delivery hook too; never re-emit the
						// persisted signed URL after Phase-2 reuse.
						if w.resolver != nil {
							deliveryURL, resolveErr := w.resolver.ResolveForUpload(ctx, post, time.Hour)
							if resolveErr != nil {
								w.logger.Warn("publish worker: skipping Phase-2 completion delivery because fresh media URL resolution failed",
									"target_id", target.ID, "post_id", target.PostID, "error", resolveErr)
							} else {
								w.dispatchPostCompletion(ctx, target, account, &models.MediaAsset{
									ID: mediaReferenceValue(post.MediaAssetID), UploadKey: mediaReferenceValue(post.StorageObjectKey),
									Bucket: mediaReferenceValue(post.Bucket), ContentType: "video/mp4",
								}, deliveryURL)
							}
						}
						return true, nil
					}
				}
			}
			w.logger.Warn(
				"publish worker: YouTube provider missing YouTubePrivacyUpdater capability; falling through to publisher.Publish (Phase 1 video may be orphaned unless uploader is upgraded)",
				"target_id", target.ID, "platform_account_id", account.ID,
			)
		}
	}

	return false, nil
}

// canonicalCanaryUploader (Task 7/10) is the YouTube canary pre-flight
// capability. The bootstrap (internal/bootstrap/app.go) wires the
// shared *services.YouTubeOAuthService here so the publish_worker
// hot path doesn't need a router lookup per target. Nil by default
// — the canary block logs a warning and falls through.
//
// Setter below is used by tests (assignable via the *PublishWorker
// pointer); production wires it once at startup.

// SetCanonicalCanaryUploader assigns the canary uploader used by
// the publish worker's pre-flight block. Pass nil to disable canary
// pre-flight (handler will fall through with a warn-level log).
func (w *PublishWorker) SetCanonicalCanaryUploader(u services.YouTubeCanaryUploader) {
	w.canonicalCanaryUploader = u
}

// YouTubeTargetPublicationLookup is the narrow interface the publish
// worker needs from the youtube_target_publications table for the
// Phase-2 upload-bypass (read existing video_id, stamp PublishedAt
// after videos.update). Distinct from the full
// YouTubeTargetPublicationStore interface in
// internal/repository/youtube_target_publication_repo.go so the
// worker's dep surface stays minimal (the worker doesn't need
// FindByID, FindByYouTubeVideoID, MarkYouTubeUploaded,
// IncrementAttempt, etc. — only the two methods below).
//
// Implementations must:
//   - Return (nil, nil) when no row exists for the given postTargetID
//     (matches the codebase's repository convention; not-found is
//     NOT a hard error in this read-side call shape).
//   - Plain-wrap non-ErrNoRows errors so the worker can mark the
//     target failed and let the next tick retry.
type YouTubeTargetPublicationLookup interface {
	FindByPostTargetID(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error)
	MarkPublished(ctx context.Context, id int64) error
	// ClearYouTubeUpload (Blocco #1 followup — Finding #4 orphan-video
	// recovery) nullifies the Phase-1 youtube_video_id stamp + resets
	// status to 'upload_session_initiated' and attempt_count to 0.
	// Called by PublishWorker.publishTarget when videos.update reports
	// a 404 on the Phase-1 stamped video_id so the next tick does NOT
	// re-take the bypass branch with a dead video_id.
	ClearYouTubeUpload(ctx context.Context, id int64) error
}

// SetYouTubeTargetPublicationStore assigns the YouTube target
// publication store used by the publish worker's Phase-2 bypass
// block. Pass nil to disable the bypass (worker falls through to
// publisher.Publish per the pre-fix behaviour, matching the
// canonicalCanaryUploader nil-safe pattern).
//
// Production wire-up: internal/bootstrap/app.go calls this with
// *repository.YouTubeTargetPublicationRepository right after
// NewPublishWorker.
func (w *PublishWorker) SetYouTubeTargetPublicationStore(s YouTubeTargetPublicationLookup) {
	w.ytPubLookup = s
}

// SetContentPackageStateSynchronizer wires the projection that keeps the
// product-level Content Package lifecycle aligned with target publication.
func (w *PublishWorker) SetContentPackageStateSynchronizer(s ContentPackageStateSynchronizer) {
	w.packageStateSync = s
}

// isCanaryEnabled (Task 7/10) returns true when the post's metadata
// JSON carries {"canary_upload": true}. Reads metadata via the
// dedicated GetMetadata repo call so the FindByID lockstep invariant
// stays isolated. Malformed JSON is treated as canary=false (safe
// default — documented here so future maintainers don't surface
// the error and accidentally flip semantics).
func (w *PublishWorker) isCanaryEnabled(ctx context.Context, post *models.Post) bool {
	if post == nil {
		return false
	}
	meta, err := w.postRepo.GetMetadata(post.ID)
	if err != nil || len(meta) == 0 {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(meta, &m); err != nil {
		return false
	}
	v, _ := m["canary_upload"].(bool)
	return v
}

// isOrphanedYouTubeVideo (Blocco #1 followup — Finding #4) is the
// classifier the publish worker uses to detect the Phase-1 orphan-video
// case: UpdateVideoPrivacy returned a 404 referencing our yt_pub row's
// youtube_video_id (user deleted the orphan via YouTube Studio, moderator
// takedown, etc.). Primary signal: typed sentinel errors.Is on
// services.ErrYouTubeVideoNotFound (wrapped in UpdateVideoPrivacy via
// Go 1.20+ multi-%w). Defense-in-depth substring fallback also fires
// when the err message contains BOTH the offending video_id AND a
// "not found" marker — covers any future code path not yet re-wired
// to wrap with the typed sentinel.
//
// Returns false for nil errors so the worker's caller can defensively
// `if isOrphanedYouTubeVideo(err, *ytPub.YouTubeVideoID)` without
// nil-checking first.
func isOrphanedYouTubeVideo(err error, videoID string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, services.ErrYouTubeVideoNotFound) {
		return true
	}
	if videoID == "" {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, videoID) && strings.Contains(strings.ToLower(msg), "not found")
}
