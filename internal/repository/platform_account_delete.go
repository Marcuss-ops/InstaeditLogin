package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// PermanentlyDeleteAccountTx tombstones a platform account so it disappears
// from every normal query while keeping the row for FK integrity (historical
// publications, thumbnail assignments, livestreams all reference it).
//
// The row is NOT physically deleted: post_targets / livestreams /
// thumbnail_assignments keep referencing platform_accounts(id), and a real
// DELETE would either fail on the composite FK or cascade-destroy the
// publication history. Instead the row is tombstoned:
//
//	status = 'deleted'   → account_state=deleted → hidden by GET /accounts
//	username = '[deleted]'
//	metadata = '{}'      → no provider profile data survives
//	oauth_connection_id → SET NULL when the grant is removed (043 FK);
//	platform_user_id    → replaced with an account-scoped tombstone marker;
//	                      provider identity is not retained in the deleted row.
//
// All channel-scoped data is removed in the SAME transaction:
//
//	group_accounts, workspace_channels (thumbnail_assignments cascade),
//	account_resource_snapshots, non-terminal post_targets (future jobs) with
//	parent aggregate recompute — the same lock order as CancelPost.
//
// The shared-grant decision mirrors DisconnectPlatformAccount: when no
// ACTIVE sibling remains on the oauth_connection, the grant is revoked
// (via the optional revoke callback — the caller holds the vault + provider
// revoker), its token rows are deleted and the oauth_connections row is
// removed (tokens cascade too). When a sibling is still active, the grant
// and its shared tokens are preserved so the sibling keeps working.
// Legacy accounts without an oauth_connection (pre-043 attach or
// already-revoked grant) skip the grant work but still purge any
// per-account token rows keyed by platform_account_id.
//
// Outbox (platform_account.deleted) and audit (account_deleted) are written
// inside the same transaction, so a failure rolls everything back.
func (r *UserRepository) PermanentlyDeleteAccountTx(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (handled bool, err error) {
	if accountID <= 0 {
		return false, fmt.Errorf("permanently delete account: invalid id %d", accountID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("permanently delete account: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var (
		userID            int64
		platform          string
		platformUserID    string
		accountStatus     string
		oauthConnectionID int64
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id, platform, platform_user_id, status, COALESCE(oauth_connection_id, 0)
		   FROM platform_accounts
		  WHERE id = $1
		  FOR UPDATE`, accountID,
	).Scan(&userID, &platform, &platformUserID, &accountStatus, &oauthConnectionID); err != nil {
		return false, fmt.Errorf("permanently delete account: lock account %d: %w", accountID, err)
	}
	// A completed tombstone is an idempotent no-op. In particular, do not
	// call a provider again or emit duplicate audit/outbox records on retry.
	if accountStatus == models.AccountStatusDeleted {
		return true, nil
	}

	lastOnGrant := false
	if oauthConnectionID > 0 {
		if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", oauthConnectionID); err != nil {
			return false, fmt.Errorf("permanently delete account: acquire grant lock: %w", err)
		}
		var activeSiblings int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*)
			   FROM platform_accounts
			  WHERE oauth_connection_id = $1
			    AND status = 'active'
			    AND id <> $2`, oauthConnectionID, accountID,
		).Scan(&activeSiblings); err != nil {
			return false, fmt.Errorf("permanently delete account: count active siblings: %w", err)
		}
		lastOnGrant = activeSiblings == 0
		if lastOnGrant && platform == models.PlatformYouTube {
			if revoke == nil {
				return false, fmt.Errorf("permanently delete account: remote revoke is not configured")
			}
			// Remote provider revocation runs while the grant lock is
			// held and BEFORE the token rows are removed (the refresh
			// token is needed for the provider call). A failure rolls
			// the whole delete back — the caller maps it to a typed
			// 502/503 via the OAuth-grant error writer.
			if err := revoke(ctx, tx); err != nil {
				return false, fmt.Errorf("permanently delete account: remote revoke: %w", err)
			}
		}
		if lastOnGrant {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM tokens WHERE oauth_connection_id = $1`, oauthConnectionID,
			); err != nil {
				return false, fmt.Errorf("permanently delete account: delete grant tokens: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM oauth_connections WHERE id = $1`, oauthConnectionID,
			); err != nil {
				return false, fmt.Errorf("permanently delete account: delete oauth connection: %w", err)
			}
		}
	} else {
		// Legacy account (pre-043 attach or already-revoked grant): no grant
		// work. Any token rows still keyed by platform_account_id are purged
		// explicitly — the platform_accounts row is NOT deleted, so the
		// ON DELETE CASCADE from migration 001 would not fire.
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM tokens WHERE platform_account_id = $1`, accountID,
		); err != nil {
			return false, fmt.Errorf("permanently delete account: delete legacy tokens: %w", err)
		}
	}

	// Channel-scoped cleanup (same transaction as the tombstone).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM group_accounts WHERE account_id = $1`, accountID,
	); err != nil {
		return false, fmt.Errorf("permanently delete account: remove group memberships: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM workspace_channels WHERE platform_account_id = $1`, accountID,
	); err != nil {
		return false, fmt.Errorf("permanently delete account: remove workspace channels: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM account_resource_snapshots WHERE platform_account_id = $1`, accountID,
	); err != nil {
		return false, fmt.Errorf("permanently delete account: remove snapshots: %w", err)
	}
	// These account-scoped caches and editor/batch records do not all have
	// foreign keys with ON DELETE CASCADE because the account row is kept as
	// a tombstone. Remove them explicitly so no provider profile, capability
	// cache, metric history, editor draft, or batch item survives the request.
	cleanupQueries := []struct {
		name  string
		query string
	}{
		{"capabilities", `DELETE FROM account_capabilities WHERE platform_account_id = $1`},
		{"metric history", `DELETE FROM account_metric_history WHERE platform_account_id = $1`},
		{"editor sessions", `DELETE FROM youtube_video_edits WHERE platform_account_id = $1`},
		{"thumbnail batch items", `DELETE FROM youtube_thumbnail_batch_items WHERE platform_account_id = $1`},
		// Keep destination rows that have delivery history because
		// external_deliveries references them with ON DELETE RESTRICT.
		// Disable and clear operational metadata instead: historical
		// deliveries remain resolvable while the deleted channel cannot
		// be used as a publish target.
		{"external destinations", `UPDATE external_destinations
		    SET enabled = FALSE,
		        default_metadata = '{}'::jsonb,
		        updated_at = NOW()
		  WHERE platform_account_id = $1`},
		{"livestreams", `DELETE FROM livestreams WHERE platform_account_id = $1`},
	}
	for _, cleanup := range cleanupQueries {
		if _, err := tx.ExecContext(ctx, cleanup.query, accountID); err != nil {
			return false, fmt.Errorf("permanently delete account: remove %s: %w", cleanup.name, err)
		}
	}
	if err := cancelFutureJobsTx(tx, accountID); err != nil {
		return false, fmt.Errorf("permanently delete account: cancel future jobs: %w", err)
	}

	// Tombstone the row (kept for FK integrity; provider identity anonymized).
	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		    SET status = 'deleted',
		        username = '[deleted]',
		        platform_user_id = '[deleted:' || id::text || ']',
		        metadata = '{}'::jsonb,
		        connected_at = NULL,
		        last_validated_at = NULL,
		        last_refresh_at = NULL,
		        reauth_required_at = NULL,
		        last_error_code = 'DELETED',
		        last_error_message = 'account permanently deleted by user',
		        updated_at = NOW()
		  WHERE id = $1`, accountID,
	); err != nil {
		return false, fmt.Errorf("permanently delete account: tombstone: %w", err)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"platform_account_id": accountID,
		"platform":            platform,
		"user_id":             userID,
		"last_on_grant":       lastOnGrant,
		"reason":              "user_requested",
	})
	if err != nil {
		return false, fmt.Errorf("permanently delete account: marshal outbox payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)`,
		"platform_account", accountID, "platform_account.deleted", string(payload),
	); err != nil {
		return false, fmt.Errorf("permanently delete account: insert outbox event: %w", err)
	}

	auditMetadata, err := json.Marshal(map[string]interface{}{
		"platform":      platform,
		"last_on_grant": lastOnGrant,
		"scope":         "account_data",
	})
	if err != nil {
		return false, fmt.Errorf("permanently delete account: marshal audit metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_logs (user_id, action, resource_type, resource_id, result, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		userID, models.AuditActionAccountDeleted, "platform_account", accountID,
		models.AuditResultSuccess, string(auditMetadata),
	); err != nil {
		return false, fmt.Errorf("permanently delete account: insert audit log: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("permanently delete account: commit: %w", err)
	}
	committed = true
	return true, nil
}
