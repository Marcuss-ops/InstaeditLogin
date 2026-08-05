package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// YouTubePoolAwareLogin is the optional capability on the YouTube
// OAuth provider that lets handleLogin build the consent URL against a
// pool client selected from the YouTube OAuth Client Pool (instead of
// the legacy single client). The provider's GetLoginURLWithPoolClient
// uses the client's client_id + redirect_uri so the URL Google echoes
// back is bound to the same client that will exchange the code in the
// callback.
type YouTubePoolAwareLogin interface {
	GetLoginURLWithPoolClient(state string, options services.OAuthLoginOptions, client *services.YouTubeOAuthClientConfig) string
}

// YouTubePoolAwareCallback is the optional capability on the YouTube
// OAuth provider that lets handleCallback exchange the authorization
// code with the pool client selected at login time (baked into the
// signed state's oauth_client_key). Handlers MUST route through this
// when the state carries a client key: re-selecting a client at
// callback time would exchange the code against a different client_id
// than the one that built the consent URL, and Google rejects such an
// exchange with invalid_grant. If the provider cannot honour the
// keyed exchange the request fails closed — never silently fall back
// to the default client.
type YouTubePoolAwareCallback interface {
	HandleCallbackWithClient(ctx context.Context, state, code string, client *services.YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error)
}

// exchangeOAuthCode runs the code→token exchange in step 2 of
// handleCallback. When the callback resolved an oauth_client_key from
// the sibling oauth_state_{provider}_oauth_client cookie (YouTube OAuth
// Client Pool), the exchange MUST use exactly that pool client — see
// YouTubePoolAwareCallback. An empty key keeps the legacy single-client
// exchange.
func (r *Router) exchangeOAuthCode(ctx context.Context, provider string, p services.OAuthProvider, state, code, oauthClientKey string) (*models.PlatformProfile, *models.TokenData, error) {
	if oauthClientKey == "" {
		return p.HandleCallback(ctx, state, code)
	}
	if r.youtubeOAuthClientRegistry == nil {
		return nil, nil, fmt.Errorf("oauth state selected youtube pool client %q but no pool registry is configured", oauthClientKey)
	}
	client, err := r.youtubeOAuthClientRegistry.Resolve(oauthClientKey)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve youtube pool client from oauth state: %w", err)
	}
	keyed, ok := p.(YouTubePoolAwareCallback)
	if !ok {
		return nil, nil, fmt.Errorf("provider %s cannot exchange the authorization code with the state-selected pool client %q", provider, oauthClientKey)
	}
	return keyed.HandleCallbackWithClient(ctx, state, code, client)
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
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
	case "add":
		// Account selection is sufficient for a normal add flow. Do not
		// force consent here: repeatedly issuing refresh tokens can
		// invalidate older grants. A YouTube add flow that binds a
		// specific channel refines ForceConsent below via the R7 health
		// check.
		options.SelectAccount = true
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

// handleCallback drives the security-critical OAuth callback flow.
// The body is decomposed into step methods so each transition is
// independently testable; each step either returns its result or
// writes the terminal HTTP response itself and signals the
// orchestrator to stop (stop=true). The step order, status codes,
// messages and log lines are identical to the previous monolith.
func (r *Router) handleCallback(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.capabilities.OAuth(provider)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}
	code := req.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}
	state := req.URL.Query().Get("state")
	if state == "" {
		writeError(w, http.StatusBadRequest, "missing state parameter")
		return
	}

	// Step 1 — state validation (connect-link JWT, oauth-flow JWT or
	// CSRF cookie).
	expectedChannelID, oauthClientKey, fromConnectLinkState, stop := r.resolveCallbackState(w, req, provider, state)
	if stop {
		return
	}

	// Step 2 — exchange the authorization code for profile + tokens.
	// When the state carries an oauth_client_key (YouTube OAuth Client
	// Pool) the exchange ALWAYS uses that client — never re-selects.
	profile, tokenData, err := r.exchangeOAuthCode(req.Context(), provider, p, state, code, oauthClientKey)
	if err != nil {
		metrics.RecordOAuthLoginError(provider, metrics.ErrorKind(err))
		logAndError(w, req, "OAuth authentication failed", err, "provider", provider)
		return
	}
	metrics.RecordOAuthLoginSuccess(provider)

	// SPRINT 7.1 (P0#14): session requirement is enforced by the
	// oauthSessionRedirect middleware mounted in Setup(). The user
	// is guaranteed to exist here.
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil {
		// Defence-in-depth: the middleware should have redirected,
		// but if it didn't (e.g. wired without the new option in a
		// test fixture), refuse the connect with 401 rather than
		// silently auto-creating users.
		writeError(w, http.StatusUnauthorized, "oauth social requires an InstaEdit session")
		return
	}
	userID := identity.UserID()

	// Step 3 — attach platform account(s) to the authenticated user.
	// Providers that expose AccountDiscoverer (Facebook Pages) expand
	// one OAuth grant into N platform accounts. For those providers we
	// discover the pages, create one PlatformAccount per page, and
	// persist the per-page access token. Otherwise we fall back to the
	// single-account attach path.
	var account *models.PlatformAccount
	if discoverer, ok := r.capabilities.Discoverer(provider); ok {
		account, stop = r.callbackAttachDiscovered(w, req, provider, userID, discoverer, tokenData, expectedChannelID, oauthClientKey, fromConnectLinkState)
	} else {
		account, stop = r.callbackAttachSingle(w, req, provider, userID, profile, tokenData, oauthClientKey)
	}
	if stop {
		return
	}

	// Step 4 — success: redirect to the SPA (or JSON in CLI/test mode).
	r.writeCallbackSuccess(w, req, provider, userID, account)
}

// resolveCallbackState is step 1 of handleCallback: validate the OAuth
// state parameter and resolve the expected YouTube channel binding +
// the pool client that must exchange the code.
//
// Three state shapes are accepted, in order:
//
//  1. P2 — admin connect-link JWT (2 dots, state_type=connect_link):
//     issued by POST /admin/channels/{channel_id}/connect-link, already
//     carries the expected_channel_id signed HS256 with the same secret
//     as the auth JWTs. We re-verify here so the callback can refuse
//     forged / replayed connect-link state without involving the CSRF
//     state-cookie row (the connect flow has the manager browser, not
//     the admin's). The fromConnectLink boolean return is threaded down
//     so the ErrYouTubeChannelMismatch mapping in
//     callbackAttachDiscovered can switch its status code from 409
//     (legacy cookie path) to 422 (P2 connect-link per the operator's
//     intent).
//
//  2. YouTube OAuth Client Pool JWT (2 dots, state_type=oauth_flow):
//     issued by handleLogin when a pool registry is configured. It
//     carries expected_channel_id + workspace_id + a single-use jti.
//     The jti is consumed atomically here, exactly like the
//     connect-link nonce, so a state cannot be replayed within its
//     10-minute window. The pool client key that MUST exchange the
//     code comes from the sibling oauth_state_{provider}_oauth_client
//     cookie (bound to the jti), NOT from this JWT — the exchange
//     uses the same client that built the consent URL.
//
//  3. Legacy cookie-backed CSRF nonce: verified against the
//     oauth_state_{provider} cookie (constant-time compare) and its
//     optional expected_channel sibling cookie.
//
// On failure it writes the HTTP error itself and returns stop=true.
func (r *Router) resolveCallbackState(w http.ResponseWriter, req *http.Request, provider, state string) (expectedChannelID string, oauthClientKey string, fromConnectLink bool, stop bool) {
	if strings.Count(state, ".") == 2 {
		// Connect-link states are tried first (they predate the pool
		// and carry no oauth_client_key); oauth-flow states fall
		// through because VerifyConnectLinkState rejects any
		// state_type that is not "connect_link".
		claims, sErr := r.auth.VerifyConnectLinkState(state)
		if sErr == nil {
			// Atomically consume the connect-link jti so the same
			// signed URL cannot be replayed. Missing/expired/already-
			// consumed jti are treated as a replay attempt.
			if r.connectLinkNonceStore != nil {
				consumeErr := r.connectLinkNonceStore.Consume(claims.ID)
				if consumeErr != nil {
					reason := connectLinkConsumeReason(consumeErr)
					if reason != "" {
						// Known rejection: log structured diagnostics and
						// emit a metric so operators can distinguish
						// missing/expired/consumed links from genuine
						// failures.
						slog.WarnContext(req.Context(), "connect-link nonce rejected",
							"reason", reason,
							"provider", provider,
							"expected_channel_id", claims.ExpectedChannelID,
						)
						metrics.RecordConnectLinkConsume(reason)
						writeError(w, http.StatusGone, "connect-link already consumed or expired")
						return "", "", false, true
					}
					logAndError(w, req, "could not verify connect-link state", consumeErr)
					return "", "", false, true
				}
				metrics.RecordConnectLinkConsume("ok")
			}
			return claims.ExpectedChannelID, "", true, false
		}
		// Fall through to the oauth-flow verifier only when the token
		// was well-formed and carried a different state_type keyword
		// (i.e. it IS an oauth-flow state, just not a connect-link one).
		// Signature/expiry/issuer failures on a connect-link-shaped
		// token keep the pre-pool "invalid connect-link state" error
		// surface instead of a misleading oauth-flow one.
		if !strings.Contains(sErr.Error(), "state_type=") {
			writeError(w, http.StatusBadRequest, "invalid connect-link state: "+sErr.Error())
			return "", "", false, true
		}
		// OAuth Client Pool state — issued by handleLogin. Same
		// single-use nonce contract as connect-link. The pool client
		// key is NOT in the JWT: it round-trips in the sibling
		// oauth_state_{provider}_oauth_client cookie, bound to the jti.
		flowClaims, fErr := r.auth.VerifyOAuthFlowState(state)
		if fErr != nil {
			writeError(w, http.StatusBadRequest, "invalid oauth state: "+fErr.Error())
			return "", "", false, true
		}
		slog.DebugContext(req.Context(), "oauth flow state verified",
			"provider", provider,
			"workspace_id", flowClaims.WorkspaceID,
			"expected_channel_id", flowClaims.ExpectedChannelID,
		)
		if r.connectLinkNonceStore != nil {
			consumeErr := r.connectLinkNonceStore.Consume(flowClaims.ID)
			if consumeErr != nil {
				reason := connectLinkConsumeReason(consumeErr)
				if reason != "" {
					slog.WarnContext(req.Context(), "oauth flow nonce rejected",
						"reason", reason,
						"provider", provider,
					)
					writeError(w, http.StatusGone, "oauth state already consumed or expired")
					return "", "", false, true
				}
				logAndError(w, req, "could not verify oauth flow state", consumeErr)
				return "", "", false, true
			}
		}
		// The pool client key comes from the sibling cookie — fail
		// closed on a missing / stale / forged cookie: the exchange
		// must never guess a client.
		clientKey, keyErr := verifyOAuthClientCookie(w, req, provider, flowClaims.ID, r.cookieDomain)
		if keyErr != nil {
			writeError(w, http.StatusBadRequest, "invalid oauth state: "+keyErr.Error())
			return "", "", false, true
		}
		return flowClaims.ExpectedChannelID, clientKey, false, false
	}
	expectedChannelID, stateErr := verifyOAuthState(w, req, provider, state, r.cookieDomain)
	if stateErr != nil {
		writeError(w, http.StatusBadRequest, "invalid state: "+stateErr.Error())
		return "", "", false, true
	}
	return expectedChannelID, "", false, false
}

// callbackAttachDiscovered is the AccountDiscoverer branch of step 3:
// expand the OAuth grant into N platform accounts and map the typed
// attach errors onto their HTTP contract. On any error it writes the
// HTTP response itself and returns stop=true.
//
// oauthClientKey is the pool client label from the signed state (R4);
// it is threaded into AuthorizeChannel so the reconnect persists the
// client that issued the grant (R7).
func (r *Router) callbackAttachDiscovered(w http.ResponseWriter, req *http.Request, provider string, userID int64, discoverer services.AccountDiscoverer, tokenData *models.TokenData, expectedChannelID string, oauthClientKey string, fromConnectLinkState bool) (*models.PlatformAccount, bool) {
	account, err := r.attachDiscoveredAccounts(req.Context(), userID, provider, discoverer, tokenData, expectedChannelID, oauthClientKey)
	if err == nil {
		return account, false
	}
	// YouTube-only typed errors surface as 409 Conflict so the
	// SPA knows to ask the operator to disambiguate before
	// retrying. Other discoverer failures stay 500 (genuine
	// server / DB problems).
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		writeError(w, http.StatusConflict, err.Error())
		return account, true
	}
	if errors.Is(err, services.ErrOAuthRefreshTokenRequired) {
		if flagErr := r.markOAuthRefreshTokenRequired(req.Context(), account); flagErr != nil {
			slog.WarnContext(req.Context(), "could not flag platform_account reauth_required after missing YouTube refresh token",
				"platform_account_id", platformAccountIDForLog(account), "error", flagErr)
			logAndError(w, req, "failed to persist YouTube reauthorization state", flagErr)
			return account, true
		}
		writeError(w, http.StatusUnprocessableEntity, "YouTube reconnection required: grant offline access and retry")
		return account, true
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		// Task 2/10: best-effort flip
		// platform_account.status to 'reauth_required'
		// so the operator dashboard surfaces the
		// failure immediately. The publish_worker's
		// next tick will also flip the per-target
		// rows to PostStatusBlockedAuth via
		// markPublishBlockedAuth, but we want UI
		// visibility before the next tick fires.
		// Soft error: a MarkReauthRequired failure
		// does NOT prevent the 422/409 writeError
		// from returning (publish_worker is the
		// authoritative sweep on a longer horizon).
		if account != nil && r.userRepo != nil {
			if flagErr := r.userRepo.MarkReauthRequired(req.Context(), account.ID, "youtube_channel_mismatch", err.Error()); flagErr != nil {
				slog.WarnContext(req.Context(), "could not flag platform_account reauth_required after youtube channel mismatch",
					"platform_account_id", account.ID, "error", flagErr)
			}
		}
		// P2 — connect-link refinement: 422 when the state
		// was a JWT issued by /admin/channels/{id}/connect-link
		// (the operator bound a specific channel_id via
		// the admin dashboard; mismatch is a semantic
		// contradiction, prefer 422). Legacy path
		// (?expected_channel_id=UC… cookie) keeps 409 for
		// backwards-compat with operators wired before
		// the connect-link flow landed.
		if fromConnectLinkState {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return account, true
		}
		writeError(w, http.StatusConflict, err.Error())
		return account, true
	}
	logAndError(w, req, "failed to attach discovered accounts", err, "provider", provider)
	return account, true
}

// callbackAttachSingle is the single-account branch of step 3: attach
// the platform account to the authenticated user (never auto-create),
// then run the atomic OAuth finalize. On any error it writes the HTTP
// response itself and returns stop=true.
//
// oauthClientKey is the pool client label from the signed state (R4);
// it is threaded into AuthorizeChannel so the reconnect persists the
// client that issued the grant (R7).
func (r *Router) callbackAttachSingle(w http.ResponseWriter, req *http.Request, provider string, userID int64, profile *models.PlatformProfile, tokenData *models.TokenData, oauthClientKey string) (*models.PlatformAccount, bool) {
	// Attach to the authenticated user — never auto-create.
	account, err := r.userRepo.AttachPlatformAccount(userID, profile, provider)
	if err != nil {
		if errors.Is(err, repository.ErrAccountAlreadyLinked) {
			// Operator runbook: the legal owner of the link must
			// disconnect via DELETE /api/v1/accounts/{id} before
			// re-link is possible.
			writeError(w, http.StatusConflict, err.Error())
			return nil, true
		}
		logAndError(w, req, "failed to attach platform account", err, "provider", provider)
		return nil, true
	}

	// Task 1/10 — atomic OAuth finalize. We use the
	// services.ChannelAuthorizer (wired via WithChannelAuthorizer
	// in internal/bootstrap.Wire) for the non-discoverer branch
	// too: passing expectedChannelID="" tells the service to
	// skip the channels.list(mine=true) YouTube-only pre-tx
	// guard, but the (UPSERT oauth_connections + INSERT tokens
	// via SaveTokenTx + UPDATE platform_accounts.status='active')
	// atomic flow still applies. Any partial failure rolls back
	// BOTH writes plus the status flip so a process crash
	// between AttachPlatformAccount (commits row at pending_authorization)
	// and this AuthorizeChannel call leaves the account in
	// pending_authorization, never in the legacy "active but
	// no cipher row" failure mode.
	//
	// expectedChannelID "" → no YouTube binder call (binder
	// may still be wired for other providers' flows). The
	// service's empty-string short-circuit is the documented
	// no-op for non-YouTube paths (Facebook Pages, Threads,
	// TikTok, …).
	if r.authorizer == nil {
		// Fail-fast on misconfiguration (mirrors the postStore /
		// workspaceStore nil-guard pattern). A misconfigured
		// main.go that forgets WithChannelAuthorizer would never
		// have been caught by Wire() but would silently leave
		// platform_accounts in pending_authorization forever
		// on every callback — the operator's dashboard would
		// show a stuck "needs reconnect" storm. Fail-fast
		// surfaces the wiring mistake at first-callback time.
		logAndError(w, req, "channel authorizer not configured", errors.New("channel authorizer not configured"))
		return nil, true
	}
	if _, err := r.authorizer.AuthorizeChannel(req.Context(), account.ID, "", oauthClientKey, tokenData.Scopes, tokenData); err != nil {
		if errors.Is(err, services.ErrOAuthRefreshTokenRequired) {
			if flagErr := r.markOAuthRefreshTokenRequired(req.Context(), account); flagErr != nil {
				slog.WarnContext(req.Context(), "could not flag platform_account reauth_required after missing YouTube refresh token",
					"platform_account_id", platformAccountIDForLog(account), "error", flagErr)
				logAndError(w, req, "failed to persist YouTube reauthorization state", flagErr)
				return nil, true
			}
			writeError(w, http.StatusUnprocessableEntity, "YouTube reconnection required: grant offline access and retry")
			return nil, true
		}
		logAndError(w, req, "failed to authorize channel", err, "provider", provider)
		return nil, true
	}
	return account, false
}

// writeCallbackSuccess is step 4 of handleCallback: the terminal
// success response.
//
// SPRINT 7.1 redirect target: the SPA's account-linking page. No
// one-time code is needed — the session cookie validated at the
// top of the handler IS the active session.
func (r *Router) writeCallbackSuccess(w http.ResponseWriter, req *http.Request, provider string, userID int64, account *models.PlatformAccount) {
	if r.frontendURL != "" {
		q := url.Values{}
		q.Set("provider", provider)
		q.Set("status", "connected")
		http.Redirect(w, req, strings.TrimRight(r.frontendURL, "/")+"/app/linking?"+q.Encode(), http.StatusFound)
		return
	}
	// CLI / test mode (no FRONTEND_URL): typed JSON response so
	// callers can pipeline the result without following a redirect.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "connected",
		"provider":   provider,
		"user_id":    userID,
		"account_id": account.ID,
	})
}

// HandleOAuthCallbackRouteForTest returns the OAuth /callback
// handler without the production oauthSessionRedirect middleware
// (handlers.go:1034). Use only in tests that want to exercise the
// bind-check + 422-mapping flow without booting the full session
// middleware chain. Caller MUST inject identity via
// auth.WithIdentity(ctx, identity) before calling ServeHTTP —
// the production middleware does this automatically; the test
// seam expects callers to do it explicitly.
//
// This is a test seam — NOT part of the production public API.
// Production auth gating goes through r.oauthSessionRedirect
// (handlers.go:1034)
func (r *Router) HandleOAuthCallbackRouteForTest() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// The production route is registered by chi as
		// /api/v1/auth/{provider}/callback, which populates
		// req.PathValue("provider") before invoking handleCallback.
		// This seam intentionally bypasses that middleware/route stack,
		// so reproduce only the route-param binding needed by the handler.
		// Preserve an explicitly supplied path value for tests that want
		// to exercise a custom route context.
		if req.PathValue("provider") == "" {
			const (
				prefix = "/api/v1/auth/"
				suffix = "/callback"
			)
			requestPath := req.URL.Path
			if strings.HasPrefix(requestPath, prefix) && strings.HasSuffix(requestPath, suffix) {
				provider := strings.TrimSuffix(strings.TrimPrefix(requestPath, prefix), suffix)
				if provider != "" && !strings.Contains(provider, "/") {
					req.SetPathValue("provider", provider)
				}
			}
		}
		r.handleCallback(w, req)
	})
}

// handleExchangeCode exchanges a one-time code (from /auth/callback?code=...)
// for a fresh session row + access JWT + refresh token. The code is
// single-use and 60s TTL; on success both cookies are set and 204 is
// returned. The SPA's /auth/callback page calls this immediately on
// mount, then redirects to /dashboard.
//
// SPRINT 1.1: the JWT MUST carry the user's active workspace.
// Resolution order: ExplicitWorkspaceID (set by /api/v1/connections/{p}/start
// in Sprint 1.2 future work — currently always nil) > first owned
// workspace > workspace_members. If none, we create a personal workspace
// and add the user as admin so the JWT can be issued.
//
// SPRINT 7.4 (P0#14-blocco-1.4): JWT issuance migrated to
// SessionsService.Start. Previously this handler called
// r.auth.Issue(payload.UserID, activeWS) which minted a
// sessionID=0 JWT — incompatible with Manager.Verify post-Sprint-2.1
// hardening. The single SessionsService.Start call now creates the
// session row AND binds the row's positive ID to the access JWT.
func (r *Router) handleExchangeCode(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured (Blocco #1.4 migration requires it)")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}
	payload, err := r.oneTimeCodes.Consume(body.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}
	activeWS, err := r.resolveActiveWorkspace(req.Context(), payload.UserID)
	if err != nil {
		logAndError(w, req, "failed to resolve active workspace", err)
		return
	}
	result, err := r.sessionsSvc.Start(services.StartSessionRequest{
		UserID:      payload.UserID,
		WorkspaceID: activeWS,
		UserAgent:   req.UserAgent(),
		IP:          r.clientIP(req),
	})
	if err != nil {
		logAndError(w, req, "failed to start session", err)
		return
	}
	metrics.IncJWTIssued()
	r.setSessionCookie(w, result)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current user identity, including the active
// workspace_id stamped on the JWT. Used by the SPA on every page load
// to learn who's logged in (no JWT in localStorage anymore) and to
// align the dashboard's "current workspace" indicator with the server's
// view.
func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	// Existing sessions created before the CSRF cookie was introduced (or
	// after a browser cleared only that cookie) must be repaired on the next
	// authenticated bootstrap request. Do not rotate an existing value: the
	// SPA may issue several requests concurrently after /auth/me.
	if _, err := req.Cookie(auth.CSRFTokenCookieName); err != nil {
		_, _ = auth.SetCSRFToken(w, auth.CSRFConfig{
			Secure:       r.cookieSecure,
			Path:         "/",
			CookieDomain: r.cookieDomain,
			SameSite:     http.SameSiteNoneMode,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":      id.UserID(),
		"workspace_id": id.WorkspaceID(),
		"is_admin":     id.IsAdmin(),
	})
}

// resolveActiveWorkspace returns the workspace_id which should be
// stamped on a freshly-issued JWT for the given user. Shared by
// /auth/exchange (OAuth callback) and the switch endpoint's re-bind
// after token rotation. Strategy (SPRINT 1.1):
//
//  1. Owned workspaces: pick most recent (ListByOwner desc).
//  2. Memberships: pick most recent (ListForUser desc).
//  3. None → auto-create a "Personal" workspace + admin membership.
//
// Step 3 is required so OAuth users who never went through the
// email/password onboarding still receive a JWT carrying a valid
// workspace claim (Manager.Issue refuses to sign without one).
func (r *Router) resolveActiveWorkspace(ctx context.Context, userID int64) (int64, error) {
	if r.userAndWorkspaceHelper == nil {
		return 0, fmt.Errorf("user workspace helper not configured")
	}
	if r.workspaceStore == nil || r.teamStore == nil {
		return 0, fmt.Errorf("workspace or team store not configured")
	}
	// owned
	if owned, err := r.userAndWorkspaceHelper.ListOwned(ctx, userID); err == nil && len(owned) > 0 {
		return owned[0], nil
	}
	// membership
	if memberships, err := r.userAndWorkspaceHelper.ListMemberships(ctx, userID); err == nil && len(memberships) > 0 {
		return memberships[0], nil
	}
	// Create personal workspace on the fly.
	ws := &models.Workspace{Name: "Personal", OwnerID: userID}
	if err := r.workspaceStore.Create(ws); err != nil {
		return 0, fmt.Errorf("create personal workspace on oauth exchange: %w", err)
	}
	if err := r.teamStore.AddMember(ws.ID, userID, repository.RoleAdmin); err != nil {
		return 0, fmt.Errorf("add oauth user as admin: %w", err)
	}
	return ws.ID, nil
}

// handleLogout is defined in pkg/api/sessions.go (SPRINT 2.1).
// It withdraws the session row matching the refresh-token cookie
// and clears all session cookies in one step. The route
// registration in Setup() resolves to that method directly.
