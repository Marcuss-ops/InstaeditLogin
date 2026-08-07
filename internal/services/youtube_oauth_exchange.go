package services

import (
	"context"
	"net/url"
)

func (s *YouTubeOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*youtubeTokenResponse, error) {
	return s.exchangeCodeForTokenWithClient(ctx, code, nil)
}

// exchangeCodeForTokenWithClient performs the authorization-code
// exchange against the given pool client. A nil client falls back to
// the legacy single-client config; a non-nil client supplies its own
// client_id / client_secret / redirect_uri (the redirect_uri must match
// the one registered for that client on the Google Cloud console).
func (s *YouTubeOAuthService) exchangeCodeForTokenWithClient(ctx context.Context, code string, client *YouTubeOAuthClientConfig) (*youtubeTokenResponse, error) {
	clientID, clientSecret, redirectURI := s.cfg.Auth.YouTubeClientID, s.cfg.Auth.YouTubeClientSecret, s.cfg.Auth.YouTubeRedirectURI
	if client != nil {
		clientID, clientSecret, redirectURI = client.ClientID, client.ClientSecret, client.RedirectURI
	}
	body := url.Values{}
	body.Set("client_id", clientID)
	body.Set("client_secret", clientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", redirectURI)

	return s.postTokenRequest(ctx, body)
}
