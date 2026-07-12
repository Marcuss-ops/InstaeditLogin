// Package repository provides TeamRepository for workspace team management:
// members (add, remove, list, get-role) and invites (create, accept).
//
// FASE 2.3: Workspace team repository.
package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Member role constants match the workspace_member_role enum.
const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// TeamRepository handles workspace_members and workspace_invites tables.
type TeamRepository struct {
	db *sql.DB
}

// NewTeamRepository creates a TeamRepository.
func NewTeamRepository(db *sql.DB) *TeamRepository {
	return &TeamRepository{db: db}
}

// -----------------------------------------------------------------------
//  Members
// -----------------------------------------------------------------------

// AddMember inserts a row into workspace_members. Returns an error if the
// (workspace_id, user_id) pair already exists (UNIQUE violation) or if
// the user is already a member (Go-level idempotency check via GetRole).
func (r *TeamRepository) AddMember(workspaceID, userID int64, role string) error {
	// Idempotency: if already a member, update the role silently.
	existing, err := r.GetRole(workspaceID, userID)
	if err != nil {
		return fmt.Errorf("add member: check existing: %w", err)
	}
	if existing != "" {
		if existing == role {
			return nil // already has this role
		}
		// Update role.
		_, err := r.db.Exec(
			`UPDATE workspace_members SET role = $1, updated_at = $2
			 WHERE workspace_id = $3 AND user_id = $4`,
			role, time.Now(), workspaceID, userID,
		)
		if err != nil {
			return fmt.Errorf("add member: update role: %w", err)
		}
		return nil
	}

	_, err = r.db.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, $3)`,
		workspaceID, userID, role,
	)
	if err != nil {
		return fmt.Errorf("add member: insert: %w", err)
	}
	return nil
}

// RemoveMember deletes a row from workspace_members.
func (r *TeamRepository) RemoveMember(workspaceID, userID int64) error {
	result, err := r.db.Exec(
		`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("member not found: workspace=%d user=%d", workspaceID, userID)
	}
	return nil
}

// ListMembers returns all members of a workspace with their roles.
func (r *TeamRepository) ListMembers(workspaceID int64) ([]models.WorkspaceMember, error) {
	rows, err := r.db.Query(
		`SELECT wm.id, wm.workspace_id, wm.user_id, wm.role, wm.joined_at,
		        u.email, u.name
		 FROM workspace_members wm
		 JOIN users u ON u.id = wm.user_id
		 WHERE wm.workspace_id = $1
		 ORDER BY wm.joined_at ASC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	var members []models.WorkspaceMember
	for rows.Next() {
		m := models.WorkspaceMember{}
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.JoinedAt,
			&m.Email, &m.Name); err != nil {
			return nil, fmt.Errorf("list members: scan: %w", err)
		}
		members = append(members, m)
	}
	return members, nil
}

// ListForUser returns every workspace the user is a member of, in
// joined_at-descending order (most recent first). Used by
// AuthService.resolveActiveWorkspace to pick the user's active
// workspace at sign-in / OAuth callback time. Result includes
// WorkspaceID; other WorkspaceMember fields are zeroed.
func (r *TeamRepository) ListForUser(userID int64) ([]models.WorkspaceMember, error) {
	rows, err := r.db.Query(
		`SELECT wm.id, wm.workspace_id, wm.user_id, wm.role, wm.joined_at
		 FROM workspace_members wm
		 WHERE wm.user_id = $1
		 ORDER BY wm.joined_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list for user: %w", err)
	}
	defer rows.Close()

	var members []models.WorkspaceMember
	for rows.Next() {
		m := models.WorkspaceMember{}
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, fmt.Errorf("list for user: scan: %w", err)
		}
		members = append(members, m)
	}
	return members, nil
}

// GetRole returns the role string for a user in a workspace, or empty
// string if the user is not a member.
func (r *TeamRepository) GetRole(workspaceID, userID int64) (string, error) {
	var role string
	err := r.db.QueryRow(
		`SELECT role FROM workspace_members
		 WHERE workspace_id = $1 AND user_id = $2`,
		workspaceID, userID,
	).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get role: %w", err)
	}
	return role, nil
}

// IsAdmin returns true if the user has admin role in the workspace.
func (r *TeamRepository) IsAdmin(workspaceID, userID int64) (bool, error) {
	role, err := r.GetRole(workspaceID, userID)
	if err != nil {
		return false, err
	}
	return role == RoleAdmin, nil
}

// -----------------------------------------------------------------------
//  Invites
// -----------------------------------------------------------------------

// GenerateInviteToken returns a hex-encoded random 32-byte token.
func GenerateInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate invite token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CreateInvite inserts a new workspace_invites row. The token is
// auto-generated. Returns the invite record.
func (r *TeamRepository) CreateInvite(workspaceID, invitedBy int64, email, role string) (*models.WorkspaceInvite, error) {
	token, err := GenerateInviteToken()
	if err != nil {
		return nil, err
	}
	invite := &models.WorkspaceInvite{
		WorkspaceID: workspaceID,
		Email:       email,
		Role:        role,
		Token:       token,
		InvitedBy:   invitedBy,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	}
	err = r.db.QueryRow(
		`INSERT INTO workspace_invites (workspace_id, email, role, token, invited_by, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		invite.WorkspaceID, invite.Email, invite.Role, invite.Token,
		invite.InvitedBy, invite.ExpiresAt,
	).Scan(&invite.ID, &invite.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}
	return invite, nil
}

// FindInviteByToken returns the invite for a given token, or nil if not found.
func (r *TeamRepository) FindInviteByToken(token string) (*models.WorkspaceInvite, error) {
	invite := &models.WorkspaceInvite{}
	err := r.db.QueryRow(
		`SELECT id, workspace_id, email, role, token, invited_by, expires_at, accepted_at, created_at
		 FROM workspace_invites
		 WHERE token = $1`,
		token,
	).Scan(&invite.ID, &invite.WorkspaceID, &invite.Email, &invite.Role,
		&invite.Token, &invite.InvitedBy, &invite.ExpiresAt, &invite.AcceptedAt,
		&invite.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find invite by token: %w", err)
	}
	return invite, nil
}

// AcceptInvite marks an invite as accepted and adds the user to the workspace.
func (r *TeamRepository) AcceptInvite(token string, userID int64) error {
	invite, err := r.FindInviteByToken(token)
	if err != nil {
		return fmt.Errorf("accept invite: %w", err)
	}
	if invite == nil {
		return fmt.Errorf("invite not found")
	}
	if invite.AcceptedAt != nil {
		return fmt.Errorf("invite already accepted")
	}
	if time.Now().After(invite.ExpiresAt) {
		return fmt.Errorf("invite expired")
	}

	// Add member and mark invite as accepted in a transaction.
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("accept invite: begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		invite.WorkspaceID, userID, invite.Role,
	)
	if err != nil {
		return fmt.Errorf("accept invite: add member: %w", err)
	}

	_, err = tx.Exec(
		`UPDATE workspace_invites SET accepted_at = $1 WHERE token = $2`,
		time.Now(), token,
	)
	if err != nil {
		return fmt.Errorf("accept invite: mark accepted: %w", err)
	}

	return tx.Commit()
}
