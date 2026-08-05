package api

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// youtubeReconnectHint is the R7 decision input for the YouTube login
// handler: whether a channel-pinned reconnect must force prompt=consent,
// and — when the channel is healthy — which pool client already issued
// its grant (so the reconnect stays on that client instead of selecting
// a new one).
//
//	consentNeeded     true → force prompt=consent (Google re-shows the
//	                          consent screen and mints a NEW refresh
//	                          token; only necessary for new/unhealthy
//	                          grants)
//	existingClientKey ""   → no reusable healthy grant
//	                  "youtube_pool_a|b" → the healthy grant's pool
//	                          client key; the login MUST reuse it
//	                          (grant reuse returns the SAME refresh
//	                          token — exchanging with a different
//	                          client would mint a new grant and burn
//	                          another slot against the 100-token cap)
//	providerSubjectID ""   → the Google subject is unknown (brand-new
//	                          channel with no grant lineage)
//	                  otherwise → the subject of the channel's existing
//	                          grant, when reachable. The login passes it
//	                          to SelectForNewConnection so an unhealthy
//	                          reconnect uses the capacity-aware
//	                          least-loaded pool selection instead of the
//	                          deterministic first-client fallback.
type youtubeReconnectHint struct {
	consentNeeded     bool
	existingClientKey string
	providerSubjectID string
}

// youtubeReconnectHintFor classifies a channel-pinned YouTube reconnect
// as consent-necessary or healthy. Consent is forced when ANY of:
//
//   - the repository cannot be consulted (nil store) or the user is
//     unknown (fail towards consent — the safe default);
//   - no platform account row exists for the channel yet (brand-new
//     grant: a previously-cached consent for ANOTHER channel would
//     skip Google's Brand-Account selection and bind the grant to the
//     wrong channel, so the consent screen must re-show);
//   - the account is not 'active' (reauth_required / pending /
//     error / …);
//   - the account has no grant lineage, the grant row is missing, or
//     the grant is not 'active';
//   - the grant lacks the youtube.force-ssl scope the consent URL
//     requests (scope-missing detection: without consent, Google
//     reuses the cached approval and never grants the new scope).
//
// Only a fully healthy row (active account + active grant + complete
// scopes) returns consentNeeded=false and the grant's pool client key.
// Everything else fails towards consent: an unnecessary consent screen
// costs one refresh token, while a MISSED consent screen on a broken
// grant leaves the channel dead — the asymmetry favours consent.
func (r *Router) youtubeReconnectHintFor(ctx context.Context, userID int64, expectedChannelID string) youtubeReconnectHint {
	if r.userRepo == nil || userID <= 0 {
		return youtubeReconnectHint{consentNeeded: true}
	}
	accounts, err := r.userRepo.ListPlatformAccountsByUser(userID, models.PlatformYouTube)
	if err != nil || len(accounts) == 0 {
		return youtubeReconnectHint{consentNeeded: true}
	}
	for _, acc := range accounts {
		if acc.PlatformUserID != expectedChannelID {
			continue
		}
		// Resolve the grant whenever the account has a lineage. Its
		// provider_subject_id makes the reconnect capacity-aware even
		// when the grant is NOT healthy (reauth_required, missing
		// scope): the login can then pass the known subject to
		// SelectForNewConnection and pick the least-loaded pool for
		// THAT Google account instead of the deterministic first
		// client. Fail towards consent (and no subject) when the
		// lineage is missing or the grant cannot be read.
		if acc.OAuthConnectionID == nil || *acc.OAuthConnectionID <= 0 {
			return youtubeReconnectHint{consentNeeded: true}
		}
		grant, err := r.userRepo.FindOAuthConnectionByID(ctx, *acc.OAuthConnectionID)
		if err != nil || grant == nil {
			return youtubeReconnectHint{consentNeeded: true}
		}
		hint := youtubeReconnectHint{consentNeeded: true, providerSubjectID: grant.ProviderSubjectID}
		if acc.Status != models.AccountStatusActive {
			return hint
		}
		if grant.Status != models.AccountStatusActive {
			return hint
		}
		if !grantHasForceSSLScope(grant.GrantedScopes) {
			return hint
		}
		hint.consentNeeded = false
		hint.existingClientKey = grant.OAuthClientKey
		return hint
	}
	return youtubeReconnectHint{consentNeeded: true}
}

// grantHasForceSSLScope reports whether the grant carries the canonical
// force-ssl scope the consent URL requests (services.YouTubeForceSSLScope,
// the same constant the credential resolver requires before publish). The
// scope check is intentionally a single well-known scope: the three
// youtube.* scopes are always granted together, and force-ssl is the one
// the resolver treats as the canonical completeness signal.
func grantHasForceSSLScope(scopes []string) bool {
	for _, s := range scopes {
		if s == services.YouTubeForceSSLScope {
			return true
		}
	}
	return false
}
