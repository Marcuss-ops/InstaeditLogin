package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeForceSSLScope is the canonical scope required for YouTube Live
// operations and metadata writes. Keep this as the single resolver-side
// scope constant; short aliases are deliberately not accepted here.
const YouTubeForceSSLScope = "https://www.googleapis.com/auth/youtube.force-ssl"

var (
	// These sentinels classify resolver failures without exposing account,
	// grant, provider, or credential material in an API error or log line.
	ErrYouTubeCredentialInvalidRequest = errors.New("invalid YouTube credential request")
	ErrYouTubeCredentialWorkspace      = errors.New("YouTube credential workspace access denied")
	ErrYouTubeCredentialAccount        = errors.New("YouTube platform account unavailable")
	ErrYouTubeCredentialGrant          = errors.New("YouTube OAuth grant unavailable")
	ErrYouTubeCredentialScope          = errors.New("YouTube OAuth grant lacks youtube.force-ssl scope")
	ErrYouTubeCredentialToken          = errors.New("YouTube access token unavailable")
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
	Binder      YouTubeChannelBinder
	Logger      *slog.Logger
}

// YouTubeCredentialResolver verifies tenant ownership, account/grant
// readiness, scope, token freshness, and channel binding before returning a
// runtime-only access token. It never calls Vault.Save/Rotate and never writes
// the returned token itself.
type YouTubeCredentialResolver struct {
	deps YouTubeCredentialResolverDeps
}

func NewYouTubeCredentialResolver(deps YouTubeCredentialResolverDeps) *YouTubeCredentialResolver {
	return &YouTubeCredentialResolver{deps: deps}
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
	if r.deps.Accounts == nil || r.deps.Workspaces == nil || r.deps.Memberships == nil ||
		r.deps.Grants == nil || r.deps.Vault == nil || r.deps.OAuth == nil || r.deps.Binder == nil {
		return nil, fmt.Errorf("%w: resolver dependencies are incomplete", ErrYouTubeCredentialInvalidRequest)
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

	if account.OAuthConnectionID == nil || *account.OAuthConnectionID <= 0 {
		return nil, ErrYouTubeCredentialGrant
	}
	grant, err := r.deps.Grants.FindOAuthConnectionByID(ctx, *account.OAuthConnectionID)
	if err != nil {
		return nil, fmt.Errorf("%w: grant lookup failed", ErrYouTubeCredentialGrant)
	}
	if grant == nil || grant.ID != *account.OAuthConnectionID || grant.UserID != account.UserID ||
		grant.Provider != models.PlatformYouTube || grant.Status != models.AccountStatusActive {
		return nil, ErrYouTubeCredentialGrant
	}
	if !containsYouTubeForceSSL(grant.GrantedScopes) {
		return nil, ErrYouTubeCredentialScope
	}

	// RenewYouTubeToken uses the canonical bearer row and the temporary
	// legacy fallback already implemented by credentials. The refresher
	// receives decrypted material only in this call; the resolver never
	// receives or persists a refresh token.
	logger := r.deps.Logger
	token, err := credentials.RenewYouTubeToken(ctx, r.deps.Vault, account.ID, r.deps.OAuth.RefreshOAuthToken, logger)
	if err != nil {
		// Keep both the resolver classification and the underlying typed
		// renewal classification (invalid grant vs transient renewal).
		return nil, fmt.Errorf("%w: renewal failed: %w", ErrYouTubeCredentialToken, err)
	}
	if token == nil || token.AccessToken == "" || !containsYouTubeForceSSL(token.Scopes) {
		return nil, ErrYouTubeCredentialScope
	}

	if err := VerifyChannelIdentity(ctx, r.deps.Binder, token.AccessToken, account.PlatformUserID); err != nil {
		// Preserve both classifications: callers can detect channel drift
		// with errors.Is(ErrYouTubeChannelMismatch), while the resolver's
		// own category remains available for generic credential handling.
		return nil, fmt.Errorf("%w: %w", ErrYouTubeCredentialBinding, err)
	}

	return &YouTubeResolvedCredential{
		PlatformAccountID: account.ID,
		ChannelID:         account.PlatformUserID,
		Token:             token,
	}, nil
}

func containsYouTubeForceSSL(scopes []string) bool {
	for _, scope := range scopes {
		if scope == YouTubeForceSSLScope {
			return true
		}
	}
	return false
}
