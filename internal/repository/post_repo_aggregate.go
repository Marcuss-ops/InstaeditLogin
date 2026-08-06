package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func postIDForTargetTx(tx *sql.Tx, targetID int64) (int64, error) {
	var postID int64
	if err := tx.QueryRow(qSelectPostIDByTarget, targetID).Scan(&postID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
		}
		return 0, fmt.Errorf("find parent for target %d: %w", targetID, err)
	}
	return postID, nil
}

// lockTargetsForPostTx locks every target in deterministic ID order. The
// repository uses target -> parent lock ordering for all target transitions.
func lockTargetsForPostTx(tx *sql.Tx, postID int64) ([]int64, error) {
	rows, err := tx.Query(qSelectTargetIDsByPost, postID)
	if err != nil {
		return nil, fmt.Errorf("lock targets for post %d: %w", postID, err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan target lock for post %d: %w", postID, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("target lock rows for post %d: %w", postID, err)
	}
	return ids, nil
}

func lockTargetTx(tx *sql.Tx, targetID int64) error {
	var lockedID int64
	if err := tx.QueryRow(qLockTargetForAggregate, targetID).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
		}
		return fmt.Errorf("lock target for aggregate: %w", err)
	}
	return nil
}

func lockPostTx(tx *sql.Tx, postID int64) error {
	var lockedID int64
	if err := tx.QueryRow(qLockPostForAggregate, postID).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id=%d", ErrPostNotFound, postID)
		}
		return fmt.Errorf("lock post for aggregate: %w", err)
	}
	return nil
}

func resolvePostStatusLockedTx(tx *sql.Tx, postID int64) (models.PostStatus, error) {
	rows, err := tx.Query(qSelectTargetStatusesByPost, postID)
	if err != nil {
		return "", fmt.Errorf("select target statuses for post %d: %w", postID, err)
	}
	defer rows.Close()

	var targets []models.PostTarget
	for rows.Next() {
		var target models.PostTarget
		if err := rows.Scan(&target.Status); err != nil {
			return "", fmt.Errorf("scan target status for post %d: %w", postID, err)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("target status rows for post %d: %w", postID, err)
	}
	status, err := models.NewPostAggregateStatusResolver().Resolve(targets)
	if err != nil {
		return "", err
	}
	return status, nil
}

func persistAggregatePostStatusLockedTx(tx *sql.Tx, postID int64) error {
	status, err := resolvePostStatusLockedTx(tx, postID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(qUpdatePostAggregateStatus, status, postID); err != nil {
		return fmt.Errorf("update aggregate status for post %d: %w", postID, err)
	}
	return nil
}

func updateTargetStatusTx(tx *sql.Tx, target *models.PostTarget) error {
	postID, err := postIDForTargetTx(tx, target.ID)
	if err != nil {
		return err
	}
	if err := lockTargetTx(tx, target.ID); err != nil {
		return err
	}
	if err := lockPostTx(tx, postID); err != nil {
		return err
	}

	result, err := tx.Exec(
		qUpdateTargetStatus,
		target.Status, target.PlatformPostID, target.ErrorMessage,
		target.PublishedAt, target.ID, target.ProviderState, target.ContainerID,
	)
	if err != nil {
		return fmt.Errorf("failed to update post_target status: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		var current models.PostStatus
		statusErr := tx.QueryRow(qSelectTargetStatusByID, target.ID).Scan(&current)
		if errors.Is(statusErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, target.ID)
		}
		if statusErr != nil {
			return fmt.Errorf("read stale target %d: %w", target.ID, statusErr)
		}
		if current.IsTerminal() {
			return fmt.Errorf("%w: id=%d current=%s", ErrPostTargetTransitionStale, target.ID, current)
		}
		return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, target.ID)
	}
	return persistAggregatePostStatusLockedTx(tx, postID)
}

// claimTargetTx is called after SELECT ... FOR UPDATE SKIP LOCKED has
// already locked the target. It locks the parent, mutates the target, and
// resolves the aggregate before commit.
func claimTargetTx(tx *sql.Tx, targetID int64, updateQuery string, args ...any) error {
	postID, err := postIDForTargetTx(tx, targetID)
	if err != nil {
		return err
	}
	if err := lockPostTx(tx, postID); err != nil {
		return err
	}
	result, err := tx.Exec(updateQuery, args...)
	if err != nil {
		return fmt.Errorf("update claimed target: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read claimed target rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
	}
	return persistAggregatePostStatusLockedTx(tx, postID)
}

// resetPostTargetsAndAggregateTx handles PublishPost/RetryPost. It locks
// children in deterministic order, then the parent, performs the reset, and
// derives the parent exclusively through PostAggregateStatusResolver.
func resetPostTargetsAndAggregateTx(tx *sql.Tx, postID int64, resetQuery string) error {
	if _, err := lockTargetsForPostTx(tx, postID); err != nil {
		return err
	}
	if err := lockPostTx(tx, postID); err != nil {
		return err
	}
	if _, err := tx.Exec(resetQuery, postID); err != nil {
		return fmt.Errorf("reset targets for post %d: %w", postID, err)
	}
	return persistAggregatePostStatusLockedTx(tx, postID)
}

// resetTargetAndAggregateTx handles a single explicit retry transition.
func resetTargetAndAggregateTx(tx *sql.Tx, targetID int64) error {
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
	result, err := tx.Exec(qRetryTargetResetTarget, targetID)
	if err != nil {
		return fmt.Errorf("reset target %d: %w", targetID, err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read reset target rows affected: %w", err)
	} else if n == 0 {
		var current models.PostStatus
		statusErr := tx.QueryRow(qSelectTargetStatusByID, targetID).Scan(&current)
		if errors.Is(statusErr, sql.ErrNoRows) {
			return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
		}
		if statusErr != nil {
			return fmt.Errorf("read stale target %d: %w", targetID, statusErr)
		}
		if current.IsTerminal() {
			return fmt.Errorf("%w: id=%d current=%s", ErrPostTargetTransitionStale, targetID, current)
		}
		return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
	}
	return persistAggregatePostStatusLockedTx(tx, postID)
}

// mutateLeasedTargetTx handles DLQ/retrying/rate-limit transitions. A lost
// lease remains an idempotent no-op, preserving the existing worker contract.
func mutateLeasedTargetTx(tx *sql.Tx, targetID int64, ownerID, query string, args ...any) error {
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
	bindArgs := make([]any, 0, 2+len(args))
	bindArgs = append(bindArgs, targetID, ownerID)
	bindArgs = append(bindArgs, args...)
	result, err := tx.Exec(query, bindArgs...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read leased target rows affected: %w", err)
	}
	if n == 0 {
		return nil
	}
	return persistAggregatePostStatusLockedTx(tx, postID)
}

func (r *PostRepository) mutateLeasedTarget(targetID int64, ownerID, query string, args ...any) (err error) {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin leased target mutation: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = mutateLeasedTargetTx(tx, targetID, ownerID, query, args...); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit leased target mutation: %w", err)
	}
	return nil
}

func (r *PostRepository) updateStatusWithAggregate(target *models.PostTarget) (err error) {
	if target == nil {
		return errors.New("failed to update post_target status: target is nil")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin target-status tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = updateTargetStatusTx(tx, target); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit target-status tx: %w", err)
	}
	return nil
}

// ListDirtyAggregatePostIDs returns the oldest deduplicated parent IDs that
// were marked dirty by the post_targets transition trigger. It is deliberately
// bounded so repair work is proportional to changed targets, not database age.
func (r *PostRepository) ListDirtyAggregatePostIDs(limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("list dirty aggregate posts: limit must be positive (got %d)", limit)
	}
	rows, err := r.db.Query(qSelectDirtyAggregatePostIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("list dirty aggregate posts: %w", err)
	}
	defer rows.Close()

	var postIDs []int64
	for rows.Next() {
		var postID int64
		if err := rows.Scan(&postID); err != nil {
			return nil, fmt.Errorf("scan dirty aggregate post: %w", err)
		}
		postIDs = append(postIDs, postID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read dirty aggregate posts: %w", err)
	}
	return postIDs, nil
}

// RepairDirtyAggregatePost resolves one queued parent and removes its queue
// row in the same transaction. If repair fails, the transaction rolls back
// and the queue row remains for a later retry.
func (r *PostRepository) RepairDirtyAggregatePost(postID int64) error {
	if postID <= 0 {
		return fmt.Errorf("repair dirty aggregate post: postID must be positive (got %d)", postID)
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin dirty aggregate repair for post %d: %w", postID, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Match every target transition's lock order: target(s) → parent →
	// queue row. This avoids a transition holding a target/parent lock while
	// waiting for a queue row that the repair worker already holds.
	if _, err = lockTargetsForPostTx(tx, postID); err != nil {
		return fmt.Errorf("lock dirty aggregate targets for post %d: %w", postID, err)
	}
	if err = lockPostTx(tx, postID); err != nil {
		return fmt.Errorf("lock dirty aggregate parent %d: %w", postID, err)
	}
	var queuedPostID int64
	if err = tx.QueryRow(qLockDirtyAggregatePost, postID).Scan(&queuedPostID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			_ = tx.Rollback()
			return nil
		}
		return fmt.Errorf("lock dirty aggregate post %d: %w", postID, err)
	}
	if err = persistAggregatePostStatusLockedTx(tx, postID); err != nil {
		return fmt.Errorf("repair dirty aggregate post %d: %w", postID, err)
	}
	if _, err = tx.Exec(qDeleteDirtyAggregatePost, postID); err != nil {
		return fmt.Errorf("dequeue dirty aggregate post %d: %w", postID, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit dirty aggregate repair for post %d: %w", postID, err)
	}
	return nil
}

// RepairAggregateStatusForPost repairs one parent aggregate in a transaction.
// The target set is always resolved by PostAggregateStatusResolver; this
// method never infers or assigns a target status. It is used by the targeted
// operational repair command for a known YouTube publication and is also
// safe to call repeatedly.
func (r *PostRepository) RepairAggregateStatusForPost(postID int64) (oldStatus, newStatus models.PostStatus, changed bool, err error) {
	if postID <= 0 {
		return "", "", false, fmt.Errorf("repair aggregate status: postID must be positive (got %d)", postID)
	}

	tx, err := r.db.Begin()
	if err != nil {
		return "", "", false, fmt.Errorf("begin targeted aggregate repair for post %d: %w", postID, err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = lockTargetsForPostTx(tx, postID); err != nil {
		return "", "", false, fmt.Errorf("lock targeted aggregate targets for post %d: %w", postID, err)
	}
	if err = tx.QueryRow(qLockPostStatusForAggregate, postID).Scan(&oldStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, fmt.Errorf("%w: id=%d", ErrPostNotFound, postID)
		}
		return "", "", false, fmt.Errorf("lock targeted aggregate post %d: %w", postID, err)
	}

	newStatus, err = resolvePostStatusLockedTx(tx, postID)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve targeted aggregate status for post %d: %w", postID, err)
	}
	changed = oldStatus != newStatus
	if changed {
		if _, err = tx.Exec(qUpdatePostAggregateStatus, newStatus, postID); err != nil {
			return "", "", false, fmt.Errorf("persist targeted aggregate status for post %d: %w", postID, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return "", "", false, fmt.Errorf("commit targeted aggregate repair for post %d: %w", postID, err)
	}
	return oldStatus, newStatus, changed, nil
}

// RepairAggregateStatuses is an idempotent safety sweep. It locks targets
// before parents, matching every target transition path, includes zero-target
// posts, and writes the parent only when drift exists.
func (r *PostRepository) RepairAggregateStatuses() (repaired int, err error) {
	rows, err := r.db.Query(`SELECT id, status FROM posts ORDER BY id ASC`)
	if err != nil {
		return 0, fmt.Errorf("select aggregate repair candidates: %w", err)
	}
	var postIDs []int64
	for rows.Next() {
		var postID int64
		var status models.PostStatus
		if err := rows.Scan(&postID, &status); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan aggregate repair candidate: %w", err)
		}
		postIDs = append(postIDs, postID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("aggregate repair rows: %w", err)
	}
	rows.Close()

	for _, postID := range postIDs {
		tx, txErr := r.db.Begin()
		if txErr != nil {
			return repaired, fmt.Errorf("begin aggregate repair for post %d: %w", postID, txErr)
		}
		if _, txErr = lockTargetsForPostTx(tx, postID); txErr != nil {
			_ = tx.Rollback()
			return repaired, fmt.Errorf("lock aggregate repair targets for post %d: %w", postID, txErr)
		}
		var oldStatus models.PostStatus
		if txErr = tx.QueryRow(qLockPostStatusForAggregate, postID).Scan(&oldStatus); txErr != nil {
			_ = tx.Rollback()
			if errors.Is(txErr, sql.ErrNoRows) {
				continue
			}
			return repaired, fmt.Errorf("lock aggregate repair post %d: %w", postID, txErr)
		}
		newStatus, resolveErr := resolvePostStatusLockedTx(tx, postID)
		if resolveErr != nil {
			_ = tx.Rollback()
			return repaired, fmt.Errorf("repair aggregate status for post %d: %w", postID, resolveErr)
		}
		if newStatus != oldStatus {
			if _, txErr = tx.Exec(qUpdatePostAggregateStatus, newStatus, postID); txErr != nil {
				_ = tx.Rollback()
				return repaired, fmt.Errorf("persist repaired status for post %d: %w", postID, txErr)
			}
			repaired++
		}
		if txErr = tx.Commit(); txErr != nil {
			return repaired, fmt.Errorf("commit aggregate repair for post %d: %w", postID, txErr)
		}
	}
	return repaired, nil
}

func (r *PostRepository) setTargetStatusWithAggregate(ctx context.Context, targetID int64, status models.PostStatus, errorMessage string) (err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("post target SetTargetStatus begin: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	postID, err := postIDForTargetTx(tx, targetID)
	if err != nil {
		return err
	}
	if err = lockTargetTx(tx, targetID); err != nil {
		return err
	}
	if err = lockPostTx(tx, postID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE post_targets
		 SET status = $2,
		     error_message = COALESCE(NULLIF($3, ''), error_message),
		     version = version + 1,
		     updated_at = NOW()
		 WHERE id = $1
		   AND (status = $2 OR status NOT IN ('published', 'partially_published', 'failed', 'dlq', 'dead_letter', 'blocked_auth'))`, targetID, string(status), errorMessage)
	if err != nil {
		return fmt.Errorf("post target SetTargetStatus: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("post target SetTargetStatus rows affected: %w", err)
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
		if current.IsTerminal() {
			return fmt.Errorf("%w: id=%d current=%s", ErrPostTargetTransitionStale, targetID, current)
		}
		return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, targetID)
	}
	if err = persistAggregatePostStatusLockedTx(tx, postID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("post target SetTargetStatus commit: %w", err)
	}
	return nil
}
