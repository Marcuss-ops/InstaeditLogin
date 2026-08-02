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

// MarkOAuthConnectionAccountsReauthRequired marks every YouTube channel
// attached to the supplied OAuth grant. The caller passes the canonical
// oauth_connections.id directly; no platform-account lookup is needed and
// the fan-out happens in one UPDATE so a shared grant cannot leave sibling
// channels publishable after invalid_grant.
func (r *UserRepository) MarkOAuthConnectionAccountsReauthRequired(ctx context.Context, oauthConnectionID int64, code, message string) error {
	if oauthConnectionID <= 0 {
		return fmt.Errorf("mark OAuth connection reauth required: invalid OAuth connection id %d", oauthConnectionID)
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE platform_accounts
		 SET status = 'reauth_required',
		     reauth_required_at = NOW(),
		     last_error_code = $1,
		     last_error_message = $2,
		     updated_at = NOW()
		 WHERE platform = 'youtube'
		   AND oauth_connection_id = $3`,
		code, message, oauthConnectionID)
	if err != nil {
		return fmt.Errorf("mark OAuth connection reauth required: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark OAuth connection reauth required: read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("mark OAuth connection reauth required: no linked YouTube accounts for OAuth connection %d", oauthConnectionID)
	}
	return nil
}
