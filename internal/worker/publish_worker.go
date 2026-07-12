// Package worker implements background processes that run alongside the
// HTTP server: the publish worker drives the scheduled-post fan-out, picking
// up post_targets whose scheduled_at <= NOW() and dispatching them through
// the appropriate per-platform implementation via the PlatformRegistry.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// PublisherPostStore is the narrow slice of the post + post_targets repository
// the worker depends on. Defined here (not in repository package) so the
// worker can be unit-tested with a small in-memory mock without touching
// sql.DB or sqlmock.
type PublisherPostStore interface {
	// ListPending returns post_targets whose status='scheduled' AND whose
	// parent post.scheduled_at <= before. Ordered by post.scheduled_at ASC.
	ListPending(before time.Time) ([]models.PostTarget, error)
	// FindByID loads the parent post for the publish payload (caption/title/media_url).
	FindByID(id int64) (*models.Post, error)
	// ClaimScheduledTarget atomically transitions a target from
	// status='scheduled' to 'publishing'. Returns true on claim, false
	// if already claimed by another worker (verdict §10 — this is
	// the atomic primitive that unblocks 2+ worker replicas).
	ClaimScheduledTarget(id int64) (bool, error)
	// UpdateStatus persists the status transitions (publishing→
	// published|failed). The claim guarantees only the winning worker
	// reaches this step, so no atomic check is needed here.
	UpdateStatus(target *models.PostTarget) error
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
// platforms. It is intentionally simple: one goroutine, sequential per-target
// processing, ctx-cancellable. The 3-step status transition (`scheduled` →
// `publishing` → `published | failed`) acts as a logical lock so two worker
// instances (future-sh) cannot double-publish the same target.
type PublishWorker struct {
	postRepo PublisherPostStore
	userRepo PublisherUserStore
	registry *services.PlatformRegistry
	interval time.Duration
	logger   *slog.Logger
}

// NewPublishWorker wires the dependencies. interval <= 0 falls back to a safe
// default of 30s to prevent tight loops from misconfiguration. nil logger
// inherits slog.Default().
func NewPublishWorker(
	postRepo PublisherPostStore,
	userRepo PublisherUserStore,
	registry *services.PlatformRegistry,
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
		registry: registry,
		interval: interval,
		logger:   logger,
	}
}

// Run blocks until ctx is cancelled, executing one tick per interval period.
// Performs a graceful drain: when ctx.Done() fires while a tick is mid-flight,
// the current tick completes naturally and Run returns only after that.
// Returns ctx.Err() on shutdown; logs non-nil errors and continues otherwise.
//
// Initial tick is fired immediately (no wait) so freshly-scheduled posts are
// picked up without waiting `interval` for the first sweep.
func (w *PublishWorker) Run(ctx context.Context) error {
	w.logger.Info("publish worker started", "interval_seconds", w.interval.Seconds())
	defer w.logger.Info("publish worker stopped")

	if processed, ok, ko, err := w.tick(ctx); err != nil {
		w.logger.Warn("publish worker initial tick failed", "error", err)
	} else if processed > 0 {
		w.logger.Info("publish worker initial tick done",
			"processed", processed, "succeeded", ok, "failed", ko)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if processed, ok, ko, err := w.tick(ctx); err != nil {
				w.logger.Warn("publish worker tick failed", "error", err)
			} else if processed > 0 {
				w.logger.Info("publish worker tick done",
					"processed", processed, "succeeded", ok, "failed", ko)
			}
		}
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

// publishTarget drives the per-target 3-step status transition:
//
//  1. ATOMIC CLAIM: scheduled → publishing (verdict §10). The single
//     UPDATE in ClaimScheduledTarget uses WHERE status='scheduled' as
//     a logical lock so only ONE worker wins. The loser sees a
//     `claimed=false` return and skips — no double-publish.
//  2. Load parent Post (caption/title/media_url for the publish payload)
//     AND PlatformAccount (platform name + platform_user_id for dispatch).
//     Safe to do AFTER the claim: if either is missing, we transition
//     to 'failed' (we own the row), so the next tick won't re-pick it.
//  3. Refresh OAuth token (try Bearer, fall back to LongLived for Meta-style
//     providers). On failure: status → `failed` with error_message.
//  4. Publish via the per-platform PlatformService.Publish.
//     On success: status → `published` with platform_post_id + published_at.
//     On failure: status → `failed` with error_message.
//
// The 'failed' transitions only happen AFTER a successful claim, so two
// workers running in parallel won't redundantly write 'failed' to the
// same row (the loser would have already returned with claimed=false).
func (w *PublishWorker) publishTarget(ctx context.Context, target *models.PostTarget) error {
	// 1. ATOMIC CLAIM: scheduled → publishing. If another worker
	// already claimed this target, claim returns false and we skip.
	claimed, err := w.postRepo.ClaimScheduledTarget(target.ID)
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

	// 4. Resolve platform service
	p, err := w.registry.Resolve(account.Platform)
	if err != nil {
		return w.markFailed(target, "no platform service registered for: "+account.Platform)
	}

	// 5. Refresh token. Try Bearer first (refresh-capable), then LongLived
	// (Meta-style re-exchange). Mirrors the pattern in routes.go publishContent.
	oauthToken, err := p.EnsureFreshToken(ctx, account.ID, models.TokenTypeBearer, p.RefreshOAuthToken)
	if err != nil {
		oauthToken, err = p.EnsureFreshToken(ctx, account.ID, models.TokenTypeLongLived, p.RefreshOAuthToken)
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
	result, err := p.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
	if err != nil {
		return w.markFailed(target, err.Error())
	}

	// 7. Transition: publishing → published. PostTarget.PublishedAt is
	// *time.Time (nullable per existing token.ExpiresAt convention), so
	// we capture time.Now() into a local before taking its address.
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
// already successfully claimed the target (via ClaimScheduledTarget)
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
