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
	var storageObjectKey, bucket *string
	if job.SourceType == models.UploadJobSourceVeloxArtifact {
		// Velox artifacts are already materialised in media_assets by the
		// Velox ingest consumer. SourceID is the remote artifact URL, not
		// the original upload source, so BuildUploadKey(UserID, SourceID)
		// would manufacture a key that does not belong to assetID. Keep
		// the canonical asset ID only; MediaDownloadResolver will resolve
		// the actual media_assets row and its upload key by ownership.
		mediaURL = ""
	} else {
		storageObjectKey = strPtr(key)
		bucketValue := storageBucket(w.storage)
		bucket = strPtr(bucketValue)
	}

	post := &models.Post{
		WorkspaceID:      job.WorkspaceID,
		Title:            job.Title,
		Caption:          job.Caption,
		Metadata:         append([]byte(nil), job.Metadata...),
		MediaURL:         mediaURL,
		MediaAssetID:     strPtr(assetID),
		StorageObjectKey: storageObjectKey,
		Bucket:           bucket,
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
	// via RETURNING id). The job claim NO LONGER runs the YouTube
	// uploads inline: processPublishJob materializes one delivery row
	// per YouTube target (state='ready_to_upload', one row per
	// (video, channel) pair) and the GLOBAL delivery pool
	// (runYouTubeDeliveryPool, bounded by YOUTUBE_UPLOAD_CONCURRENCY)
	// claims those rows independently. A single job with N channels
	// therefore fans out to N concurrent uploads instead of one
	// worker looping targets sequentially inside a single claim — the
	// multi-channel fan-out with a bounded global worker pool.
	//
	// Materialization is idempotent: UNIQUE(post_target_id) + the
	// FindByPostTargetID short-circuit mean a re-run of a retried
	// upload_job re-fetches the existing row instead of stacking a
	// duplicate delivery; rows already 'youtube_uploaded' are left
	// untouched (the delivery pool skips them on claim).
	if w.ytPubStore != nil {
		if err := w.materializeYouTubeDeliveries(ctx, job, targets, post); err != nil {
			return fmt.Errorf("materialize youtube deliveries: %w", err)
		}
	} else {
		w.logger.Warn("upload worker: ytPubStore not wired — per-target youtube delivery materialization skipped (publish-phase trigger will still fire)",
			"job_id", job.ID)
	}

	// Trigger publishing only for jobs that should publish NOW.
	// Future-scheduled jobs (job.PublishAt > now) stay in the
	// `status='queued'` state and the publish_worker picks them up
	// when publish_at <= now(). Calling PublishPost on a future post
	// would race the scheduler and risk an out-of-order publish.
	//
	// P1#4 — defense-in-depth keep this go-level gate. The publish pool
	// now claims at ingest_after (the preparation cursor), so future
	// rows must remain queued in the post table until publish_at. The
	// go-level check stays
	// for legacy single-file flows (POST /posts direct + cmd
	// binaries) where rows bypass the upload_jobs batching path and
	// the publish pool's CTE has no claim opportunity. A future
	// Taskilino can remove this check once every flow routes through the
	// canonical scheduled-post path.
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

	// Mark the job prepared or completed. CAS against workerID ensures a peer that
	// stole the lease (reaper release + peer's ClaimBatch
	// re-claim) cannot overwrite a peer's terminal write.
	if job.PublishAt != nil && job.PublishAt.After(time.Now()) {
		if prepared, ok := w.jobRepo.(PreparedUploadJobStore); ok {
			if err := prepared.MarkPrepared(ctx, job.ID, workerID, post.ID, assetID); err != nil {
				return fmt.Errorf("mark job prepared: %w", err)
			}
		} else if err := w.jobRepo.MarkCompleted(ctx, job.ID, workerID, post.ID, assetID); err != nil {
			// Compatibility fallback for legacy adapters. The post itself
			// remains protected by publish_at.
			return fmt.Errorf("mark scheduled job prepared: %w", err)
		}
		w.logger.Info("upload worker: preparation done; publish scheduled",
			"pool", "upload", "job_id", job.ID, "post_id", post.ID,
			"publish_at", job.PublishAt.Format(time.RFC3339))
		return nil
	}
	if err := w.jobRepo.MarkCompleted(ctx, job.ID, workerID, post.ID, assetID); err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}

	w.logger.Info("upload worker: publish done",
		"pool", "upload", "job_id", job.ID, "post_id", post.ID, "asset_id", assetID)
	return nil
}

// materializeYouTubeDeliveries enqueues one delivery row per YouTube
// target of the job (state='ready_to_upload'). The rows are the queue
// units the GLOBAL delivery pool consumes; this runs at job-claim time
// (upload pool) and does NOT touch YouTube — the heavy videos.insert
// happens per-delivery in runYouTubeDeliveryPool.
func (w *UploadWorker) materializeYouTubeDeliveries(
	ctx context.Context,
	job *models.UploadJob,
	targets []*models.PostTarget,
	post *models.Post,
) error {
	if w.ytPubStore == nil {
		return nil
	}
	for _, target := range targets {
		if target == nil {
			continue
		}
		account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
		if err != nil {
			return fmt.Errorf("FindPlatformAccountByID(%d) during delivery materialization: %w", target.PlatformAccountID, err)
		}
		if account == nil {
			return fmt.Errorf("nil platform account for id=%d during delivery materialization", target.PlatformAccountID)
		}
		if account.Platform != models.PlatformYouTube {
			// Per verdict: only YouTube gets the per-delivery private
			// upload step. TikTok / Instagram / Facebook keep using
			// publish_worker's synchronous upload+publish flow at
			// publish_at.
			continue
		}
		if err := w.materializeYouTubeDelivery(ctx, job, target, post); err != nil {
			return err
		}
	}
	return nil
}

// materializeYouTubeDelivery creates (or idempotently re-fetches) the
// single (video, channel) delivery row for one YouTube target.
// Idempotent: UNIQUE(post_target_id) + the FindByPostTargetID
// short-circuit mean a re-run of a retried upload_job re-fetches the
// existing row instead of stacking a duplicate delivery; rows already
// 'youtube_uploaded' are left untouched (the delivery pool skips them
// on claim).
func (w *UploadWorker) materializeYouTubeDelivery(
	ctx context.Context,
	job *models.UploadJob,
	target *models.PostTarget,
	post *models.Post,
) error {
	// Idempotency skip: a previous claim already uploaded this target.
	pub, err := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("FindByPostTargetID(target=%d): %w", target.ID, err)
	}
	if pub != nil && pub.YouTubeUploadStatus == "youtube_uploaded" {
		w.logger.Debug("upload worker: youtube delivery already uploaded (materialization skip)",
			"job_id", job.ID, "target_id", target.ID, "yt_pub_id", pub.ID)
		return nil
	}

	// Native scheduled publishing (migration 126): when the post has a
	// FUTURE publish_at AND the desired privacy is public, bake
	// status.publishAt into the private videos.insert so YouTube itself
	// owns the private→public transition at that time — the publish
	// worker then skips its videos.update (saves one ~50-unit call from
	// the 2026 general quota bucket per scheduled public video). All
	// other cases (immediate publish, past publish_at, desired privacy
	// unlisted/private) pass nil and keep the videos.update path.
	desiredPrivacy := resolveDesiredPrivacyForTarget(post, target)
	var nativePublishAt *time.Time
	if post.PublishAt != nil && post.PublishAt.After(time.Now()) && desiredPrivacy == "public" {
		nativePublishAt = post.PublishAt
	}

	if pub == nil {
		pub = &models.YouTubeTargetPublication{
			UploadJobID:         job.ID,
			PostTargetID:        target.ID,
			PlatformAccountID:   target.PlatformAccountID,
			YouTubeUploadStatus: "youtube_uploading",
			DesiredPrivacy:      desiredPrivacy,
			PublishAt:           post.PublishAt,
			// Stamped when the upload actually carries status.publishAt so
			// the publish phase can skip the redundant videos.update.
			NativePublishAt: nativePublishAt,
			// Delivery-queue cursor (migration 125): claimable by the
			// global delivery pool. priority mirrors upload_jobs.priority;
			// max_attempts mirrors the queue retry cap.
			State:       "ready_to_upload",
			Priority:    int16(job.Priority),
			MaxAttempts: 8,
		}
		if snapshot, ok := snapshotForAccount(post.Metadata, target.PlatformAccountID); ok && snapshot.ThumbnailMediaID != "" {
			pub.ThumbnailMediaID = strPtr(snapshot.ThumbnailMediaID)
			status := "pending"
			pub.ThumbnailStatus = &status
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
	return nil
}

// processYouTubeDelivery runs the private upload for ONE claimed
// (video, channel) delivery row. This is the unit of work of the
// global delivery pool: N channels of the same video are claimed and
// processed concurrently by different pool workers, each with its own
// lease, heartbeat and retry budget — a slow channel can no longer
// block its siblings inside a single job claim.
func (w *UploadWorker) processYouTubeDelivery(
	ctx context.Context,
	delivery *models.YouTubeTargetPublication,
	workerID string,
) error {
	if delivery == nil {
		return fmt.Errorf("process youtube delivery: nil delivery")
	}
	if w.ytPubStore == nil {
		return nil
	}
	// Idempotent skip: a previous claim uploaded this row.
	if delivery.YouTubeUploadStatus == "youtube_uploaded" {
		if err := w.ytPubStore.ReleaseDeliveryLease(ctx, delivery.ID, workerID); err != nil {
			return fmt.Errorf("release lease on already-uploaded delivery %d: %w", delivery.ID, err)
		}
		return nil
	}
	if w.deliveryPostStore == nil {
		return fmt.Errorf("process youtube delivery %d: delivery post store not wired", delivery.ID)
	}
	target, err := w.deliveryPostStore.FindTargetByID(delivery.PostTargetID)
	if err != nil {
		return fmt.Errorf("find post target %d for delivery %d: %w", delivery.PostTargetID, delivery.ID, err)
	}
	if target == nil {
		return w.failYouTubeDelivery(ctx, delivery, workerID, "target_missing",
			fmt.Sprintf("post_target %d not found", delivery.PostTargetID))
	}
	post, err := w.deliveryPostStore.FindByID(target.PostID)
	if err != nil {
		return fmt.Errorf("find post %d for delivery %d: %w", target.PostID, delivery.ID, err)
	}
	if post == nil {
		return w.failYouTubeDelivery(ctx, delivery, workerID, "post_missing",
			fmt.Sprintf("post %d not found", target.PostID))
	}

	if err := w.uploadVideoAsPrivateForDelivery(ctx, delivery, target, post, workerID); err != nil {
		// Route to retry_wait / dead_letter with exponential backoff.
		backoff := deliveryRetryBackoff(delivery.AttemptCount)
		if fErr := w.ytPubStore.MarkDeliveryFailed(ctx, delivery.ID, workerID, "upload_failed", err.Error(), time.Now().Add(backoff)); fErr != nil {
			w.logger.Warn("upload worker: MarkDeliveryFailed failed", "delivery_id", delivery.ID, "error", fErr)
		}
		return err
	}
	return nil
}

// failYouTubeDelivery dead-letters a delivery whose dependency rows are
// missing (post/target gone — permanent; retrying cannot fix it). The
// retry loop exhausts to dead_letter via MarkDeliveryFailed.
func (w *UploadWorker) failYouTubeDelivery(
	ctx context.Context,
	delivery *models.YouTubeTargetPublication,
	workerID, code, message string,
) error {
	backoff := deliveryRetryBackoff(delivery.AttemptCount)
	if err := w.ytPubStore.MarkDeliveryFailed(ctx, delivery.ID, workerID, code, message, time.Now().Add(backoff)); err != nil {
		w.logger.Warn("upload worker: MarkDeliveryFailed (permanent) failed", "delivery_id", delivery.ID, "error", err)
	}
	return fmt.Errorf("%s: %s (delivery=%d)", code, message, delivery.ID)
}

// deliveryRetryBackoff computes the exponential backoff delay for a
// failed delivery attempt: 1m, 2m, 4m, … capped at 30m.
func deliveryRetryBackoff(attempt int) time.Duration {
	const (
		base = time.Minute
		max  = 30 * time.Minute
	)
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 30 {
		attempt = 30
	}
	d := base * time.Duration(1<<uint(attempt))
	if d > max {
		return max
	}
	return d
}

// uploadVideoAsPrivateForDelivery performs the per-(video, channel)
// YouTube resumable upload-as-private for a single claimed delivery
// row. The upload lands regardless of publish_at so the rest of the
// pipeline (Velox thumbnail editor, etc.) can resolve to a real
// youtube_video_id immediately. publish_at remains on the delivery row
// for the LATER publish phase (Blocco #1 phase 2, owned by
// publish_worker).
func (w *UploadWorker) uploadVideoAsPrivateForDelivery(
	ctx context.Context,
	delivery *models.YouTubeTargetPublication,
	target *models.PostTarget,
	post *models.Post,
	workerID string,
) error {
	if w.ytPubStore == nil {
		w.logger.Warn("upload worker: ytPubStore unset at delivery upload time (skipping)",
			"delivery_id", delivery.ID, "post_target_id", delivery.PostTargetID)
		return nil
	}
	if target == nil || target.ID == 0 {
		return fmt.Errorf("per-delivery private upload on nil/zero-id target (delivery_id=%d)", delivery.ID)
	}
	if delivery.PlatformAccountID == 0 {
		return fmt.Errorf("per-delivery private upload on delivery with platform_account_id=0 (delivery_id=%d)", delivery.ID)
	}

	// Idempotency skip: a previous claim already stamped
	// youtube_upload_status='youtube_uploaded' on this row. Re-runs
	// would otherwise re-fire a fresh videos.insert for the same
	// channel (wasted YouTube quota + a duplicate video on the channel).
	// UNIQUE(post_target_id) keeps the row singular; this check is a
	// CPU-only short-circuit on top of that.
	if delivery.YouTubeUploadStatus == "youtube_uploaded" {
		w.logger.Debug("upload worker: delivery already uploaded (idempotent skip)",
			"delivery_id", delivery.ID, "post_target_id", delivery.PostTargetID)
		return nil
	}

	// Resolve platform_account so we know the channel + grant.
	account, err := w.userRepo.FindPlatformAccountByID(delivery.PlatformAccountID)
	if err != nil {
		return fmt.Errorf("FindPlatformAccountByID(%d): %w", delivery.PlatformAccountID, err)
	}
	if account == nil {
		return fmt.Errorf("nil platform account for id=%d", delivery.PlatformAccountID)
	}
	if account.Platform != models.PlatformYouTube {
		// Per verdict: only YouTube gets the per-delivery private
		// upload step. TikTok / Instagram / Facebook keep using
		// publish_worker's synchronous upload+publish flow at
		// publish_at.
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
	// (non-recoverable without user re-auth) so we route the delivery
	// to blocked_auth + reauth_required + DON'T retry the row.
	if binder, hasBinder := provider.(services.YouTubeChannelBinder); hasBinder {
		bindErr := binder.ValidateChannelBinding(ctx, oauthToken.AccessToken, account.PlatformUserID)
		if bindErr != nil {
			if errors.Is(bindErr, services.ErrYouTubeChannelMismatch) {
				if err := w.handleTargetBlockedAuth(ctx, delivery, account, post.ID, bindErr.Error(), workerID); err != nil {
					w.logger.Warn("upload worker: handleTargetBlockedAuth partial-failure (delivery terminal-failed)",
						"delivery_id", delivery.ID, "platform_account_id", account.ID, "error", err)
				}
				return nil
			}
			// Transient (5xx, network, decode) — retry.
			return fmt.Errorf("channel binding check platform_account=%d (transient): %w", account.ID, bindErr)
		}
	}

	// The delivery row already exists (materialized at job-claim time
	// with state='ready_to_upload'); the native-publish stamp decided at
	// materialization (migration 126) is passed through to the
	// videos.insert — YouTube owns the private→public transition at
	// publish_at and the publish worker skips its videos.update (saves
	// one ~50-unit call from the 2026 general quota bucket per
	// scheduled public video).
	var nativePublishAt *time.Time
	if delivery.NativePublishAt != nil {
		nativePublishAt = delivery.NativePublishAt
	}

	// Resolve the UploadChannelUploader capability + start the upload.
	uploader, ok := provider.(services.UploadChannelUploader)
	if !ok {
		return fmt.Errorf("provider for %s does not implement UploadChannelUploader (YouTubeOAuthService implements it; bootstrap must register the capability)", account.Platform)
	}
	if w.resolver == nil {
		return fmt.Errorf("resolve media asset for private YouTube upload: media download resolver is not configured")
	}
	mediaURL, err := w.resolver.ResolveForUpload(ctx, post, time.Hour)
	if err != nil {
		if errors.Is(err, services.ErrAssetExpired) {
			return fmt.Errorf("resolve media asset for private YouTube upload: media asset expired; re-upload required")
		}
		return fmt.Errorf("resolve media asset for private YouTube upload: %w", err)
	}
	// Google 2026 quota gate — reserve the videos.insert charge BEFORE
	// the API call. Fail-closed: when the gate cannot decide (DB down
	// etc.) we do NOT call YouTube, we bubble the error up to the
	// retry path. When the video_uploads bucket is exhausted for the
	// Pacific day, the delivery is parked in 'quota_wait' with
	// next_attempt_at = daily reset (NOT a failed attempt: the retry
	// budget is untouched and the row resumes after the reset).
	if w.quotaGate != nil {
		allowed, retryAfter, qErr := w.quotaGate.ReserveOperation(ctx, services.YouTubeOpVideoInsert)
		if qErr != nil {
			return fmt.Errorf("youtube quota reserve videos.insert delivery=%d: %w", delivery.ID, qErr)
		}
		if !allowed {
			if retryAfter <= 0 {
				retryAfter = 3600
			}
			w.logger.Warn("upload worker: video_uploads bucket exhausted; parking delivery in quota_wait",
				"delivery_id", delivery.ID, "retry_after_seconds", retryAfter)
			if qErr := w.ytPubStore.MarkDeliveryQuotaWait(ctx, delivery.ID, workerID,
				time.Now().Add(time.Duration(retryAfter)*time.Second)); qErr != nil {
				w.logger.Warn("upload worker: MarkDeliveryQuotaWait failed (delivery left claimed)",
					"delivery_id", delivery.ID, "error", qErr)
			}
			return nil
		}
	}

	videoID, err := uploader.UploadVideoAsPrivate(ctx, oauthToken.AccessToken, post, mediaURL, nativePublishAt)
	if err != nil {
		// A real API failure (5xx / transport / validation): record it
		// against the video_uploads bucket (informational counter — the
		// reserved charge already stands, Google counts the call even
		// when it fails), then bubble up so processYouTubeDelivery
		// routes the row to retry_wait / dead_letter via
		// MarkDeliveryFailed (attempt++ + last_error + backoff in ONE
		// atomic UPDATE — the split-tx rationale of
		// MarkYouTubeUploadedAtomic applies here too).
		if w.quotaGate != nil {
			if rErr := w.quotaGate.RecordError(ctx, services.YouTubeQuotaBucketVideoUploads); rErr != nil {
				w.logger.Warn("upload worker: RecordError(video_uploads) failed",
					"delivery_id", delivery.ID, "error", rErr)
			}
		}
		return fmt.Errorf("UploadVideoAsPrivate delivery=%d target=%d: %w", delivery.ID, target.ID, err)
	}
	if videoID == "" {
		return fmt.Errorf("UploadVideoAsPrivate delivery=%d target=%d returned empty videoID", delivery.ID, target.ID)
	}

	// Transition the delivery row: state='youtube_uploaded' +
	// youtube_upload_status='youtube_uploaded' + youtube_video_id +
	// attempt++ + lease release in one atomic UPDATE.
	if err := w.ytPubStore.MarkDeliveryUploaded(ctx, delivery.ID, workerID, videoID); err != nil {
		return fmt.Errorf("MarkDeliveryUploaded(delivery=%d, videoID=%s): %w", delivery.ID, videoID, err)
	}
	w.logger.Info("upload worker: youtube delivery private upload OK",
		"delivery_id", delivery.ID, "job_id", delivery.UploadJobID, "target_id", target.ID,
		"platform_account_id", account.ID, "youtube_video_id", videoID)
	return nil
}

// handleTargetBlockedAuth centralizes the per-delivery side effects on
// a channels.list(mine=true) mismatch:
//  1. persist the delivery row: state='failed' + attempt++ +
//     last_error + last_error_code + lease release (MarkDeliveryBlockedAuth),
//     so dashboards + the unified pipeline view surface the failure cause.
//  2. set post_target.status='blocked_auth' so the publish worker
//     skips the row (and any "schedule it again" UI flow prompts
//     re-connect first).
//  3. set platform_account.status='reauth_required' (P0#3 server-side
//     channel-binding guard) so the operator's UI prompts the user to
//     reconnect.
//
// All side effects are best-effort — a partial failure logs WARN and
// returns nil. The caller treats a nil result as "delivery done
// (terminal failed, no retry)".
func (w *UploadWorker) handleTargetBlockedAuth(
	ctx context.Context,
	delivery *models.YouTubeTargetPublication,
	account *models.PlatformAccount,
	postID int64,
	reason string,
	workerID string,
) error {
	w.logger.Warn("upload worker: youtube channel binding mismatch; routing delivery to failed/blocked_auth",
		"delivery_id", delivery.ID, "post_id", postID, "platform_account_id", account.ID,
		"expected_channel_id", account.PlatformUserID, "reason", reason)

	// (1) Persist the delivery row: state='failed' + attempt++ +
	// last_error + last_error_code + lease release in one atomic UPDATE.
	if bErr := w.ytPubStore.MarkDeliveryBlockedAuth(ctx, delivery.ID, workerID,
		"youtube_channel_mismatch: "+reason); bErr != nil {
		w.logger.Warn("upload worker: MarkDeliveryBlockedAuth failed (continuing)",
			"delivery_id", delivery.ID, "error", bErr)
	}

	// (2) post_target.status='blocked_auth'. error_message stamps the
	// mismatch reason for the operator's audit log.
	if tErr := w.postStore.SetTargetStatus(ctx, delivery.PostTargetID, models.PostStatusBlockedAuth,
		"youtube channel mismatch: "+reason); tErr != nil {
		w.logger.Warn("upload worker: post_target SetTargetStatus(blocked_auth) failed (continuing)",
			"post_target_id", delivery.PostTargetID, "error", tErr)
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

func resolveDesiredPrivacyForTarget(post *models.Post, target *models.PostTarget) string {
	if target != nil {
		if snapshot, ok := snapshotForAccount(post.Metadata, target.PlatformAccountID); ok && snapshot.PrivacyStatus != "" {
			return snapshot.PrivacyStatus
		}
	}
	return resolveDesiredPrivacy(post)
}
