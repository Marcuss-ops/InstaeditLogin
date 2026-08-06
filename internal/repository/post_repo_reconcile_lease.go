package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const defaultReconcileLeaseTTL = 60 * time.Second

// ErrReconcileLeaseLost means a worker attempted to renew, release, schedule,
// or complete a target after another worker had taken over or the lease expired.
var ErrReconcileLeaseLost = fmt.Errorf("reconcile target lease lost")

func validateReconcileLease(ownerID string, leaseTTL time.Duration) (string, error) {
	if ownerID == "" {
		return "", fmt.Errorf("reconcile lease owner is empty")
	}
	if leaseTTL <= 0 {
		return "", fmt.Errorf("reconcile lease TTL must be positive (got %v)", leaseTTL)
	}
	seconds := int(leaseTTL.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%d", seconds), nil
}

// ClaimPublishingTargetWithReconcileLease atomically claims one ready target
// and persists ownership before provider I/O. Expired leases are reclaimable;
// active leases owned by another worker are skipped.
func (r *PostRepository) ClaimPublishingTarget(id int64, ownerID string, leaseTTL time.Duration) (bool, error) {
	seconds, err := validateReconcileLease(ownerID, leaseTTL)
	if err != nil {
		return false, err
	}
	var claimedID int64
	err = r.db.QueryRow(qClaimPublishingTargetSelect,
		id, ownerID, seconds).Scan(&claimedID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim publishing target with reconcile lease: %w", err)
	}
	return claimedID == id, nil
}

// HeartbeatReconcileTarget extends an active lease using owner-and-expiry CAS.
func (r *PostRepository) HeartbeatReconcileTarget(id int64, ownerID string, leaseTTL time.Duration) error {
	seconds, err := validateReconcileLease(ownerID, leaseTTL)
	if err != nil {
		return err
	}
	result, err := r.db.Exec(qHeartbeatReconcileTarget, id, ownerID, seconds)
	if err != nil {
		return fmt.Errorf("heartbeat reconcile target %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("heartbeat reconcile target %d rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: heartbeat target=%d owner=%s", ErrReconcileLeaseLost, id, ownerID)
	}
	return nil
}

// ReleaseReconcileTarget clears an active lease without changing target state.
// It is used when a provider poll remains in flight and the next poll is
// scheduled, or when a worker aborts before a terminal transition.
func (r *PostRepository) ReleaseReconcileTarget(id int64, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("reconcile lease owner is empty")
	}
	result, err := r.db.Exec(qReleaseReconcileTarget, id, ownerID)
	if err != nil {
		return fmt.Errorf("release reconcile target %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("release reconcile target %d rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: release target=%d owner=%s", ErrReconcileLeaseLost, id, ownerID)
	}
	return nil
}

// ScheduleNextReconcileWithLease advances adaptive backoff and releases the
// lease in one owner-CAS update. A stale worker cannot move the schedule.
func (r *PostRepository) ScheduleNextReconcileWithLease(id int64, ownerID string, expectedAttempt int, next time.Time) error {
	if ownerID == "" {
		return fmt.Errorf("reconcile lease owner is empty")
	}
	if expectedAttempt < 0 {
		return fmt.Errorf("reconcile attempt must be non-negative (got %d)", expectedAttempt)
	}
	if next.IsZero() {
		return fmt.Errorf("next reconcile time must be non-zero")
	}
	result, err := r.db.Exec(qScheduleNextReconcile, id, next, expectedAttempt, ownerID)
	if err != nil {
		return fmt.Errorf("schedule next reconcile for target %d: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("schedule next reconcile target %d rows affected: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: schedule target=%d owner=%s", ErrReconcileLeaseLost, id, ownerID)
	}
	return nil
}

// UpdateReconcileStatusWithLease persists a terminal transition only while the
// caller still owns a non-expired reconciler lease. It also clears that lease.
func (r *PostRepository) UpdateReconcileStatusWithLease(target *models.PostTarget, ownerID string) (err error) {
	if target == nil {
		return fmt.Errorf("update reconcile status: nil target")
	}
	if ownerID == "" {
		return fmt.Errorf("reconcile lease owner is empty")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reconcile status for target %d: %w", target.ID, err)
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
	if err = lockTargetTx(tx, target.ID); err != nil {
		return err
	}
	if err = lockPostTx(tx, postID); err != nil {
		return err
	}
	result, err := tx.Exec(qUpdateTargetStatusWithReconcileLease,
		target.Status, target.PlatformPostID, target.ErrorMessage,
		target.PublishedAt, target.ID, target.ProviderState, target.ContainerID, ownerID)
	if err != nil {
		return fmt.Errorf("update reconcile status for target %d: %w", target.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update reconcile status target %d rows affected: %w", target.ID, err)
	}
	if affected == 0 {
		return fmt.Errorf("%w: terminal target=%d owner=%s", ErrReconcileLeaseLost, target.ID, ownerID)
	}
	if err = persistAggregatePostStatusLockedTx(tx, postID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit reconcile status for target %d: %w", target.ID, err)
	}
	return nil
}
