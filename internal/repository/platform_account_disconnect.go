package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// DisconnectPlatformAccount marks one platform account disconnected and
// returns whether no active sibling remains on its OAuth grant. The status
// transition and sibling decision run under the grant's transaction-scoped
// advisory lock, so concurrent channel disconnects cannot both miss the last
// active channel and orphan the shared grant.
//
// P1 (account-lifecycle audit) — the explicit disconnect also performs the
// channel cleanup in the SAME transaction as the status flip:
//   - removes the account from every group (group_accounts rows);
//   - removes it from publishable destinations (workspace_channels rows);
//   - cancels its future jobs (non-terminal post_targets → draft, parent
//     aggregates recomputed via PostAggregateStatusResolver);
//   - never touches platform_accounts beyond the status flip (row kept for
//     audit) and never revokes the shared grant while a sibling is active
//     (the caller decides revocation from the returned lastOnGrant).
func (r *UserRepository) DisconnectPlatformAccount(ctx context.Context, accountID int64) (lastOnGrant bool, handled bool, err error) {
	if accountID <= 0 {
		return false, true, fmt.Errorf("disconnect platform account: invalid id %d", accountID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, true, fmt.Errorf("disconnect platform account: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var oauthConnectionID sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT oauth_connection_id
		   FROM platform_accounts
		  WHERE id = $1
		  FOR UPDATE`, accountID,
	).Scan(&oauthConnectionID); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: load account %d: %w", accountID, err)
	}

	if oauthConnectionID.Valid {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", oauthConnectionID.Int64); err != nil {
			return false, true, fmt.Errorf("disconnect platform account: acquire grant lock: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		    SET status = 'disconnected',
		        connected_at = NULL,
		        last_error_code = 'DISCONNECTED',
		        last_error_message = 'account disconnected by user',
		        updated_at = NOW()
		  WHERE id = $1`, accountID,
	); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: mark disconnected: %w", err)
	}

	if oauthConnectionID.Valid {
		var activeAccounts int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*)
			   FROM platform_accounts
			  WHERE oauth_connection_id = $1
			    AND status = 'active'`, oauthConnectionID.Int64,
		).Scan(&activeAccounts); err != nil {
			return false, true, fmt.Errorf("disconnect platform account: count active siblings: %w", err)
		}
		lastOnGrant = activeAccounts == 0
	} else {
		lastOnGrant = false
	}

	// P1 cleanup: folders, publishable destinations, future jobs.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM group_accounts WHERE account_id = $1`, accountID,
	); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: remove group memberships: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM workspace_channels WHERE platform_account_id = $1`, accountID,
	); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: remove workspace channels: %w", err)
	}
	if err := cancelFutureJobsTx(tx, accountID); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: cancel future jobs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: commit: %w", err)
	}
	committed = true
	return lastOnGrant, true, nil
}

// cancelFutureJobsTx cancels every non-terminal post target for the account
// (future/scheduled jobs) by resetting it to 'draft' and recomputes the
// parent post aggregates with the same lock order and resolver as
// PostRepository.CancelPost. Terminal targets (published, failed, DLQ) are
// preserved as historical record.
func cancelFutureJobsTx(tx *sql.Tx, accountID int64) error {
	rows, err := tx.Query(qCancelFutureJobsForAccount, accountID)
	if err != nil {
		return fmt.Errorf("cancel future jobs for account %d: %w", accountID, err)
	}
	seen := make(map[int64]struct{})
	var postIDs []int64
	for rows.Next() {
		var postID int64
		if err := rows.Scan(&postID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("cancel future jobs for account %d: scan post_id: %w", accountID, err)
		}
		if _, ok := seen[postID]; !ok {
			seen[postID] = struct{}{}
			postIDs = append(postIDs, postID)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("cancel future jobs for account %d: iterate: %w", accountID, err)
	}
	_ = rows.Close()
	for _, postID := range postIDs {
		if _, err := lockTargetsForPostTx(tx, postID); err != nil {
			return fmt.Errorf("cancel future jobs for account %d: lock targets of post %d: %w", accountID, postID, err)
		}
		if err := lockPostTx(tx, postID); err != nil {
			return fmt.Errorf("cancel future jobs for account %d: lock post %d: %w", accountID, postID, err)
		}
		if err := persistAggregatePostStatusLockedTx(tx, postID); err != nil {
			return fmt.Errorf("cancel future jobs for account %d: recompute aggregate of post %d: %w", accountID, postID, err)
		}
	}
	return nil
}
