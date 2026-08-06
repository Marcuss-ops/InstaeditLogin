package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeForceSSLScope is the canonical scope required for YouTube Live
// operations and metadata writes. Keep this as the single resolver-side
// scope constant; short aliases are deliberately not accepted here.
const YouTubeForceSSLScope = "https://www.googleapis.com/auth/youtube.force-ssl"

const (
	grantCacheTTL      = 60 * time.Second
	grantValidationTTL = 30 * time.Second
	grantRefreshGrace  = 60 * time.Second
)

var (
	// These sentinels classify resolver failures without exposing account,
	// grant, provider, or credential material in an API error or log line.
	ErrYouTubeCredentialInvalidRequest = errors.New("invalid YouTube credential request")
	ErrYouTubeCredentialWorkspace      = errors.New("YouTube credential workspace access denied")
	ErrYouTubeCredentialAccount        = errors.New("YouTube platform account unavailable")
	ErrYouTubeCredentialGrant          = errors.New("YouTube OAuth grant unavailable")
	ErrYouTubeCredentialScope          = errors.New("YouTube OAuth grant lacks youtube.force-ssl scope")
	ErrYouTubeCredentialToken          = errors.New("YouTube access token unavailable")
	ErrYouTubeCredentialTokenInfo      = errors.New("YouTube access token validation unavailable")
	ErrYouTubeCredentialAudience       = errors.New("YouTube OAuth audience mismatch")
	ErrYouTubeCredentialBinding        = errors.New("YouTube channel binding unavailable")
)

// YouTubeCredentialRequest identifies the tenant and channel whose grant
// should be resolved. UserID is the authenticated application user, not a
// provider-side identity.
type YouTubeCredentialRequest struct {
	UserID            int64
	WorkspaceID       int64
	PlatformAccountID int64
}

// YouTubeResolvedCredential contains runtime-only OAuth material. Callers
// must keep it in memory and must never marshal, persist, or log AccessToken.
type YouTubeResolvedCredential struct {
	PlatformAccountID int64
	ChannelID         string
	Token             *models.OAuthToken
}

// YouTubeGrantValidation is the shared, grant-scoped result of refresh and
// provider token introspection. Channel binding is deliberately not included:
// one oauth_connection_id may serve several channels, so binding is cached
// separately per (oauth_connection_id, platform channel).
type YouTubeGrantValidation struct {
	Token *models.OAuthToken
	Info  *YouTubeTokenInfo
}

// YouTubeCredentialAccountStore is the existing platform-account lookup
// surface. *repository.UserRepository satisfies it.
type YouTubeCredentialAccountStore interface {
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
}

// YouTubeCredentialWorkspaceStore exposes the existing workspace binding
// repository. Membership is kept as a separate narrow interface so the
// resolver verifies both the channel binding and caller membership.
type YouTubeCredentialWorkspaceStore interface {
	FindChannel(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error)
}

type YouTubeCredentialMembershipStore interface {
	GetRole(workspaceID, userID int64) (string, error)
}

// YouTubeCredentialGrantStore loads the OAuth grant lineage attached to the
// platform account. It is intentionally read-only: the Vault owns token
// persistence and renewal.
type YouTubeCredentialGrantStore interface {
	FindOAuthConnectionByID(ctx context.Context, id int64) (*models.OAuthConnection, error)
}

// YouTubeCredentialOAuthProvider is the refresh-only capability required by
// the resolver. The concrete YouTubeOAuthService satisfies this interface;
// keeping it narrower than OAuthProvider prevents the resolver from gaining
// login/callback responsibilities and makes the credential boundary obvious.
type YouTubeCredentialOAuthProvider interface {
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
}

// YouTubeCredentialTokenValidator is optional for the general resolver and
// required by ValidateAccount. It lets all channels sharing a grant reuse one
// tokeninfo/audience/scope validation result.
type YouTubeCredentialTokenValidator interface {
	GetTokenInfo(ctx context.Context, accessToken string) (*YouTubeTokenInfo, error)
	ClientID() string
}

// YouTubeCredentialResolverDeps are the only collaborators needed by the
// resolver. The OAuth provider is used solely as the in-memory refresher;
// Vault.RenewYouTubeToken remains the credential-storage boundary.
type YouTubeCredentialResolverDeps struct {
	Accounts    YouTubeCredentialAccountStore
	Workspaces  YouTubeCredentialWorkspaceStore
	Memberships YouTubeCredentialMembershipStore
	Grants      YouTubeCredentialGrantStore
	Vault       credentials.VaultAPI
	OAuth       YouTubeCredentialOAuthProvider
	TokenInfo   YouTubeCredentialTokenValidator
	Binder      YouTubeChannelBinder
	Logger      *slog.Logger
	Clock       func() time.Time
}

type cachedOAuthGrant struct {
	grant     *models.OAuthConnection
	expiresAt time.Time
}

type cachedGrantValidation struct {
	validation *YouTubeGrantValidation
	expiresAt  time.Time
}

// YouTubeCredentialResolver verifies tenant ownership, account/grant
// readiness, scope, token freshness, and channel binding before returning a
// runtime-only access token. Successful grant validation is cached by
// oauth_connection_id and concurrent refresh/tokeninfo calls for a shared
// grant are collapsed into one operation.
type YouTubeCredentialResolver struct {
	deps YouTubeCredentialResolverDeps

	cacheMu      sync.RWMutex
	grantCache   map[int64]cachedOAuthGrant
	validation   map[int64]cachedGrantValidation
	bindingCache map[string]time.Time
	flight       singleflight.Group
}

func NewYouTubeCredentialResolver(deps YouTubeCredentialResolverDeps) *YouTubeCredentialResolver {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	return &YouTubeCredentialResolver{
		deps:         deps,
		grantCache:   make(map[int64]cachedOAuthGrant),
		validation:   make(map[int64]cachedGrantValidation),
		bindingCache: make(map[string]time.Time),
	}
}

// Resolve performs all checks before returning a usable YouTube credential.
// Cross-workspace and cross-user resources intentionally collapse into the
// same typed access-denied classifications so callers cannot enumerate tenant
// resources through error differences.
func (r *YouTubeCredentialResolver) Resolve(ctx context.Context, req YouTubeCredentialRequest) (*YouTubeResolvedCredential, error) {
	if r == nil || req.UserID <= 0 || req.WorkspaceID <= 0 || req.PlatformAccountID <= 0 {
		return nil, ErrYouTubeCredentialInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.validateDependencies(false); err != nil {
		return nil, err
	}

	account, err := r.deps.Accounts.FindPlatformAccountByID(req.PlatformAccountID)
	if err != nil {
		return nil, fmt.Errorf("%w: account lookup failed", ErrYouTubeCredentialAccount)
	}
	if account == nil || account.Platform != models.PlatformYouTube || account.UserID != req.UserID ||
		account.Status != models.AccountStatusActive {
		return nil, ErrYouTubeCredentialAccount
	}

	binding, err := r.deps.Workspaces.FindChannel(ctx, req.WorkspaceID, req.PlatformAccountID)
	if err != nil {
		return nil, fmt.Errorf("%w: workspace binding lookup failed", ErrYouTubeCredentialWorkspace)
	}
	if binding == nil || !binding.Enabled {
		return nil, ErrYouTubeCredentialWorkspace
	}
	role, err := r.deps.Memberships.GetRole(req.WorkspaceID, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("%w: workspace membership lookup failed", ErrYouTubeCredentialWorkspace)
	}
	if role == "" {
		return nil, ErrYouTubeCredentialWorkspace
	}

	grant, err := r.grantForAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	validation, err := r.sharedGrantValidation(ctx, account.ID, grant, false)
	if err != nil {
		return nil, err
	}
	if err := r.validateChannel(ctx, grant.ID, account.PlatformUserID, validation.Token.AccessToken); err != nil {
		return nil, err
	}

	return &YouTubeResolvedCredential{
		PlatformAccountID: account.ID,
		ChannelID:         account.PlatformUserID,
		Token:             validation.Token,
	}, nil
}

// ValidateAccount is the account-validation path used by the HTTP readiness
// endpoint. It intentionally skips workspace membership because that handler
// has already authenticated ownership; it still resolves the canonical grant,
// refreshes it once per oauth_connection_id, and introspects the resulting
// access token once for all linked channels.
func (r *YouTubeCredentialResolver) ValidateAccount(ctx context.Context, account *models.PlatformAccount) (*YouTubeGrantValidation, error) {
	if r == nil || account == nil || account.ID <= 0 || account.Platform != models.PlatformYouTube {
		return nil, ErrYouTubeCredentialAccount
	}
	if err := r.validateDependencies(true); err != nil {
		return nil, err
	}
	grant, err := r.grantForAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	return r.sharedGrantValidation(ctx, account.ID, grant, true)
}

// InvalidateGrant drops cached grant/token validation and channel bindings.
// Call this after an explicit disconnect or a confirmed provider revocation.
func (r *YouTubeCredentialResolver) InvalidateGrant(oauthConnectionID int64) {
	if r == nil || oauthConnectionID <= 0 {
		return
	}
	prefix := fmt.Sprintf("%d:", oauthConnectionID)
	r.cacheMu.Lock()
	delete(r.grantCache, oauthConnectionID)
	delete(r.validation, oauthConnectionID)
	for key := range r.bindingCache {
		if strings.HasPrefix(key, prefix) {
			delete(r.bindingCache, key)
		}
	}
	r.cacheMu.Unlock()
}

func (r *YouTubeCredentialResolver) validateDependencies(requireTokenInfo bool) error {
	if r.deps.Accounts == nil || r.deps.Grants == nil || r.deps.Vault == nil || r.deps.OAuth == nil || r.deps.Binder == nil {
		return fmt.Errorf("%w: resolver dependencies are incomplete", ErrYouTubeCredentialInvalidRequest)
	}
	if requireTokenInfo && r.deps.TokenInfo == nil {
		return fmt.Errorf("%w: token validator is not configured", ErrYouTubeCredentialInvalidRequest)
	}
	return nil
}

func (r *YouTubeCredentialResolver) grantForAccount(ctx context.Context, account *models.PlatformAccount) (*models.OAuthConnection, error) {
	if account.OAuthConnectionID == nil || *account.OAuthConnectionID <= 0 {
		return nil, ErrYouTubeCredentialGrant
	}
	oid := *account.OAuthConnectionID
	now := r.deps.Clock()
	r.cacheMu.RLock()
	cached, ok := r.grantCache[oid]
	r.cacheMu.RUnlock()
	if ok && now.Before(cached.expiresAt) {
		return cached.grant, r.validateGrantOwnership(account, cached.grant)
	}

	value, err, _ := r.flight.Do(fmt.Sprintf("grant-row:%d", oid), func() (any, error) {
		now := r.deps.Clock()
		r.cacheMu.RLock()
		cached, ok := r.grantCache[oid]
		r.cacheMu.RUnlock()
		if ok && now.Before(cached.expiresAt) {
			return cached.grant, nil
		}
		grant, err := r.deps.Grants.FindOAuthConnectionByID(ctx, oid)
		if err != nil {
			return nil, fmt.Errorf("%w: grant lookup failed", ErrYouTubeCredentialGrant)
		}
		if err := r.validateGrantOwnership(account, grant); err != nil {
			return nil, err
		}
		r.cacheMu.Lock()
		r.grantCache[oid] = cachedOAuthGrant{grant: grant, expiresAt: now.Add(grantCacheTTL)}
		r.cacheMu.Unlock()
		return grant, nil
	})
	if err != nil {
		return nil, err
	}
	grant, ok := value.(*models.OAuthConnection)
	if !ok || grant == nil {
		return nil, ErrYouTubeCredentialGrant
	}
	return grant, nil
}

func (r *YouTubeCredentialResolver) validateGrantOwnership(account *models.PlatformAccount, grant *models.OAuthConnection) error {
	if grant == nil || account.OAuthConnectionID == nil || grant.ID != *account.OAuthConnectionID ||
		grant.UserID != account.UserID || grant.Provider != models.PlatformYouTube ||
		grant.Status != models.AccountStatusActive {
		return ErrYouTubeCredentialGrant
	}
	if !containsYouTubeForceSSL(grant.GrantedScopes) {
		return ErrYouTubeCredentialScope
	}
	return nil
}

func (r *YouTubeCredentialResolver) sharedGrantValidation(ctx context.Context, accountID int64, grant *models.OAuthConnection, introspect bool) (*YouTubeGrantValidation, error) {
	oid := grant.ID
	now := r.deps.Clock()
	if cached, ok := r.cachedValidation(oid, now); ok {
		return cached, nil
	}

	value, err, _ := r.flight.Do(fmt.Sprintf("grant-validate:%d", oid), func() (any, error) {
		now := r.deps.Clock()
		if cached, ok := r.cachedValidation(oid, now); ok {
			return cached, nil
		}
		token, err := credentials.RenewYouTubeToken(ctx, r.deps.Vault, accountID, r.deps.OAuth.RefreshOAuthToken, r.deps.Logger)
		if err != nil {
			return nil, fmt.Errorf("%w: renewal failed: %w", ErrYouTubeCredentialToken, err)
		}
		if token == nil || token.AccessToken == "" || !containsYouTubeForceSSL(token.Scopes) {
			return nil, ErrYouTubeCredentialScope
		}

		var info *YouTubeTokenInfo
		if introspect {
			info, err = r.deps.TokenInfo.GetTokenInfo(ctx, token.AccessToken)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrYouTubeCredentialTokenInfo, err)
			}
			if info == nil {
				return nil, ErrYouTubeCredentialTokenInfo
			}
			if info.Aud != r.deps.TokenInfo.ClientID() {
				return nil, fmt.Errorf("%w: tokeninfo.aud=%q cfg.Auth.YouTubeClientID=%q", ErrYouTubeCredentialAudience, info.Aud, r.deps.TokenInfo.ClientID())
			}
			if !info.HasUpload || !info.HasReadonly || !info.HasForceSSL {
				return nil, fmt.Errorf("%w: HasUpload=%v HasReadonly=%v HasForceSSL=%v scope=%q", ErrYouTubeCredentialScope, info.HasUpload, info.HasReadonly, info.HasForceSSL, info.Scope)
			}
		}

		validation := &YouTubeGrantValidation{Token: token, Info: info}
		validUntil := now.Add(grantValidationTTL)
		if token.ExpiresAt != nil {
			if expiry := token.ExpiresAt.Add(-grantRefreshGrace); expiry.Before(validUntil) {
				validUntil = expiry
			}
		}
		if !validUntil.After(now) {
			return nil, ErrYouTubeCredentialToken
		}
		r.cacheMu.Lock()
		r.validation[oid] = cachedGrantValidation{validation: validation, expiresAt: validUntil}
		r.cacheMu.Unlock()
		return validation, nil
	})
	if err != nil {
		return nil, err
	}
	validation, ok := value.(*YouTubeGrantValidation)
	if !ok || validation == nil {
		return nil, ErrYouTubeCredentialToken
	}
	return validation, nil
}

func (r *YouTubeCredentialResolver) cachedValidation(oid int64, now time.Time) (*YouTubeGrantValidation, bool) {
	r.cacheMu.RLock()
	cached, ok := r.validation[oid]
	r.cacheMu.RUnlock()
	if !ok || !now.Before(cached.expiresAt) {
		return nil, false
	}
	return cached.validation, true
}

// ValidateChannelBinding reuses the grant-scoped refresh result and
// singleflights the channel-specific binding check. It is exported for
// account validation handlers that already performed tenant ownership.
func (r *YouTubeCredentialResolver) ValidateChannelBinding(ctx context.Context, account *models.PlatformAccount, accessToken string) error {
	if r == nil || account == nil || account.OAuthConnectionID == nil || *account.OAuthConnectionID <= 0 || account.PlatformUserID == "" {
		return ErrYouTubeCredentialInvalidRequest
	}
	if r.deps.Binder == nil {
		return fmt.Errorf("%w: channel binder is not configured", ErrYouTubeCredentialInvalidRequest)
	}
	return r.validateChannel(ctx, *account.OAuthConnectionID, account.PlatformUserID, accessToken)
}

func (r *YouTubeCredentialResolver) validateChannel(ctx context.Context, oid int64, channelID, accessToken string) error {
	key := fmt.Sprintf("%d:%s", oid, channelID)
	now := r.deps.Clock()
	r.cacheMu.RLock()
	valid := r.bindingCache[key]
	r.cacheMu.RUnlock()
	if now.Before(valid) {
		return nil
	}
	_, err, _ := r.flight.Do("channel-bind:"+key, func() (any, error) {
		now := r.deps.Clock()
		r.cacheMu.RLock()
		valid := r.bindingCache[key]
		r.cacheMu.RUnlock()
		if now.Before(valid) {
			return nil, nil
		}
		if err := VerifyChannelIdentity(ctx, r.deps.Binder, accessToken, channelID); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrYouTubeCredentialBinding, err)
		}
		r.cacheMu.Lock()
		r.bindingCache[key] = now.Add(grantValidationTTL)
		r.cacheMu.Unlock()
		return nil, nil
	})
	return err
}

func containsYouTubeForceSSL(scopes []string) bool {
	for _, scope := range scopes {
		if scope == YouTubeForceSSLScope {
			return true
		}
	}
	return false
}
