// Package worker implements background processes that run alongside the
// HTTP server: the publish worker drives the scheduled-post fan-out, picking
// up post_targets whose scheduled_at <= NOW() and dispatching them through
// the appropriate per-platform PlatformService implementation.
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
	// UpdateStatus persists the status transitions (scheduled→publishing→
	// published|failed). Internal RowsAffected check ensures we never update
	// a row that already moved on (defensive against double-publish).
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
	postRepo  PublisherPostStore
	userRepo  PublisherUserStore
	platforms map[string]services.PlatformService
	interval  time.Duration
	logger    *slog.Logger
}

// NewPublishWorker wires the dependencies. interval <= 0 falls back to a safe
// default of 30s to prevent tight loops from misconfiguration. nil logger
// inherits slog.Default().
func NewPublishWorker(
	postRepo PublisherPostStore,
	userRepo PublisherUserStore,
	platforms map[string]services.PlatformService,
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
		postRepo:  postRepo,
		userRepo:  userRepo,
		platforms: platforms,
		interval:  interval,
		logger:    logger,
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
//  1. Load parent Post (caption/title/media_url for the publish payload)
//     AND PlatformAccount (platform name + platform_user_id for dispatch).
//  2. Transition status → `publishing` (logical lock against double-publish).
//     If the platform is unknown we go straight to `failed`.
//  3. Refresh OAuth token (try Bearer, fall back to LongLived for Meta-style
//     providers). On failure: status → `failed` with error_message.
//  4. Publish via the per-platform PlatformService.Publish.
//     On success: status → `published` with platform_post_id + published_at.
//     On failure: status → `failed` with error_message.
//
// Any error path before status=publishing is terminal-failed and returns early
// so the next tick doesn't re-pick the same target.
func (w *PublishWorker) publishTarget(ctx context.Context, target *models.PostTarget) error {
	// 1. Load parent Post
	post, err := w.postRepo.FindByID(target.PostID)
	if err != nil {
		return fmt.Errorf("load post %d: %w", target.PostID, err)
	}
	if post == nil {
		// Vanished record — cannot publish. Mark failed so we don't loop forever.
		target.Status = models.PostStatusFailed
		target.ErrorMessage = fmt.Sprintf("post %d not found", target.PostID)
		_ = w.postRepo.UpdateStatus(target)
		return fmt.Errorf("post %d not found", target.PostID)
	}

	// 2. Load PlatformAccount
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return fmt.Errorf("load account %d: %w", target.PlatformAccountID, err)
	}
	if account == nil {
		target.Status = models.PostStatusFailed
		target.ErrorMessage = fmt.Sprintf("platform_account %d not found", target.PlatformAccountID)
		_ = w.postRepo.UpdateStatus(target)
		return fmt.Errorf("platform_account %d not found", target.PlatformAccountID)
	}

	// 3. Resolve platform service
	p, ok := w.platforms[account.Platform]
	if !ok {
		target.Status = models.PostStatusFailed
		target.ErrorMessage = "no platform service registered for: " + account.Platform
		_ = w.postRepo.UpdateStatus(target)
		return errors.New("unsupported platform: " + account.Platform)
	}

	// 4. Transition: scheduled → publishing (logical lock)
	target.Status = models.PostStatusPublishing
	if err := w.postRepo.UpdateStatus(target); err != nil {
		return fmt.Errorf("transition to publishing: %w", err)
	}

	// 5. Refresh token. Try Bearer first (refresh-capable), then LongLived
	// (Meta-style re-exchange). Mirrors the pattern in routes.go publishContent.
	oauthToken, err := p.EnsureFreshToken(ctx, account.ID, models.TokenTypeBearer, p.RefreshOAuthToken)
	if err != nil {
		oauthToken, err = p.EnsureFreshToken(ctx, account.ID, models.TokenTypeLongLived, p.RefreshOAuthToken)
		if err != nil {
			target.Status = models.PostStatusFailed
			target.ErrorMessage = "token refresh failed: " + err.Error()
			_ = w.postRepo.UpdateStatus(target)
			return fmt.Errorf("token refresh: %w", err)
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
		target.Status = models.PostStatusFailed
		target.ErrorMessage = err.Error()
		_ = w.postRepo.UpdateStatus(target)
		return fmt.Errorf("publish: %w", err)
	}

	// 7. Transition: publishing → published
	target.Status = models.PostStatusPublished
	target.PlatformPostID = result.PlatformMediaID
	target.PublishedAt = time.Now()
	if err := w.postRepo.UpdateStatus(target); err != nil {
		return fmt.Errorf("transition to published: %w", err)
	}
	return nil
}
