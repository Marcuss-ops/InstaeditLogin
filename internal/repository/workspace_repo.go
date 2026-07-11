package repository

import (
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
