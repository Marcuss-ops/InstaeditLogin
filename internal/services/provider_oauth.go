package services

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// OAuthLoginOptions carries optional per-login overrides. Providers that
// do not support a given option simply ignore it. The handler reads
// query parameters from /api/v1/auth/{provider}/login?mode=add|reconnect
// and translates them into these options.
type OAuthLoginOptions struct {
	// ForceConsent forces Google to show the consent screen again.
	// Used when reconnecting or adding another channel.
	ForceConsent bool

	// SelectAccount forces the Google account picker.
	// Used when adding another channel.
	SelectAccount bool

	// LoginHint suggests which account to pre-select (email or sub).
	LoginHint string
}

// OAuthProvider handles the OAuth login flow: build login URL, exchange the
// authorization code for a token, fetch the user profile, refresh the token
// when it expires. Every provider that supports user login implements this.
type OAuthProvider interface {
	NameProvider

	// GetLoginURL builds the OAuth authorization URL for user redirection.
	GetLoginURL(state string) string

	// GetLoginURLWithOptions builds the OAuth authorization URL with
	// per-login overrides (consent prompt, account selection, etc.).
	// Providers that do not support options should return the same
	// result as GetLoginURL(state), ignoring the options.
	GetLoginURLWithOptions(state string, options OAuthLoginOptions) string

	// HandleCallback processes the full OAuth callback flow:
	//  1. Exchange code for token
	//  2. Fetch user profile
	// Returns the profile and token data.
	HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error)

	// RefreshOAuthToken obtains a fresh access token from the platform.
	// For YouTube/Twitter/TikTok the argument is a refresh token; for Meta,
	// it is the current long-lived access token (re-exchange via fb_exchange_token).
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
}
