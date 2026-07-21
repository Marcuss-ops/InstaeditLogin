package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// executePublish performs the actual platform publish call and finalizes
// the target state. It waits for the per-platform throttle, calls
// Publisher.Publish, and handles both synchronous and asynchronous
// outcomes. After a successful sync publish it also fires the optional
// post-completion delivery hook.
func (w *PublishWorker) executePublish(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, post *models.Post, oauthToken *models.OAuthToken, payload models.PublishPayload, publisher services.Publisher) error {
	// FASE 1.3: throttle per-platform API calls to avoid rate-limit
	// bans. If the throttle is nil (test mode), skip. If the platform's
	// bucket is empty, Wait() blocks until a token is available or
	// ctx is cancelled (graceful shutdown).
	if w.throttle != nil {
		if err := w.throttle.Wait(ctx, account.Platform); err != nil {
			return fmt.Errorf("throttle wait for %s: %w", account.Platform, err)
		}
	}

	result, err := publisher.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
	if err != nil {
		return w.markFailed(target, err.Error())
	}

	// ASYNC PUBLISH (Taglio 4.2): if the platform has the
	// AsyncPublisher capability, the Publish() call returned a
	// publish_id (in result.PlatformMediaID) but did NOT complete
	// the publish — the platform is still processing. We store the
	// publish_id on the target and KEEP status='publishing' (the
	// claim already wrote 'publishing' to the DB; we just need to
	// ensure UpdateStatus doesn't revert it back to the in-memory
	// 'queued' value the target struct carries from ListPending).
	// The ReconcileWorker goroutine will pick this target up on
	// subsequent ticks and drive the state machine to completion.
	if _, isAsync := w.router.AsyncPublisher(account.Platform); isAsync && result.PlatformMediaID != "" {
		target.PlatformPostID = result.PlatformMediaID
		target.Status = models.PostStatusPublishing // preserve the claim's status transition
		w.logger.Info("async publish initiated, reconciler will poll",
			"target_id", target.ID, "platform", account.Platform,
			"publish_id", result.PlatformMediaID)
		if err := w.postRepo.UpdateStatus(target); err != nil {
			return fmt.Errorf("update status for async publish: %w", err)
		}
		return nil
	}

	// SYNC PUBLISH: transition publishing → published.
	target.Status = models.PostStatusPublished
	target.PlatformPostID = result.PlatformMediaID
	now := time.Now()
	target.PublishedAt = &now
	if err := w.postRepo.UpdateStatus(target); err != nil {
		return fmt.Errorf("transition to published: %w", err)
	}
	// Post-completion dispatch (Task 7/10): fire DeliveryRegistry
	// for the platform_account.Platform key. Best-effort: a missing
	// provider OR a Deliver error is warn-logged and NOT propagated
	// (the publish row is already in 'published' state; a rollback
	// would cause a retry that double-uploads). Asset is a zero-
	// value placeholder: the YouTube adapter is a no-op forward
	// (doesn't re-publish) so the asset is decoration only; Drive
	// + Velox are stubs and the registry's nil-asset path returns
	// ErrDeliveryProviderNotImplemented which the helper swallows.
	// The Drive exporter needs the actual staged media URL and its size;
	// post.MediaURL is the canonical object URL produced by ingest.
	w.dispatchPostCompletion(ctx, target, account, &models.MediaAsset{
		ID: post.MediaURL, UploadKey: post.MediaURL, ContentType: "video/mp4",
	}, post.MediaURL)
	return nil
}
