package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// DisconnectOAuthGrantTx atomically disconnects an entire OAuth grant.
// It performs no provider call; callers that need remote revocation should use
// DisconnectOAuthGrantWithRevocationTx so the grant lock also covers token
// selection and remote revocation.

// The grant row and every linked platform account are locked before any
// mutation. The parent grant row is locked first, followed by its child
// account rows, so concurrent grant-level operations cannot form a parent/
// child lock cycle. Grant status, account status, token deletion,
// outbox delivery, and audit logging share one commit boundary.
//
// The operation is idempotent for an already disconnected grant: it still
// enforces the disconnected state and removes any leftover token rows, while
// recording the requested operation in the audit/outbox trail.
func (r *UserRepository) DisconnectOAuthGrantTx(ctx context.Context, oauthConnectionID int64) error {
	return r.disconnectOAuthGrantTx(ctx, oauthConnectionID, nil)
}

// DisconnectOAuthGrantWithRevocationTx locks the grant and linked accounts,
// invokes revoke while that lock is held, then performs the local disconnect
// mutations in the same transaction. The callback must return nil before any
// local mutation begins; a remote failure rolls back and leaves the grant
// retryable. The callback receives the transaction so vault token reads share
// the exact grant lock and cannot race token rotation.
func (r *UserRepository) DisconnectOAuthGrantWithRevocationTx(ctx context.Context, oauthConnectionID int64, revoke func(context.Context, *sql.Tx) error) error {
	return r.disconnectOAuthGrantTx(ctx, oauthConnectionID, revoke)
}

func (r *UserRepository) disconnectOAuthGrantTx(ctx context.Context, oauthConnectionID int64, revoke func(context.Context, *sql.Tx) error) error {
	if oauthConnectionID <= 0 {
		return fmt.Errorf("disconnect OAuth grant: invalid id %d", oauthConnectionID)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("disconnect OAuth grant: %w", err)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("disconnect OAuth grant: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Lock the parent grant before its child account rows. OAuth attach paths
	// already acquire the grant row while upserting it, so this parent-first
	// order prevents a grant/account lock cycle with a concurrent attach.
	var userID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id
		   FROM oauth_connections
		  WHERE id = $1
		  FOR UPDATE`,
		oauthConnectionID,
	).Scan(&userID); err != nil {
		return fmt.Errorf("disconnect OAuth grant: lock grant %d: %w", oauthConnectionID, err)
	}

	accountIDs := make([]int64, 0)
	rows, err := tx.QueryContext(ctx,
		`SELECT id
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		  ORDER BY id
		  FOR UPDATE`,
		oauthConnectionID,
	)
	if err != nil {
		return fmt.Errorf("disconnect OAuth grant: lock linked accounts: %w", err)
	}
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("disconnect OAuth grant: scan linked account: %w", err)
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("disconnect OAuth grant: iterate linked accounts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("disconnect OAuth grant: close linked accounts: %w", err)
	}

	if revoke != nil {
		if err := revoke(ctx, tx); err != nil {
			return fmt.Errorf("disconnect OAuth grant: remote revoke: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		    SET status = 'disconnected',
		        connected_at = NULL,
		        reauth_required_at = NULL,
		        last_error_code = 'DISCONNECTED',
		        last_error_message = 'OAuth grant disconnected by user',
		        updated_at = NOW()
		  WHERE oauth_connection_id = $1`,
		oauthConnectionID,
	); err != nil {
		return fmt.Errorf("disconnect OAuth grant: disconnect linked accounts: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE oauth_connections
		    SET status = 'disconnected',
		        reauth_required_at = NULL,
		        last_refresh_error = NULL,
		        updated_at = NOW()
		  WHERE id = $1`,
		oauthConnectionID,
	); err != nil {
		return fmt.Errorf("disconnect OAuth grant: disconnect grant: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tokens WHERE oauth_connection_id = $1`,
		oauthConnectionID,
	); err != nil {
		return fmt.Errorf("disconnect OAuth grant: delete tokens: %w", err)
	}

	if accountIDs == nil {
		accountIDs = []int64{}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"oauth_connection_id":  oauthConnectionID,
		"platform_account_ids": accountIDs,
		"user_id":              userID,
		"reason":               "user_requested",
	})
	if err != nil {
		return fmt.Errorf("disconnect OAuth grant: marshal outbox payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)`,
		"oauth_connection", oauthConnectionID, "oauth_connection.disconnected", string(payload),
	); err != nil {
		return fmt.Errorf("disconnect OAuth grant: insert outbox event: %w", err)
	}

	metadata, err := json.Marshal(map[string]interface{}{
		"oauth_connection_id":    oauthConnectionID,
		"platform_account_ids":   accountIDs,
		"platform_account_count": len(accountIDs),
		"scope":                  "oauth_grant",
	})
	if err != nil {
		return fmt.Errorf("disconnect OAuth grant: marshal audit metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_logs (user_id, action, resource_type, resource_id, result, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		userID, models.AuditActionOAuthGrantDisconnected, "oauth_connection", oauthConnectionID,
		models.AuditResultSuccess, string(metadata),
	); err != nil {
		return fmt.Errorf("disconnect OAuth grant: insert audit log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("disconnect OAuth grant: commit: %w", err)
	}
	committed = true
	return nil
}
