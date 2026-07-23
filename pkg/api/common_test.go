package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
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
	platform       string
	loginURL       string
	handleCallback func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)
	publishFn      func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	refreshFn      func(ctx context.Context, refreshToken string) (*models.TokenData, error)

	handleCallbackCalls int
	publishCalls        int
}

func (m *mockProvider) Name() string { return m.platform }
func (m *mockProvider) GetLoginURL(state string) string {
	return m.loginURL + "?state=" + state
}
func (m *mockProvider) GetLoginURLWithOptions(state string, _ services.OAuthLoginOptions) string {
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
	saveFn   func(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error
	getFn    func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error)
	renewFn  func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error)
	revokeFn func(ctx context.Context, platformAccountID int64) error
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
	if m.renewFn == nil {
		return nil, fmt.Errorf("Renew not implemented (override via mockCredentialVault.renewFn)")
	}
	return m.renewFn(ctx, accountID, tokenType, refresh)
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
	deleteFn          func(id int64) error
	saveTargetFn      func(*models.PostTarget) error
	publishPostFn     func(id int64) error
	cancelPostFn      func(id int64) error
	retryPostFn       func(id int64) error
	retryTargetFn     func(id int64) error
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const testJWTSecret = "test-jwt-secret-must-be-long-enough-for-hs256"

func withBearerJWT(t *testing.T, req *http.Request, userID int64) {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, userID))
}

// newTestRouter builds a Router wired with a mock provider and store.
//
// Taglio 2.2: the Router takes a CapabilityRouter (per-capability lookups)
// plus a CredentialVault (via WithCredentialVault). The default vault
// is a no-op mock that succeeds on save/revoke and errors on get/renew —
// that is what most tests (login, callback happy path, workspace, post
// CRUD) want. Tests that exercise the publish path or want to force a
// save/renew error override via WithCredentialVault(&mockCredentialVault{...})
// in opts.
func newTestRouter(
	platformSvc services.NameProvider,
	store *mockUserStore,
	frontendURL string,
	opts ...RouterOption,
) *Router {
	capRouter := services.NewCapabilityRouter()
	capRouter.Register(platformSvc.Name(), platformSvc)
	capRouter.Register("instagram", platformSvc)
	capRouter.Register("tiktok", platformSvc)
	capRouter.Register("twitter", platformSvc)
	otc := NewInMemoryOneTimeCodeStore(60 * time.Second)
	// Note: the sweeper goroutine leaks until the test binary exits —
	// acceptable for unit tests; the 1s ticker has no observable effect
	// on test behaviour and the OS reclaims everything on process exit.
	defaultVault := &mockCredentialVault{}
	return NewRouter(
		capRouter,
		store,
		auth.NewManager(testJWTSecret, 24),
		frontendURL,
		nil,
		append([]RouterOption{
			WithOneTimeCodeStore(otc),
			WithCredentialVault(defaultVault),
			// Task 1/10 — atomic OAuth finalize. newTestRouter
			// wires a default fakeChannelAuthorizer that
			// independently records every token write in
			// tokenWrites. Tests assert len(tokenWrites) for
			// the cipher-write count semantic and override
			// the canonical seam via WithChannelAuthorizer
			// only when they need specific failure injection
			// (e.g. TestHandleCallback_AuthorizeChannelError).
			WithChannelAuthorizer(&fakeChannelAuthorizer{}),
		}, opts...)...,
	)
}

// issueTestJWT mints a JWT carrying (userID, workspaceID=1, sessionID=1).
// SPRINT 7.1 couples /auth/{provider}/* to a session-gating middleware
// (oauthSessionRedirect) that calls Manager.Verify on the Authorization
// header or session cookie. Manager.Verify rejects any token with
// UserID<=0 || WorkspaceID<=0 || SessionID<=0, so the legacy
// `Issue(userID)` path (which signs with wsID=0, sessionID=0) no longer
// produces an acceptable token. IssueAccess requires all three IDs
// positive; tests that previously relied on Issue(userID) implicitly
// expected the OAuth layer to ignore the Authorization header — that
// assumption no longer holds.
func issueTestJWT(t *testing.T, userID int64) string {
	t.Helper()
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.IssueAccess(userID, 1, 1)
	if err != nil {
		t.Fatalf("issue access jwt (user=%d, ws=1, session=1): %v", userID, err)
	}
	return tok
}

var successCallback = func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	return &models.PlatformProfile{
			PlatformUserID: "pf-123",
			Username:       "testuser",
			Name:           "Test User",
			Email:          "test@example.com",
		}, &models.TokenData{
			AccessToken: "at-secret",
			TokenType:   "bearer",
			ExpiresIn:   3600,
		}, nil
}

// successAttach models the SPRINT 7.1 connect path: the JWT's user_id
// (1) is the linkage target, never a freshly-allocated id from a
// FindOrCreateUserByPlatform query.
var successAttach = func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	return &models.PlatformAccount{
		ID:             10,
		UserID:         userID,
		Platform:       platform,
		PlatformUserID: profile.PlatformUserID,
		Username:       profile.Username,
	}, nil
}

func setOAuthStateCookieForTest(req *http.Request, provider, state string) {
	req.AddCookie(&http.Cookie{
		Name:     OAuthStateCookieName(provider),
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// setOAuthExpectedChannelCookieForTest mirrors setOAuthStateCookieForTest
// for the sibling oauth_state_{provider}_expected_channel cookie used by
// the YouTube P0 fix to round-trip ?expected_channel_id=UC... across
// the OAuth callback. The cookie value is "<state>:<channelID>" — the
// state nonce prefix binds the channel hint to the SAME flow so a
// stale sibling cookie from a previous OAuth round-trip cannot leak
// into a new one (the production code in handlers.go enforces this
// prefix check; this helper just mirrors the production format for
// tests).
func setOAuthExpectedChannelCookieForTest(req *http.Request, provider, state, channelID string) {
	req.AddCookie(&http.Cookie{
		Name:     OAuthStateExpectedChannelCookieName(provider),
		Value:    state + ":" + channelID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ---------------------------------------------------------------------------
// CORS middleware tests
// ---------------------------------------------------------------------------

func newCORSTestRouter(allowedOrigins []string) *Router {
	return NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		allowedOrigins,
		WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)),
	)
}

// ---------------------------------------------------------------------------
// handleListAccounts tests (SPRINT 7.1 / Taglio 1.4 — closes the
// user-facing /connections page gap. Endpoint is behind r.protected,
// reads identity EXCLUSIVELY from auth.IdentityFromContext, NEVER
// from ?user_id / body / path.)
// ---------------------------------------------------------------------------

// twoAccountFixtures returns two synthetic accounts the list test
// uses as fixtures. The shape is exactly what ListPlatformAccountsByUser
// returns from the repo (subset of the full fixture model).
func twoAccountFixtures() []*models.PlatformAccount {
	t0 := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 7, 15, 9, 30, 0, 0, time.UTC)
	return []*models.PlatformAccount{
		{
			ID: 21, UserID: 1, Platform: "instagram",
			PlatformUserID: "1784deadbeef", Username: "alice_ig",
			Status: models.AccountStatusActive, CreatedAt: t0, UpdatedAt: t0,
		},
		{
			ID: 22, UserID: 1, Platform: "facebook",
			PlatformUserID: "1029384cafebabe", Username: "alice.fb.page",
			Status: models.AccountStatusActive, CreatedAt: t1, UpdatedAt: t1,
		},
	}
}

// ---------------------------------------------------------------------------
// handleGetAccount / handleValidateAccount / handleReconnectAccount /
// handleDeleteAccount tests (Taglio 1.4 — full implementations replacing
// the 501 stubs). Workspace-isolation matrix: cross-tenant probes return
// 404 (existential non-leak); no-session returns 401; vault errors
// surface as 500; happy paths return the spec'd response shape.
// ---------------------------------------------------------------------------

// ownedAccountFixture returns a synthetic account owned by ownerID —
// the template for the 4 happy-path tests below.
func ownedAccountFixture(ownerID int64, platform string) *models.PlatformAccount {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	return &models.PlatformAccount{
		ID: 21, UserID: ownerID, Platform: platform,
		PlatformUserID: "pf-21", Username: "alice_" + platform,
		Status:    models.AccountStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
}

// validTokenFuture returns a non-nil OAuthToken that the mock vault
// hands back for "token is valid" cases in handleValidateAccount tests.
func validTokenFuture() *models.OAuthToken {
	exp := time.Now().Add(time.Hour)
	return &models.OAuthToken{
		AccessToken: "valid-token",
		TokenType:   models.TokenTypeShortLived,
		ExpiresAt:   &exp,
	}
}

// ---------------------------------------------------------------------------
// Snapshot + AccountDetails/Content tests
// ---------------------------------------------------------------------------

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
	contentFn func(ctx context.Context, accessToken, platformUserID string, cursor string, limit int) (*models.AccountContentPage, error)
}

func (m *mockDetailProvider) GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	if m.detailsFn != nil {
		return m.detailsFn(ctx, accessToken, platformUserID)
	}
	return nil, fmt.Errorf("GetAccountDetails not implemented")
}
func (m *mockDetailProvider) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int) (*models.AccountContentPage, error) {
	if m.contentFn != nil {
		return m.contentFn(ctx, accessToken, platformUserID, cursor, limit)
	}
	return nil, fmt.Errorf("ListAccountContent not implemented")
}

// TestAccountContent_Paginates proves that cursor and limit query params
// are forwarded to the provider and the next_cursor is returned to the client.
func TestAccountContent_Paginates(t *testing.T) {
	var gotCursor string
	var gotLimit int
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
		contentFn: func(ctx context.Context, accessToken, platformUserID string, cursor string, limit int) (*models.AccountContentPage, error) {
			gotCursor = cursor
			gotLimit = limit
			return &models.AccountContentPage{
				Items: []models.AccountContentItem{
					{ExternalID: "vid1", Title: "Video One"},
					{ExternalID: "vid2", Title: "Video Two"},
				},
				NextCursor: "page-2-token",
			}, nil
		},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token"}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21/content?cursor=page-1-token&limit=5", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("content: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotCursor != "page-1-token" {
		t.Errorf("cursor forwarded: want page-1-token, got %q", gotCursor)
	}
	if gotLimit != 5 {
		t.Errorf("limit forwarded: want 5, got %d", gotLimit)
	}

	var resp struct {
		Items []struct {
			ExternalID string `json:"external_id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode content response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: want 2, got %d", len(resp.Items))
	}
	if resp.Items[0].ExternalID != "vid1" {
		t.Errorf("first item: want vid1, got %q", resp.Items[0].ExternalID)
	}
	if resp.NextCursor != "page-2-token" {
		t.Errorf("next_cursor: want page-2-token, got %q", resp.NextCursor)
	}
}
