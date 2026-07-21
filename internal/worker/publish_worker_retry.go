package worker

import (
	"errors"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// markFailed transitions the target to status='failed' with the given
// reason and returns a wrapped error. The caller is expected to have
// already successfully claimed the target (via ClaimQueuedTarget) —
// the 'failed' write is only legal AFTER the claim, otherwise two
// workers could both redundantly update the same row.
//
// The UpdateStatus error is intentionally ignored (logged at the
// caller's warning level) so the returned error reflects the
// original failure reason rather than the bookkeeping error.
func (w *PublishWorker) markFailed(target *models.PostTarget, reason string) error {
	target.Status = models.PostStatusFailed
	target.ErrorMessage = reason
	_ = w.postRepo.UpdateStatus(target)
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
// helper); both fire AFTER ClaimQueuedTarget succeeds so two
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
	_ = w.postRepo.UpdateStatus(target)
	return errors.New(reason)
}
