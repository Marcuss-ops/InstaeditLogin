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

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.capabilities.OAuth(provider)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}
	// Translate ?mode=add|reconnect into OAuthLoginOptions.
	// "add" forces account selection (Google account picker).
	// "reconnect" forces consent re-approval.
	mode := req.URL.Query().Get("mode")
	var options services.OAuthLoginOptions
	switch mode {
	case "add":
		// Account selection is sufficient for a normal add flow. Do not
		// force consent here: repeatedly issuing refresh tokens can
		// invalidate older grants. A YouTube add flow that binds a
		// specific channel sets ForceConsent below via expectedChannelID.
		options.SelectAccount = true
	case "reconnect":
		options.ForceConsent = true
	}
	// YouTube-only: ?expected_channel_id=UC... tells the server which
	// channel the operator intends to bind the OAuth grant to. Without
	// it, a Google account with N>1 channels cannot be attached safely
	// (channels.list(mine=true) returns every Brand Account under the
	// grant, and the bearer token is bound to one channel per
	// Brand-Account selection). The hint round-trips through a sibling
	// HttpOnly cookie (NOT the URL state param — Google echoes the URL
	// state verbatim, and we keep it a pure CSRF nonce).
	expectedChannelID := ""
	if raw := req.URL.Query().Get("expected_channel_id"); raw != "" {
		if provider == models.PlatformYouTube && isValidYouTubeChannelID(raw) {
			expectedChannelID = raw
			// expected_channel_id ALWAYS implies account picker +
			// consent so a previously-cached grant cannot bind to a
			// different Brand Account.
			options.SelectAccount = true
			options.ForceConsent = true
		}
	}

	state, err := generateOAuthState(w, provider, expectedChannelID, r.cookieDomain)
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

	// Step 1 — state validation (connect-link JWT or CSRF cookie).
	expectedChannelID, fromConnectLinkState, stop := r.resolveCallbackState(w, req, provider, state)
	if stop {
		return
	}

	// Step 2 — exchange the authorization code for profile + tokens.
	profile, tokenData, err := p.HandleCallback(req.Context(), state, code)
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
		account, stop = r.callbackAttachDiscovered(w, req, provider, userID, discoverer, tokenData, expectedChannelID, fromConnectLinkState)
	} else {
		account, stop = r.callbackAttachSingle(w, req, provider, userID, profile, tokenData)
	}
	if stop {
		return
	}

	// Step 4 — success: redirect to the SPA (or JSON in CLI/test mode).
	r.writeCallbackSuccess(w, req, provider, userID, account)
}

// resolveCallbackState is step 1 of handleCallback: validate the OAuth
// state parameter and resolve the expected YouTube channel binding.
//
// P2 — admin connect-link. When the state param is JWT-shaped
// (2 dots: header.payload.sig), it was issued by the admin
// POST /admin/channels/{channel_id}/connect-link handler and
// already carries the expected_channel_id, signed HS256 with
// the same secret as the auth JWTs. We re-verify here so the
// callback can refuse forged / replayed connect-link state
// without involving the CSRF state-cookie row (the connect
// flow has the manager browser, not the admin's). The
// fromConnectLink boolean return is threaded down so the
// ErrYouTubeChannelMismatch mapping in callbackAttachDiscovered can
// switch its status code from 409 (legacy cookie path) to 422 (P2
// connect-link per the operator's intent).
//
// On failure it writes the HTTP error itself and returns stop=true.
func (r *Router) resolveCallbackState(w http.ResponseWriter, req *http.Request, provider, state string) (expectedChannelID string, fromConnectLink bool, stop bool) {
	if strings.Count(state, ".") == 2 {
		claims, sErr := r.auth.VerifyConnectLinkState(state)
		if sErr != nil {
			writeError(w, http.StatusBadRequest, "invalid connect-link state: "+sErr.Error())
			return "", false, true
		}
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
					return "", false, true
				}
				logAndError(w, req, "could not verify connect-link state", consumeErr)
				return "", false, true
			}
			metrics.RecordConnectLinkConsume("ok")
		}
		return claims.ExpectedChannelID, true, false
	}
	expectedChannelID, stateErr := verifyOAuthState(w, req, provider, state, r.cookieDomain)
	if stateErr != nil {
		writeError(w, http.StatusBadRequest, "invalid state: "+stateErr.Error())
		return "", false, true
	}
	return expectedChannelID, false, false
}

// callbackAttachDiscovered is the AccountDiscoverer branch of step 3:
// expand the OAuth grant into N platform accounts and map the typed
// attach errors onto their HTTP contract. On any error it writes the
// HTTP response itself and returns stop=true.
func (r *Router) callbackAttachDiscovered(w http.ResponseWriter, req *http.Request, provider string, userID int64, discoverer services.AccountDiscoverer, tokenData *models.TokenData, expectedChannelID string, fromConnectLinkState bool) (*models.PlatformAccount, bool) {
	account, err := r.attachDiscoveredAccounts(req.Context(), userID, provider, discoverer, tokenData, expectedChannelID)
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
func (r *Router) callbackAttachSingle(w http.ResponseWriter, req *http.Request, provider string, userID int64, profile *models.PlatformProfile, tokenData *models.TokenData) (*models.PlatformAccount, bool) {
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
	if _, err := r.authorizer.AuthorizeChannel(req.Context(), account.ID, "", tokenData.Scopes, tokenData); err != nil {
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
