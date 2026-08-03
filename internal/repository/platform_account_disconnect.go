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

	if err := tx.Commit(); err != nil {
		return false, true, fmt.Errorf("disconnect platform account: commit: %w", err)
	}
	committed = true
	return lastOnGrant, true, nil
}
