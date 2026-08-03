package services

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type resolverAccountStore struct {
	account *models.PlatformAccount
	err     error
}

func (s resolverAccountStore) FindPlatformAccountByID(int64) (*models.PlatformAccount, error) {
	return s.account, s.err
}

type resolverWorkspaceStore struct {
	binding *models.WorkspaceChannel
	err     error
}

func (s resolverWorkspaceStore) FindChannel(context.Context, int64, int64) (*models.WorkspaceChannel, error) {
	return s.binding, s.err
}

type resolverMembershipStore struct {
	role string
	err  error
}

func (s resolverMembershipStore) GetRole(int64, int64) (string, error) {
	return s.role, s.err
}

type resolverGrantStore struct {
	grant *models.OAuthConnection
	err   error
}

func (s resolverGrantStore) FindOAuthConnectionByID(context.Context, int64) (*models.OAuthConnection, error) {
	return s.grant, s.err
}

type resolverOAuthProvider struct {
	calls int
	data  *models.TokenData
	err   error
}

func (p *resolverOAuthProvider) RefreshOAuthToken(context.Context, string) (*models.TokenData, error) {
	p.calls++
	return p.data, p.err
}

type resolverBinder struct {
	calls int
	err   error
}

func (b *resolverBinder) Name() string { return models.PlatformYouTube }

func (b *resolverBinder) ValidateChannelBinding(context.Context, string, string) error {
	b.calls++
	return b.err
}

type resolverVault struct {
	renewCalls      int
	saveCalls       int
	rotateCalls     int
	token           *models.OAuthToken
	err             error
	invokeRefresher bool
}

func (v *resolverVault) Save(context.Context, int64, *models.TokenData) error {
	v.saveCalls++
	return nil
}
func (v *resolverVault) Get(context.Context, int64, string) (*models.OAuthToken, error) {
	return v.token, nil
}
func (v *resolverVault) Rotate(context.Context, int64, *models.TokenData) error {
	v.rotateCalls++
	return nil
}
func (v *resolverVault) Renew(ctx context.Context, _ int64, _ string, refresher credentials.TokenRefresher) (*models.OAuthToken, error) {
	v.renewCalls++
	if v.invokeRefresher {
		data, err := refresher(ctx, "refresh-material-only-in-memory")
		if err != nil {
			return nil, err
		}
		return &models.OAuthToken{AccessToken: data.AccessToken, TokenType: data.TokenType, Scopes: data.Scopes}, nil
	}
	return v.token, v.err
}
func (v *resolverVault) Revoke(context.Context, int64) error { return nil }

func newResolverFixture() YouTubeCredentialResolverDeps {
	connectionID := int64(700)
	return YouTubeCredentialResolverDeps{
		Accounts: resolverAccountStore{account: &models.PlatformAccount{
			ID: 42, UserID: 9, Platform: models.PlatformYouTube,
			PlatformUserID: "UC-test-channel", Status: models.AccountStatusActive,
			OAuthConnectionID: &connectionID,
		}},
		Workspaces: resolverWorkspaceStore{binding: &models.WorkspaceChannel{
			WorkspaceID: 11, PlatformAccountID: 42, Enabled: true,
		}},
		Memberships: resolverMembershipStore{role: "editor"},
		Grants: resolverGrantStore{grant: &models.OAuthConnection{
			ID: connectionID, UserID: 9, Provider: models.PlatformYouTube,
			Status: models.AccountStatusActive, GrantedScopes: []string{YouTubeForceSSLScope},
		}},
		Vault:  &resolverVault{token: &models.OAuthToken{AccessToken: "runtime-secret", TokenType: models.TokenTypeBearer, Scopes: []string{YouTubeForceSSLScope}}},
		OAuth:  &resolverOAuthProvider{data: &models.TokenData{AccessToken: "refreshed-runtime-secret", TokenType: models.TokenTypeBearer, Scopes: []string{YouTubeForceSSLScope}}},
		Binder: &resolverBinder{},
	}
}

func TestYouTubeCredentialResolver_ResolvesOnlyAfterAllGuards(t *testing.T) {
	deps := newResolverFixture()
	vault := deps.Vault.(*resolverVault)
	binder := deps.Binder.(*resolverBinder)

	got, err := NewYouTubeCredentialResolver(deps).Resolve(context.Background(), YouTubeCredentialRequest{UserID: 9, WorkspaceID: 11, PlatformAccountID: 42})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.Token == nil || got.Token.AccessToken != "runtime-secret" {
		t.Fatalf("resolved credential: %#v", got)
	}
	if got.ChannelID != "UC-test-channel" {
		t.Errorf("channel id: want UC-test-channel, got %q", got.ChannelID)
	}
	if vault.renewCalls != 1 || binder.calls != 1 {
		t.Errorf("calls: renew=%d binder=%d; want renew=1 binder=1", vault.renewCalls, binder.calls)
	}
	if vault.saveCalls != 0 || vault.rotateCalls != 0 {
		t.Errorf("resolver must not persist credentials: save=%d rotate=%d", vault.saveCalls, vault.rotateCalls)
	}
}

func TestYouTubeCredentialResolver_RenewUsesProviderInMemoryAndPreservesErrors(t *testing.T) {
	deps := newResolverFixture()
	vault := deps.Vault.(*resolverVault)
	oauth := deps.OAuth.(*resolverOAuthProvider)
	vault.invokeRefresher = true

	got, err := NewYouTubeCredentialResolver(deps).Resolve(context.Background(), YouTubeCredentialRequest{UserID: 9, WorkspaceID: 11, PlatformAccountID: 42})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Token.AccessToken != "refreshed-runtime-secret" || oauth.calls != 1 {
		t.Errorf("refreshed token/calls: token=%q calls=%d", got.Token.AccessToken, oauth.calls)
	}
	if vault.saveCalls != 0 || vault.rotateCalls != 0 {
		t.Errorf("renew path must not call resolver persistence: save=%d rotate=%d", vault.saveCalls, vault.rotateCalls)
	}

	deps = newResolverFixture()
	vault = deps.Vault.(*resolverVault)
	vault.err = credentials.ErrInvalidGrant
	_, err = NewYouTubeCredentialResolver(deps).Resolve(context.Background(), YouTubeCredentialRequest{UserID: 9, WorkspaceID: 11, PlatformAccountID: 42})
	if !errors.Is(err, ErrYouTubeCredentialToken) || !errors.Is(err, credentials.ErrYouTubeInvalidGrant) {
		t.Fatalf("renew error must preserve typed classifications, got %v", err)
	}
}

func TestYouTubeCredentialResolver_RejectsCrossUserAndDisabledBinding(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*YouTubeCredentialResolverDeps)
		want   error
	}{
		{
			name: "cross user",
			mutate: func(d *YouTubeCredentialResolverDeps) {
				d.Accounts = resolverAccountStore{account: &models.PlatformAccount{ID: 42, UserID: 88, Platform: models.PlatformYouTube, Status: models.AccountStatusActive}}
			}, want: ErrYouTubeCredentialAccount,
		},
		{
			name: "disabled workspace binding",
			mutate: func(d *YouTubeCredentialResolverDeps) {
				d.Workspaces = resolverWorkspaceStore{binding: &models.WorkspaceChannel{WorkspaceID: 11, PlatformAccountID: 42, Enabled: false}}
			}, want: ErrYouTubeCredentialWorkspace,
		},
		{
			name: "missing force ssl",
			mutate: func(d *YouTubeCredentialResolverDeps) {
				d.Grants = resolverGrantStore{grant: &models.OAuthConnection{ID: 700, UserID: 9, Provider: models.PlatformYouTube, Status: models.AccountStatusActive, GrantedScopes: []string{"youtube.readonly"}}}
			}, want: ErrYouTubeCredentialScope,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := newResolverFixture()
			tc.mutate(&deps)
			_, err := NewYouTubeCredentialResolver(deps).Resolve(context.Background(), YouTubeCredentialRequest{UserID: 9, WorkspaceID: 11, PlatformAccountID: 42})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error: want errors.Is(%v), got %v", tc.want, err)
			}
		})
	}
}

func TestYouTubeCredentialResolver_RejectsChannelMismatchWithoutPersistingToken(t *testing.T) {
	deps := newResolverFixture()
	vault := deps.Vault.(*resolverVault)
	binder := deps.Binder.(*resolverBinder)
	binder.err = ErrYouTubeChannelMismatch

	_, err := NewYouTubeCredentialResolver(deps).Resolve(context.Background(), YouTubeCredentialRequest{UserID: 9, WorkspaceID: 11, PlatformAccountID: 42})
	if err == nil || !errors.Is(err, ErrYouTubeCredentialBinding) || !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Fatalf("binding error must preserve both typed classifications, got %v", err)
	}
	if vault.saveCalls != 0 || vault.rotateCalls != 0 {
		t.Errorf("binding failure must not persist token: save=%d rotate=%d", vault.saveCalls, vault.rotateCalls)
	}
}

func TestYouTubeCredentialResolver_RequiresAllDependencies(t *testing.T) {
	deps := newResolverFixture()
	deps.Binder = nil
	_, err := NewYouTubeCredentialResolver(deps).Resolve(context.Background(), YouTubeCredentialRequest{UserID: 9, WorkspaceID: 11, PlatformAccountID: 42})
	if !errors.Is(err, ErrYouTubeCredentialInvalidRequest) {
		t.Fatalf("missing binder: want invalid request, got %v", err)
	}
}
