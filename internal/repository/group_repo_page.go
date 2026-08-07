package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListByWorkspacePage returns a lightweight keyset-paginated group page.
// The deterministic created_at/id ordering avoids OFFSET scans and does not
// load group membership rows; use ListByWorkspaceWithAccounts for aggregate
// membership reads.
func (r *GroupRepository) ListByWorkspacePage(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.Group, bool, error) {
	if workspaceID <= 0 {
		return nil, false, fmt.Errorf("list groups: invalid workspace id %d", workspaceID)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	var after interface{}
	if afterTime != nil {
		after = *afterTime
	}
	rows, err := r.db.Query(
		`SELECT id, workspace_id, parent_group_id, name, created_at, updated_at
		 FROM groups
		 WHERE workspace_id = $1
		   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
		 ORDER BY created_at DESC, id DESC
		 LIMIT $4`,
		workspaceID, after, afterID, limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list paginated groups: %w", err)
	}
	defer rows.Close()
	groups := make([]models.Group, 0, limit+1)
	for rows.Next() {
		group, err := scanGroupRow(rows, nil)
		if err != nil {
			return nil, false, fmt.Errorf("scan paginated group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate paginated groups: %w", err)
	}
	hasMore := len(groups) > limit
	if hasMore {
		groups = groups[:limit]
	}
	return groups, hasMore, nil
}

// ListByWorkspaceWithAccountsPage keyset-paginates groups while retaining
// the aggregate endpoint's one-query membership read. pageCount is carried
// by the CTE so the handler can compute hasMore without a second count query.
func (r *GroupRepository) ListByWorkspaceWithAccountsPage(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.GroupWithAccounts, bool, error) {
	if workspaceID <= 0 {
		return nil, false, fmt.Errorf("list groups with accounts: invalid workspace id %d", workspaceID)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	var after interface{}
	if afterTime != nil {
		after = *afterTime
	}
	rows, err := r.db.Query(
		`WITH page_groups AS (
			SELECT id, workspace_id, parent_group_id, name, created_at, updated_at
			FROM groups
			WHERE workspace_id = $1
			  AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
			ORDER BY created_at DESC, id DESC
			LIMIT $4
		)
		SELECT pg.id, pg.workspace_id, pg.parent_group_id, pg.name, pg.created_at, pg.updated_at,
		       ga.account_id, (SELECT COUNT(*) FROM page_groups) AS page_count
		FROM page_groups pg
		LEFT JOIN group_accounts ga ON ga.group_id = pg.id
		ORDER BY pg.created_at DESC, pg.id DESC, ga.account_id ASC`,
		workspaceID, after, afterID, limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("list paginated groups with accounts: %w", err)
	}
	defer rows.Close()
	groups := make([]models.GroupWithAccounts, 0, limit+1)
	indexByID := make(map[int64]int)
	pageCount := 0
	for rows.Next() {
		var (
			account sql.NullInt64
			count   int
		)
		group, err := scanGroupRow(rows, &account, &count)
		if err != nil {
			return nil, false, fmt.Errorf("scan paginated group with accounts: %w", err)
		}
		pageCount = count
		index, ok := indexByID[group.ID]
		if !ok {
			index = len(groups)
			indexByID[group.ID] = index
			groups = append(groups, models.GroupWithAccounts{Group: group, AccountIDs: []int64{}})
		}
		if account.Valid {
			groups[index].AccountIDs = append(groups[index].AccountIDs, account.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate paginated groups with accounts: %w", err)
	}
	hasMore := pageCount > limit
	if len(groups) > limit {
		groups = groups[:limit]
	}
	return groups, hasMore, nil
}
