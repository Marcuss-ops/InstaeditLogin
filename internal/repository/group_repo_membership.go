package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
)

func (r *GroupRepository) ListAccountsInGroup(groupID int64) ([]int64, error) {
	rows, err := r.db.Query(
		`SELECT account_id FROM group_accounts WHERE group_id = $1 ORDER BY account_id ASC`,
		groupID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list accounts in group: %w", err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan account_id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate group accounts: %w", err)
	}
	return out, nil
}

func (r *GroupRepository) AddAccount(groupID, accountID int64) error {
	_, err := r.db.Exec(
		`INSERT INTO group_accounts (group_id, account_id)
		 VALUES ($1, $2)
		 ON CONFLICT (group_id, account_id) DO NOTHING`,
		groupID, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to add account to group: %w", err)
	}
	return nil
}

func (r *GroupRepository) RemoveAccountFromGroupTx(ctx context.Context, groupID, workspaceID, accountID int64) error {
	if groupID <= 0 || workspaceID <= 0 || accountID <= 0 {
		return fmt.Errorf("remove account from group: invalid identifiers")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remove account from group: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Lock the group row so a concurrent settings write cannot interleave
	// with the membership deletion + resync (same discipline as
	// UpdateSettings). Missing row / foreign workspace → ErrGroupNotFound.
	if _, err := lockGroupForUpdate(ctx, tx, groupID, workspaceID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: id=%d", ErrGroupNotFound, groupID)
		}
		return fmt.Errorf("remove account from group: lock group: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM group_accounts WHERE group_id = $1 AND account_id = $2`,
		groupID, accountID,
	); err != nil {
		return fmt.Errorf("remove account from group: delete membership: %w", err)
	}

	// Recompute the workspace_channels group binding for this account from
	// the memberships that remain after the removal (NULL when the account
	// is no longer in any group of the workspace). Mirrors the resync step
	// in UpdateSettings.
	if err := resyncWorkspaceChannels(ctx, tx, workspaceID, groupID, []int64{accountID}); err != nil {
		return fmt.Errorf("remove account from group: resync workspace channel: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("remove account from group: commit: %w", err)
	}
	return nil
}

func (r *GroupRepository) RemoveAccount(groupID, accountID int64) error {
	_, err := r.db.Exec(
		`DELETE FROM group_accounts WHERE group_id = $1 AND account_id = $2`,
		groupID, accountID,
	)
	if err != nil {
		return fmt.Errorf("failed to remove account from group: %w", err)
	}
	return nil
}

func (r *GroupRepository) SetAccounts(groupID int64, accountIDs []int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin set-accounts tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.Exec(`DELETE FROM group_accounts WHERE group_id = $1`, groupID); err != nil {
		return fmt.Errorf("failed to clear group_accounts: %w", err)
	}
	for _, accountID := range accountIDs {
		if accountID <= 0 {
			continue
		}
		if _, err = tx.Exec(
			`INSERT INTO group_accounts (group_id, account_id) VALUES ($1, $2)
			 ON CONFLICT (group_id, account_id) DO NOTHING`,
			groupID, accountID,
		); err != nil {
			return fmt.Errorf("failed to insert group_account %d: %w", accountID, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit set-accounts tx: %w", err)
	}
	return nil
}

func (r *GroupRepository) ValidateAccountOwnership(userID, workspaceID int64, accountIDs []int64) ([]int64, error) {
	if len(accountIDs) == 0 {
		return []int64{}, nil
	}
	rows, err := r.db.Query(
		`SELECT pa.id
		 FROM platform_accounts pa
		 JOIN workspaces w ON w.id = $2
		 WHERE pa.id = ANY($3)
		   AND (
		     w.owner_id = $1
		     OR EXISTS (
		       SELECT 1 FROM workspace_members wm
		       WHERE wm.workspace_id = $2 AND wm.user_id = $1
		     )
		   )`,
		userID, workspaceID, pq.Array(accountIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to validate account ownership: %w", err)
	}
	defer rows.Close()
	out := make([]int64, 0, len(accountIDs))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("failed to scan validated account_id: %w", err)
		}
		out = append(out, id)
	}
	return out, nil
}
