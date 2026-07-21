package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AuthEmailStore is the subset of AuthService methods consumed by the
// email/password handlers. The interface is local to the api package so
// test fakes can implement it without importing internal/services.
//
// SPRINT 7.4 (P0#14-blocco-1.4): Register/Login signatures migrate
// from `(userID, jwtToken, error)` to `(user, wsID, error)` — JWT
// issuance is no longer AuthService's responsibility. The handler is
// the integration point that, in cooperation with SessionsService,
// creates the row + binds the JWT. This removes every path that could
// mint a sessionID=0 JWT from production code.
type AuthEmailStore interface {
	// Register creates the user + Personal Workspace + admin
	// membership, returning the user and the new workspace_id.
	Register(email, password, name string) (user *models.User, wsID int64, err error)
	// Login authenticates the user + resolves the active workspace,
	// returning the user and the active workspace_id.
	Login(email, password string) (user *models.User, wsID int64, err error)
}

// AuthEmailServiceAdapter adapts *services.AuthService to the local
// AuthEmailStore interface used by the API handlers. Exported via
// NewAuthEmailServiceAdapter so cmd/server/main.go can wrap the
// concrete service without exporting every private helper.
type AuthEmailServiceAdapter struct {
	svc *services.AuthService
}

// NewAuthEmailServiceAdapter wraps a *services.AuthService as an
// AuthEmailStore. Required since AuthService has a richer API
// (Register returns *models.User, the adapter projects user.ID).
func NewAuthEmailServiceAdapter(svc *services.AuthService) AuthEmailStore {
	return &AuthEmailServiceAdapter{svc: svc}
}

// Register projects AuthService.Register into the API interface.
// SPRINT 7.4: no JWT in the return — handler is responsible for
// calling SessionsService.Start to bind the user to a session.
func (a *AuthEmailServiceAdapter) Register(email, password, name string) (*models.User, int64, error) {
	user, wsID, err := a.svc.Register(email, password, name)
	if err != nil {
		return nil, 0, err
	}
	return user, wsID, nil
}

// Login projects AuthService.Login into the API interface — same
// rationale as Register.
func (a *AuthEmailServiceAdapter) Login(email, password string) (*models.User, int64, error) {
	user, wsID, err := a.svc.Login(email, password)
	if err != nil {
		return nil, 0, err
	}
	return user, wsID, nil
}

// -----------------------------------------------------------------------
//  Handler registration
// -----------------------------------------------------------------------

// registerAuthEmailRoutes adds email/password auth endpoints to the chi mux.
// Called from Router.Setup() when authEmailSvc is configured.
func (r *Router) registerAuthEmailRoutes() {
	r.mux.Method(http.MethodPost, "/api/v1/auth/register", http.HandlerFunc(r.handleRegister))
	r.mux.Method(http.MethodPost, "/api/v1/auth/login", http.HandlerFunc(r.handleLoginEmail))
}

// -----------------------------------------------------------------------
//  Handlers
// -----------------------------------------------------------------------

// handleRegister creates a new SaaS user with email + password, mints
// a session row via SessionsService.Start, and writes both the access
// (HttpOnly) and refresh (HttpOnly) cookies via writeSessionCookies.
// POST /api/v1/auth/register
//
// SPRINT 7.4 (P0#14-blocco-1.4): JWT issuance moved out of
// AuthService. This handler now owns the integration: AuthService
// returns (user, workspaceID); the handler binds them to a session
// row through SessionsService.Start and writes the cookies.
//
// Invite-only beta gate: the public endpoint requires the
// X-Admin-Token header to equal cfg.AdminInviteToken (constant-time
// compare). An empty token or a missing/incorrect header returns
// 403 "registration is invite-only". Operators create users by
// supplying the header (curl / admin script) or via an internal
// provisioning tool; the SPA does NOT expose /register.
func (r *Router) handleRegister(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
		return
	}
	if r.adminInviteToken == "" ||
		subtle.ConstantTimeCompare([]byte(req.Header.Get("X-Admin-Token")), []byte(r.adminInviteToken)) != 1 {
		writeError(w, http.StatusForbidden, "registration is invite-only")
		return
	}
	if r.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured (Blocco #1.4 migration requires it)")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Email == "" || body.Password == "" || body.Name == "" {
		writeError(w, http.StatusBadRequest, "email, password, and name are required")
		return
	}

	user, wsID, err := r.authEmailSvc.Register(body.Email, body.Password, body.Name)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrPasswordTooShort) || errors.Is(err, services.ErrPasswordNoDigit):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, services.ErrEmailAlreadyTaken):
			writeError(w, http.StatusConflict, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "registration failed")
		}
		return
	}

	// Issue a session row + access JWT bound to (user, ws). Both
	// tokens are returned by SessionsService.Start; the handler
	// writes both cookies via writeSessionCookies (which honours
	// r.cookieSecure — see pkg/api/sessions.go).
	result, err := r.sessionsSvc.Start(services.StartSessionRequest{
		UserID:      user.ID,
		WorkspaceID: wsID,
		UserAgent:   req.UserAgent(),
		IP:          r.clientIP(req),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start session: "+err.Error())
		return
	}
	r.setSessionCookie(w, req, result)

	resp := map[string]interface{}{
		"user_id":      user.ID,
		"workspace_id": wsID,
		"email":        body.Email,
		"session_id":   result.SessionID,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleLoginEmail authenticates a SaaS user with email + password,
// mints a session row via SessionsService.Start, and writes both
// the access (HttpOnly) and refresh (HttpOnly) cookies.
// POST /api/v1/auth/login
//
// SPRINT 7.4 (P0#14-blocco-1.4): same integration pattern as
// handleRegister — AuthService returns (user, wsID); the handler
// binds them to a session row through SessionsService.Start.
func (r *Router) handleLoginEmail(w http.ResponseWriter, req *http.Request) {
	if r.authEmailSvc == nil {
		writeError(w, http.StatusNotImplemented, "email/password auth not configured")
		return
	}
	if r.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured (Blocco #1.4 migration requires it)")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Email == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, wsID, err := r.authEmailSvc.Login(body.Email, body.Password)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrInvalidPassword):
			writeError(w, http.StatusUnauthorized, "invalid email or password")
		case errors.Is(err, services.ErrNoWorkspace):
			// SPRINT 1.1: signal SPA to route the user into onboarding.
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":               err.Error(),
				"code":                "no_workspace",
				"onboarding_required": true,
			})
		default:
			writeError(w, http.StatusInternalServerError, "login failed")
		}
		return
	}

	// Issue a session row + access JWT bound to (user, ws). Both
	// tokens are returned by SessionsService.Start; the handler
	// writes both cookies via writeSessionCookies.
	result, err := r.sessionsSvc.Start(services.StartSessionRequest{
		UserID:      user.ID,
		WorkspaceID: wsID,
		UserAgent:   req.UserAgent(),
		IP:          r.clientIP(req),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start session: "+err.Error())
		return
	}
	r.setSessionCookie(w, req, result)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":      user.ID,
		"workspace_id": wsID,
		"email":        body.Email,
		"session_id":   result.SessionID,
	})
}

// -----------------------------------------------------------------------
//  Helpers
// -----------------------------------------------------------------------
//
// SPRINT 7.4 (P0#14-blocco-1.4) removed the legacy clientIPFromRequest /
// setSessionCookie / setRefreshCookie helpers. The cookie-write path
// moved to pkg/api/sessions.go's writeSessionCookies (which honors
// r.cookieSecure instead of hardcoding Secure=true) and the
// client-IP extraction moved to Router.clientIP(req) (which respects
// the configured trusted proxy list and strips the ephemeral port).
// Helpers were duplicate and diverged in behaviour: secure-cookie
// paths now uniformly honour the production cookieSecure toggle.
//
// The legacy layering is gone: every login, refresh, and workspace
// switch writes its cookies through a single function so the cookie
// attributes line up across endpoints.
