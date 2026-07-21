package worker

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// claimTarget atomically claims the target from status='queued' to
// 'publishing'. Returns false without an error when another worker
// already claimed the target. Returns an error only when the claim
// itself failed (e.g. DB error), in which case the caller must NOT
// mark the target failed because the row was never owned.
func (w *PublishWorker) claimTarget(ctx context.Context, target *models.PostTarget) (bool, error) {
	claimed, err := w.postRepo.ClaimQueuedTarget(target.ID)
	if err != nil {
		return false, fmt.Errorf("claim target %d: %w", target.ID, err)
	}
	if !claimed {
		w.logger.Info("target already claimed by another worker, skipping",
			"target_id", target.ID, "post_id", target.PostID)
		return false, nil
	}
	return true, nil
}

// loadPostAndAccount loads the parent post and the platform account
// for a target after the claim succeeded. Errors are returned with the
// same message shape as the original implementation so the caller can
// route them to markFailed.
func (w *PublishWorker) loadPostAndAccount(ctx context.Context, target *models.PostTarget) (*models.Post, *models.PlatformAccount, error) {
	post, err := w.postRepo.FindByID(target.PostID)
	if err != nil {
		return nil, nil, fmt.Errorf("load post %d: %v", target.PostID, err)
	}
	if post == nil {
		return nil, nil, fmt.Errorf("post %d not found", target.PostID)
	}

	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return nil, nil, fmt.Errorf("load account %d: %v", target.PlatformAccountID, err)
	}
	if account == nil {
		return nil, nil, fmt.Errorf("platform_account %d not found", target.PlatformAccountID)
	}

	return post, account, nil
}
