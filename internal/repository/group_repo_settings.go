package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func (r *GroupRepository) UpdateSettings(ctx context.Context, groupID, workspaceID, userID int64, updates []models.GroupAccountLanguageUpdate) error {
	if groupID <= 0 || workspaceID <= 0 || userID <= 0 {
		return fmt.Errorf("update group settings: invalid identifiers")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update group settings: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	groupName, err := lockGroupForUpdate(ctx, tx, groupID, workspaceID)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: id=%d", ErrGroupNotFound, groupID)
		}
		return fmt.Errorf("update group settings: lock group: %w", err)
	}

	accountIDs := make([]int64, 0, len(updates))
	for _, update := range updates {
		if update.AccountID <= 0 {
			return fmt.Errorf("update group settings: invalid account_id %d", update.AccountID)
		}
		accountIDs = append(accountIDs, update.AccountID)
	}
	if len(accountIDs) > 0 {
		rows, err := tx.QueryContext(ctx,
			`SELECT id FROM platform_accounts WHERE user_id = $1 AND id = ANY($2)`,
			userID, pq.Array(accountIDs),
		)
		if err != nil {
			return fmt.Errorf("update group settings: validate accounts: %w", err)
		}
		valid := make(map[int64]struct{}, len(accountIDs))
		for rows.Next() {
			var accountID int64
			if err := rows.Scan(&accountID); err != nil {
				_ = rows.Close()
				return fmt.Errorf("update group settings: scan account: %w", err)
			}
			valid[accountID] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("update group settings: iterate accounts: %w", err)
		}
		_ = rows.Close()
		for _, accountID := range accountIDs {
			if _, ok := valid[accountID]; !ok {
				return fmt.Errorf("%w: one or more accounts are not owned by the caller", ErrGroupAccountOwnership)
			}
		}
	}

	existingRows, err := tx.QueryContext(ctx,
		`SELECT account_id FROM group_accounts WHERE group_id = $1`,
		groupID,
	)
	if err != nil {
		return fmt.Errorf("update group settings: read existing memberships: %w", err)
	}
	existingAccountIDs := make(map[int64]struct{}, len(updates))
	for existingRows.Next() {
		var accountID int64
		if err := existingRows.Scan(&accountID); err != nil {
			_ = existingRows.Close()
			return fmt.Errorf("update group settings: scan existing membership: %w", err)
		}
		existingAccountIDs[accountID] = struct{}{}
	}
	if err := existingRows.Err(); err != nil {
		_ = existingRows.Close()
		return fmt.Errorf("update group settings: iterate existing memberships: %w", err)
	}
	_ = existingRows.Close()

	incomingAccountIDs := make(map[int64]struct{}, len(updates))
	for _, update := range updates {
		incomingAccountIDs[update.AccountID] = struct{}{}
	}
	affectedAccountIDs := make(map[int64]struct{}, len(existingAccountIDs)+len(incomingAccountIDs))
	for accountID := range existingAccountIDs {
		affectedAccountIDs[accountID] = struct{}{}
	}
	for accountID := range incomingAccountIDs {
		affectedAccountIDs[accountID] = struct{}{}
	}
	affectedIDs := make([]int64, 0, len(affectedAccountIDs))
	for accountID := range affectedAccountIDs {
		affectedIDs = append(affectedIDs, accountID)
	}
	sort.Slice(affectedIDs, func(i, j int) bool { return affectedIDs[i] < affectedIDs[j] })

	if _, err := tx.ExecContext(ctx, `DELETE FROM group_accounts WHERE group_id = $1`, groupID); err != nil {
		return fmt.Errorf("update group settings: clear memberships: %w", err)
	}
	for _, update := range updates {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO group_accounts (group_id, account_id) VALUES ($1, $2)`,
			groupID, update.AccountID,
		); err != nil {
			return fmt.Errorf("update group settings: insert membership %d: %w", update.AccountID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE platform_accounts
			 SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('language', $1::text),
			     updated_at = NOW()
			 WHERE id = $2 AND user_id = $3`,
			update.Language, update.AccountID, userID,
		); err != nil {
			return fmt.Errorf("update group settings: update language %d: %w", update.AccountID, err)
		}
	}

	if len(updates) > 0 {
		incomingIDs := make([]int64, 0, len(updates))
		for _, update := range updates {
			incomingIDs = append(incomingIDs, update.AccountID)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workspace_channels (workspace_id, platform_account_id, group_name, enabled)
			 SELECT $1, ids.account_id, $2, TRUE
			   FROM unnest($3::bigint[]) AS ids(account_id)
			 ON CONFLICT (workspace_id, platform_account_id)
			 DO UPDATE SET group_name = EXCLUDED.group_name`,
			workspaceID, groupName, pq.Array(incomingIDs),
		); err != nil {
			return fmt.Errorf("update group settings: create workspace channels: %w", err)
		}
	}

	// workspace_channels stores one group_name per workspace/account pair,
	// while group_accounts allows membership in multiple groups. Recompute
	// the binding from the memberships that remain after this replacement,
	// preferring the group being edited and then a deterministic fallback.
	if err := resyncWorkspaceChannels(ctx, tx, workspaceID, groupID, affectedIDs); err != nil {
		return fmt.Errorf("update group settings: resync workspace channels: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update group settings: commit: %w", err)
	}
	return nil
}
