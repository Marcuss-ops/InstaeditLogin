package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/editorlaunch"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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

// editorSessionProtected accepts either the normal InstaEdit session or
// the short-lived in-memory editor session bearer. The latter is already
// bound to one project, so it is converted into the same Identity shape
// expected by the existing YouTube editor handlers without making the
// handlers trust a browser-supplied user/workspace identifier.
func (r *Router) editorSessionProtected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if r.editorLaunchTokenIssuer != nil {
			raw := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
			projectID := strings.TrimSpace(chi.URLParam(req, "velox_project_id"))
			if raw != "" && projectID != "" {
				scope := editorlaunch.ScopeRead
				if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
					scope = editorlaunch.ScopeWrite
				}
				if claims, err := r.editorLaunchTokenIssuer.VerifySession(raw, projectID, scope); err == nil {
					ctx := editorlaunch.WithClaims(req.Context(), claims)
					ctx = auth.WithIdentity(ctx, auth.NewUserIdentity(claims.UserID, claims.WorkspaceID, 0))
					next(w, req.WithContext(ctx))
					return
				}
			}
		}
		r.protected(next)(w, req)
	}
}

// editorSessionProtectedUnscoped accepts the short-lived editor session
// bearer on endpoints whose URL does not carry a velox_project_id
// segment (media presign/complete). The project is already bound inside
// the bearer's own claims, so the same VerifySession call that guards
// the project-scoped routes is used with an empty URL project — the
// token's own project_id + workspace + user claims become the identity,
// exactly like the project-scoped variant. Requests without a valid
// editor bearer fall through to the regular protected chain (cookie /
// normal JWT session), so the InstaEdit SPA keeps working unchanged.
func (r *Router) editorSessionProtectedUnscoped(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if r.editorLaunchTokenIssuer != nil {
			raw := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
			if raw != "" {
				scope := editorlaunch.ScopeRead
				if req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodOptions {
					scope = editorlaunch.ScopeWrite
				}
				if claims, err := r.editorLaunchTokenIssuer.VerifySession(raw, "", scope); err == nil {
					ctx := editorlaunch.WithClaims(req.Context(), claims)
					ctx = auth.WithIdentity(ctx, auth.NewUserIdentity(claims.UserID, claims.WorkspaceID, 0))
					// requireUserID in the media handlers reads the user_id
					// context key (set by auth.Manager.putIdentity on the
					// normal session path); mirror it so presign/complete
					// work identically for editor-session callers.
					ctx = auth.WithUserID(ctx, claims.UserID)
					next(w, req.WithContext(ctx))
					return
				}
			}
		}
		r.protected(next)(w, req)
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
				provider := models.NormalizePlatformIdentifier(req.PathValue("provider"))
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
