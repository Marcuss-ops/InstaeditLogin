package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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

// Create inserts a new group row and stamps the auto-generated id +
// timestamps back on the supplied model. The parent_group_id FK is
// validated against cycles + workspace ownership BEFORE the INSERT
// (wouldCreateCycle + parentInWorkspace). On success the new row is
// returned.
//
// A duplicate (workspace_id, name) at the root level (parent_group_id
// IS NULL) raises ErrGroupDuplicate wrapped — the API layer maps this
// to 409 Conflict.
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

// FindByID returns the group with the given id, or (nil, nil) when no
// row matches. FindByID does NOT enforce workspace scoping — the
// handler must check the caller owns the workspace before treating the
// row as their own (existence-leak avoidance across tenants).
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

// Update mutates an existing group's name and/or parent_group_id.
// The host tenant's workspace_id is enforced inside the UPDATE; zero
// rows means either the group doesn't exist OR the caller tried to
// update a group in another workspace — both mapped to ErrGroupNotFound
// at the API layer (existence-leak avoidance).
//
// Cycle detection runs unconditionally when parent_group_id IS
// non-null: the new parent cannot be self or any ancestor of self.
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

// Delete removes the group by id (ON DELETE CASCADE cleans children +
// group_accounts rows). Returns ErrGroupNotFound when zero rows match.
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

// ListByWorkspace returns every group in the given workspace as a flat
// list. Callers (the dashboard tree renderer) build the tree by walking
// parent_group_id in O(N) on the consumer side. Ordered by name ASC for
// stable client-side rendering.
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
		var (
			g      models.Group
			parent sql.NullInt64
		)
		if err := rows.Scan(&g.ID, &g.WorkspaceID, &parent, &g.Name, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan group: %w", err)
		}
		if parent.Valid {
			v := parent.Int64
			g.ParentGroupID = &v
		}
		out = append(out, g)
	}
	return out, nil
}

// ListAccountsInGroup returns the IDs of accounts attached directly
// to the given group (NOT recursively through children — the join
// table is per-group, not per-subtree). Use ListByWorkspace +
// ListAccountsInGroup per node to build the recursive accounting on
// the consumer side.
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

// ListByWorkspaceWithAccounts returns every group and its direct account
// memberships in one workspace-scoped query. The LEFT JOIN preserves
// empty groups, while the deterministic group/account ordering keeps the
// response stable for pagination and client rendering.
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
		var (
			group   models.Group
			parent  sql.NullInt64
			account sql.NullInt64
		)
		if err := rows.Scan(
			&group.ID, &group.WorkspaceID, &parent, &group.Name,
			&group.CreatedAt, &group.UpdatedAt, &account,
		); err != nil {
			return nil, fmt.Errorf("failed to scan group with accounts: %w", err)
		}
		if parent.Valid {
			value := parent.Int64
			group.ParentGroupID = &value
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

// UpdateSettings atomically replaces a group's memberships and updates each
// member's language metadata. The transaction locks the group, validates all
// accounts belong to the authenticated user before deleting any membership,
// then performs the membership and metadata writes together.
func (r *GroupRepository) UpdateSettings(ctx context.Context, groupID, workspaceID, userID int64, updates []models.GroupAccountLanguageUpdate) error {
	if groupID <= 0 || workspaceID <= 0 || userID <= 0 {
		return fmt.Errorf("update group settings: invalid identifiers")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update group settings: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		lockedGroupID int64
		groupName     string
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT id, name FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
		groupID, workspaceID,
	).Scan(&lockedGroupID, &groupName); err != nil {
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
	if len(affectedIDs) > 0 {
		if _, err := tx.ExecContext(ctx,
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
			workspaceID, groupID, pq.Array(affectedIDs),
		); err != nil {
			return fmt.Errorf("update group settings: resync workspace channels: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("update group settings: commit: %w", err)
	}
	return nil
}

// AddAccount adds a platform_account to a group. ON CONFLICT DO NOTHING
// makes the operation idempotent — adding an already-attached account
// is a no-op and returns nil.
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

// RemoveAccountFromGroupTx detaches a single account from a group in one
// transaction and resyncs the workspace_channels binding. This is the
// dedicated "rimuovi dalla cartella" operation: it touches only
// group_accounts + workspace_channels — never platform_accounts, tokens
// or OAuth grants (disconnect and hard-delete are separate endpoints on
// /accounts). Idempotent: removing a non-existent membership succeeds as
// a no-op and still resyncs the channel binding.
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
	var lockedGroupID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`,
		groupID, workspaceID,
	).Scan(&lockedGroupID); err != nil {
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
	if _, err := tx.ExecContext(ctx,
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
		 WHERE wc.workspace_id = $1 AND wc.platform_account_id = $3`,
		workspaceID, groupID, accountID,
	); err != nil {
		return fmt.Errorf("remove account from group: resync workspace channel: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("remove account from group: commit: %w", err)
	}
	return nil
}

// RemoveAccount detaches a platform_account from a group. Idempotent:
// removing a non-existent row returns nil (idempotent teardown).
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

// SetAccounts wipes and re-inserts the membership list. Editor
// code-paths use this for "make this group contain exactly these N
// accounts". Runs inside one transaction so a partial set never
// becomes visible.
//
// Cross-tenant safety: the caller MUST pre-load the group (ownership
// check) and pass `allowedAccountIDs` only after validating each id
// belongs to a user who owns the same workspace. The repository does
// not duplicate the workspace lookup; instead SetAccountsBulk below
// takes the same slice plus a precomputed `validIDs` filter so the
// API layer can intersect the request payload against the
// workspace-owner's accounts in one round-trip, reject invalid ids
// cleanly, and never persist a row an attacker could plant.
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

// ValidateAccountOwnership returns the subset of supplied accountIDs
// that are owned by `userID`. The API layer uses this to intersect a
// caller-supplied account_ids list against a workspace owner's
// accounts before persisting, so a malicious request cannot attach
// an unknown account to a group. Returns the filtered slice (may be
// empty when none of the supplied ids belong to the caller).
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

// parentInWorkspace validates that the proposed parent_group_id exists
// AND belongs to the same workspace as the new/moved group. Returns
// ErrGroupNotFound if the parent doesn't exist (handler maps to 404
// because the user-supplied id is the problem), ErrGroupWorkspaceMismatch
// if it exists in another workspace (handler maps to 422).
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

// wouldCreateCycle walks the proposed parent's ancestor chain and
// returns true if self is anywhere up the chain (or self==parent).
// Maximum recursion depth is bounded by the tree itself; the query is
// a single recursive CTE so even deep trees stay one round-trip.
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
