package repository

import (
	"context"
	"database/sql"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// groupRowScanner is the common subset implemented by *sql.Rows and used by
// the flat and aggregate group read models. Keeping row mapping here makes
// parent_group_id handling identical without merging their SQL queries.
type groupRowScanner interface {
	Scan(dest ...any) error
}

// scanGroupRow maps the common groups columns and optionally one joined
// account_id column. The caller owns the surrounding rows loop and its
// context-specific error message.
func scanGroupRow(scanner groupRowScanner, account *sql.NullInt64, extra ...any) (models.Group, error) {
	var (
		group  models.Group
		parent sql.NullInt64
	)
	destinations := []any{
		&group.ID,
		&group.WorkspaceID,
		&parent,
		&group.Name,
		&group.CreatedAt,
		&group.UpdatedAt,
	}
	if account != nil {
		destinations = append(destinations, account)
	}
	destinations = append(destinations, extra...)
	if err := scanner.Scan(destinations...); err != nil {
		return models.Group{}, err
	}
	if parent.Valid {
		value := parent.Int64
		group.ParentGroupID = &value
	}
	return group, nil
}

// lockGroupForUpdate is the shared transaction guard for membership writes.
// It always includes workspace_id in the predicate and must be called before
// touching group_accounts or workspace_channels. The returned name is used
// when UpdateSettings creates the workspace channel binding.
func lockGroupForUpdate(ctx context.Context, tx *sql.Tx, groupID, workspaceID int64) (string, error) {
	var (
		lockedGroupID int64
		groupName     string
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT id, name FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
		groupID, workspaceID,
	).Scan(&lockedGroupID, &groupName); err != nil {
		return "", err
	}
	return groupName, nil
}

// resyncWorkspaceChannels recomputes the denormalized group_name binding
// from memberships that remain in the same workspace. It runs on the caller's
// transaction and preserves the edited group as the deterministic preference.
func resyncWorkspaceChannels(ctx context.Context, tx *sql.Tx, workspaceID, preferredGroupID int64, accountIDs []int64) error {
	if len(accountIDs) == 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE workspace_channels AS wc
		 SET group_name = (
		     SELECT g.name
		       FROM group_accounts AS ga
		       JOIN groups AS g ON g.id = ga.group_id
		      WHERE ga.account_id = wc.platform_account_id
		        AND g.workspace_id = $1
		      ORDER BY CASE WHEN g.id = $2 THEN 0 ELSE 1 END, g.name, g.id
		      LIMIT 1
		 )
		 WHERE wc.workspace_id = $1
		   AND wc.platform_account_id = ANY($3)`,
		workspaceID, preferredGroupID, pq.Array(accountIDs),
	)
	return err
}
