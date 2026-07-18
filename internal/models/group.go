package models

import "time"

// Group is a user-defined folder/label that groups PlatformAccounts under a
// Workspace. Groups are organised as a tree: each row may reference a parent
// group via ParentGroupID (NULLABLE = root group). The tree depth is
// unbounded by the schema but enforced at the repository layer
// (GroupRepository.Create / Update reject cycles via ancestor check).
//
// Mirrors the `groups` table introduced by migration 041_groups.sql.
// Cross-tenant safety: every Group is scoped to a workspace (WorkspaceID
// is NOT NULL), and the API layer additionally enforces
// workspace.OwnerID == JWT.userID before any read/write.
type Group struct {
	ID            int64     `json:"id"`
	WorkspaceID   int64     `json:"workspace_id"`
	ParentGroupID *int64    `json:"parent_group_id,omitempty"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
