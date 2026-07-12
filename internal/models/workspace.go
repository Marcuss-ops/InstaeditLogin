package models

import "time"

// Workspace is a team/group container that owns platform accounts and posts.
// A user can own multiple workspaces (1:N ownership). Mirrors the `workspaces`
// table introduced by migration 003_posts_workspaces.sql.
type Workspace struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	OwnerID   int64     `json:"owner_id"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkspaceMember represents a user's membership in a workspace with a role.
// Mirrors the workspace_members table (migration 028).
type WorkspaceMember struct {
	ID          int64     `json:"id"`
	WorkspaceID int64     `json:"workspace_id"`
	UserID      int64     `json:"user_id"`
	Role        string    `json:"role"`
	Email       string    `json:"email,omitempty"`
	Name        string    `json:"name,omitempty"`
	JoinedAt    time.Time `json:"joined_at"`
}

// WorkspaceInvite represents a pending invitation to join a workspace.
// Mirrors the workspace_invites table (migration 028).
type WorkspaceInvite struct {
	ID          int64      `json:"id"`
	WorkspaceID int64      `json:"workspace_id"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	Token       string     `json:"token"`
	InvitedBy   int64      `json:"invited_by"`
	ExpiresAt   time.Time  `json:"expires_at"`
	AcceptedAt  *time.Time `json:"accepted_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
