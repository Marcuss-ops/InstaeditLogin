package models

import "time"

// WorkspaceID uniquely identifies a Workspace.
type WorkspaceID int64

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

// WorkspaceChannel binds a PlatformAccount to a Workspace under an
// optional group_name tag. Mirrors the workspace_channels table
// (migration 044). Composite PK (workspace_id, platform_account_id)
// means a single platform_account can belong to many workspaces
// simultaneously (shared-agency pool) without losing per-binding state
// (group_name, enabled).
//
// enabled lets the operator soft-disable a channel in a specific
// workspace (e.g. a YouTube channel muted in the marketing workspace
// but active in the editorial one). A future publish-worker step will
// filter on enabled=true; for now it is just metadata the operator
// can flip via PATCH /api/v1/workspaces/{id}/channels/{accountId}.
type WorkspaceChannel struct {
	WorkspaceID       int64     `json:"workspace_id"`
	PlatformAccountID int64     `json:"platform_account_id"`
	GroupName         string    `json:"group_name,omitempty"`
	Enabled           bool      `json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
}
