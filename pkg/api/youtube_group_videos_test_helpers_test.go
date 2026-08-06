package api

import (
	"context"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"testing"
	"time"
)

type mockGroupStore struct {
	findByIDFn                        func(id int64) (*models.Group, error)
	listByWorkspaceFn                 func(workspaceID int64) ([]models.Group, error)
	listByWorkspaceWithAccountsFn     func(workspaceID int64) ([]models.GroupWithAccounts, error)
	listByWorkspacePageFn             func(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.Group, bool, error)
	listByWorkspaceWithAccountsPageFn func(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.GroupWithAccounts, bool, error)
	listAccountsInGroupFn             func(groupID int64) ([]int64, error)
	validateAccountOwnershipFn        func(userID, workspaceID int64, accountIDs []int64) ([]int64, error)
	createFn                          func(g *models.Group) error
	updateFn                          func(g *models.Group) error
	deleteFn                          func(id int64) error
	setAccountsFn                     func(groupID int64, accountIDs []int64) error
	updateSettingsFn                  func(ctx context.Context, groupID, workspaceID, userID int64, updates []models.GroupAccountLanguageUpdate) error
	removeAccountFromGroupTxFn        func(ctx context.Context, groupID, workspaceID, accountID int64) error
}

func (m *mockGroupStore) FindByID(id int64) (*models.Group, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return nil, nil
}

func (m *mockGroupStore) ListByWorkspace(workspaceID int64) ([]models.Group, error) {
	if m.listByWorkspaceFn != nil {
		return m.listByWorkspaceFn(workspaceID)
	}
	return nil, nil
}

func (m *mockGroupStore) ListByWorkspaceWithAccounts(workspaceID int64) ([]models.GroupWithAccounts, error) {
	if m.listByWorkspaceWithAccountsFn != nil {
		return m.listByWorkspaceWithAccountsFn(workspaceID)
	}
	return nil, nil
}
func (m *mockGroupStore) ListByWorkspacePage(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.Group, bool, error) {
	if m.listByWorkspacePageFn != nil {
		return m.listByWorkspacePageFn(workspaceID, afterTime, afterID, limit)
	}
	return nil, false, nil
}
func (m *mockGroupStore) ListByWorkspaceWithAccountsPage(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.GroupWithAccounts, bool, error) {
	if m.listByWorkspaceWithAccountsPageFn != nil {
		return m.listByWorkspaceWithAccountsPageFn(workspaceID, afterTime, afterID, limit)
	}
	return nil, false, nil
}

func (m *mockGroupStore) ListAccountsInGroup(groupID int64) ([]int64, error) {
	if m.listAccountsInGroupFn != nil {
		return m.listAccountsInGroupFn(groupID)
	}
	return nil, nil
}

func (m *mockGroupStore) ValidateAccountOwnership(userID, workspaceID int64, accountIDs []int64) ([]int64, error) {
	if m.validateAccountOwnershipFn != nil {
		return m.validateAccountOwnershipFn(userID, workspaceID, accountIDs)
	}
	return accountIDs, nil
}

func (m *mockGroupStore) Create(g *models.Group) error {
	if m.createFn != nil {
		return m.createFn(g)
	}
	return nil
}

func (m *mockGroupStore) Update(g *models.Group) error {
	if m.updateFn != nil {
		return m.updateFn(g)
	}
	return nil
}

func (m *mockGroupStore) Delete(id int64) error {
	if m.deleteFn != nil {
		return m.deleteFn(id)
	}
	return nil
}

func (m *mockGroupStore) SetAccounts(groupID int64, accountIDs []int64) error {
	if m.setAccountsFn != nil {
		return m.setAccountsFn(groupID, accountIDs)
	}
	return nil
}

func (m *mockGroupStore) UpdateSettings(ctx context.Context, groupID, workspaceID, userID int64, updates []models.GroupAccountLanguageUpdate) error {
	if m.updateSettingsFn != nil {
		return m.updateSettingsFn(ctx, groupID, workspaceID, userID, updates)
	}
	return nil
}

func (m *mockGroupStore) RemoveAccountFromGroupTx(ctx context.Context, groupID, workspaceID, accountID int64) error {
	if m.removeAccountFromGroupTxFn != nil {
		return m.removeAccountFromGroupTxFn(ctx, groupID, workspaceID, accountID)
	}
	return nil
}

func newGroupVideosRouter(
	t *testing.T,
	workspace *models.Workspace,
	groupStore *mockGroupStore,
	userStore *mockUserStore,
	editStore *mockYouTubeVideoEditStore,
	ytSvc *mockYouTubeOAuthServiceForEditor,
	vault *mockCredentialVault,
	opts ...RouterOption,
) *Router {
	t.Helper()
	workspaceStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		},
	}
	baseOpts := []RouterOption{
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithYouTubeService(ytSvc),
		WithCredentialVault(vault),
	}
	if groupStore != nil {
		baseOpts = append(baseOpts, WithGroupStore(groupStore))
	}
	baseOpts = append(baseOpts, opts...)
	resolvedUserStore := UserStore(userStoreIfNil(userStore))
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		resolvedUserStore,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		baseOpts...,
	)
}

func userStoreIfNil(s *mockUserStore) UserStore {
	if s != nil {
		return s
	}
	return &mockUserStore{}
}

func stringPtr(s string) *string {
	return &s
}
