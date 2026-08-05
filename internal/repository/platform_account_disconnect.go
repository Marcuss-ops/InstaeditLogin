package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// DisconnectPlatformAccount marks one platform account disconnected and
// returns whether no active sibling remains on its OAuth grant. The status
// transition, sibling decision, optional remote revoke, token cleanup and
// channel cleanup run under the grant's transaction-scoped advisory lock.
//
// It is retained as a compatibility wrapper; new callers should use
// DisconnectPlatformAccountTx when they need remote provider revocation
// coordinated with the local transaction.
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
	return r.disconnectPlatformAccountTx(ctx, accountID, nil, false)
}

// DisconnectPlatformAccountTx performs the complete single-channel
// disconnect in one transaction. The revoke callback is invoked only when
// this account was active and is the last active account on its grant. It is
// called while the grant lock is held and before local token deletion; a
// non-nil error rolls the transaction back.
func (r *UserRepository) DisconnectPlatformAccountTx(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (lastOnGrant bool, handled bool, err error) {
	return r.disconnectPlatformAccountTx(ctx, accountID, revoke, true)
}

func (r *UserRepository) disconnectPlatformAccountTx(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error, transactionalGrantRevoke bool) (lastOnGrant bool, handled bool, err error) {
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
	var status string
	var loadErr error
	if transactionalGrantRevoke {
		loadErr = tx.QueryRowContext(ctx,
			`SELECT oauth_connection_id, status
			   FROM platform_accounts
			  WHERE id = $1
			  FOR UPDATE`, accountID,
		).Scan(&oauthConnectionID, &status)
	} else {
		loadErr = tx.QueryRowContext(ctx,
			`SELECT oauth_connection_id
			   FROM platform_accounts
			  WHERE id = $1
			  FOR UPDATE`, accountID,
		).Scan(&oauthConnectionID)
	}
	if loadErr != nil {
		return false, true, fmt.Errorf("disconnect platform account: load account %d: %w", accountID, loadErr)
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

	// A repeated request for an already-disconnected account is a no-op for
	// grant revocation. This prevents a double click/retry from revoking the
	// same remote grant again or deleting a sibling's shared credentials.
	if oauthConnectionID.Valid && (!transactionalGrantRevoke || status != "disconnected") {
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
	}

	if lastOnGrant && transactionalGrantRevoke {
		if revoke != nil {
			if err := revoke(ctx, tx); err != nil {
				return false, true, fmt.Errorf("disconnect platform account: remote revoke: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE oauth_connections
			    SET status = 'disconnected',
			        reauth_required_at = NULL,
			        last_refresh_error = NULL,
			        updated_at = NOW()
			  WHERE id = $1`, oauthConnectionID.Int64,
		); err != nil {
			return false, true, fmt.Errorf("disconnect platform account: disconnect OAuth connection: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tokens WHERE oauth_connection_id = $1`, oauthConnectionID.Int64,
		); err != nil {
			return false, true, fmt.Errorf("disconnect platform account: delete grant tokens: %w", err)
		}
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
