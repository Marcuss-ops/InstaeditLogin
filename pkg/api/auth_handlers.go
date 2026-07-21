package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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
		options.SelectAccount = true
		options.ForceConsent = true
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
	// P2 — admin connect-link. When the state param is JWT-shaped
	// (2 dots: header.payload.sig), it was issued by the admin
	// POST /admin/channels/{channel_id}/connect-link handler and
	// already carries the expected_channel_id, signed HS256 with
	// the same secret as the auth JWTs. We re-verify here so the
	// callback can refuse forged / replayed connect-link state
	// without involving the CSRF state-cookie row (the connect
	// flow has the manager browser, not the admin's). The
	// boolean return is threaded down so the ErrYouTubeChannelMismatch
	// mapping at the bottom of this handler can switch its status
	// code from 409 (legacy cookie path) to 422 (P2 connect-link
	// per the operator's intent).
	expectedChannelID := ""
	fromConnectLinkState := false
	var stateErr error
	if strings.Count(state, ".") == 2 {
		claims, sErr := r.auth.VerifyConnectLinkState(state)
		if sErr != nil {
			writeError(w, http.StatusBadRequest, "invalid connect-link state: "+sErr.Error())
			return
		}
		// Atomically consume the connect-link nonce so the same
		// signed URL cannot be replayed. Missing/expired/already-
		// consumed nonces are treated as a replay attempt.
		if r.connectLinkNonceStore != nil {
			consumeErr := r.connectLinkNonceStore.Consume(claims.Nonce)
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
					return
				}
				logAndError(w, req, "could not verify connect-link state", consumeErr)
				return
			}
			metrics.RecordConnectLinkConsume("ok")
		}
		expectedChannelID = claims.ExpectedChannelID
		fromConnectLinkState = true
	} else {
		expectedChannelID, stateErr = verifyOAuthState(w, req, provider, state)
		if stateErr != nil {
			writeError(w, http.StatusBadRequest, "invalid state: "+stateErr.Error())
			return
		}
	}
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

	// Providers that expose AccountDiscoverer (Facebook Pages) expand
	// one OAuth grant into N platform accounts. For those providers we
	// discover the pages, create one PlatformAccount per page, and
	// persist the per-page access token. Otherwise we fall back to the
	// single-account attach path.
	var account *models.PlatformAccount
	if discoverer, ok := r.capabilities.Discoverer(provider); ok {
		account, err = r.attachDiscoveredAccounts(req.Context(), userID, provider, discoverer, tokenData, expectedChannelID)
		if err != nil {
			// YouTube-only typed errors surface as 409 Conflict so the
			// SPA knows to ask the operator to disambiguate before
			// retrying. Other discoverer failures stay 500 (genuine
			// server / DB problems).
			if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
				writeError(w, http.StatusConflict, err.Error())
				return
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
					return
				}
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			logAndError(w, req, "failed to attach discovered accounts", err, "provider", provider)
			return
		}
	} else {
		// Attach to the authenticated user — never auto-create.
		account, err = r.userRepo.AttachPlatformAccount(userID, profile, provider)
		if err != nil {
			if errors.Is(err, repository.ErrAccountAlreadyLinked) {
				// Operator runbook: the legal owner of the link must
				// disconnect via DELETE /api/v1/accounts/{id} before
				// re-link is possible.
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			logAndError(w, req, "failed to attach platform account", err, "provider", provider)
			return
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
			return
		}
		if _, err := r.authorizer.AuthorizeChannel(req.Context(), account.ID, "", tokenData.Scopes, tokenData); err != nil {
			logAndError(w, req, "failed to authorize channel", err, "provider", provider)
			return
		}
	}

	// SPRINT 7.1 redirect target: the SPA's account-linking page. No
	// one-time code is needed — the session cookie validated at the
	// top of this handler IS the active session.
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
	return http.HandlerFunc(r.handleCallback)
}

// attachDiscoveredAccounts is used by handleCallback for providers that
// expose AccountDiscoverer (Facebook Pages, YouTube Channels). It creates
// one PlatformAccount per discovered account and persists tokens.
//
// Token strategy per provider:
//   - YouTube: every discovered channel receives the root OAuth bearer
//     token (the same token is shared across all channels from one grant).
//     SupplementalTokens is nil/empty for YouTube.
//   - Facebook Pages: each Page carries a SupplementalToken
//     (TokenTypePageAccess) with the per-Page Page Access Token, plus the
//     root long-lived user token stored as TokenTypeLongLived on every
//     discovered page (so refresh can re-exchange from any page).
//
// The generalized flow:
//  1. Discover accounts via the provider's DiscoverAccounts.
//  2. For each DiscoveredAccount, AttachPlatformAccount (idempotent).
//  3. Save metadata from DiscoveredAccount.Metadata on the account row.
//  4. Save the root token on every discovered account.
//  5. Save every DiscoveredAccount.SupplementalTokens entry as an
//     additional token in the vault. This replaces the old provider-
//     specific hack that checked for Metadata["page_access_token"].
//
// ErrYouTubeAmbiguousAuthorization is returned by attachDiscoveredAccounts
// when a YouTube OAuth grant's channels.list(mine=true) returns >1
// channel AND no expected_channel_id was supplied at login time.
//
// P0: a single Google account can own multiple YouTube channels
// (Brand Accounts, multi-channel networks). YouTube's OAuth grant is
// bound to ONE channel per Brand-Account selection at consent time.
// Cloning the root bearer token across every channel silently
// violates Google's YouTube Data API contract and misroutes uploads
// to whatever channel the grant happens to target. The operator must
// re-authorize via /api/v1/auth/youtube/login with
// ?expected_channel_id=UC... so channels.list can be filtered to a
// single channel before any token is saved. Handler maps this to
// HTTP 409 Conflict so the SPA can ask the operator to disambiguate.
var ErrYouTubeAmbiguousAuthorization = errors.New("youtube authorization is ambiguous: re-authorize with expected_channel_id")

// ErrYouTubeChannelMismatch is returned when expected_channel_id was
// supplied but channels.list(mine=true) does NOT contain that ID. The
// operator authenticated the wrong Google account, mistyped the ID,
// or a Brand Account was added since the inventory was imported. We
// refuse to attach ANY account because saving the root token on a
// different channel would silently misroute uploads. Handler maps
// this to HTTP 409 Conflict.
var ErrYouTubeChannelMismatch = errors.New("youtube authorized channel does not match expected channel")

// connectLinkConsumeReason maps the known connect-link nonce
// repository sentinel errors to a short reason string used in logs
// and the connect_link_consume_total metric. It returns an empty
// string for any other error so callers can fall through to a
// generic 500 response.
func connectLinkConsumeReason(err error) string {
	switch {
	case errors.Is(err, repository.ErrNonceMissing):
		return "missing"
	case errors.Is(err, repository.ErrNonceExpired):
		return "expired"
	case errors.Is(err, repository.ErrNonceConsumed):
		return "consumed"
	default:
		return ""
	}
}

func (r *Router) attachDiscoveredAccounts(ctx context.Context, userID int64, provider string, discoverer services.AccountDiscoverer, tokenData *models.TokenData, expectedChannelID string) (*models.PlatformAccount, error) {
	accounts, err := discoverer.DiscoverAccounts(ctx, tokenData.AccessToken, "")
	if err != nil {
		return nil, fmt.Errorf("discover accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts discovered for provider %s", provider)
	}

	// YouTube enforces a 1:1 OAuth-grant-to-channel mapping. The
	// root bearer token is bound to whichever Brand Account the
	// operator selected in Google's consent screen; cloning it
	// across every channel silently misroutes uploads. Other
	// AccountDiscoverer providers (Facebook Pages, Instagram
	// Business Accounts) intentionally fan the root token out to
	// every discovered account — that path stays unchanged.
	if provider == models.PlatformYouTube {
		if expectedChannelID != "" {
			filtered := accounts[:0]
			matched := 0
			for _, acc := range accounts {
				if acc.Profile.PlatformUserID == expectedChannelID {
					filtered = append(filtered, acc)
					matched++
				}
			}
			if matched == 0 {
				return nil, fmt.Errorf("%w: %q is not in channels.list(mine=true) result", ErrYouTubeChannelMismatch, expectedChannelID)
			}
			if matched > 1 {
				// Defensive against channels.list returning duplicates
				// for the same resource; the first match wins.
				filtered = filtered[:1]
			}
			accounts = filtered
		} else if len(accounts) != 1 {
			return nil, fmt.Errorf("%w: channels.list returned %d channels for this grant", ErrYouTubeAmbiguousAuthorization, len(accounts))
		}
	}

	var first *models.PlatformAccount
	for _, acc := range accounts {
		profile := &models.PlatformProfile{
			PlatformUserID: acc.Profile.PlatformUserID,
			Username:       acc.Profile.Username,
		}
		created, err := r.userRepo.AttachPlatformAccount(userID, profile, provider)
		if err != nil {
			if errors.Is(err, repository.ErrAccountAlreadyLinked) {
				// Already linked to this user — load the existing row so
				// we can update its token below.
				existing, findErr := r.userRepo.FindPlatformAccount(provider, acc.Profile.PlatformUserID)
				if findErr != nil {
					return nil, fmt.Errorf("find existing account: %w", findErr)
				}
				if existing == nil {
					return nil, fmt.Errorf("account already linked but not found")
				}
				created = existing
			} else {
				return nil, fmt.Errorf("attach account %s: %w", acc.Profile.PlatformUserID, err)
			}
		}

		if first == nil {
			first = created
		}

		// Persist metadata from discovery (handle, avatar, stats, etc.)
		if len(acc.Metadata) > 0 {
			if created.Metadata == nil {
				created.Metadata = make(models.Metadata)
			}
			for k, v := range acc.Metadata {
				// Do not overwrite existing metadata keys.
				if _, exists := created.Metadata[k]; !exists {
					created.Metadata[k] = v
				}
			}
			if err := r.userRepo.UpdatePlatformAccount(created); err != nil {
				return nil, fmt.Errorf("update metadata for account %d: %w", created.ID, err)
			}
		}

		// P2 — admin connect-link: Task 1/10 atomic flip. The
		// previous two-call sequence (FinalizeAttach + vault.Save
		// + supplemental vault.Save) could leave the platform_account
		// row in status='active' WITHOUT a tokens row if the vault
		// save failed AFTER FinalizeAttach committed. The new
		// services.ChannelAuthorizer.AuthorizeChannel merges those
		// writes into ONE transaction inside services/
		// channel_authorization.go: any failure rolls every write
		// back, keeping the platform_account row in its pre-call
		// state (typically 'pending_authorization').
		// Equivalent codes behaviour preserved:
		//   - ErrYouTubeChannelMismatch → 422 (via the binder
		//     guard inside AuthorizeChannel)
		//   - Eligibility-gate reject → 422 (status not in
		//     pending_authorization / active / reauth_required)
		//   - DB write failure → 5xx (wrapped, retryable)
		// The principal token + every supplemental token are
		// persisted inside the SAME tx so a Page Access Token
		// (Facebook) failure rolls back its principal user token
		// write AND the oauth_connections row too.
		channelTokens := make([]*models.TokenData, 0, 1+len(acc.SupplementalTokens))
		channelTokens = append(channelTokens, tokenData)
		channelTokens = append(channelTokens, acc.SupplementalTokens...)
		if r.authorizer == nil {
			// Fail-fast on misconfiguration (symmetric to the
			// non-discoverer branch). Mirrors the postStore /
			// workspaceStore nil-guard pattern. Without this,
			// a misconfigured main.go (missing
			// WithChannelAuthorizer) would silently leave every
			// discovered-discoverer account stuck at
			// pending_authorization with no encrypted token
			// row, even though AttachPlatformAccount's commit
			// looks successful. The fail-fast 500 surfaces the
			// wiring mistake at first-callback time.
			return nil, errors.New("channel authorizer not configured")
		}
		if _, err := r.authorizer.AuthorizeChannel(ctx, created.ID, expectedChannelID, tokenData.Scopes, channelTokens...); err != nil {
			return nil, fmt.Errorf("authorize channel for account %d: %w", created.ID, err)
		}
	}

	return first, nil
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
	r.setSessionCookie(w, req, result)
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

// csrfConfig returns the CSRF config that matches the
// session_cookie defaults: Secure=r.cookieSecure, SameSite=None
// (required for cross-origin SPA + cross-site cookie; browsers
// require Secure when SameSite=None), Path=/, HttpOnly=false
// (SPA reads via document.cookie).
//
// Blocco #1.3 — the csrf_token cookie is set by every endpoint that
// mints a session (handleExchangeCode, handleRegister,
// handleLoginEmail, handleRefresh) so the SPA can immediately echo
// it on the next unsafe request. The token is regenerated on
// every successful login to ensure the post-login token cannot be
// guessed by a pre-login attacker (see internal/auth/csrf.go).
func (r *Router) csrfConfig() auth.CSRFConfig {
	return auth.CSRFConfig{
		Secure:       r.cookieSecure,
		Path:         "/",
		CookieDomain: r.cookieDomain,
		SameSite:     http.SameSiteNoneMode,
	}
}

// protected wraps an http.HandlerFunc with the CSRF double-submit
// check (outermost) and the JWT/cookie auth.Middleware (inner).
// Failure modes:
//   - safe methods (GET/HEAD/OPTIONS) skip CSRF and reach auth.Middleware
//     (which 401s on missing/invalid session).
//   - Authorization Bearer-prefixed requests skip CSRF (JWT or API-key
//     paths) and reach auth.Middleware.
//   - cookie-authenticated unsafe requests MUST carry a csrf_token
//     cookie equal to the X-CSRF-Token request header — otherwise 403.
//
// Other helpers in this file also use r.csrfConfig() to issue the
// csrf_token cookie on login / refresh / exchange / register so the
// SPA's first post-login POST can succeed.
func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		csrfHandler := auth.NewCSRF(r.csrfConfig(), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.auth.Middleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				// P0#3: access tokens must be backed by a live session row.
				// API-key identities are stateless and bypass this check.
				if r.sessionsSvc != nil {
					if id := auth.IdentityFromContext(req.Context()); id != nil && !id.IsAPIKey() && id.SessionID() > 0 {
						active, err := r.sessionsSvc.IsActive(id.SessionID())
						if err != nil || !active {
							writeError(w, http.StatusUnauthorized, "session inactive, revoked or expired")
							return
						}
					}
				}
				next.ServeHTTP(w, req)
			})).ServeHTTP(w, req)
		}))
		csrfHandler.ServeHTTP(w, req)
	}
}

// oauthSessionRedirect validates the session (Bearer or HttpOnly
// cookie) BEFORE running the wrapped OAuth handler, but unlike
// `protected` it does not write a 401 on failure: it 302-redirects
// to ${frontendURL}/login?next=/connections/{provider} so the SPA
// can show the login UI and resume the OAuth connect after the user
// authenticates. SPRINT 7.1 (P0#14) — OAuth social is now a
// "connect an account to an existing product session" operation,
// not a registration pathway. The handleLogin and handleCallback
// routes both mount this middleware so the OAuth dialog is never
// reachable without an InstaEdit session.
//
// When frontendURL is empty (CLI / test mode) the helper falls
// back to writeError(401) so callers can still rely on a typed
// error response — the SPA path is irrelevant in CLI mode anyway.
func (r *Router) oauthSessionRedirect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		identity := r.extractSessionIdentity(req)
		if identity == nil {
			if r.frontendURL != "" {
				provider := req.PathValue("provider")
				nextURL := url.QueryEscape("/connections/" + provider)
				http.Redirect(w, req,
					strings.TrimRight(r.frontendURL, "/")+"/login?next="+nextURL,
					http.StatusFound)
				return
			}
			writeError(w, http.StatusUnauthorized, "missing user identity (OAuth social requires an InstaEdit session — post /api/v1/auth/register or /login first)")
			return
		}
		ctx := auth.WithIdentity(req.Context(), identity)
		next(w, req.WithContext(ctx))
	}
}

// extractSessionIdentity returns the UserIdentity from the request's
// Bearer token or `session` HttpOnly cookie, or nil when no valid
// identity is present. Mirrors auth.Manager.Middleware's verification
// logic but returns a typed result instead of writing a response,
// so the caller can decide between 401 (protected endpoints) and
// 302→/login (OAuth endpoints). API-key Bearer tokens are NOT
// considered valid for OAuth social — OAuth is a human flow that
// requires a JWT-path session (sessionID > 0).
func (r *Router) extractSessionIdentity(req *http.Request) auth.Identity {
	if r.auth == nil {
		return nil
	}
	// Bearer path.
	if header := req.Header.Get("Authorization"); header != "" {
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			return nil
		}
		raw := strings.TrimSpace(header[len(prefix):])
		if auth.IsApiKeyBearer(raw) {
			return nil
		}
		uid, wsID, sid, err := r.auth.Verify(raw)
		if err != nil || uid <= 0 || wsID <= 0 || sid <= 0 {
			return nil
		}
		return auth.NewUserIdentity(uid, wsID, sid)
	}
	// Cookie path (`session` HttpOnly).
	if c, err := req.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		uid, wsID, sid, err := r.auth.Verify(c.Value)
		if err != nil || uid <= 0 || wsID <= 0 || sid <= 0 {
			return nil
		}
		return auth.NewUserIdentity(uid, wsID, sid)
	}
	return nil
}

// OAuthStartLimitIfConfigured is a no-op identity when the rate
// limiter is not wired; otherwise it wraps with OAuthStartLimit.
// Used by Setup() so the OAuth start route registration stays
// unconditional (no nil-guard branching in the route table).
func OAuthStartLimitIfConfigured(svc *services.RateLimitService, trusted []*net.IPNet) func(http.Handler) http.Handler {
	if svc == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return OAuthStartLimit(svc, trusted)
}

const (
	oauthStateCookiePrefix = "oauth_state_"
	oauthStateMaxAge       = 10 * time.Minute
)

// oauthStateExpectedChannelSuffix is appended to oauth_state_{provider}
// to form the sibling cookie that round-trips an optional
// expected_channel_id across the OAuth callback. Kept distinct from the
// state cookie (which holds the pure-CSRF nonce) so the URL state param
// remains a 32-byte base64url random — verified by
// TestHandleLogin_RedirectsToProviderURL (length 43 invariant).
const oauthStateExpectedChannelSuffix = "_expected_channel"

func OAuthStateCookieName(provider string) string { return oauthStateCookiePrefix + provider }

// OAuthStateExpectedChannelCookieName returns the sibling cookie name used
// when /api/v1/auth/{provider}/login is invoked with
// ?expected_channel_id=. The cookie is HttpOnly Secure SameSite=Lax with
// MaxAge matching the state cookie; it's deleted together with the state
// cookie on successful verifyOAuthState. Kept outside the URL state
// parameter (which Google echoes back verbatim, so we keep it a pure
// CSRF nonce).
func OAuthStateExpectedChannelCookieName(provider string) string {
	return oauthStateCookiePrefix + provider + oauthStateExpectedChannelSuffix
}

// isValidYouTubeChannelID returns true for strings that look like a
// YouTube channel ID (e.g. UC_x5XG1OV2P6uZZ5FSM9Ttw): "UC" + 22 chars,
// drawn from the URL-safe alphabet [A-Za-z0-9_-]. Used server-side to
// reject malformed expected_channel_id query params before storing them
// in the round-trip cookie. Failure mode: silently drop the hint — the
// OAuth flow still proceeds without the binding assertion; the actual
// binding check happens inside attachDiscoveredAccounts.
func isValidYouTubeChannelID(s string) bool {
	if len(s) != 24 || !strings.HasPrefix(s, "UC") {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func generateOAuthState(w http.ResponseWriter, provider, expectedChannelID, cookieDomain string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth state rand failed: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: state, Path: "/",
		Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oauthStateMaxAge.Seconds()),
	})
	if expectedChannelID != "" {
		// Sibling cookie carries the operator-supplied binding hint.
		// The URL state param stays a pure CSRF nonce (Google echoes
		// it back verbatim) and this HttpOnly cookie is the only path
		// for the hint to round-trip. Issued only when handleLogin
		// saw a validated ?expected_channel_id=; deleted on
		// verifyOAuthState.
		//
		// Value format: "<state_nonce>:<channelID>". The state prefix
		// binds the channel hint to the SAME flow — a stale sibling
		// cookie from a previous OAuth round-trip cannot silently
		// leak into a new one (e.g., operator clicked Connect without
		// ?expected_channel_id= after a previous abandoned flow).
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateExpectedChannelCookieName(provider), Value: state + ":" + expectedChannelID, Path: "/",
			Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			MaxAge: int(oauthStateMaxAge.Seconds()),
		})
	}
	return state, nil
}

// verifyOAuthState checks the CSRF nonce against the
// oauth_state_{provider} cookie and (if present) reads + deletes the
// sibling oauth_state_{provider}_expected_channel cookie. The returned
// expectedChannelID is "" when no hint was set; a non-empty value means
// the operator told us which channel/resource the OAuth grant must
// bind to.
func verifyOAuthState(w http.ResponseWriter, req *http.Request, provider, stateParam string) (string, error) {
	c, err := req.Cookie(OAuthStateCookieName(provider))
	if err != nil {
		return "", fmt.Errorf("oauth state cookie missing for provider %q", provider)
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(stateParam)) != 1 {
		return "", fmt.Errorf("oauth state mismatch for provider %q (CSRF protection)", provider)
	}
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
	expectedChannelID := ""
	if ec, ecErr := req.Cookie(OAuthStateExpectedChannelCookieName(provider)); ecErr == nil && ec.Value != "" {
		// Strip the "<state_nonce>:" prefix; only return the channel ID
		// when it matches the current flow's just-verified state
		// nonce. A stale sibling cookie from a previous OAuth
		// round-trip (different state) is silently ignored — the
		// operator must re-issue ?expected_channel_id= to bind it
		// explicitly. Defence-in-depth on top of the bearer-validated
		// channels.list(mine=true) check inside attachDiscoveredAccounts.
		// Also run the extracted channel ID through the same
		// isValidYouTubeChannelID gate handleLogin uses, so a malformed
		// value (e.g. someone forged "<state>:<bogus>:<extra>") cannot
		// pass through here — it would always 409 via the channels.list
		// mismatch anyway, but the gate keeps the error surface clean.
		if id, ok := strings.CutPrefix(ec.Value, stateParam+":"); ok && isValidYouTubeChannelID(id) {
			expectedChannelID = id
		}
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateExpectedChannelCookieName(provider), Value: "", Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			MaxAge: -1, Expires: time.Unix(1, 0),
		})
	}
	return expectedChannelID, nil
}
