package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// WorkspaceRepository handles CRUD operations for workspaces.
//
// A workspace is a team/group container introduced by migration
// 003_posts_workspaces.sql. It owns platform_accounts (via the workspace_id
// FK column added in 003) and posts (via posts.workspace_id). A user can
// own multiple workspaces (1:N ownership via workspaces.owner_id).
//
// Method style intentionally mirrors UserRepository / TokenRepository:
// no context.Context parameter, not-found returns (nil, nil), errors
// wrapped with fmt.Errorf("%w", err).
type WorkspaceRepository struct {
	db *sql.DB
}

// NewWorkspaceRepository creates a new WorkspaceRepository.
func NewWorkspaceRepository(db *sql.DB) *WorkspaceRepository {
	return &WorkspaceRepository{db: db}
}

// Create inserts a new workspace and assigns the auto-generated id and
// created_at back to the supplied *models.Workspace. Mirrors the workspaces
// INSERT in migration 003 (name NOT NULL, owner_id NOT NULL, created_at
// DEFAULT NOW()).
func (r *WorkspaceRepository) Create(w *models.Workspace) error {
	err := r.db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2)
		 RETURNING id, created_at`,
		w.Name, w.OwnerID,
	).Scan(&w.ID, &w.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}
	return nil
}

// FindByID returns the workspace with the given id, or (nil, nil) when no
// row matches. The convention (nil, nil) for not-found matches the rest of
// the repository layer so callers can write
//
//	if w == nil { /* create-new path */ } else { /* update-existing path */ }
//
// without needing to inspect sql.ErrNoRows.
func (r *WorkspaceRepository) FindByID(id int64) (*models.Workspace, error) {
	w := &models.Workspace{}
	err := r.db.QueryRow(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE id = $1`,
		id,
	).Scan(&w.ID, &w.Name, &w.OwnerID, &w.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find workspace by id: %w", err)
	}
	return w, nil
}

// ListByOwner returns every workspace where owner_id matches the supplied
// id, ordered by created_at DESC (most-recent first). The standard ordering
// for "my workspaces" listings.
func (r *WorkspaceRepository) ListByOwner(ownerID int64) ([]models.Workspace, error) {
	rows, err := r.db.Query(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE owner_id = $1
		 ORDER BY created_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces by owner: %w", err)
	}
	defer rows.Close()

	var workspaces []models.Workspace
	for rows.Next() {
		w := models.Workspace{}
		if err := rows.Scan(&w.ID, &w.Name, &w.OwnerID, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan workspace: %w", err)
		}
		workspaces = append(workspaces, w)
	}
	return workspaces, nil
}

// Delete removes the workspace with the given id. Returns
// ErrWorkspaceNotFound wrapped with id context when zero rows match — the
// API layer maps this to 404 via errors.Is (consistent with the post_repo
// audit pattern). Mirrors the rows-affected surface from
// commit d89d56a ("fix(repo): surface rows-affected in Update + ...").
//
// Note: the handler in pkg/api/workspaces.go performs the ownership check
// (OwnerID == userID) BEFORE calling this method, because the SQL DELETE
// statement alone cannot distinguish "id wrong" from "wrong owner". The
// handler pre-loads via FindByID and verifies; this method only enforces
// that the row actually existed at delete time.
func (r *WorkspaceRepository) Delete(id int64) error {
	result, err := r.db.Exec(`DELETE FROM workspaces WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrWorkspaceNotFound, id)
	}
	return nil
}

// =============================================================================
// workspace_channels (P0#4)
// =============================================================================
//
// These methods own the join between platform_accounts and workspaces. They
// follow the same (nil, nil) not-found convention + sentinel-wrapped error
// pattern as the rest of the repository layer. Authz (ws.OwnerID ==
// callerID) is the handler's responsibility; the repo trusts that the
// caller has already proven tenant ownership.

// AttachChannel attaches (or refreshes the group_name on an existing row)
// a platform_account to a workspace. Idempotent via ON CONFLICT DO UPDATE:
// re-calling with the same (workspace_id, platform_account_id) and a
// different group_name rewrites the group_name without error. The
// enabled column is forced to TRUE on first insert; the operator can
// flip it later via UpdateChannel.
//
// Returns the freshly-written WorkspaceChannel row (PostgreSQL's
// RETURNING clause guarantees a single round-trip).
//
// Errors:
//   - Foreign-key violation on workspace_id or platform_account_id → driver
//     error wrapped with %w (caller maps via pq.Error.Code or fallback to
//     400/404).
//   - SQL driver error → wrapped.
func (r *WorkspaceRepository) AttachChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName string) (*models.WorkspaceChannel, error) {
	if workspaceID <= 0 {
		return nil, fmt.Errorf("attach channel: invalid workspace id %d", workspaceID)
	}
	if platformAccountID <= 0 {
		return nil, fmt.Errorf("attach channel: invalid platform_account id %d", platformAccountID)
	}
	// NULLIF maps the empty-string "clear group" signal to a SQL NULL
	// so the partial index doesn't pick it up. Empty group_name is a
	// legitimate "no group" choice and shouldn't cost an index slot.
	var groupArg interface{}
	if groupName != "" {
		groupArg = groupName
	}
	row := &models.WorkspaceChannel{}
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO workspace_channels (workspace_id, platform_account_id, group_name, enabled)
		 VALUES ($1, $2, $3, TRUE)
		 ON CONFLICT (workspace_id, platform_account_id)
		 DO UPDATE SET group_name = EXCLUDED.group_name
		 RETURNING workspace_id, platform_account_id, COALESCE(group_name, '') AS group_name, enabled, created_at`,
		workspaceID, platformAccountID, groupArg,
	).Scan(&row.WorkspaceID, &row.PlatformAccountID, &row.GroupName, &row.Enabled, &row.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("attach channel: workspace_id=%d account_id=%d: %w",
			workspaceID, platformAccountID, err)
	}
	return row, nil
}

// ListChannels returns every (platform_account_id, group_name, enabled)
// binding for the supplied workspace, ordered by created_at DESC. Empty
// workspace returns an empty slice (not nil) so the API can JSON-encode
// it as `[]` instead of `null`.
func (r *WorkspaceRepository) ListChannels(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error) {
	if workspaceID <= 0 {
		return nil, fmt.Errorf("list channels: invalid workspace id %d", workspaceID)
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT workspace_id, platform_account_id,
		        COALESCE(group_name, '') AS group_name, enabled, created_at
		 FROM workspace_channels
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list channels: workspace_id=%d: %w", workspaceID, err)
	}
	defer rows.Close()

	out := make([]models.WorkspaceChannel, 0)
	for rows.Next() {
		var wc models.WorkspaceChannel
		if err := rows.Scan(&wc.WorkspaceID, &wc.PlatformAccountID, &wc.GroupName, &wc.Enabled, &wc.CreatedAt); err != nil {
			return nil, fmt.Errorf("list channels: scan: %w", err)
		}
		out = append(out, wc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list channels: iterate: %w", err)
	}
	return out, nil
}

// UpdateChannel mutates an existing binding's group_name and/or enabled
// flag. Both arguments are pointers so the SQL COALESCE pattern lets the
// caller leave either field untouched (passing nil preserves the existing
// value). Returns ErrWorkspaceNotFound when zero rows match — the handler
// maps it to 404.
//
// group_name supports "clear the group" semantics via empty string
// (NULLIF maps "" → SQL NULL). Passing nil leaves the current value
// intact.
func (r *WorkspaceRepository) UpdateChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName *string, enabled *bool) error {
	if workspaceID <= 0 {
		return fmt.Errorf("update channel: invalid workspace id %d", workspaceID)
	}
	if platformAccountID <= 0 {
		return fmt.Errorf("update channel: invalid platform_account id %d", platformAccountID)
	}
	// Resolving the group_name pointer to an interface{} SQL arg lets
	// COALESCE distinguish "untouched" (nil) from "clear" (NULL after
	// NULLIF).
	var groupArg interface{}
	if groupName != nil {
		if *groupName == "" {
			groupArg = nil // explicit clear → SQL NULL
		} else {
			groupArg = *groupName
		}
	}
	var enabledArg interface{}
	if enabled != nil {
		enabledArg = *enabled
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE workspace_channels
		 SET group_name = COALESCE($1, group_name),
		     enabled    = COALESCE($2, enabled)
		 WHERE workspace_id = $3 AND platform_account_id = $4`,
		groupArg, enabledArg, workspaceID, platformAccountID,
	)
	if err != nil {
		return fmt.Errorf("update channel: workspace_id=%d account_id=%d: %w",
			workspaceID, platformAccountID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update channel: read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: workspace_id=%d account_id=%d",
			ErrWorkspaceNotFound, workspaceID, platformAccountID)
	}
	return nil
}

// DetachChannel removes the binding for a (workspace_id,
// platform_account_id) pair. Returns ErrWorkspaceNotFound when zero
// rows match — the handler maps it to 404.
//
// Idempotent at the SQL level (DELETE naturally no-ops on missing rows),
// but the handler interprets "no binding existed" as 404 to follow the
// REST conventions of DELETE.
func (r *WorkspaceRepository) DetachChannel(ctx context.Context, workspaceID, platformAccountID int64) error {
	if workspaceID <= 0 {
		return fmt.Errorf("detach channel: invalid workspace id %d", workspaceID)
	}
	if platformAccountID <= 0 {
		return fmt.Errorf("detach channel: invalid platform_account id %d", platformAccountID)
	}
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM workspace_channels
		 WHERE workspace_id = $1 AND platform_account_id = $2`,
		workspaceID, platformAccountID,
	)
	if err != nil {
		return fmt.Errorf("detach channel: workspace_id=%d account_id=%d: %w",
			workspaceID, platformAccountID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("detach channel: read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: workspace_id=%d account_id=%d",
			ErrWorkspaceNotFound, workspaceID, platformAccountID)
	}
	return nil
}

// FindChannel returns the single WorkspaceChannel row for the supplied
// (workspace_id, platform_account_id) pair, or (nil, nil) when no row
// matches. PK-indexed (single-row LIMIT 1 implicit), so it is O(1) on
// the workspace_channels_pkey. The handler uses this after UpdateChannel
// to read back the merged row without paying the cost of ListChannels +
// in-Go scan. The (nil, nil) not-found convention matches the rest of
// the repository layer.
func (r *WorkspaceRepository) FindChannel(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
	if workspaceID <= 0 {
		return nil, fmt.Errorf("find channel: invalid workspace id %d", workspaceID)
	}
	if platformAccountID <= 0 {
		return nil, fmt.Errorf("find channel: invalid platform_account id %d", platformAccountID)
	}
	c := &models.WorkspaceChannel{}
	err := r.db.QueryRowContext(ctx,
		`SELECT workspace_id, platform_account_id,
		        COALESCE(group_name, '') AS group_name, enabled, created_at
		 FROM workspace_channels
		 WHERE workspace_id = $1 AND platform_account_id = $2`,
		workspaceID, platformAccountID,
	).Scan(&c.WorkspaceID, &c.PlatformAccountID, &c.GroupName, &c.Enabled, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find channel: workspace_id=%d account_id=%d: %w",
			workspaceID, platformAccountID, err)
	}
	return c, nil
}
