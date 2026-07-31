package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// processPublishJob handles the S3 → post → YouTube publish path.
// Assumes the row is in 'ready_to_publish' state with asset_id set.
func (w *UploadWorker) processPublishJob(ctx context.Context, job *models.UploadJob, workerID string) error {
	if job.AssetID == nil || *job.AssetID == "" {
		return fmt.Errorf("publish job %d missing asset_id; ingest did not complete", job.ID)
	}
	assetID := *job.AssetID

	key := services.BuildUploadKey(job.UserID, job.SourceID)
	mediaURL := w.storage.AssetURL(key)

	post := &models.Post{
		WorkspaceID:      job.WorkspaceID,
		Title:            job.Title,
		Caption:          job.Caption,
		MediaURL:         mediaURL,
		MediaAssetID:     strPtr(assetID),
		StorageObjectKey: strPtr(key),
		Bucket:           strPtr(storageBucket(w.storage)),
		Status:           models.PostStatusQueued,
		// P1#4 — IngestAfter is server-side DEFAULT NOW() at SQL
		// level; we pass job.IngestAfter through so a queued
		// ingest-after-future row preserves its ingest schedule.
		IngestAfter: job.IngestAfter,
		// PublishAt stamps the user-facing "what time should this
		// fire" cursor onto the created post. The publish_worker
		// ListPending predicate (queries.go::qSelectPendingTargets)
		// gates on publish_at <= NOW(), so the post stays queued
		// until the cursor elapses.
		PublishAt: job.PublishAt,
		// P1 (migration 053) — propagate the inherited batch default
		// onto the post. The publish_worker uses this as the middle
		// term of the precedence cascade:
		//   payload override (post.PrivacyLevel) > post.DefaultPrivacyLevel
		//   > "unlisted" (YouTube fallback) > PUBLIC_TO_EVERYONE (other platforms)
		// post.PrivacyLevel is left empty by this flow — the operator
		// sets it explicitly via the post-update endpoint when they want a
		// per-post override.
		DefaultPrivacyLevel: job.DefaultPrivacyLevel,
		// Blocco #1 P0 — FIXED via migration 077: stamp the upload_job_id
		// onto the post so PostRepository.Create's ON CONFLICT
		// (upload_job_id) DO NOTHING path can re-use the existing row on
		// a MarkRetry instead of stacking phantom posts. The pointer
		// &job.ID is taken because models.Post.UploadJobID is *int64
		// (Migration 077 made the column nullable + partial-unique so
		// the HTTP /api/v1/posts path can leave it nil and coexist).
		UploadJobID: &job.ID,
	}
	targets := make([]*models.PostTarget, 0, len(job.Targets))
	for _, accountID := range job.Targets {
		targets = append(targets, &models.PostTarget{
			PlatformAccountID: accountID,
			Status:            models.PostStatusQueued,
		})
	}
	if err := w.postStore.Create(post, targets); err != nil {
		return fmt.Errorf("create post: %w", err)
	}

	// Blocco #1 P0 — per-target YouTube private upload phase. Runs
	// AFTER post + targets are persisted (so target.ID is populated
	// via RETURNING id) and BEFORE the publish_at gate below. The
	// upload lands as privacy='private' immediately so the rest of
	// the pipeline (Velox thumbnail editor, etc.) can resolve to a
	// real youtube_video_id without waiting on the user's calendar
	// cursor. publish_at stays on the post_target row for the LATER
	// videos.update phase (Blocco #1 phase 2, owned by publish_worker).
	//
	// Inside the loop, transient failures bubble up so handleProcessingError
	// MarkRetry's the parent upload_job (next claim re-runs the helper
	// idempotently — UNIQUE(post_target_id) on the YT pub table means
	// re-runs hit the existing row + idempotently stamp status).
	// blocked_auth (channel-binding mismatch) is handled IN-band: the
	// helper routes that target to status='blocked_auth' and returns
	// nil so the parent job can continue for OTHER targets.
	if w.ytPubStore != nil {
		for _, target := range targets {
			if target == nil {
				continue
			}
			if err := w.uploadVideoAsPrivateForTarget(ctx, job, target, post, mediaURL); err != nil {
				return fmt.Errorf("per-target youtube private upload target=%d: %w", target.ID, err)
			}
		}
	} else {
		w.logger.Warn("upload worker: ytPubStore not wired — per-target youtube private upload skipped (publish-phase trigger will still fire)",
			"job_id", job.ID)
	}

	// Trigger publishing only for jobs that should publish NOW.
	// Future-scheduled jobs (job.PublishAt > now) stay in the
	// `status='queued'` state and the publish_worker picks them up
	// when publish_at <= now(). Calling PublishPost on a future post
	// would race the scheduler and risk an out-of-order publish.
	//
	// P1#4 — defense-in-depth keep this go-level gate: ingest and
	// publish pools are separate goroutines; the publish pool's
	// ClaimBatchForPublish CTE also gates on (publish_at IS NULL OR
	// publish_at <= NOW()) so under normal conditions a row claimed
	// here already has publish_at <= now. The go-level check stays
	// for legacy single-file flows (POST /posts direct + cmd
	// binaries) where rows bypass the upload_jobs batching path and
	// the publish pool's CTE has no claim opportunity. A future
	// Taskilino can remove this check once every flow routes through
	// ClaimBatchForPublish.
	if job.PublishAt == nil || !job.PublishAt.After(time.Now()) {
		if err := w.postStore.PublishPost(post.ID); err != nil {
			return fmt.Errorf("trigger publish: %w", err)
		}
	} else {
		w.logger.Info("upload worker: post scheduled for future publish",
			"job_id", job.ID, "post_id", post.ID, "publish_at", job.PublishAt.Format(time.RFC3339))
	}

	// P2 — publish-phase throughput counter. Increment on the
	// hot path after the post + targets are persisted but BEFORE
	// the MarkCompleted CAS so a worker crash between persist
	// and the CAS double-counts on retry (acceptable: the
	// operator's "throughput" is a 5-minute rate over a counter,
	// not a strict sum, so a one-byte overcount per failed
	// transition is invisible at the dashboard).
	if assetID != "" && job.TotalBytes != nil && *job.TotalBytes > 0 {
		metrics.RecordUploadBytes(models.PlatformYouTube, "publish", *job.TotalBytes)
	}

	// Mark job completed. CAS against workerID ensures a peer that
	// stole the lease (reaper release + peer's ClaimBatch
	// re-claim) cannot overwrite a peer's terminal write.
	if err := w.jobRepo.MarkCompleted(ctx, job.ID, workerID, post.ID, assetID); err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}

	w.logger.Info("upload worker: publish done",
		"pool", "upload", "job_id", job.ID, "post_id", post.ID, "asset_id", assetID)
	return nil
}

func (w *UploadWorker) uploadVideoAsPrivateForTarget(
	ctx context.Context,
	job *models.UploadJob,
	target *models.PostTarget,
	post *models.Post,
	mediaURL string,
) error {
	if w.ytPubStore == nil {
		w.logger.Warn("upload worker: ytPubStore unset at per-target upload time (skipping)",
			"job_id", job.ID, "target_id", target.ID)
		return nil
	}
	if target == nil || target.ID == 0 {
		return fmt.Errorf("per-target private upload on nil/zero-id target (PostRepository.Create must populate via RETURNING id)")
	}
	if target.PlatformAccountID == 0 {
		return fmt.Errorf("per-target private upload on target with platform_account_id=0 (target_id=%d)", target.ID)
	}

	// Resolve platform_account so we know the channel + grant.
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return fmt.Errorf("FindPlatformAccountByID(%d): %w", target.PlatformAccountID, err)
	}
	if account == nil {
		return fmt.Errorf("nil platform account for id=%d", target.PlatformAccountID)
	}
	if account.Platform != models.PlatformYouTube {
		// Per verdict: only YouTube gets the per-target private upload
		// step. TikTok / Instagram / Facebook keep using publish_worker's
		// synchronous upload+publish flow at publish_at.
		return nil
	}

	provider, has := w.capRouter.Get(account.Platform)
	if !has {
		return fmt.Errorf("provider not found for platform=%s", account.Platform)
	}

	// Token refresh via vault.Renew + OAuthProvider.RefreshOAuthToken.
	// Mirrors publish_worker_process.go::prepareCredentials.
	oauthProvider, ok := provider.(services.OAuthProvider)
	if !ok {
		return fmt.Errorf("provider for %s does not implement OAuthProvider", account.Platform)
	}
	refresher := credentials.TokenRefresher(func(c context.Context, refreshToken string) (*models.TokenData, error) {
		return oauthProvider.RefreshOAuthToken(c, refreshToken)
	})
	var oauthToken *models.OAuthToken
	if account.Platform == models.PlatformYouTube {
		oauthToken, err = credentials.RenewYouTubeToken(ctx, w.vault, account.ID, refresher, w.logger)
	} else {
		oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	}
	if err != nil {
		// Same transient-classify as publish_worker::prepareCredentials:
		// retry via outer MarkRetry. The helper deliberately returns a
		// token-free generic error for YouTube.
		return fmt.Errorf("token refresh for platform_account=%d", account.ID)
	}

	// Channel-binding check (channels.list mine=true verify) — same
	// pre-flight publish_worker drives. Mismatch is structural
	// (non-recoverable without user re-auth) so we route to
	// blocked_auth + reauth_required + DON'T fail the parent job.
	if binder, hasBinder := provider.(services.YouTubeChannelBinder); hasBinder {
		bindErr := binder.ValidateChannelBinding(ctx, oauthToken.AccessToken, account.PlatformUserID)
		if bindErr != nil {
			if errors.Is(bindErr, services.ErrYouTubeChannelMismatch) {
				if err := w.handleTargetBlockedAuth(ctx, target, account, post.ID, bindErr.Error()); err != nil {
					w.logger.Warn("upload worker: handleTargetBlockedAuth partial-failure (continuing with parent job)",
						"target_id", target.ID, "platform_account_id", account.ID, "error", err)
				}
				return nil
			}
			// Transient (5xx, network, decode) — retry.
			return fmt.Errorf("channel binding check platform_account=%d (transient): %w", account.ID, bindErr)
		}
	}

	// Create or fetch the per-target publication row. The Create path
	// stamps server-side fields (id, created_at, updated_at) and lands
	// with youtube_upload_status='youtube_uploading' (DB DEFAULT).
	pub, err := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("FindByPostTargetID(target=%d): %w", target.ID, err)
	}
	// Idempotency skip: a previous claim's helper already stamped
	// youtube_upload_status='youtube_uploaded' on this target. Re-runs
	// would otherwise re-fire a fresh videos.insert for the same
	// channel (wasted YouTube quota + a duplicate video on the channel).
	// UNIQUE(post_target_id) keeps the row singular; this check is a
	// CPU-only short-circuit on top of that. The retry path still
	// re-runs the channel-binding check + DB writes (idempotent) so a
	// crash mid-MarkYouTubeUploaded is recoverable (next claim finds a
	// row with status='youtube_uploading' or 'failed' and retries).
	if pub != nil && pub.YouTubeUploadStatus == "youtube_uploaded" {
		w.logger.Debug("upload worker: per-target youtube already uploaded (idempotent skip)",
			"job_id", job.ID, "target_id", target.ID, "yt_pub_id", pub.ID)
		return nil
	}
	if pub == nil {
		pub = &models.YouTubeTargetPublication{
			UploadJobID:         job.ID,
			PostTargetID:        target.ID,
			PlatformAccountID:   account.ID,
			YouTubeUploadStatus: "youtube_uploading",
			DesiredPrivacy:      resolveDesiredPrivacy(post),
			PublishAt:           post.PublishAt,
		}
		if err := w.ytPubStore.Create(ctx, pub); err != nil {
			// UNIQUE violation on post_target_id OR a peer raced to
			// create — re-fetch and continue without re-creating.
			existing, eErr := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
			if eErr == nil && existing != nil {
				pub = existing
			} else {
				return fmt.Errorf("Create youtube_target_publication: %w", err)
			}
		}
	}

	// Resolve the UploadChannelUploader capability + start the upload.
	uploader, ok := provider.(services.UploadChannelUploader)
	if !ok {
		return fmt.Errorf("provider for %s does not implement UploadChannelUploader (YouTubeOAuthService implements it; bootstrap must register the capability)", account.Platform)
	}
	if w.resolver == nil {
		return fmt.Errorf("resolve media asset for private YouTube upload: media download resolver is not configured")
	}
	mediaURL, err = w.resolver.ResolveForUpload(ctx, post, time.Hour)
	if err != nil {
		if errors.Is(err, services.ErrAssetExpired) {
			return fmt.Errorf("resolve media asset for private YouTube upload: media asset expired; re-upload required")
		}
		return fmt.Errorf("resolve media asset for private YouTube upload: %w", err)
	}
	videoID, err := uploader.UploadVideoAsPrivate(ctx, oauthToken.AccessToken, post, mediaURL)
	if err != nil {
		// Stamp attempt + last_error then bubble up so the parent
		// upload_job retry path picks up the row on its next claim.
		if incErr := w.ytPubStore.IncrementAttempt(ctx, pub.ID, fmt.Sprintf("upload failed: %v", err)); incErr != nil {
			w.logger.Warn("upload worker: IncrementAttempt failed (continuing with parent error)",
				"yt_pub_id", pub.ID, "target_id", target.ID, "error", incErr)
		}
		return fmt.Errorf("UploadVideoAsPrivate target=%d: %w", target.ID, err)
	}
	if videoID == "" {
		return fmt.Errorf("UploadVideoAsPrivate target=%d returned empty videoID", target.ID)
	}

	// Transition the per-target row: status='youtube_uploaded' +
	// youtube_video_id set. Blocco #1 followup — Finding #3 split-tx
	// drift fix: use MarkYouTubeUploadedAtomic instead of the
	// standalone MarkYouTubeUploaded so the attempt_count++ bump is
	// folded into the same row-level Postgres UPDATE. Row-level UPDATEs
	// are ACID-atomic, so a worker crash mid-call cannot leave the
	// row in status='youtube_uploading' with attempt_count bumped (the
	// pre-fix failure mode that produced orphan videos.insert on the
	// next claim).
	if err := w.ytPubStore.MarkYouTubeUploadedAtomic(ctx, pub.ID, videoID); err != nil {
		return fmt.Errorf("MarkYouTubeUploadedAtomic(pub=%d, videoID=%s): %w", pub.ID, videoID, err)
	}
	w.logger.Info("upload worker: per-target youtube private upload OK",
		"job_id", job.ID, "target_id", target.ID, "platform_account_id", account.ID, "youtube_video_id", videoID)
	return nil
}

// handleTargetBlockedAuth centralizes the per-target side effects on a
// channels.list(mine=true) mismatch:
//  1. persist last_error on youtube_target_publications (status='failed' + attempt++),
//     so dashboards + the unified pipeline view surface the failure cause.
//  2. set post_target.status='blocked_auth' so the publish worker
//     skips the row (and any "schedule it again" UI flow prompts
//     re-connect first).
//  3. set platform_account.status='reauth_required' (P0#3 server-side
//     channel-binding guard) so the operator's UI prompts the user to
//     reconnect.
//
// All side effects are best-effort — a partial failure logs WARN and
// returns nil so the parent job continues for OTHER targets. The
// uploadVideoAsPrivateForTarget caller treats a nil result as a
// "target done" so the loop advances to the next target.
func (w *UploadWorker) handleTargetBlockedAuth(
	ctx context.Context,
	target *models.PostTarget,
	account *models.PlatformAccount,
	postID int64,
	reason string,
) error {
	w.logger.Warn("upload worker: youtube channel binding mismatch; routing target to blocked_auth",
		"target_id", target.ID, "post_id", postID, "platform_account_id", account.ID,
		"expected_channel_id", account.PlatformUserID, "reason", reason)

	// (1) Persist YT pub row's last_error + attempted-count. Find
	// first (idempotent — may already exist from a prior partial upload).
	pub, err := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
	if err == nil && pub != nil {
		if uErr := w.ytPubStore.Update(ctx, &models.YouTubeTargetPublication{
			ID:                  pub.ID,
			UploadJobID:         pub.UploadJobID,
			PostTargetID:        pub.PostTargetID,
			PlatformAccountID:   pub.PlatformAccountID,
			YouTubeUploadStatus: "failed",
			DesiredPrivacy:      pub.DesiredPrivacy,
			PublishAt:           pub.PublishAt,
			LastError:           "youtube_channel_mismatch: " + reason,
			AttemptCount:        pub.AttemptCount + 1,
			CreatedAt:           pub.CreatedAt,
			UpdatedAt:           time.Now().UTC(),
		}); uErr != nil {
			w.logger.Warn("upload worker: YT pub row Update on blocked_auth failed (continuing)",
				"yt_pub_id", pub.ID, "target_id", target.ID, "error", uErr)
		}
	}

	// (2) post_target.status='blocked_auth'. error_message stamps the
	// mismatch reason for the operator's audit log.
	if tErr := w.postStore.SetTargetStatus(ctx, target.ID, models.PostStatusBlockedAuth,
		"youtube channel mismatch: "+reason); tErr != nil {
		w.logger.Warn("upload worker: post_target SetTargetStatus(blocked_auth) failed (continuing)",
			"target_id", target.ID, "error", tErr)
	}

	// (3) platform_account.status='reauth_required' (mirrors
	// publish_worker_process.go::prepareCredentials).
	if aErr := w.userRepo.MarkReauthRequired(ctx, account.ID, "youtube_channel_mismatch", reason); aErr != nil {
		w.logger.Warn("upload worker: userRepo.MarkReauthRequired failed (continuing)",
			"platform_account_id", account.ID, "error", aErr)
	}
	return nil
}

// resolveDesiredPrivacy mirrors the publish_worker_process.go buildPayload
// cascade (post.PrivacyLevel > post.DefaultPrivacyLevel > "unlisted"
// YouTube-safe fallback). Used at Create-time of the per-target
// youtube_target_publications row so the row snapshots the EVENTUAL
// desired privacy the publish phase will target via videos.update.
// The upload ITSELF always uses "private" (independent of this
// snapshot) — the publish phase flips to the snapshot value at
// publish_at.
func resolveDesiredPrivacy(post *models.Post) string {
	if post.PrivacyLevel != "" {
		return post.PrivacyLevel
	}
	if post.DefaultPrivacyLevel != "" {
		return post.DefaultPrivacyLevel
	}
	return "unlisted"
}
