package api

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/contracts"
)

// UserWorkspaceHelper is re-exported from the contracts package so the
// OAuth/session layer can depend on a small leaf interface instead of Router.
type UserWorkspaceHelper = contracts.UserWorkspaceHelper

// repoUserWorkspaceHelper adapts the workspace repositories to the small
// OAuth-facing contract. Keeping this adapter out of router.go prevents the
// router composition file from also owning repository projection logic.
type repoUserWorkspaceHelper struct {
	workspaceRepo *repository.WorkspaceRepository
	teamRepo      *repository.TeamRepository
}

// RepoUserWorkspaceHelper builds the production adapter.
func RepoUserWorkspaceHelper(w *repository.WorkspaceRepository, t *repository.TeamRepository) UserWorkspaceHelper {
	return &repoUserWorkspaceHelper{workspaceRepo: w, teamRepo: t}
}

func (h repoUserWorkspaceHelper) ListOwned(_ context.Context, userID int64) ([]int64, error) {
	owned, err := h.workspaceRepo.ListByOwner(userID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(owned))
	for _, w := range owned {
		out = append(out, w.ID)
	}
	return out, nil
}

func (h repoUserWorkspaceHelper) ListMemberships(_ context.Context, userID int64) ([]int64, error) {
	members, err := h.teamRepo.ListForUser(userID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(members))
	for _, m := range members {
		out = append(out, m.WorkspaceID)
	}
	return out, nil
}
