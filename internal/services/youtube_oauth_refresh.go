package services

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"log/slog"
	"net/url"
)

// RefreshOAuthToken exchanges a YouTube refresh token for a new access
// token. The grant's oauth_client_key (stamped on ctx by
// CredentialVault.Renew) selects the exact pool client that issued it;
// a nil pool or empty key falls back to the legacy single client.
func (s *YouTubeOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformYouTube, &err)
	// Pool-scoped observability (youtube_oauth_refresh_total{oauth_client_key,
	// result}): label the attempt with the pool client that issued the grant
	// — the key CredentialVault.Renew stamped on ctx — so the operator can
	// compute per-client success/failure rates. Consistent with the
	// invalid_grant metric (also labelled by the grant's stored key). An
	// empty key (non-vault caller) is normalized to
	// legacy_single_client inside RecordYouTubeOAuthRefreshMetrics.
	defer RecordYouTubeOAuthRefreshMetrics(credentials.OAuthClientKeyFromContext(ctx), &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("youtube RefreshOAuthToken: empty refresh token")
	}

	// R4 — YouTube OAuth Client Pool: resolve the client from the
	// grant's oauth_client_key (stamped on ctx by vault.Renew) and
	// refresh with EXACTLY that client. Fail-closed: an unknown key is
	// an error — never fall back to a different client (refreshing a
	// pool A token with client B would surface as invalid_client /
	// invalid_grant from Google). A nil pool (legacy deployment) or
	// empty key (non-vault caller) falls back to the legacy single
	// client.
	client, err := s.poolClientForRefresh(ctx)
	if err != nil {
		return nil, err
	}

	slog.Info("YouTube: refreshing access token", "oauth_client_key", clientKeyForLog(client))
	body := url.Values{}
	if client != nil {
		body.Set("client_id", client.ClientID)
		body.Set("client_secret", client.ClientSecret)
	} else {
		body.Set("client_id", s.cfg.Auth.YouTubeClientID)
		body.Set("client_secret", s.cfg.Auth.YouTubeClientSecret)
	}
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	tr, err := s.postTokenRequest(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("youtube refresh: %w", err)
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}
	return &models.TokenData{
		AccessToken:           tr.AccessToken,
		RefreshToken:          refresh,
		TokenType:             models.TokenTypeBearer,
		ExpiresIn:             tr.ExpiresIn,
		RefreshTokenExpiresIn: tr.RefreshTokenExpiresIn,
		Scopes:                nonEmptyScopes(tr.Scope),
	}, nil
}

// poolClientForRefresh resolves the pool client that must refresh the
// grant, from the oauth_client_key CredentialVault.Renew stamped on
// ctx.
//
//	key empty       → (nil, nil): legacy single-client caller
//	pool nil        → (nil, nil): legacy deployment (no pool wired)
//	key resolvable  → that client (never a different one)
//	key unknown     → error, fail-closed (cross-pool refresh refused)
//
// Fail-closed semantics mean a legacy grant stamped with the migration
// default youtube_pool_a on a deployment that configured ONLY pool B
// will refuse to refresh (the key does not resolve). That is intended:
// operators must keep pool A as the legacy client's continuation when
// enabling the pool, otherwise old grants would be silently refreshed
// with a client that never issued them.
//
// Never returns a client different from the one that issued the grant.
func (s *YouTubeOAuthService) poolClientForRefresh(ctx context.Context) (*YouTubeOAuthClientConfig, error) {
	key := credentials.OAuthClientKeyFromContext(ctx)
	if key == "" || s.pool == nil {
		return nil, nil
	}
	client, err := s.pool.Resolve(key)
	if err != nil {
		// Fail-closed: refuse to refresh with any other client. The
		// error is redacted (registry errors never carry secrets).
		return nil, fmt.Errorf("youtube refresh: %w", err)
	}
	return client, nil
}
