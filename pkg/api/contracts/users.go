package contracts

import "context"

// UserWorkspaceHelper is the interface used by route handlers to
// resolve a user's active workspace without tying pkg/api to the
// concrete *sql.DB-bound repositories.
//
// Implementations are injected via RouterOption.WithUserWorkspaceHelper.
// Tests inject a stub implementing these methods (see
// pkg/api/workspaces_test.go). Production wiring in
// internal/bootstrap.Wire supplies the concrete *repository.TeamRepository
// + *repository.WorkspaceRepository via api.RepoUserWorkspaceHelper
// (the constructor stays in pkg/api/handlers.go so the contracts
// package remains a leaf with no dependency on internal/repository).
type UserWorkspaceHelper interface {
	// ListOwned returns the workspaces the user owns, ordered by
	// created_at desc. The handler picks the first as the JWT's
	// active workspace when no explicit switch request is present.
	ListOwned(ctx context.Context, userID int64) ([]int64, error)
	// ListMemberships returns the workspaces the user is a team
	// member of (non-owner), ordered by joined_at desc. Used as a
	// fallback when the user owns no workspace yet (e.g. fresh
	// invitation completed before personal workspace creation).
	ListMemberships(ctx context.Context, userID int64) ([]int64, error)
}
