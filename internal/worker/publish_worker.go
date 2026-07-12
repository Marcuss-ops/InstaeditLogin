// Package worker implements background processes that run alongside the
// HTTP server: the publish worker drives the scheduled-post fan-out, picking
// up post_targets whose scheduled_at <= NOW() and dispatching them through
// the appropriate per-platform implementation via the CapabilityRouter.
//
// Taglio 4.2 adds a second goroutine: the reconciler periodically polls
// targets in status='publishing' with a non-null platform_post_id, driving
// the 4-step async state machine (CheckPublishStatus → state transition)
// instead of the old synchronous 30x2s polling loop that used to live
// inside the provider's ReconcilePublish.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TikTok publish states (Taglio 4.2) — the platform-specific state
// strings returned by TikTok's status/fetch endpoint. Constants here
// so the reconciler can compare without typos and so tests can
// reference the canonical values.
const (
	tikTokStateProcessingUpload = "PROCESSING_UPLOAD"
	tikTokStatePendingPublish   = "PENDING_PUBLISH"
	tikTokStateInReview         = "IN_REVIEW"
	tikTokStatePublishComplete  = "PUBLISH_COMPLETE"
	tikTokStateFailed           = "FAILED"
)

// PublisherPostStore is the narrow slice of the post + post_targets repository
// the worker depends on. Defined here (not in repository package) so the
// worker can be unit-tested with a small in-memory mock without touching
// sql.DB or sqlmock.
//
// Taglio 4.2: added ListPublishing + UpdatePublishState to support the
// async reconciler goroutine.
type PublisherPostStore interface {
	// ListPending returns post_targets whose status='queued' AND whose
	// parent post.scheduled_at <= before. Ordered by post.scheduled_at ASC.
	ListPending(before time.Time) ([]models.PostTarget, error)
	// ListPublishing (Taglio 4.2) returns post_targets whose
	// status='publishing' AND platform_post_id IS NOT NULL. These are
	// the targets the reconciler needs to poll for completion. Ordered
	// by id ASC for stable iteration.
	ListPublishing() ([]models.PostTarget, error)
	// FindByID loads the parent post for the publish payload (caption/title/media_url).
	FindByID(id int64) (*models.Post, error)
	// ClaimQueuedTarget atomically transitions a target from
	// status='queued' to 'publishing'. Returns true on claim, false
	// if already claimed by another worker (verdict §10 — this is
	// the atomic primitive that unblocks 2+ worker replicas).
	ClaimQueuedTarget(id int64) (bool, error)
	// UpdateStatus persists the status transitions (publishing→
	// published|failed). The claim guarantees only the winning worker
	// reaches this step, so no atomic check is needed here.
	UpdateStatus(target *models.PostTarget) error
	// UpdatePublishState (Taglio 4.2) updates only the provider_state
	// column on a post_target. Used by the reconciler to record the
	// current platform-specific state (PROCESSING_UPLOAD /
	// PENDING_PUBLISH / IN_REVIEW) on every CheckPublishStatus call
	// without triggering a full status transition. Idempotent:
	// provider_state is debugging/observability metadata, not
	// lifecycle state, so the worker does NOT need to claim the row
	// first.
	UpdatePublishState(id int64, providerState string) error
}

// PublisherUserStore is the narrow slice of the user / platform_accounts
// repository the worker depends on. Just enough to resolve the
// platform_account for a pending post_target without dragging in the full
// UserRepository surface.
type PublisherUserStore interface {
	// FindPlatformAccountByID returns (nil, nil) when no row matches, matching
	// the codebase's repository convention (nil/nil not-found, no ErrNoRows).
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
}

// PublishWorker periodically dispatches scheduled posts to their target
// platforms. It is intentionally simple: one struct, two goroutines
// (tick for the queued→publishing transition, tickReconcile for the
// async publishing→published/failed transition), ctx-cancellable. The
// 3-step status transition (`queued` → `publishing` → `published |
// failed`) acts as a logical lock so two worker instances cannot
// double-publish the same target.
//
// Taglio 2.2: the worker depends on the CapabilityRouter (per-capability
// lookups: OAuthProvider for refresh, Publisher for the actual call) and a
// CredentialVault (for the encrypt + store + refresh-with-advisory-lock).
// The OAuthProvider is adapted to a credentials.TokenRefresher closure
// at the call site so the vault has zero knowledge of per-platform types.
//
// Taglio 4.2: the worker also uses the AsyncPublisher capability to
// drive the 4-step state machine (StartPublish / CheckPublishStatus /
// ContinuePublish / Reconcile) for platforms whose publish is async
// (TikTok today). The reconciler goroutine replaces the old synchronous
// polling loop that used to live inside the provider.
type PublishWorker struct {
	postRepo PublisherPostStore
	userRepo PublisherUserStore
	router   *services.CapabilityRouter
	vault    credentials.VaultAPI
	interval time.Duration
	logger   *slog.Logger
}

// NewPublishWorker wires the dependencies. interval <= 0 falls back to a safe
// default of 30s to prevent tight loops from misconfiguration. nil logger
// inherits slog.Default(). router and vault must be non-nil; a nil will
// panic on the first tick (fail-fast for misconfigured wiring).
func NewPublishWorker(
	postRepo PublisherPostStore,
	userRepo PublisherUserStore,
	router *services.CapabilityRouter,
	vault credentials.VaultAPI,
	interval time.Duration,
	logger *slog.Logger,
) *PublishWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PublishWorker{
		postRepo: postRepo,
		userRepo: userRepo,
		router:   router,
		vault:    vault,
		interval: interval,
		logger:   logger,
	}
}

// Run blocks until ctx is cancelled, executing one tick per interval period.
// Performs a graceful drain: when ctx.Done() fires while a tick is mid-flight,
// the current tick completes naturally and Run returns only after that.
// Returns ctx.Err() on shutdown; logs non-nil errors and continues otherwise.
//
// Taglio 4.2: on every tick, the worker now runs BOTH tick() and
// tickReconcile() sequentially. They share the same interval because:
//  1. The reconciler needs to see fresh rows quickly after Publish
//     assigns the publish_id — the interval is already short enough
//     (30s default) to bound the publishing→published latency.
//  2. Sequential execution prevents the reconciler from racing the
//     tick that just created the publishing row.
func (w *PublishWorker) Run(ctx context.Context) error {
	w.logger.Info("publish worker started", "interval_seconds", w.interval.Seconds())
	defer w.logger.Info("publish worker stopped")

	// Initial ticks — no wait for the first sweep.
	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

// runOnce executes one tick + one reconcile pass and logs the results.
// Both passes are sequential; reconcile runs AFTER tick so any rows
// that the tick just created (with a fresh platform_post_id) are
// immediately visible to the reconciler.
func (w *PublishWorker) runOnce(ctx context.Context) {
	if processed, ok, ko, err := w.tick(ctx); err != nil {
		w.logger.Warn("publish worker tick failed", "error", err)
	} else if processed > 0 {
		w.logger.Info("publish worker tick done",
			"processed", processed, "succeeded", ok, "failed", ko)
	}
	if reconciled, failed, err := w.tickReconcile(ctx); err != nil {
		w.logger.Warn("publish worker reconcile failed", "error", err)
	} else if reconciled > 0 {
		w.logger.Info("publish worker reconcile done",
			"reconciled", reconciled, "failed", failed)
	}
}

// tick processes all pending targets exactly once. Returns
// (processed, succeeded, failed, err). Sequential per-target — no
// per-target goroutines — for predictable load on the OAuth APIs and
// easier rate-limit debugging.
//
// Per-target errors are LOGGED and counted but do not abort the tick; the
// worker should keep trying other targets even if Meta/Twitter/etc. are
// flapping.
func (w *PublishWorker) tick(ctx context.Context) (processed, succeeded, failed int, err error) {
	pending, err := w.postRepo.ListPending(time.Now())
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list pending: %w", err)
	}
	if len(pending) == 0 {
		return 0, 0, 0, nil
	}

	for i := range pending {
		// Index-based loop (not `for _, target`): we mutate &pending[i] inside
		// publishTarget and the local copy must reflect those mutations when
		// we pass it to UpdateStatus.
		if err := w.publishTarget(ctx, &pending[i]); err != nil {
			w.logger.Warn("publish target failed",
				"target_id", pending[i].ID,
				"post_id", pending[i].PostID,
				"error", err)
			failed++
		} else {
			succeeded++
		}
		processed++
	}
	return processed, succeeded, failed, nil
}

// tickReconcile (Taglio 4.2) processes all targets in status='publishing'
// with a non-null platform_post_id. For each, it looks up the
// AsyncPublisher capability and calls CheckPublishStatus (single GET,
// no polling). On PUBLISH_COMPLETE it transitions to 'published'; on
// FAILED it transitions to 'failed'; on any in-flight state it
// updates the provider_state column and leaves the target as-is.
//
// Safety: this goroutine does NOT claim the row before reading it.
// That's safe because the only thing the reconciler MUTATES on a
// publishing target is provider_state (a debugging/observability
// column). The status transition (publishing→published|failed) is
// idempotent — if two reconcilers race the same target, the second
// UPDATE will simply overwrite the first with the same terminal
// state. (The original tick's ClaimQueuedTarget already prevents
// two workers from racing the queued→publishing transition.)
func (w *PublishWorker) tickReconcile(ctx context.Context) (reconciled, failed int, err error) {
	publishing, err := w.postRepo.ListPublishing()
	if err != nil {
		return 0, 0, fmt.Errorf("list publishing: %w", err)
	}
	if len(publishing) == 0 {
		return 0, 0, nil
	}

	for i := range publishing {
		target := &publishing[i]
		ok, wasFailed, err := w.reconcileTarget(ctx, target)
		if err != nil {
			w.logger.Warn("reconcile target failed",
				"target_id", target.ID,
				"post_id", target.PostID,
				"error", err)
			failed++
			continue
		}
		if wasFailed {
			failed++
		}
		if ok {
			reconciled++
		}
	}
	return reconciled, failed, nil
}

// reconcileTarget (Taglio 4.2) is the per-target reconciler. It:
//  1. Loads the platform account (skipped if missing — orphan target).
//  2. Looks up the AsyncPublisher capability. If the platform doesn't
//     support async publishing (e.g. Meta, YouTube), leaves the target
//     alone — sync platforms complete their publish in the original
//     tick() call.
//  3. Refreshes the OAuth token via the vault.
//  4. Calls CheckPublishStatus (single GET, no polling).
//  5. Updates provider_state to the current state (debugging/observability).
//  6. Transitions:
//       PUBLISH_COMPLETE → status='published', set published_at
//       FAILED          → status='failed', save error_message
//       in-flight       → leave status='publishing', no transition
//
// Returns (reconciled bool, wasFailed bool, err). The bools let the
// caller increment the per-tick counters without parsing the error.
func (w *PublishWorker) reconcileTarget(ctx context.Context, target *models.PostTarget) (reconciled bool, wasFailed bool, err error) {
	// 1. Load platform account.
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return false, false, fmt.Errorf("load account %d: %w", target.PlatformAccountID, err)
	}
	if account == nil {
		// Orphan target — mark failed so it doesn't loop forever.
		return w.markFailedAndReturn(target, fmt.Sprintf("platform_account %d not found", target.PlatformAccountID))
	}

	// 2. Look up AsyncPublisher capability.
	ap, ok := w.router.AsyncPublisher(account.Platform)
	if !ok {
		// Platform doesn't support async publishing — leave the target
		// alone. Sync platforms complete their publish in the original
		// tick() call.
		return false, false, nil
	}

	// 3. Refresh OAuth token via the vault.
	oauth, oauthOK := w.router.OAuth(account.Platform)
	if !oauthOK {
		return w.markFailedAndReturn(target, fmt.Sprintf("platform %q missing OAuth capability", account.Platform))
	}
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	oauthToken, err := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		if oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher); err != nil {
			return w.markFailedAndReturn(target, "token refresh failed: "+err.Error())
		}
	}

	// 4. Check publish status (single GET, no polling).
	state, err := ap.CheckPublishStatus(ctx, oauthToken.AccessToken, target.PlatformPostID)
	if err != nil {
		// Network / API error — leave the target alone, retry next tick.
		// The publish_id is stable; a transient 5xx shouldn't mark the
		// target failed (TikTok's SLO is too loose for that).
		w.logger.Warn("CheckPublishStatus error (will retry next tick)",
			"target_id", target.ID, "publish_id", target.PlatformPostID, "error", err)
		return false, false, nil
	}

	// 5. Update provider_state (always, even on terminal states, for
	// observability — operators can see the final state in the DB).
	if updateErr := w.postRepo.UpdatePublishState(target.ID, state); updateErr != nil {
		// Non-fatal: the state transition below is the source of truth.
		w.logger.Warn("UpdatePublishState failed (continuing with transition)",
			"target_id", target.ID, "error", updateErr)
	}

	// 6. Transition based on state.
	switch state {
	case tikTokStatePublishComplete:
		target.Status = models.PostStatusPublished
		// platform_post_id stays as the publish_id (the TikTok media id
		// is the same as the publish_id for completed publishes).
		now := time.Now()
		target.PublishedAt = &now
		if err := w.postRepo.UpdateStatus(target); err != nil {
			return false, false, fmt.Errorf("transition to published: %w", err)
		}
		return true, false, nil
	case tikTokStateFailed:
		return w.markFailedAndReturn(target, fmt.Sprintf("tiktok publish failed (publish_id=%s, state=%s)", target.PlatformPostID, state))
	default:
		// PROCESSING_UPLOAD / PENDING_PUBLISH / IN_REVIEW — still in flight.
		return false, false, nil
	}
}

// markFailedAndReturn transitions the target to status='failed' and
// returns the bookkeeping so the reconciler can increment its
// counters. The (true, true, nil) return values signal "yes, this
// target was reconciled (to failed), yes it failed, no error".
func (w *PublishWorker) markFailedAndReturn(target *models.PostTarget, reason string) (reconciled bool, wasFailed bool, err error) {
	target.Status = models.PostStatusFailed
	target.ErrorMessage = reason
	_ = w.postRepo.UpdateStatus(target)
	return true, true, nil
}

// publishTarget drives the per-target 3-step status transition:
//
//  1. ATOMIC CLAIM: queued → publishing (verdict §10). The single
//     UPDATE in ClaimQueuedTarget uses WHERE status='queued' as a
//     logical lock so only ONE worker wins. The loser sees a
//     `claimed=false` return and skips — no double-publish.
//  2. Load parent Post (caption/title/media_url for the publish payload)
//     AND PlatformAccount (platform name + platform_user_id for dispatch).
//     Safe to do AFTER the claim: if either is missing, we transition
//     to 'failed' (we own the row), so the next tick won't re-pick it.
//  3. Refresh OAuth token via the CredentialVault (which serialises
//     concurrent refreshes with a Postgres advisory lock).
//     On failure: status → `failed` with error_message.
//  4. Publish via the platform's Publisher capability.
//     On sync platforms: status → 'published' with platform_post_id + published_at.
//     On async platforms (Taglio 4.2): status stays 'publishing', the
//     platform_post_id gets the publish_id from the result, and the
//     reconciler goroutine will drive the state machine on subsequent ticks.
//     On failure: status → 'failed` with error_message.
//
// The 'failed' transitions only happen AFTER a successful claim, so two
// workers running in parallel won't redundantly write 'failed' to the
// same row (the loser would have already returned with claimed=false).
func (w *PublishWorker) publishTarget(ctx context.Context, target *models.PostTarget) error {
	// 1. ATOMIC CLAIM: queued → publishing. If another worker
	// already claimed this target, claim returns false and we skip.
	claimed, err := w.postRepo.ClaimQueuedTarget(target.ID)
	if err != nil {
		return fmt.Errorf("claim target %d: %w", target.ID, err)
	}
	if !claimed {
		w.logger.Info("target already claimed by another worker, skipping",
			"target_id", target.ID, "post_id", target.PostID)
		return nil // not an error — just skip
	}

	// 2. Load parent Post
	post, err := w.postRepo.FindByID(target.PostID)
	if err != nil {
		return w.markFailed(target, fmt.Sprintf("load post %d: %v", target.PostID, err))
	}
	if post == nil {
		// Vanished record — cannot publish. Mark failed so we don't loop forever.
		return w.markFailed(target, fmt.Sprintf("post %d not found", target.PostID))
	}

	// 3. Load PlatformAccount
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return w.markFailed(target, fmt.Sprintf("load account %d: %v", target.PlatformAccountID, err))
	}
	if account == nil {
		return w.markFailed(target, fmt.Sprintf("platform_account %d not found", target.PlatformAccountID))
	}

	// 4. Resolve platform capabilities. We need the OAuthProvider (for
	// token refresh) AND the Publisher (for the actual call). A platform
	// missing either cannot be published to.
	oauth, oauthOK := w.router.OAuth(account.Platform)
	publisher, pubOK := w.router.Publisher(account.Platform)
	if !oauthOK || !pubOK {
		return w.markFailed(target, fmt.Sprintf("platform %q missing capability (oauth=%v publish=%v)", account.Platform, oauthOK, pubOK))
	}

	// 5. Refresh token via the CredentialVault. The provider's
	// RefreshOAuthToken method is adapted to a credentials.TokenRefresher
	// closure so the vault only knows the function signature. Try Bearer
	// first (refresh-capable), then LongLived (Meta-style re-exchange).
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	oauthToken, err := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher)
		if err != nil {
			return w.markFailed(target, "token refresh failed: "+err.Error())
		}
	}

	// 6. Build payload + publish. MediaURL goes through as VideoURL (the
	// payload's ImageURL branch is reserved for image-only posts that don't
	// have a content_type column — future enhancement).
	payload := models.PublishPayload{
		Text:  post.Caption,
		Title: post.Title,
	}
	if post.MediaURL != "" {
		payload.VideoURL = post.MediaURL
	}
	result, err := publisher.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
	if err != nil {
		return w.markFailed(target, err.Error())
	}

	// 7. ASYNC PUBLISH (Taglio 4.2): if the platform has the
	// AsyncPublisher capability, the Publish() call returned a
	// publish_id (in result.PlatformMediaID) but did NOT complete
	// the publish — the platform is still processing. We store the
	// publish_id on the target and KEEP status='publishing' (the
	// claim already wrote 'publishing' to the DB; we just need to
	// ensure UpdateStatus doesn't revert it back to the in-memory
	// 'queued' value the target struct carries from ListPending).
	// The reconciler goroutine will pick this target up on
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

	// 8. SYNC PUBLISH: transition publishing → published.
	target.Status = models.PostStatusPublished
	target.PlatformPostID = result.PlatformMediaID
	now := time.Now()
	target.PublishedAt = &now
	if err := w.postRepo.UpdateStatus(target); err != nil {
		return fmt.Errorf("transition to published: %w", err)
	}
	return nil
}

// markFailed transitions the target to status='failed' with the given
// reason and returns a wrapped error. The caller is expected to have
// already successfully claimed the target (via ClaimQueuedTarget)
// — the 'failed' write is only legal AFTER the claim, otherwise two
// workers could both redundantly update the same row.
//
// The UpdateStatus error is intentionally ignored (logged at the
// caller's warning level) so the returned error reflects the original
// failure reason rather than the bookkeeping error.
func (w *PublishWorker) markFailed(target *models.PostTarget, reason string) error {
	target.Status = models.PostStatusFailed
	target.ErrorMessage = reason
	_ = w.postRepo.UpdateStatus(target)
	return errors.New(reason)
}
