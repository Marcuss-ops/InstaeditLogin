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
