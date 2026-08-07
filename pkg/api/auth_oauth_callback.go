package api

// OAuth callback handling and callback-state verification.

import (
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"log/slog"
	"net/http"
	"strings"
)

func (r *Router) handleCallback(w http.ResponseWriter, req *http.Request) {
	provider := models.NormalizePlatformIdentifier(req.PathValue("provider"))
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
