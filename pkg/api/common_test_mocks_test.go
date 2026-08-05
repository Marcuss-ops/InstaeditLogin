package api

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockProvider implements the two capabilities the API router consumes:
// OAuthProvider (Name, GetLoginURL, HandleCallback, RefreshOAuthToken) and
// Publisher (Name, Publish). The real per-platform structs implement both,
// so the single mock struct mirrors that.
//
// Taglio 2.2: token persistence moved to the central CredentialVault.
// The mock is unchanged by Taglio 2.2.
type mockProvider struct {
	platform           string
	loginURL           string
	loginWithOptionsFn func(state string, options services.OAuthLoginOptions) string
	handleCallback     func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)
	publishFn          func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	refreshFn          func(ctx context.Context, refreshToken string) (*models.TokenData, error)

	handleCallbackCalls int
	publishCalls        int
}

func (m *mockProvider) Name() string { return m.platform }
func (m *mockProvider) GetLoginURL(state string) string {
	return m.loginURL + "?state=" + state
}
func (m *mockProvider) GetLoginURLWithOptions(state string, options services.OAuthLoginOptions) string {
	if m.loginWithOptionsFn != nil {
		return m.loginWithOptionsFn(state, options)
	}
	return m.GetLoginURL(state)
}
func (m *mockProvider) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	m.handleCallbackCalls++
	if m.handleCallback == nil {
		return nil, nil, fmt.Errorf("HandleCallback not implemented")
	}
	return m.handleCallback(ctx, state, code)
}
func (m *mockProvider) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	if m.refreshFn != nil {
		return m.refreshFn(ctx, refreshToken)
	}
	return nil, fmt.Errorf("refresh not implemented")
}

// mockDiscoverableProvider extends mockProvider with AccountDiscoverer.
// Use it when testing providers (e.g. Facebook Pages) that expand one
// OAuth grant into multiple PlatformAccounts.
type mockDiscoverableProvider struct {
	mockProvider
	discoverFn func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error)
}

// mockTokenPolicyProvider extends mockProvider with TokenPolicyProvider.
// Use it when testing handleValidateAccount's provider-specific token
// type resolution.
type mockTokenPolicyProvider struct {
	mockProvider
	preferredTokenTypes []string
}

func (m *mockTokenPolicyProvider) PreferredTokenTypes() []string {
	return m.preferredTokenTypes
}

func (m *mockDiscoverableProvider) DiscoverAccounts(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
	if m.discoverFn != nil {
		return m.discoverFn(ctx, accessToken, platformUserID)
	}
	return nil, fmt.Errorf("DiscoverAccounts not implemented")
}
func (m *mockProvider) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	m.publishCalls++
	if m.publishFn == nil {
		return nil, fmt.Errorf("Publish not implemented")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}

// mockCredentialVault implements credentials.VaultAPI for tests. The
// default (nil fields) returns success on Save and Revoke, an error
// on Get, and an error on Renew — that is what most tests (login,
// callback happy path, workspace, post CRUD) want. Tests that
// exercise the publish path or want to force a save/get/renew error
// override the relevant field in the constructor and pass via
// WithCredentialVault in opts.
//
// Taglio 2.2: renamed from mockTokenService. The `renewFn` field
// receives a credentials.TokenRefresher (plain function) rather than
// a services.OAuthProvider interface — the vault no longer knows
// about per-platform types.
type mockCredentialVault struct {
	saveFn            func(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error
	getFn             func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error)
	renewFn           func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error)
	revokeFn          func(ctx context.Context, platformAccountID int64) error
	getRefreshTokenFn func(ctx context.Context, platformAccountID int64) (string, error)
}

func (m *mockCredentialVault) Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	if m.saveFn != nil {
		return m.saveFn(ctx, platformAccountID, tokenData)
	}
	return nil
}
func (m *mockCredentialVault) Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	if m.getFn != nil {
		return m.getFn(ctx, platformAccountID, tokenType)
	}
	return nil, fmt.Errorf("Get not implemented in this test mock (override via mockCredentialVault.getFn)")
}
func (m *mockCredentialVault) Renew(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
	if m.renewFn != nil {
		return m.renewFn(ctx, accountID, tokenType, refresh)
	}
	// Keep older group-video tests focused on their access-token fixture while
	// production code correctly uses Renew for expired YouTube bearer grants.
	if m.getFn != nil {
		return m.getFn(ctx, accountID, tokenType)
	}
	return nil, fmt.Errorf("Renew not implemented (override via mockCredentialVault.renewFn)")
}
func (m *mockCredentialVault) Revoke(ctx context.Context, platformAccountID int64) error {
	if m.revokeFn != nil {
		return m.revokeFn(ctx, platformAccountID)
	}
	return nil
}
func (m *mockCredentialVault) Rotate(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	return m.Save(ctx, platformAccountID, tokenData)
}

// GetRefreshToken implements the optional RefreshTokenReader capability
// the account-disconnect flow uses to revoke a YouTube grant remotely.
func (m *mockCredentialVault) GetRefreshTokenForOAuthConnectionTx(ctx context.Context, tx *sql.Tx, oauthConnectionID int64) (string, error) {
	if m.getRefreshTokenFn != nil {
		return m.getRefreshTokenFn(ctx, oauthConnectionID)
	}
	return "", fmt.Errorf("GetRefreshTokenForOAuthConnectionTx not implemented")
}

func (m *mockCredentialVault) GetRefreshToken(ctx context.Context, platformAccountID int64) (string, error) {
	if m.getRefreshTokenFn != nil {
		return m.getRefreshTokenFn(ctx, platformAccountID)
	}
	return "", fmt.Errorf("GetRefreshToken not implemented in this test mock (override via mockCredentialVault.getRefreshTokenFn)")
}

// fakeYouTubeRevoker implements the narrow YouTubeRevoker capability used
// by the account-disconnect flow tests.
type fakeYouTubeRevoker struct {
	revokeFn func(ctx context.Context, token string) error
}

func (f *fakeYouTubeRevoker) Revoke(ctx context.Context, token string) error {
	if f.revokeFn != nil {
		return f.revokeFn(ctx, token)
	}
	return nil
}

type fakeOAuthGrantRevoker struct {
	revokeFn func(ctx context.Context, token string) error
}

func (f *fakeOAuthGrantRevoker) RevokeGrant(ctx context.Context, token string) error {
	if f.revokeFn != nil {
		return f.revokeFn(ctx, token)
	}
	return nil
}

// fakeChannelAuthorizer (Task 1/10 test seam) is the no-op
// implementation of services.ChannelAuthorizer that newTestRouter
// wires by default. Each AuthorizeChannel call records every
// TokenData into tokenWrites (mirroring the production sequence:
// UPSERT oauth_connections + SaveTokenTx + status flip ⇒ produce a
// single cipher write per token). Tests inspect tokenWrites to
// assert the atomic-flow contract — exactly how many tokens were
// saved, on which (accountID, tokenType) pair, with which
// AccessToken.
//
// Production wiring in internal/bootstrap.Wire passes a real
// *services.ChannelAuthorizationService; tests override via
// WithChannelAuthorizer(&fakeChannelAuthorizer{...}) when they
// need to inject specific behaviour (failure injection on
// authorizeErr, channel guard verification on lastExpectedCh, etc.).
type fakeChannelAuthorizer struct {
	authorizeCalls atomic.Int32
	lastAccountID  int64
	lastExpectedCh string
	lastScopes     []string
	lastTokens     []*models.TokenData
	// tokenWrites is the per-token independent audit trail. Tests
	// assert len(tokenWrites) for "exactly N cipher writes on
	// success" and len(tokenWrites)==0 for "no writes on failure".
	// Concurrency: protected by mu because the production router
	// does not parallelize AuthorizeChannel calls, but the
	// invariant makes future races safe.
	mu          sync.Mutex
	tokenWrites []fakeAuthTokenWrite
	// authorizeErr is returned (early) when non-nil. Replaces the
	// old vault.Save-error tests; tokenWrites stays empty when
	// authorizeErr fires before any token is processed.
	authorizeErr error
	// oauthConnectionID is returned as the AuthorizeChannel
	// oauth_connection_id; tests that read it (none today) can
	// override.
	oauthConnectionID int64
}

type fakeAuthTokenWrite struct {
	AccountID    int64
	TokenType    string
	AccessToken  string
	RefreshToken string
}

func (f *fakeChannelAuthorizer) AuthorizeChannel(ctx context.Context, accountID int64, expectedChannelID string, scopes []string, tokens ...*models.TokenData) (int64, error) {
	f.authorizeCalls.Add(1)
	f.lastAccountID = accountID
	f.lastExpectedCh = expectedChannelID
	f.lastScopes = scopes
	// Make a defensive copy of the variadic token slice so tests
	// can inspect the inputs without aliasing.
	tokensCopy := make([]*models.TokenData, len(tokens))
	copy(tokensCopy, tokens)
	f.lastTokens = tokensCopy
	if f.authorizeErr != nil {
		return 0, f.authorizeErr
	}
	f.mu.Lock()
	for _, td := range tokens {
		if td == nil {
			continue
		}
		f.tokenWrites = append(f.tokenWrites, fakeAuthTokenWrite{
			AccountID:    accountID,
			TokenType:    td.TokenType,
			AccessToken:  td.AccessToken,
			RefreshToken: td.RefreshToken,
		})
	}
	f.mu.Unlock()
	if f.oauthConnectionID == 0 {
		return 424242, nil
	}
	return f.oauthConnectionID, nil
}

// tokenWriteCount is a snapshot helper used by tests that don't
// care about value-level inspection, only the count.
func (f *fakeChannelAuthorizer) tokenWriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.tokenWrites)
}

var _ services.ChannelAuthorizer = (*fakeChannelAuthorizer)(nil)

// mockUserStore implements UserStore with configurable function fields.
//
// SPRINT 7.1 (P0#14): FindOrCreateUserByPlatform is gone from the
// UserStore interface — the OAuth callback now ONLY attaches the
// platform account to the authenticated user (never auto-creates).
// Tests that used to return a *models.User pair from a mock callback
// now return only *models.PlatformAccount (the link side).
type mockUserStore struct {
	attachFn                      func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error)
	listFn                        func(userID int64, platform string) ([]*models.PlatformAccount, error)
	listFilteredYouTubeAccountsFn func(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error)
	findPlatformAccountFn         func(id int64) (*models.PlatformAccount, error)
	findPlatformAccountByTupleFn  func(platform, platformUserID string) (*models.PlatformAccount, error)
	updatePlatformAccountFn       func(account *models.PlatformAccount) error
	deletePlatformAccountFn       func(id int64) error
	findUserIDByEmailFn           func(ctx context.Context, email string) (int64, error)
	finalizeAttachFn              func(ctx context.Context, accountID int64, scopes []string) (int64, error)
	// markReauthRequiredFn (Task 2/10) covers the channel-binding
	// best-effort flag the OAuth callback path fires when
	// attachDiscoveredAccounts returns ErrYouTubeChannelMismatch.
	// Tests that exercise the 422/409 path override this; the others
	// get the default (no-op) below.
	markReauthRequiredFn func(ctx context.Context, accountID int64, code, message string) error
	// countActiveOnConnectionFn (P0 — shared-grant disconnect) returns
	// the number of still-active sibling channels sharing the account's
	// grant. Default 0 (single-account grants) unless overridden.
	countActiveOnConnectionFn func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error)
	// disconnectPlatformAccountFn models the production atomic shared-grant
	// operation without widening the UserStore compatibility interface.
	disconnectPlatformAccountFn func(ctx context.Context, accountID int64) (lastOnGrant bool, handled bool, err error)	disconnectOAuthGrantFn              func(ctx context.Context, oauthConnectionID int64) error
	disconnectOAuthGrantWithRevocationFn func(ctx context.Context, oauthConnectionID int64, revoke func(context.Context, *sql.Tx) error) error
}

func (m *mockUserStore) AttachPlatformAccount(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	if m.attachFn == nil {
		return nil, fmt.Errorf("AttachPlatformAccount not implemented in this test mock (override via mockUserStore.attachFn)")
	}
	return m.attachFn(userID, profile, platform)
}
func (m *mockUserStore) ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error) {
	return m.listFn(userID, platform)
}
func (m *mockUserStore) ListFilteredYouTubeAccounts(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error) {
	if m.listFilteredYouTubeAccountsFn != nil {
		return m.listFilteredYouTubeAccountsFn(userID, workspaceID, group, language, manager)
	}
	// Fallback to the standard list so tests that already wire listFn
	// continue to work without a new callback.
	if m.listFn == nil {
		return nil, fmt.Errorf("ListFilteredYouTubeAccounts not implemented in this test mock (override via listFn or listFilteredYouTubeAccountsFn)")
	}
	return m.listFn(userID, "youtube")
}
func (m *mockUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if m.findPlatformAccountFn != nil {
		return m.findPlatformAccountFn(id)
	}
	return nil, nil
}
func (m *mockUserStore) FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error) {
	if m.findPlatformAccountByTupleFn != nil {
		return m.findPlatformAccountByTupleFn(platform, platformUserID)
	}
	return nil, nil
}
func (m *mockUserStore) UpdatePlatformAccount(account *models.PlatformAccount) error {
	if m.updatePlatformAccountFn != nil {
		return m.updatePlatformAccountFn(account)
	}
	return nil
}
func (m *mockUserStore) DeletePlatformAccount(id int64) error {
	if m.deletePlatformAccountFn != nil {
		return m.deletePlatformAccountFn(id)
	}
	return nil
}

// FindUserIDByEmail implements the UserStore method added for the
// P2 admin CSV import surface (POST /admin/channels/import-csv).
// Default returns (0, nil) so tests that don't exercise the import
// path don't need to wire it up. Tests that DO exercise the path
// override findUserIDByEmailFn.
func (m *mockUserStore) FindUserIDByEmail(ctx context.Context, email string) (int64, error) {
	if m.findUserIDByEmailFn != nil {
		return m.findUserIDByEmailFn(ctx, email)
	}
	return 0, nil
}

// FinalizeAttach implements the UserStore method added for the P2
// admin connect-link surface (POST /admin/channels/{id}/connect-link
// + the OAuth callback's oauth_connection promotion). Default
// returns (0, nil) so tests that don't exercise the connect-link
// flow don't need to wire it up. Tests that DO exercise it override
// finalizeAttachFn.
func (m *mockUserStore) FinalizeAttach(ctx context.Context, accountID int64, scopes []string) (int64, error) {
	if m.finalizeAttachFn != nil {
		return m.finalizeAttachFn(ctx, accountID, scopes)
	}
	return 0, nil
}

// MarkReauthRequired (Task 2/10) implements the channel-binding
// best-effort flag the OAuth callback path fires when
// attachDiscoveredAccounts returns ErrYouTubeChannelMismatch. Default
// returns nil so the 422 writeError still completes (a hypothetical
// nil-returning repo would still satisfy the contract — the flag
// is best-effort by design).
func (m *mockUserStore) MarkReauthRequired(ctx context.Context, accountID int64, code, message string) error {
	if m.markReauthRequiredFn != nil {
		return m.markReauthRequiredFn(ctx, accountID, code, message)
	}
	return nil
}

// CountActiveAccountsOnConnection (P0 — shared-grant disconnect)
// returns the number of still-active sibling channels sharing the
// account's OAuth grant. Default 0 (single-account grant → the
// disconnect may revoke) unless a test overrides countActiveOnConnectionFn.
func (m *mockUserStore) CountActiveAccountsOnConnection(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
	if m.countActiveOnConnectionFn != nil {
		return m.countActiveOnConnectionFn(ctx, oauthConnectionID, excludeAccountID)
	}
	return 0, nil
}

func (m *mockUserStore) DisconnectPlatformAccount(ctx context.Context, accountID int64) (bool, bool, error) {
	if m.disconnectPlatformAccountFn != nil {
		return m.disconnectPlatformAccountFn(ctx, accountID)
	}
	return false, false, nil
}

func (m *mockUserStore) DisconnectOAuthGrantTx(ctx context.Context, oauthConnectionID int64) error {
	if m.disconnectOAuthGrantFn != nil {
		return m.disconnectOAuthGrantFn(ctx, oauthConnectionID)
	}
	return nil
}

func (m *mockUserStore) DisconnectOAuthGrantWithRevocationTx(ctx context.Context, oauthConnectionID int64, revoke func(context.Context, *sql.Tx) error) error {
	if m.disconnectOAuthGrantWithRevocationFn != nil {
		return m.disconnectOAuthGrantWithRevocationFn(ctx, oauthConnectionID, revoke)
	}
	if err := revoke(ctx, nil); err != nil {
		return err
	}
	return m.DisconnectOAuthGrantTx(ctx, oauthConnectionID)
}

// mockWorkspaceStore implements WorkspaceStore with configurable function fields.
type mockWorkspaceStore struct {
	createFn       func(*models.Workspace) error
	findByIDFn     func(id int64) (*models.Workspace, error)
	listByOwnerFn  func(ownerID int64) ([]models.Workspace, error)
	deleteFn       func(id int64) error
	attachChFn     func(ctx context.Context, workspaceID, platformAccountID int64, groupName string) (*models.WorkspaceChannel, error)
	listChannelsFn func(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error)
	updateChFn     func(ctx context.Context, workspaceID, platformAccountID int64, groupName *string, enabled *bool) error
	detachChFn     func(ctx context.Context, workspaceID, platformAccountID int64) error
	findChannelFn  func(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error)
}

func (m *mockWorkspaceStore) Create(w *models.Workspace) error {
	if m.createFn == nil {
		return nil
	}
	return m.createFn(w)
}
func (m *mockWorkspaceStore) FindByID(id int64) (*models.Workspace, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return &models.Workspace{
		ID:        id,
		Name:      "default",
		OwnerID:   1,
		CreatedAt: time.Now(),
	}, nil
}
func (m *mockWorkspaceStore) ListByOwner(ownerID int64) ([]models.Workspace, error) {
	if m.listByOwnerFn == nil {
		return nil, nil
	}
	return m.listByOwnerFn(ownerID)
}
func (m *mockWorkspaceStore) Delete(id int64) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(id)
}
func (m *mockWorkspaceStore) AttachChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName string) (*models.WorkspaceChannel, error) {
	if m.attachChFn != nil {
		return m.attachChFn(ctx, workspaceID, platformAccountID, groupName)
	}
	return &models.WorkspaceChannel{
		WorkspaceID:       workspaceID,
		PlatformAccountID: platformAccountID,
		GroupName:         groupName,
		Enabled:           true,
		CreatedAt:         time.Now(),
	}, nil
}
func (m *mockWorkspaceStore) ListChannels(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error) {
	if m.listChannelsFn != nil {
		return m.listChannelsFn(ctx, workspaceID)
	}
	return []models.WorkspaceChannel{}, nil
}
func (m *mockWorkspaceStore) UpdateChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName *string, enabled *bool) error {
	if m.updateChFn != nil {
		return m.updateChFn(ctx, workspaceID, platformAccountID, groupName, enabled)
	}
	return nil
}
func (m *mockWorkspaceStore) DetachChannel(ctx context.Context, workspaceID, platformAccountID int64) error {
	if m.detachChFn != nil {
		return m.detachChFn(ctx, workspaceID, platformAccountID)
	}
	return nil
}
func (m *mockWorkspaceStore) FindChannel(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
	if m.findChannelFn != nil {
		return m.findChannelFn(ctx, workspaceID, platformAccountID)
	}
	return nil, nil
}

// mockPostStore implements PostStore with configurable function fields.
type mockPostStore struct {
	createFn          func(*models.Post, []*models.PostTarget) error
	findByIDFn        func(id int64) (*models.Post, error)
	updateFn          func(*models.Post) error
	listByWorkspaceFn func(workspaceID int64) ([]models.Post, error)
	// listByPostFn + findTargetByIDFn added for the
	// Taglio 5.1 step 2 polling endpoint suite (closes the
	// empty-array handleGetPostTargets gap and adds the single
	// target GET).
	listByPostFn     func(postID int64) ([]models.PostTarget, error)
	findTargetByIDFn func(id int64) (*models.PostTarget, error)
	deleteFn         func(id int64) error
	saveTargetFn     func(*models.PostTarget) error
	publishPostFn    func(id int64) error
	cancelPostFn     func(id int64) error
	retryPostFn      func(id int64) error
	retryTargetFn    func(id int64) error
}

func (m *mockPostStore) Create(post *models.Post, targets []*models.PostTarget) error {
	if m.createFn != nil {
		return m.createFn(post, targets)
	}
	post.ID = 100
	post.CreatedAt = time.Now()
	for i, t := range targets {
		t.ID = int64(200 + i)
		t.PostID = post.ID
	}
	return nil
}
func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(id)
	}
	return &models.Post{
		ID:          id,
		WorkspaceID: 1,
		Title:       "default",
		Status:      models.PostStatusDraft,
		CreatedAt:   time.Now(),
	}, nil
}
func (m *mockPostStore) Update(post *models.Post) error {
	if m.updateFn == nil {
		return nil
	}
	return m.updateFn(post)
}
func (m *mockPostStore) ListByWorkspace(workspaceID int64) ([]models.Post, error) {
	if m.listByWorkspaceFn == nil {
		return nil, nil
	}
	return m.listByWorkspaceFn(workspaceID)
}
func (m *mockPostStore) ListByPost(postID int64) ([]models.PostTarget, error) {
	if m.listByPostFn == nil {
		return nil, nil
	}
	return m.listByPostFn(postID)
}
func (m *mockPostStore) FindTargetByID(id int64) (*models.PostTarget, error) {
	if m.findTargetByIDFn != nil {
		return m.findTargetByIDFn(id)
	}
	return nil, nil
}
func (m *mockPostStore) Delete(id int64) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(id)
}
func (m *mockPostStore) SaveTarget(target *models.PostTarget) error {
	if m.saveTargetFn == nil {
		return nil
	}
	return m.saveTargetFn(target)
}
func (m *mockPostStore) RetryPost(id int64) error {
	if m.retryPostFn == nil {
		return nil
	}
	return m.retryPostFn(id)
}
func (m *mockPostStore) CancelPost(id int64) error {
	if m.cancelPostFn == nil {
		return nil
	}
	return m.cancelPostFn(id)
}
func (m *mockPostStore) PublishPost(id int64) error {
	if m.publishPostFn == nil {
		return nil
	}
	return m.publishPostFn(id)
}
func (m *mockPostStore) RetryTarget(id int64) error {
	if m.retryTargetFn == nil {
		return nil
	}
	return m.retryTargetFn(id)
}

// mockSnapshotStore implements SnapshotStore for tests.
type mockSnapshotStore struct {
	getFn    func(platformAccountID int64) (*repository.AccountResourceSnapshot, error)
	upsertFn func(snap *repository.AccountResourceSnapshot) error
	staleFn  func(platformAccountID int64, maxAge time.Duration) (bool, error)
}

func (m *mockSnapshotStore) GetSnapshot(platformAccountID int64) (*repository.AccountResourceSnapshot, error) {
	if m.getFn != nil {
		return m.getFn(platformAccountID)
	}
	return nil, nil
}
func (m *mockSnapshotStore) UpsertSnapshot(snap *repository.AccountResourceSnapshot) error {
	if m.upsertFn != nil {
		return m.upsertFn(snap)
	}
	return nil
}
func (m *mockSnapshotStore) IsSnapshotStale(platformAccountID int64, maxAge time.Duration) (bool, error) {
	if m.staleFn != nil {
		return m.staleFn(platformAccountID, maxAge)
	}
	return true, nil
}

// mockDetailProvider extends mockProvider with AccountDetailsProvider + AccountContentProvider.
type mockDetailProvider struct {
	mockProvider
	detailsFn func(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error)
	contentFn func(ctx context.Context, accessToken, platformUserID string, cursor string, limit int, privacy string) (*models.AccountContentPage, error)
}

func (m *mockDetailProvider) GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	if m.detailsFn != nil {
		return m.detailsFn(ctx, accessToken, platformUserID)
	}
	return nil, fmt.Errorf("GetAccountDetails not implemented")
}
func (m *mockDetailProvider) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int, privacy string) (*models.AccountContentPage, error) {
	if m.contentFn != nil {
		return m.contentFn(ctx, accessToken, platformUserID, cursor, limit, privacy)
	}
	return nil, fmt.Errorf("ListAccountContent not implemented")
}
