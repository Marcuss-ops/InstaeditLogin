package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// GroupRepository handles CRUD for the hierarchical Group tree and its
// many-to-many join with PlatformAccount. Mirrors the WorkspaceRepository
// style: no context.Context parameter, (nil, nil) on Find* not-found,
// errors wrapped with fmt.Errorf("%w: ...", err).
//
// Two design notes:
//   - groups are self-referencing (parent_group_id). Cycle prevention
//     happens here, not at the SQL layer (Postgres has no native
//     self-FK cycle constraint). See SetParent / wouldCreateCycle.
//   - group_accounts is a join table. AddAccount / RemoveAccount are
//     idempotent helpers; SetAccounts wipes and re-inserts for an
//     editor's "this group now has these accounts" UI.
type GroupRepository struct {
	db *sql.DB
}

// NewGroupRepository creates a GroupRepository.
func NewGroupRepository(db *sql.DB) *GroupRepository {
	return &GroupRepository{db: db}
}

func (r *GroupRepository) Create(g *models.Group) error {
	if g.ParentGroupID != nil {
		if err := r.parentInWorkspace(*g.ParentGroupID, g.WorkspaceID); err != nil {
			return err
		}
	}
	row := r.db.QueryRow(
		`INSERT INTO groups (workspace_id, parent_group_id, name)
		 VALUES ($1, $2, $3)
		 RETURNING id, created_at, updated_at`,
		g.WorkspaceID, g.ParentGroupID, g.Name,
	)
	if err := row.Scan(&g.ID, &g.CreatedAt, &g.UpdatedAt); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return fmt.Errorf("%w: workspace=%d name=%q", ErrGroupDuplicate, g.WorkspaceID, g.Name)
		}
		return fmt.Errorf("failed to create group: %w", err)
	}
	return nil
}

func (r *GroupRepository) FindByID(id int64) (*models.Group, error) {
	g := &models.Group{}
	var parent sql.NullInt64
	err := r.db.QueryRow(
		`SELECT id, workspace_id, parent_group_id, name, created_at, updated_at
		 FROM groups
		 WHERE id = $1`,
		id,
	).Scan(&g.ID, &g.WorkspaceID, &parent, &g.Name, &g.CreatedAt, &g.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find group by id: %w", err)
	}
	if parent.Valid {
		v := parent.Int64
		g.ParentGroupID = &v
	}
	return g, nil
}

func (r *GroupRepository) Update(g *models.Group) error {
	exists, err := r.FindByID(g.ID)
	if err != nil || exists == nil {
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: id=%d", ErrGroupNotFound, g.ID)
	}
	// Cross-tenant guard via the existing row's workspace_id; the
	// UPDATE will also enforce it but we want to return ErrGroupNotFound
	// rather than zero RowsAffected integer games.
	if exists.WorkspaceID != g.WorkspaceID {
		return fmt.Errorf("%w: id=%d", ErrGroupNotFound, g.ID)
	}
	if g.ParentGroupID != nil {
		if err := r.parentInWorkspace(*g.ParentGroupID, g.WorkspaceID); err != nil {
			return err
		}
		if cycle, err := r.wouldCreateCycle(g.ID, *g.ParentGroupID); err != nil {
			return err
		} else if cycle {
			return ErrGroupCycle
		}
	}
	res, err := r.db.Exec(
		`UPDATE groups
		 SET name = $1, parent_group_id = $2, updated_at = $3
		 WHERE id = $4 AND workspace_id = $5`,
		g.Name, g.ParentGroupID, time.Now(), g.ID, g.WorkspaceID,
	)
	if err != nil {
		return fmt.Errorf("failed to update group: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrGroupNotFound, g.ID)
	}
	return nil
}

func (r *GroupRepository) Delete(id int64) error {
	res, err := r.db.Exec(`DELETE FROM groups WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrGroupNotFound, id)
	}
	return nil
}

func (r *GroupRepository) ListByWorkspace(workspaceID int64) ([]models.Group, error) {
	rows, err := r.db.Query(
		`SELECT id, workspace_id, parent_group_id, name, created_at, updated_at
		 FROM groups
		 WHERE workspace_id = $1
		 ORDER BY name ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups by workspace: %w", err)
	}
	defer rows.Close()

	var out []models.Group
	for rows.Next() {
		g, err := scanGroupRow(rows, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group: %w", err)
		}
		out = append(out, g)
	}
	return out, nil
}

func (r *GroupRepository) ListByWorkspaceWithAccounts(workspaceID int64) ([]models.GroupWithAccounts, error) {
	rows, err := r.db.Query(
		`SELECT g.id, g.workspace_id, g.parent_group_id, g.name, g.created_at, g.updated_at,
			ga.account_id
		 FROM groups g
		 LEFT JOIN group_accounts ga ON ga.group_id = g.id
		 WHERE g.workspace_id = $1
		 ORDER BY g.name ASC, g.id ASC, ga.account_id ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list groups with accounts by workspace: %w", err)
	}
	defer rows.Close()

	groups := make([]models.GroupWithAccounts, 0)
	indexByID := make(map[int64]int)
	for rows.Next() {
		var account sql.NullInt64
		group, err := scanGroupRow(rows, &account)
		if err != nil {
			return nil, fmt.Errorf("failed to scan group with accounts: %w", err)
		}
		index, ok := indexByID[group.ID]
		if !ok {
			index = len(groups)
			indexByID[group.ID] = index
			groups = append(groups, models.GroupWithAccounts{
				Group:      group,
				AccountIDs: []int64{},
			})
		}
		if account.Valid {
			groups[index].AccountIDs = append(groups[index].AccountIDs, account.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate groups with accounts: %w", err)
	}
	return groups, nil
}

func (r *GroupRepository) parentInWorkspace(parentID, workspaceID int64) error {
	row := r.db.QueryRow(
		`SELECT workspace_id FROM groups WHERE id = $1`,
		parentID,
	)
	var parentWS int64
	if err := row.Scan(&parentWS); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("%w: parent_id=%d", ErrGroupNotFound, parentID)
		}
		return fmt.Errorf("failed to lookup parent group: %w", err)
	}
	if parentWS != workspaceID {
		return fmt.Errorf("%w: parent_id=%d", ErrGroupWorkspaceMismatch, parentID)
	}
	return nil
}

func (r *GroupRepository) wouldCreateCycle(selfID, newParentID int64) (bool, error) {
	if selfID == newParentID {
		return true, nil
	}
	row := r.db.QueryRow(
		`WITH RECURSIVE ancestors(id, parent_group_id) AS (
			SELECT id, parent_group_id FROM groups WHERE id = $1
			UNION ALL
			SELECT g.id, g.parent_group_id
			FROM groups g
			JOIN ancestors a ON g.id = a.parent_group_id
			WHERE g.id <> $2
		)
		SELECT EXISTS (
			SELECT 1 FROM ancestors WHERE id = $2
		)`,
		newParentID, selfID,
	)
	var cycle bool
	if err := row.Scan(&cycle); err != nil {
		return false, fmt.Errorf("failed to detect cycle: %w", err)
	}
	return cycle, nil
}
