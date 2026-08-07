package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// leaseClaimer is the narrow claim-only slice of
// LeaseAwarePublisherPostStore that claimTarget requires. Kept as its
// own interface so small test doubles implement just the claim method
// while the full lease surface (heartbeat, reclaim, …) stays with the
// SQL repository. The legacy lease-less ClaimQueuedTarget was removed
// — every store driving the publish worker must claim with a lease.
type leaseClaimer interface {
	ClaimQueuedTargetWithLease(id int64, ownerID string, leaseTTL time.Duration) (bool, error)
}

// Compile-time assertion: *repository.PostRepository satisfies it.
var _ leaseClaimer = (*repository.PostRepository)(nil)

// claimTarget atomically claims the target from status='queued' to
// 'publishing' and stamps the per-replica lease. Returns false
// without an error when another worker already claimed the target.
// Returns an error only when the claim itself failed (e.g. DB error
// or a store that does not implement the lease-aware claim), in
// which case the caller must NOT mark the target failed because the
// row was never owned.
func (w *PublishWorker) claimTarget(ctx context.Context, target *models.PostTarget) (bool, error) {
	leaseStore, ok := w.postRepo.(leaseClaimer)
	if !ok {
		return false, fmt.Errorf("claim target %d: post store %T does not implement lease-aware claim", target.ID, w.postRepo)
	}
	claimed, err := leaseStore.ClaimQueuedTargetWithLease(target.ID, w.workerID, w.publishLeaseTTL())
	if err != nil {
		return false, fmt.Errorf("claim target %d with lease: %w", target.ID, err)
	}
	if !claimed {
		w.logger.Info("target already claimed by another worker, skipping", "target_id", target.ID, "post_id", target.PostID)
	}
	return claimed, nil
}

func (w *PublishWorker) publishLeaseTTL() time.Duration {
	return 2 * time.Minute
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
