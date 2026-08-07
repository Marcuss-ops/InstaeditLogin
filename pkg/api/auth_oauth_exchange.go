package api

// OAuth code exchange, platform-account attach, and success response.

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

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

func (r *Router) writeCallbackSuccess(w http.ResponseWriter, req *http.Request, provider string, userID int64, account *models.PlatformAccount, redirectPath string) {
	if r.frontendURL != "" {
		q := url.Values{}
		q.Set("provider", provider)
		q.Set("status", "connected")
		// Default landing is the Linking page; a validated /app/... path
		// carried in the sibling redirect cookie (from ?redirect= at login
		// time) sends the operator back to the page that started the flow.
		target := "/app/linking"
		if redirectPath != "" {
			target = redirectPath
		}
		http.Redirect(w, req, strings.TrimRight(r.frontendURL, "/")+target+"?"+q.Encode(), http.StatusFound)
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
