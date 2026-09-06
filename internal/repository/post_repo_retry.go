package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// RetryPost transitions failed post targets back to queued and recomputes the
// aggregate post status in one transaction.
func (r *PostRepository) RetryPost(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = resetPostTargetsAndAggregateTx(tx, id, qRetryPostResetFailedTargets); err != nil {
		return err
	}
	return tx.Commit()
}

// RetryTarget transitions a failed target back to queued and recomputes its
// parent post aggregate status in one transaction.
func (r *PostRepository) RetryTarget(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = resetTargetAndAggregateTx(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimQueuedTargetWithLease atomically transitions a post_target from
// status='queued' (also 'waiting_provider'/'retrying') to
// status='publishing' using SELECT FOR UPDATE SKIP LOCKED inside an
// explicit transaction, stamping a per-replica lease so a crashed
// worker doesn't leak the row forever. The lease is a
// (lease_owner_id, leased_until) tuple; the heartbeat goroutine
// (UpdatePublishProgress) extends leased_until every heartbeat tick;
// ReclaimExpiredLeases (called by the reconciler) takes over rows
// whose leased_until <= NOW() and whose lease_owner_id is not the
// calling replica.
//
// Verdict §10 (FASE 1.1 — SKIP LOCKED): the SELECT FOR UPDATE SKIP
// LOCKED + UPDATE pattern inside a single explicit transaction
// guarantees that 2+ worker replicas racing the same row NEVER
// block on each other. The first worker to SELECT locks the row;
// the loser's SELECT returns immediately with no rows (SKIP
// LOCKED), and the function returns (false, nil) — no row-level
// wait, no deadlock risk, no connection-pool exhaustion under
// multi-replica contention. The lease makes the claim canonical:
// the legacy lease-less ClaimQueuedTarget was removed.
//
// The atomic UPDATE is a single SQL statement that flips status AND
// stamps the lease fields. The lease TTL is supplied as a duration;
// the SQL converts it to an INTERVAL via NOW() + $N * INTERVAL
// '1 second'.
//
// Returns true on claim success and false if:
//   - The row is locked by another tx (SKIP LOCKED).
//   - The row's status is not claimable (someone else already claimed).
//   - The id is invalid (no row matches).
//
// On success the caller is the SOLE owner of the row for at least
// `leaseTTL`. The heartbeat goroutine must extend the lease before
// `leaseTTL` elapses; failure to do so lets the reconciler
// reclaim the row on its next tick.
func (r *PostRepository) ClaimQueuedTargetWithLease(id int64, ownerID string, leaseTTL time.Duration) (bool, error) {
	if ownerID == "" {
		return false, fmt.Errorf("ClaimQueuedTargetWithLease: ownerID is empty")
	}
	if leaseTTL <= 0 {
		return false, fmt.Errorf("ClaimQueuedTargetWithLease: leaseTTL must be positive (got %v)", leaseTTL)
	}
	leaseSeconds := int(leaseTTL.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin claim-with-lease tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var foundID int64
	err = tx.QueryRow(
		qClaimQueuedTargetWithLeaseSelect,
		id,
	).Scan(&foundID)
	if err == sql.ErrNoRows {
		_ = tx.Rollback()
		err = nil
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to select for update (lease): %w", err)
	}

	// Atomic claim + lease stamp. leased_until = NOW() + leaseTTL
	// (computed in seconds; works for TTLs from 1s to ~68 years).
	if err = claimTargetTx(tx, id, qClaimQueuedTargetWithLeaseUpdate,
		id, ownerID, fmt.Sprintf("%d", leaseSeconds)); err != nil {
		return false, fmt.Errorf("failed to update claimed target with lease: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit claim-with-lease: %w", err)
	}
	return true, nil
}

// UpdatePublishProgress (SPRINT 5.2) is the heartbeat goroutine's
// per-tick writer. CAS on lease_owner_id: only the row's current
// owner can stamp progress. Updates:
//   - upload_offset (bytes uploaded so far) — for chunked-upload
//     resume after a crash.
//   - provider_state (opaque platform state string) — for
//     observability of where the upload is on the platform side.
//   - heartbeat_at (now) — observability for the lease monitoring.
//   - leased_until (now + leaseTTL) — extends the lease for another
//     heartbeat cycle.
//
// Returns nil on success. If the CAS fails (lease_owner_id has
// changed → another replica took over via reclaim), the heartbeat
// goroutine exits silently — the next Mark* will also see the new
// owner and the heartbeat's stale writes would fail. This is the
// canonical "ownership transfer" race the user spec called out:
// the heartbeating replica stops writing when the lease flips.
func (r *PostRepository) UpdatePublishProgress(id int64, ownerID string, uploadOffset int64, providerState string, leaseTTL time.Duration) error {
	if ownerID == "" {
		return fmt.Errorf("UpdatePublishProgress: ownerID is empty")
	}
	if leaseTTL <= 0 {
		return fmt.Errorf("UpdatePublishProgress: leaseTTL must be positive (got %v)", leaseTTL)
	}
	leaseSeconds := int(leaseTTL.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	_, err := r.db.Exec(
		qUpdatePublishProgress,
		id, ownerID, uploadOffset, providerState, fmt.Sprintf("%d", leaseSeconds),
	)
	if err != nil {
		return fmt.Errorf("failed to update publish progress: %w", err)
	}
	return nil
}

// ReleaseLease (SPRINT 5.2) clears the lease fields on a terminal
// transition (published|failed|dlq). CAS on lease_owner_id so only
// the current owner can release — a reclaimed row's new owner
// can't be clobbered by a stale release from the crashed original.
//
// Idempotent: returns nil on RowsAffected = 0 (the row is already
// lease-cleared, e.g. on a prior terminal write).
func (r *PostRepository) ReleaseLease(id int64, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("ReleaseLease: ownerID is empty")
	}
	_, err := r.db.Exec(
		qReleaseLease,
		id, ownerID,
	)
	if err != nil {
		return fmt.Errorf("failed to release lease: %w", err)
	}
	return nil
}

// MarkDeadLetter (SPRINT 5.2) transitions a target to status='dlq'
// when max_attempts is exhausted on a transient error, OR when a
// terminal-class error (4xx non-429) is classified. CAS on
// lease_owner_id + clear lease in the same UPDATE. The row is
// terminal: no further transitions, the publish driver and
// reconciler both filter status IN ('queued', 'waiting_provider',
// 'publishing') and therefore skip it.
//
// lastError is persisted to error_message for operator visibility.
// last_error_code is set to 'DLQ' for consistency with
// MarkRateLimited ('RATE_LIMITED') — dashboards can filter by
// stable code without parsing the human prose of error_message.
// completed_at is set to NOW() so the DLQ-triage query
// (WHERE status='dlq' AND completed_at > now() - interval '7d')
// can find recent rows. The webhook runtime (SPRINT 4.2) emits a
// post.failed event on this transition so the workspace owner's
// webhook endpoint gets notified.
func (r *PostRepository) MarkDeadLetter(id int64, ownerID string, lastError string) error {
	if ownerID == "" {
		return fmt.Errorf("MarkDeadLetter: ownerID is empty")
	}
	return r.mutateLeasedTarget(id, ownerID, qMarkDeadLetter, lastError)
}

// MarkRetrying (SPRINT 5.2) increments attempt_count + stamps
// next_retry_at + clears the lease. CAS on lease_owner_id. The
// publish driver re-picks the row on its next tick when
// next_retry_at <= NOW() (the existing ListPending filter is
// extended to include next_retry_at in commit 2's publish_worker
// rewrite).
//
// backoff is the AWS-decorrelated-jitter delay computed by the
// worker. The supplied time.Time is the next-attempt absolute
// timestamp (now + backoff).
func (r *PostRepository) MarkRetrying(id int64, ownerID string, lastError string, nextAttemptAt time.Time) error {
	if ownerID == "" {
		return fmt.Errorf("MarkRetrying: ownerID is empty")
	}
	return r.mutateLeasedTarget(id, ownerID, qMarkRetrying, nextAttemptAt, lastError)
}

// MarkDriveRequiredFailed (Task 8/10.1) transitions a target from
// status='published' to 'drive_required_failed': the platform publish
// completed but the drive_required policy gate detected a terminally
// failed required Drive upload (see
// internal/worker/publish_worker_delivery.go). CAS on the CURRENT
// status being 'published' so the writeback can never regress a row
// that moved on (or was operator-corrected) after the publish — a
// row that is no longer 'published' at write time returns
// ErrPostTargetTransitionStale and the caller logs the outcome.
//
// The transition runs inside the canonical aggregate-aware transaction
// (lock target → lock parent → CAS → recompute parent status), the same
// shape as updateTargetStatusTx. The recompute is mandatory, not
// optional: without it the parent post would keep reading 'published'
// after a child flipped to a terminal policy failure — re-creating the
// exact "declared success without completing" bug this writeback
// closes, one level up. Until the repair sweep ticked, the parent
// would even be visible as fully published in the API.
//
// lastError is persisted to error_message for operator visibility.
// last_error_code is set to 'DRIVE_REQUIRED' (stable code indexed by
// dashboards, mirroring the 'DLQ' / 'RATE_LIMITED' convention).
func (r *PostRepository) MarkDriveRequiredFailed(id int64, lastError string) (err error) {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("MarkDriveRequiredFailed: begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = markDriveRequiredFailedTx(tx, id, lastError); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("MarkDriveRequiredFailed: commit: %w", err)
	}
	return nil
}

func markDriveRequiredFailedTx(tx *sql.Tx, targetID int64, lastError string) error {
	postID, err := postIDForTargetTx(tx, targetID)
	if err != nil {
		return err
	}
	if err := lockTargetTx(tx, targetID); err != nil {
		return err
	}
	if err := lockPostTx(tx, postID); err != nil {
		return err
	}
	result, err := tx.Exec(qMarkDriveRequiredFailed, models.PostStatusDriveRequiredFailed, lastError, targetID)
	if err != nil {
		// A 22P06-style failure means the enum value is missing —
		// only possible if migration 130 was not applied. Surface it
		// as a wrapped error, never as a silent skip.
		return fmt.Errorf("mark drive_required_failed target %d: %w", targetID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read drive_required_failed rows affected: %w", err)
	}
	if n == 0 {
		var current models.PostStatus
		statusErr := tx.QueryRow(qSelectTargetStatusByID, targetID).Scan(&current)
		if errors.Is(statusErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
		}
		if statusErr != nil {
			return fmt.Errorf("read stale target %d: %w", targetID, statusErr)
		}
		return fmt.Errorf("%w: id=%d current=%s", ErrPostTargetTransitionStale, targetID, current)
	}
	return persistAggregatePostStatusLockedTx(tx, postID)
}

var qMarkDriveRequiredFailed = `UPDATE post_targets
 SET status = $1::text::post_status,
     error_message = $2,
     last_error_code = 'DRIVE_REQUIRED',
     completed_at = NOW()
 WHERE id = $3
   AND status = 'published'::post_status`

// MarkRateLimited (SPRINT 5.2) handles the platform's 429/Retry-After
// response. Stamps next_retry_at and rate_limit_reset_at to the
// platform's hint, clears the lease, and (critically) does NOT
// increment attempt_count. Rate-limit is not a fault — the
// platform explicitly told us when to come back, so retrying
// sooner is the right behavior. attempt_count stays bounded by
// actual transient failures (5xx, network), not by platform
// throttling.
//
// The publish driver re-picks the row when next_retry_at <= NOW()
// (the existing ListPending filter, when extended in commit 2,
// handles this). status stays 'queued' so the next claim is
// permitted by ClaimQueuedTargetWithLease's WHERE clause.
// UpdateStatusWithLease is the child-job terminal/intermediate CAS. It
// releases the lease only when the caller still owns the publishing row.
func (r *PostRepository) UpdateStatusWithLease(target *models.PostTarget, ownerID string) error {
	if target == nil {
		return fmt.Errorf("update post_target with lease: nil target")
	}
	if ownerID == "" {
		return fmt.Errorf("update post_target with lease: empty ownerID")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin leased target status: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	postID, err := postIDForTargetTx(tx, target.ID)
	if err != nil {
		return err
	}
	if err = lockPostTx(tx, postID); err != nil {
		return err
	}
	result, err := tx.Exec(qUpdateTargetStatusWithLease,
		target.Status, target.PlatformPostID, target.ErrorMessage,
		target.PublishedAt, target.ID, target.ProviderState, target.ContainerID, ownerID)
	if err != nil {
		return fmt.Errorf("update leased target status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read leased target status rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d owner=%s", ErrPostTargetTransitionStale, target.ID, ownerID)
	}
	if err = persistAggregatePostStatusLockedTx(tx, postID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit leased target status: %w", err)
	}
	return nil
}

// MarkRateLimitedRetryWithLease requeues a rate-limited child while preserving
// the lease CAS used by the publication worker. The parent aggregate is
// recalculated in the same transaction, so one child can wait without
// changing any sibling's state.
func (r *PostRepository) MarkRateLimitedRetryWithLease(id int64, ownerID string, nextAttemptAt time.Time, lastError string) error {
	if ownerID == "" {
		return fmt.Errorf("MarkRateLimitedRetryWithLease: ownerID is empty")
	}
	return r.mutateLeasedTarget(id, ownerID, qMarkRateLimitedRetryWithLease, nextAttemptAt, lastError)
}

func (r *PostRepository) MarkRateLimited(id int64, ownerID string, retryAfter time.Time) error {
	if ownerID == "" {
		return fmt.Errorf("MarkRateLimited: ownerID is empty")
	}
	return r.mutateLeasedTarget(id, ownerID, qMarkRateLimited, retryAfter)
}

// ReclaimExpiredLeases (SPRINT 5.2) takes over rows whose lease
// expired. The reconciler calls this on every tick as the first
// step of its work loop. A row is reclaimable if:
//   - leased_until <= NOW() (the lease is past its TTL)
//   - lease_owner_id != $myWorkerID (I'm not the one holding the
//     expired lease; reclaiming my own would be a no-op)
//   - status IN ('publishing', 'queued') (DLQ and published are
//     terminal; the publish driver only picks up queued/waiting_provider)
//
// On reclaim the row is reset to status='queued' (a crashed
// mid-publish row becomes pending so the driver re-picks it),
// lease fields are cleared, and next_retry_at = NOW() so the
// driver picks it up immediately on the next tick (no
// next_retry_at wait for crash-recovery).
//
// NOTE: attempt_count is INTENTIONALLY NOT bumped on reclaim. A
// crash is not a "real" attempt — the platform never saw a publish
// call (or saw one that returned mid-flight). The next
// MarkRetrying on this row is the one that increments. This keeps
// attempt_count bounded by actual transient failures (5xx,
// network) and not by replica crash/restart cycles.
//
// Returns the number of rows reclaimed. A replica running
// ReclaimExpiredLeases with a unique myWorkerID can safely share
// the table with peers — the WHERE lease_owner_id != $myWorkerID
// filter ensures two replicas don't fight over the same row
// (the second replica's reclaim finds lease_owner_id = NULL
// already and is a no-op).
func (r *PostRepository) ReclaimExpiredLeases(myWorkerID string) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin reclaim leases tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.Query(qSelectExpiredLeaseTargets, myWorkerID)
	if err != nil {
		return 0, fmt.Errorf("failed to select expired leases: %w", err)
	}
	var targets []struct {
		id     int64
		postID int64
	}
	for rows.Next() {
		var target struct {
			id     int64
			postID int64
		}
		if err = rows.Scan(&target.id, &target.postID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("failed to scan expired lease: %w", err)
		}
		targets = append(targets, target)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("failed to read expired leases: %w", err)
	}
	rows.Close()

	var reclaimed int64
	for _, target := range targets {
		if err = lockPostTx(tx, target.postID); err != nil {
			return 0, err
		}
		result, execErr := tx.Exec(qReclaimExpiredLeaseByID, target.id, myWorkerID)
		if execErr != nil {
			return 0, fmt.Errorf("failed to reclaim lease %d: %w", target.id, execErr)
		}
		var affected int64
		affected, err = result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("failed to read reclaimed lease %d rows affected: %w", target.id, err)
		}
		if affected == 0 {
			continue
		}
		if err = persistAggregatePostStatusLockedTx(tx, target.postID); err != nil {
			return 0, err
		}
		reclaimed += affected
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit reclaim leases: %w", err)
	}
	return reclaimed, nil
}

// ClaimWaitingProviderTarget atomically transitions a post_target from
// status='waiting_provider' to status='publishing' using SELECT FOR
// UPDATE SKIP LOCKED (same pattern as ClaimQueuedTargetWithLease —
// see that method's docstring for the FASE 1.1 rationale).
func (r *PostRepository) ClaimWaitingProviderTarget(id int64) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin claim-waiting tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var foundID int64
	err = tx.QueryRow(
		qClaimWaitingProviderTargetSelect,
		id,
	).Scan(&foundID)
	if err == sql.ErrNoRows {
		_ = tx.Rollback()
		err = nil // prevent deferred double-rollback
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to select for update (waiting): %w", err)
	}

	if err = claimTargetTx(tx, id, qClaimQueuedTargetUpdate, id); err != nil {
		return false, fmt.Errorf("failed to update claimed waiting target: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit claim-waiting: %w", err)
	}
	return true, nil
}
