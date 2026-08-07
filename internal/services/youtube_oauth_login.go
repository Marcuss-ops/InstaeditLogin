package services

import (
	"net/url"
	"strings"
)

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

// youtubeOAuthScopes (Bug #3 fix) is the SINGLE SOURCE OF TRUTH for the
// consent-screen scope list requested by every YouTube OAuth redirect.
//
// Least-privilege rationale (each scope earns its keep):
//
//	youtube.upload     — videos.insert (chunked-PUT resumable upload).
//	                     The single scope strictly required to push
//	                     bytes to YouTube.
//	youtube.readonly   — channels.list?mine=true (channel binding guard
//	                     in ValidateChannelBinding) + videos.list
//	                     (processing-status poll). Read-only; no
//	                     quotable writes.
//	youtube.force-ssl  — thumbnails.set (custom thumbnail upload) +
//	                     videos.update (privacyStatus + publishAt +
//	                     snippet title/description). youtube.upload
//	                     alone does NOT grant write access to video
//	                     metadata, so both thumbnail and publish
//	                     rows REQUIRE this scope.
//	openid + email + profile — OIDC identity (user id, email, name).
//
// We deliberately do NOT request any of the YouTube Analytics scopes
// (yt-analytics-monetary.readonly, yt-analytics.readonly). The publish
// pipeline (upload, thumbnail, schedule, publish) does not consume
// earnings / RPM / CPM / impressions data; requesting them would (a)
// trigger a Google brand-verification re-review under the OAuth
// verification policy without delivering any functional gain, and
// (b) widen the consent screen to a "Sensitive scope" tier that
// requires justification under Google's 2024 OAuth verification
// classification. Re-introduction of an Analytics scope to this
// constant is treated as a blocking change.
//
// Mirrors:
//   - cmd/oauth-scope-canary/main.go::canonicalScopes (operator canary
//     diffs Google's tokeninfo against this set every scheduled run)
//   - docs/OAUTH-PRODUCTION.md "Step 3 -- declare the scopes
//     (minimum set)" + "Code-side guard" (public-facing mirror)
//
// A PR that edits this constant without updating either the canary
// canonicalScopes OR the docs table should be rejected.
// youtubeOAuthScopes is defined in youtube_oauth_policy.go so consent,
// token introspection, and credential resolution share one policy source.

// GetLoginURLWithOptions builds the Google authorize URL using the
// legacy single-client config (cfg.Auth.YouTubeClientID/
// YouTubeRedirectURI). YouTube OAuth Client Pool deployments route
// through GetLoginURLWithPoolClient instead; this method keeps the
// pre-pool behaviour for callers that never wire a registry (admin
// connect-link, legacy tests).
func (s *YouTubeOAuthService) GetLoginURLWithOptions(state string, options OAuthLoginOptions) string {
	return s.GetLoginURLWithPoolClient(state, options, nil)
}

// GetLoginURLWithPoolClient builds the Google authorize URL for a pool
// client (YouTube OAuth Client Pool). The client's client_id and
// redirect_uri replace the legacy single-client config; every other
// parameter (canonical scope set, access_type=offline, prompt,
// login_hint) is identical to GetLoginURLWithOptions. A nil client
// falls back to the legacy single-client config — the handler only
// passes non-nil clients selected from the pool registry, so the
// consent URL is always built against the client that will later
// exchange the code.
func (s *YouTubeOAuthService) GetLoginURLWithPoolClient(state string, options OAuthLoginOptions, client *YouTubeOAuthClientConfig) string {
	clientID, redirectURI := s.cfg.Auth.YouTubeClientID, s.cfg.Auth.YouTubeRedirectURI
	if client != nil {
		clientID, redirectURI = client.ClientID, client.RedirectURI
	}
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	// Bug #3 fix: pass the canonical scope set (no yt-analytics
	// scopes) directly. See youtubeOAuthScopes above for the full
	// per-scope rationale.
	params.Set("scope", youtubeOAuthScopes)
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("include_granted_scopes", "true")

	if options.ForceConsent || options.SelectAccount {
		var prompts []string
		if options.SelectAccount {
			prompts = append(prompts, "select_account")
		}
		if options.ForceConsent {
			prompts = append(prompts, "consent")
		}
		params.Set("prompt", strings.Join(prompts, " "))
	}

	if options.LoginHint != "" {
		params.Set("login_hint", options.LoginHint)
	}

	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}
