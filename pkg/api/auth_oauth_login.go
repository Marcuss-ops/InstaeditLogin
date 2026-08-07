package api

// OAuth login start and provider redirect.

import (
	"errors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
)

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	provider := models.NormalizePlatformIdentifier(req.PathValue("provider"))
	p, ok := r.capabilities.OAuth(provider)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}
	// Translate ?mode=add|reconnect into OAuthLoginOptions.
	// "add" forces account selection (Google account picker).
	// "reconnect" forces consent re-approval. R7 narrows this below:
	// a channel-pinned reconnect whose grant is healthy skips consent
	// (see the expected_channel_id block) so Google reuses the cached
	// grant instead of minting a new refresh token.
	mode := req.URL.Query().Get("mode")
	var options services.OAuthLoginOptions
	switch mode {
	case "add", "":
		// Account selection + consent re-approval for an UNPINNED add
		// (the Linking page's plain "Connect" button sends no mode).
		// Google caches consent per (client_id, scopes): a channel that
		// was previously authorized for this client is silently reused
		// when prompt=consent is absent, and the code exchange then
		// returns NO refresh_token ("grant offline access and retry"
		// 422). An unpinned add cannot look up the channel's grant
		// health, so forcing consent is the only way to guarantee a
		// fresh offline refresh token. The pinned-channel block below
		// may still relax ForceConsent when the R7 health check proves
		// the grant is already healthy (reconnect case).
		//
		// SelectAccount applies to every provider (benign account-picker
		// hint; non-OAuth-options providers ignore it); ForceConsent is
		// YouTube-only — "consent" is a Google prompt value, other
		// providers' builders ignore the flag but the contract stays
		// honest.
		options.SelectAccount = true
		if provider == models.PlatformYouTube {
			options.ForceConsent = true
		}
	case "reconnect":
		// Explicit reconnect requests without a pinned channel stay on
		// force-consent: the channel (and therefore the grant health) is
		// unknown, and "the user explicitly asked for a fresh
		// authorization" is one of the consent-necessary cases. When the
		// request also pins a channel the health check below may relax it.
		options.ForceConsent = true
	}
	identity := auth.IdentityFromContext(req.Context())

	// YouTube-only: ?expected_channel_id=UC... tells the server which
	// channel the operator intends to bind the OAuth grant to. Without
	// it, a Google account with N>1 channels cannot be attached safely
	// (channels.list(mine=true) returns every Brand Account under the
	// grant, and the bearer token is bound to one channel per
	// Brand-Account selection). The hint round-trips through a sibling
	// HttpOnly cookie (NOT the URL state param — Google echoes the URL
	// state verbatim, and we keep it a pure CSRF nonce).
	//
	// R7 — prompt=consent reduction: consent is forced ONLY when the
	// pinned channel has no healthy grant (brand-new connection,
	// reauth_required/pending/error, missing/inactive grant row, or
	// missing force-ssl scope). A healthy active channel reconnects
	// with prompt=select_account only — Google reuses its cached
	// grant and returns the SAME refresh token, so no new token is
	// burned against the 100-per-client cap. The hint also carries the
	// healthy grant's pool client key so the pool path below stays on
	// the client that issued the grant (grant reuse across clients is
	// impossible: the code exchange would mint a new grant).
	var reconnectHint youtubeReconnectHint
	expectedChannelID := ""
	if raw := req.URL.Query().Get("expected_channel_id"); raw != "" {
		if provider == models.PlatformYouTube && isValidYouTubeChannelID(raw) {
			expectedChannelID = raw
			// expected_channel_id ALWAYS implies account picker so the
			// operator confirms the Google account.
			options.SelectAccount = true
			var userID int64
			if identity != nil {
				userID = identity.UserID()
			}
			reconnectHint = r.youtubeReconnectHintFor(req.Context(), userID, expectedChannelID)
			options.ForceConsent = reconnectHint.consentNeeded
		}
	}

	// YouTube OAuth Client Pool path: when a pool registry is
	// configured the state stops being a cookie-backed CSRF nonce and
	// becomes a short-lived HS256-signed JWT carrying
	// expected_channel_id + workspace_id + a single-use nonce (jti).
	// The selected oauth_client_key is NOT baked into the JWT (Google
	// echoes the URL state verbatim): it round-trips in the sibling
	// HttpOnly cookie oauth_state_{provider}_oauth_client as
	// "<jti>:<clientKey>". The callback then exchanges the code with
	// EXACTLY the client that built this consent URL — never
	// re-selects at callback time. Without a registry the legacy
	// cookie path is preserved unchanged.
	var state string
	if provider == models.PlatformYouTube && r.youtubeOAuthClientRegistry != nil {
		// R7 — client selection. A HEALTHY reconnect reuses the pool
		// client that issued the existing grant (Resolve on the grant's
		// oauth_client_key): Google's cached consent is per (client,
		// account, scopes), so reusing the client returns the SAME
		// refresh token and never burns a new slot. Only new/unhealthy
		// connections go through the capacity-aware selector. When the
		// channel's existing grant is reachable, its Google subject is
		// passed to SelectForNewConnection so the selector picks the
		// least-loaded pool FOR THAT account (capacity-aware) instead
		// of the deterministic first client; a brand-new channel with
		// no lineage stays subject-less (deterministic fallback) — see
		// internal/services/youtube_oauth_client_pool.go.
		var client *services.YouTubeOAuthClientConfig
		if reconnectHint.existingClientKey != "" {
			resolved, resErr := r.youtubeOAuthClientRegistry.Resolve(reconnectHint.existingClientKey)
			if resErr != nil {
				// Fail-closed: never fall back to a different client for a
				// healthy grant (a wrong client would mint a new grant and
				// orphan the stored one).
				logAndError(w, req, "failed to resolve existing youtube pool client for reconnect", resErr,
					"provider", provider, "oauth_client_key", reconnectHint.existingClientKey)
				return
			}
			client = resolved
		} else {
			selected, selErr := r.youtubeOAuthClientRegistry.SelectForNewConnection(req.Context(), reconnectHint.providerSubjectID)
			if selErr != nil {
				logAndError(w, req, "failed to select youtube oauth pool client", selErr, "provider", provider)
				return
			}
			client = selected
		}
		workspaceID := int64(0)
		if identity != nil {
			workspaceID = identity.WorkspaceID()
		}
		signedState, nonce, expiresAt, issErr := r.auth.IssueOAuthFlowState(expectedChannelID, workspaceID)
		if issErr != nil {
			logAndError(w, req, "failed to issue oauth flow state", issErr, "provider", provider)
			return
		}
		if r.connectLinkNonceStore == nil {
			logAndError(w, req, "connect-link nonce store not configured", errors.New("connect-link nonce store not configured"), "provider", provider)
			return
		}
		// Persist the jti so the callback can atomically consume it
		// (single-use). Reuses the connect-link store: same nonce /
		// expiry / atomic-consume contract, and it is already wired
		// and required by validateRequiredDeps.
		if createErr := r.connectLinkNonceStore.Create(nonce, expectedChannelID, expiresAt); createErr != nil {
			logAndError(w, req, "failed to persist oauth flow nonce", createErr, "provider", provider)
			return
		}
		// Round-trip the selected client key in the sibling
		// oauth_state_{provider}_oauth_client cookie as
		// "<jti>:<clientKey>". The pool JWT deliberately does NOT carry
		// the key (it would be echoed in the URL by Google); the cookie
		// is HttpOnly and bound to the flow by the jti prefix, so the
		// callback exchanges the code with exactly the client that
		// built this consent URL.
		setOAuthClientCookie(w, provider, nonce, client.Key, r.cookieDomain)
		keyedLogin, ok := p.(YouTubePoolAwareLogin)
		if !ok {
			logAndError(w, req, "youtube provider cannot build a pool-client login URL", errors.New("YouTubePoolAwareLogin not implemented"), "provider", provider)
			return
		}
		state = signedState
		http.Redirect(w, req, keyedLogin.GetLoginURLWithPoolClient(state, options, client), http.StatusFound)
		return
	}

	var err error
	state, err = generateOAuthState(w, provider, expectedChannelID, r.cookieDomain)
	if err != nil {
		logAndError(w, req, "failed to start oauth flow", err, "provider", provider)
		return
	}

	http.Redirect(w, req, p.GetLoginURLWithOptions(state, options), http.StatusFound)
}
