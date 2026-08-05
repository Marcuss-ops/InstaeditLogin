package repository

import (
	"context"
	"fmt"
	"time"
)

// MarkReauthRequired (P0#3 server-side YouTube channel binding check,
// publish_worker.go) atomically flips a platform_account's lifecycle
// flags to "the grant is structurally unfit, prompt the operator to
// reconnect before the next publish attempt". The flag write is a
// single UPDATE so two concurrent calls (e.g. two worker replicas
// reacting to the same denied upload) cannot drift out of sync.
//
// Behaviour:
//   - status -> 'reauth_required'
//   - reauth_required_at -> NOW()
//   - last_error_code -> code (short, programmatic; e.g.
//     "youtube_channel_mismatch")
//   - last_error_message -> message (human-readable; e.g.
//     "expected UCabc..., grant bound to [UCxyz...]")
//   - updated_at -> NOW()
//
// Idempotent: re-calling refreshes timestamps. The publish worker
// treats the returned error as a soft failure (logs at WARN) and
// still proceeds to mark the post_target 'failed' so the user sees
// a structured error message in the dashboard.
//
// Returns ErrUserNotFound when the id does not match any row,
// wrapped with the id for caller diagnostics.
func (r *UserRepository) MarkReauthRequired(ctx context.Context, id int64, code, message string) error {
	if id <= 0 {
		return fmt.Errorf("mark reauth required: invalid id %d (must be > 0)", id)
	}
	now := time.Now()
	result, err := r.db.ExecContext(ctx,
		`UPDATE platform_accounts
		 SET status = 'reauth_required',
		     reauth_required_at = $1,
		     last_error_code = $2,
		     last_error_message = $3,
		     updated_at = $4
		 WHERE id = $5`,
		now, code, message, now, id,
	)
	if err != nil {
		return fmt.Errorf("mark reauth required: update platform_accounts: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark reauth required: read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrUserNotFound, id)
	}
	return nil
}

// CountActiveAccountsOnConnection returns the number of platform_accounts
// with status='active' that share the supplied OAuth grant
// (oauth_connection_id), excluding excludeAccountID (the account being
// disconnected). The disconnect orchestrator (pkg/api handleDeleteAccount)
// uses this to decide whether the grant tokens may be deleted: migrations
// 084/085 let several channels share one oauth_connection, so vault.Revoke
// + remote provider revoke may only run when this count is 0 — otherwise
// the sibling channels would be left 'active' with no usable credentials.
func (r *UserRepository) CountActiveAccountsOnConnection(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		    AND id <> $2
		    AND status = 'active'`,
		oauthConnectionID, excludeAccountID,
	).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active accounts on connection %d: %w", oauthConnectionID, err)
	}
	return n, nil
}

// MarkOAuthConnectionAccountsReauthRequired propagates the shared-grant
// reconnect state to every linked account except explicitly disconnected
// accounts. The two updates are committed together so a failed grant cannot
// leave siblings displaying an active state.
func (r *UserRepository) MarkOAuthConnectionAccountsReauthRequired(ctx context.Context, oauthConnectionID int64, code, message string) error {
	if oauthConnectionID <= 0 {
		return fmt.Errorf("mark OAuth connection reauth required: invalid OAuth connection id %d", oauthConnectionID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("mark OAuth connection reauth required: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx,
		`UPDATE oauth_connections
		    SET status = 'reauth_required',
		        last_refresh_error = 'invalid_grant',
		        updated_at = NOW()
		  WHERE id = $1`, oauthConnectionID); err != nil {
		return fmt.Errorf("mark OAuth connection reauth required: update grant: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		    SET status = 'reauth_required',
		        reauth_required_at = NOW(),
		        last_error_code = $1,
		        last_error_message = $2,
		        updated_at = NOW()
		  WHERE oauth_connection_id = $3
		    AND status <> 'disconnected'`,
		code, message, oauthConnectionID); err != nil {
		return fmt.Errorf("mark OAuth connection reauth required: propagate accounts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("mark OAuth connection reauth required: commit: %w", err)
	}
	committed = true
	return nil
}
