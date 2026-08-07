package worker

import (
	"context"
	"errors"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// defaultRateLimitBackoff is the fallback delay when the platform's
// rate-limit error carries no usable Retry-After hint (RetryAfter=0 is
// documented as "caller uses default backoff" in both RateLimitError
// and ProviderError). 60s is one publish-worker tick beyond the 30s
// default interval — long enough to let a per-minute window reset,
// short enough that a queued post isn't visibly delayed on dashboards.
const defaultRateLimitBackoff = 60 * time.Second

// markRateLimited (OPEN GAP closure — ARCHITECTURE.md §Rate limiting
// (d)) handles a rate-limit error from the FINAL platform publish
// call. Instead of the terminal markFailed path, the target is
// requeued with next_attempt_at = NOW() + the platform's Retry-After
// hint (or defaultRateLimitBackoff when the hint is missing), and
// attempt_count is bumped so the retry budget stays bounded.
//
// Same claim-ownership contract as markFailed: only legal AFTER a
// successful ClaimQueuedTargetWithLease (the repo-side UPDATE is
// additionally guarded by WHERE status='publishing').
//
// Returns nil — a rescheduled rate-limit is NOT a tick error: the
// row will be re-picked by ListPending once the window opens, and
// counting it as a failure would pollute the tick error metrics.
// A failed reschedule (DB error) IS returned so the tick counter
// sees it and the row is recovered later (still 'publishing' — an
// operator-visible stall rather than a silent drop).
func (w *PublishWorker) markRateLimited(target *models.PostTarget, pubErr error) error {
	retryAfter := services.RetryAfterFromError(pubErr)
	if retryAfter <= 0 {
		retryAfter = defaultRateLimitBackoff
	}
	nextAttempt := time.Now().Add(retryAfter)
	if leaseStore, ok := w.postRepo.(LeaseAwarePublisherPostStore); ok {
		if err := leaseStore.MarkRateLimitedRetryWithLease(target.ID, w.workerID, nextAttempt, pubErr.Error()); err != nil {
			return errors.Join(errors.New("reschedule rate-limited target: "+err.Error()), pubErr)
		}
	} else if err := w.postRepo.MarkRateLimitedRetry(target.ID, nextAttempt, pubErr.Error()); err != nil {
		return errors.Join(errors.New("reschedule rate-limited target: "+err.Error()), pubErr)
	}
	target.Status = models.PostStatusQueued
	target.AttemptCount++
	target.NextAttemptAt = &nextAttempt
	target.LastErrorCode = "RATE_LIMITED"
	target.ErrorMessage = pubErr.Error()
	w.logger.Warn("platform rate limited publish; target rescheduled",
		"target_id", target.ID, "post_id", target.PostID,
		"retry_after", retryAfter, "next_attempt_at", nextAttempt,
		"attempt_count", target.AttemptCount, "error", pubErr)
	return nil
}

// markFailed transitions the target to status='failed' with the given
// reason and returns a wrapped error. The caller is expected to have
// already successfully claimed the target (via
// ClaimQueuedTargetWithLease) — the 'failed' write is only legal
// AFTER the claim, otherwise two workers could both redundantly
// update the same row.
//
// The UpdateStatus error is intentionally ignored (logged at the
// caller's warning level) so the returned error reflects the
// original failure reason rather than the bookkeeping error.
func (w *PublishWorker) markFailed(target *models.PostTarget, reason string) error {
	target.Status = models.PostStatusFailed
	target.ErrorMessage = reason
	// Lease-aware repositories use the child retry state machine so a
	// transient failure affects only this target. Legacy test doubles keep
	// the historical terminal failed write through UpdateStatus.
	if _, ok := w.postRepo.(LeaseAwarePublisherPostStore); ok {
		if err := w.retryTarget(context.Background(), target, reason); err != nil {
			w.logger.Warn("publish worker: failed to persist child retry", "target_id", target.ID, "error", err)
		}
	} else {
		_ = w.updateTargetStatus(context.Background(), target)
	}
	return errors.New(reason)
}

// markPublishBlockedAuth (Task 2/10) transitions the target to
// status='blocked_auth' with the given reason and stamps
// last_error_code='blocked_auth' so dashboards + filters can
// distinguish a channel-drift refusal (terminal-per-account: until
// the operator reconnects the grant, the worker skips these rows)
// from a generic per-attempt failure ('failed', which is transient
// / retryable per the publish_state_machine rounding rules).
//
// The companion action — flipping platform_account.status to
// 'reauth_required' so the operator's dashboard prompts a
// reconnect — is performed by the caller (publish_target) BEFORE
// this helper runs; this helper only stamps the per-target row.
// Two writes total: platform_account (caller) + post_target (this
// helper); both fire AFTER ClaimQueuedTargetWithLease succeeds so two
// workers running in parallel cannot redundantly overwrite each
// other's row.
//
// UpdateStatus error is intentionally ignored (same rationale as
// markFailed): the returned error reflects the underlying reason,
// not the bookkeeping error.
func (w *PublishWorker) markPublishBlockedAuth(target *models.PostTarget, reason string) error {
	target.Status = models.PostStatusBlockedAuth
	target.ErrorMessage = reason
	// last_error_code is the short stable code dashboards index on
	// (PostTarget.LastErrorCode); 'blocked_auth' is the operator-
	// facing surface distinct from the per-error human prose in
	// ErrorMessage. Mirrors the migration-018 pattern where
	// transient failures get codes like "RATE_LIMITED" /
	// "INVALID_TOKEN" etc.
	target.LastErrorCode = "blocked_auth"
	if err := w.updateTargetStatus(context.Background(), target); err != nil {
		w.logger.Warn("publish worker: failed to persist blocked_auth target", "target_id", target.ID, "error", err)
	}
	return errors.New(reason)
}
