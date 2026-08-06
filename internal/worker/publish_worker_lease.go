package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// updateTargetStatus persists a child-job mutation with a lease CAS when the
// SQL repository supports it. The fallback keeps older in-memory fixtures
// usable while production always takes the lease-aware branch.
func (w *PublishWorker) updateTargetStatus(ctx context.Context, target *models.PostTarget) error {
	if leaseStore, ok := w.postRepo.(LeaseAwarePublisherPostStore); ok {
		if err := leaseStore.UpdateStatusWithLease(target, w.workerID); err != nil {
			return err
		}
		return nil
	}
	return w.postRepo.UpdateStatus(target)
}

func publishRetryBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	backoff := 5 * time.Second
	for i := 0; i < attempt && backoff < 5*time.Minute; i++ {
		backoff *= 2
	}
	if backoff > 5*time.Minute {
		return 5 * time.Minute
	}
	return backoff
}

func (w *PublishWorker) retryTarget(ctx context.Context, target *models.PostTarget, reason string) error {
	if leaseStore, ok := w.postRepo.(LeaseAwarePublisherPostStore); ok {
		next := time.Now().Add(publishRetryBackoff(target.AttemptCount))
		if err := leaseStore.MarkRetrying(target.ID, w.workerID, reason, next); err != nil {
			return err
		}
		target.Status = models.PostStatusRetrying
		target.NextAttemptAt = &next
		target.AttemptCount++
		target.ErrorMessage = reason
		return nil
	}
	return w.postRepo.UpdateStatus(target)
}

func (w *PublishWorker) deadLetterTarget(ctx context.Context, target *models.PostTarget, reason string) error {
	if leaseStore, ok := w.postRepo.(LeaseAwarePublisherPostStore); ok {
		if err := leaseStore.MarkDeadLetter(target.ID, w.workerID, reason); err != nil {
			return err
		}
		target.Status = models.PostStatusDLQ
		target.ErrorMessage = reason
		return nil
	}
	return w.postRepo.UpdateStatus(target)
}

var errPublishLeaseLost = errors.New("publish child job lease lost")

func wrapPublishLeaseError(targetID int64, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("publish child target %d: %w", targetID, err)
}

var _ = context.Background
var _ = errPublishLeaseLost
