package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AuthHandlers groups the HTTP handler functions used by the auth
// module. Keeping them in a nested struct keeps AuthModuleDeps readable.
type AuthHandlers struct {
	Login                         http.HandlerFunc
	Callback                      http.HandlerFunc
	ExchangeCode                  http.HandlerFunc
	Refresh                       http.HandlerFunc
	Logout                        http.HandlerFunc
	LogoutAll                     http.HandlerFunc
	ListSessions                  http.HandlerFunc
	DeleteSession                 http.HandlerFunc
	ListAccounts                  http.HandlerFunc
	GetAccount                    http.HandlerFunc
	GetAccountsPerformanceSummary http.HandlerFunc
	GetAccountPerformance         http.HandlerFunc
	ValidateAccount               http.HandlerFunc
	ReconnectAccount              http.HandlerFunc
	DeleteAccount                 http.HandlerFunc
	DisconnectAccount             http.HandlerFunc
	DeleteAccountData             http.HandlerFunc
	DeleteOAuthGrant              http.HandlerFunc
	SyncAccount                   http.HandlerFunc
	AccountContent                http.HandlerFunc
	UpdateAccount                 http.HandlerFunc
	CreateWorkspace               http.HandlerFunc
	ListWorkspaces                http.HandlerFunc
	GetWorkspace                  http.HandlerFunc
	DeleteWorkspace               http.HandlerFunc
	SwitchWorkspace               http.HandlerFunc
	AttachWorkspaceChannel        http.HandlerFunc
	ListWorkspaceChannels         http.HandlerFunc
	UpdateWorkspaceChannel        http.HandlerFunc
	DetachWorkspaceChannel        http.HandlerFunc
	ListGroups                    http.HandlerFunc
	ListGroupsWithAccounts        http.HandlerFunc
	CreateGroup                   http.HandlerFunc
	GetGroup                      http.HandlerFunc
	UpdateGroup                   http.HandlerFunc
	DeleteGroup                   http.HandlerFunc
	ListGroupAccounts             http.HandlerFunc
	SetGroupAccounts              http.HandlerFunc
	UpdateGroupSettings           http.HandlerFunc
	RemoveGroupAccount            http.HandlerFunc
	CreateApiKey                  http.HandlerFunc
	ListApiKeys                   http.HandlerFunc
	GetApiKey                     http.HandlerFunc
	DeleteApiKey                  http.HandlerFunc
	RotateApiKey                  http.HandlerFunc
}

// AuthModuleDeps is the narrow set of dependencies the auth module
// needs to mount its routes.
type AuthModuleDeps struct {
	AuthEmailSvc            AuthEmailStore
	TeamStore               TeamStore
	GroupStore              GroupStore
	WebhookStore            WebhookStore
	RateLimitSvc            *services.RateLimitService
	AuthMiddleware          func(http.Handler) http.Handler
	ApiKeyAuthMiddleware    func(http.Handler) http.Handler
	Protected               func(http.HandlerFunc) http.HandlerFunc
	CsrfConfig              func() auth.CSRFConfig
	OAuthStartLimiter       func(http.Handler) http.Handler
	OAuthSessionRedirect    func(http.HandlerFunc) http.HandlerFunc
	RegisterAuthEmailRoutes func()
	RegisterTeamRoutes      func()
	RegisterWebhookRoutes   func()
	Handlers                AuthHandlers
}

// AuthModule mounts authentication, sessions, accounts, workspaces,
// groups, API keys, team and webhook routes.  It is the broadest module
// because all of these surfaces are part of the user/workspace identity
// context.
type AuthModule struct {
	deps AuthModuleDeps
}

func NewAuthModule(deps AuthModuleDeps) RouteModule {
	return &AuthModule{deps: deps}
}

// Compile-time assertion: AuthModule implements RouteModule.
var _ RouteModule = (*AuthModule)(nil)

func (m *AuthModule) handleMe(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	resp := map[string]interface{}{
		"user_id":      id.UserID(),
		"workspace_id": id.WorkspaceID(),
		"is_admin":     id.IsAdmin(),
	}
	// Surface the logged-in account's email/name so the SPA header can
	// show the InstaEdit user instead of a linked channel. Best-effort:
	// a lookup failure or an unconfigured auth-email store degrades to
	// the historical shape (no name/email) rather than failing /auth/me.
	if m.deps.AuthEmailSvc != nil {
		if user, err := m.deps.AuthEmailSvc.GetUserByID(id.UserID()); err == nil && user != nil {
			resp["email"] = user.Email
			resp["name"] = user.Name
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (m *AuthModule) Register(mux chi.Router) {
	if m.deps.AuthEmailSvc != nil {
		m.deps.RegisterAuthEmailRoutes()
	}
	if m.deps.TeamStore != nil {
		m.deps.RegisterTeamRoutes()
	}

	mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login", m.deps.OAuthStartLimiter(http.HandlerFunc(m.deps.OAuthSessionRedirect(m.deps.Handlers.Login))))
	// Backwards-compatible alias for /api/v1/auth/{provider}/login. Some external
	// scripts and older docs reference /start as the OAuth initiation URL; the
	// canonical URL is /login. Parallel-mount (rather than 308-redirect) keeps
	// the chain single-hop so the OAuthStartLimiter IP-keyed rate-limit and the
	// OAuthSessionRedirect no-session → 302 contract fire exactly once per
	// request, identical to /login. See oauth_session_redirect_start_alias_test.go
	// for the alias-parity test matrix.
	mux.Method(http.MethodGet, "/api/v1/auth/{provider}/start", m.deps.OAuthStartLimiter(http.HandlerFunc(m.deps.OAuthSessionRedirect(m.deps.Handlers.Login))))
	mux.Method(http.MethodGet, "/api/v1/auth/{provider}/callback", http.HandlerFunc(m.deps.OAuthSessionRedirect(m.deps.Handlers.Callback)))
	mux.Method(http.MethodPost, "/api/v1/auth/exchange", http.HandlerFunc(m.deps.Handlers.ExchangeCode))
	mux.Method(http.MethodGet, "/api/v1/auth/me", m.deps.Protected(m.handleMe))
	mux.Method(http.MethodPost, "/api/v1/auth/refresh", http.HandlerFunc(m.deps.Handlers.Refresh))
	mux.Method(http.MethodPost, "/api/v1/auth/logout", http.HandlerFunc(m.deps.Handlers.Logout))
	mux.Method(http.MethodPost, "/api/v1/auth/logout-all", m.deps.Protected(m.deps.Handlers.LogoutAll))
	mux.Method(http.MethodGet, "/api/v1/auth/sessions", m.deps.Protected(m.deps.Handlers.ListSessions))
	mux.Method(http.MethodDelete, "/api/v1/auth/sessions/{id}", m.deps.Protected(m.deps.Handlers.DeleteSession))

	mux.Method(http.MethodGet, "/api/v1/accounts", m.deps.Protected(m.deps.Handlers.ListAccounts))
	mux.Method(http.MethodGet, "/api/v1/accounts/{id}", m.deps.Protected(m.deps.Handlers.GetAccount))
	mux.Method(http.MethodGet, "/api/v1/accounts/performance/summary", m.deps.Protected(m.deps.Handlers.GetAccountsPerformanceSummary))
	mux.Method(http.MethodGet, "/api/v1/accounts/{id}/performance", m.deps.Protected(m.deps.Handlers.GetAccountPerformance))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/validate", m.deps.Protected(m.deps.Handlers.ValidateAccount))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/reconnect", m.deps.Protected(m.deps.Handlers.ReconnectAccount))
	// P1 (account-lifecycle audit): DELETE /api/v1/accounts/{id} is
	// deprecated (410 Gone) — the explicit soft-disconnect lives at
	// POST /api/v1/accounts/{id}/disconnect; permanent removal will be
	// DELETE /api/v1/accounts/{id}/data.
	mux.Method(http.MethodDelete, "/api/v1/accounts/{id}", m.deps.Protected(m.deps.Handlers.DeleteAccount))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/disconnect", m.deps.Protected(m.deps.Handlers.DisconnectAccount))
	mux.Method(http.MethodDelete, "/api/v1/accounts/{id}/data", m.deps.Protected(m.deps.Handlers.DeleteAccountData))
	mux.Method(http.MethodDelete, "/api/v1/accounts/{id}/oauth-grant", m.deps.Protected(m.deps.Handlers.DeleteOAuthGrant))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/sync", m.deps.Protected(m.deps.Handlers.SyncAccount))
	mux.Method(http.MethodGet, "/api/v1/accounts/{id}/content", m.deps.Protected(m.deps.Handlers.AccountContent))
	mux.Method(http.MethodPatch, "/api/v1/accounts/{id}", m.deps.Protected(m.deps.Handlers.UpdateAccount))

	mux.Route("/api/v1/workspaces", func(sr chi.Router) {
		sr.Post("/", m.deps.Protected(m.deps.Handlers.CreateWorkspace))
		sr.Get("/", m.deps.Protected(m.deps.Handlers.ListWorkspaces))
		sr.Get("/{id}", m.deps.Protected(m.deps.Handlers.GetWorkspace))
		sr.Delete("/{id}", m.deps.Protected(m.deps.Handlers.DeleteWorkspace))
		sr.Post("/{id}/switch", m.deps.Protected(m.deps.Handlers.SwitchWorkspace))
		sr.Post("/{id}/channels", m.deps.Protected(m.deps.Handlers.AttachWorkspaceChannel))
		sr.Get("/{id}/channels", m.deps.Protected(m.deps.Handlers.ListWorkspaceChannels))
		sr.Patch("/{id}/channels/{accountId}", m.deps.Protected(m.deps.Handlers.UpdateWorkspaceChannel))
		sr.Delete("/{id}/channels/{accountId}", m.deps.Protected(m.deps.Handlers.DetachWorkspaceChannel))
	})

	if m.deps.GroupStore != nil {
		mux.Route("/api/v1/groups", func(sr chi.Router) {
			sr.Get("/", m.deps.Protected(m.deps.Handlers.ListGroups))
			sr.Get("/aggregate", m.deps.Protected(m.deps.Handlers.ListGroupsWithAccounts))
			sr.Post("/", m.deps.Protected(m.deps.Handlers.CreateGroup))
			sr.Get("/{id:[0-9]+}", m.deps.Protected(m.deps.Handlers.GetGroup))
			sr.Patch("/{id:[0-9]+}", m.deps.Protected(m.deps.Handlers.UpdateGroup))
			sr.Delete("/{id:[0-9]+}", m.deps.Protected(m.deps.Handlers.DeleteGroup))
			sr.Get("/{id:[0-9]+}/accounts", m.deps.Protected(m.deps.Handlers.ListGroupAccounts))
			sr.Put("/{id:[0-9]+}/accounts", m.deps.Protected(m.deps.Handlers.SetGroupAccounts))
			sr.Delete("/{id:[0-9]+}/accounts/{accountId:[0-9]+}", m.deps.Protected(m.deps.Handlers.RemoveGroupAccount))
			sr.Patch("/{id:[0-9]+}/settings", m.deps.Protected(m.deps.Handlers.UpdateGroupSettings))
		})
	}

	mux.Route("/api/v1/api-keys", func(sr chi.Router) {
		sr.Use(func(next http.Handler) http.Handler {
			return auth.NewCSRF(m.deps.CsrfConfig(), next)
		})
		if m.deps.ApiKeyAuthMiddleware != nil {
			sr.Use(m.deps.ApiKeyAuthMiddleware)
		}
		sr.Use(m.deps.AuthMiddleware)
		if m.deps.RateLimitSvc != nil {
			sr.Use(APIKeyReadLimit(m.deps.RateLimitSvc))
		}
		sr.Post("/", m.deps.Handlers.CreateApiKey)
		sr.Get("/", m.deps.Handlers.ListApiKeys)
		sr.Get("/{id}", m.deps.Handlers.GetApiKey)
		sr.Delete("/{id}", m.deps.Handlers.DeleteApiKey)
		sr.Post("/{id}/rotate", m.deps.Handlers.RotateApiKey)
	})

	if m.deps.WebhookStore != nil {
		m.deps.RegisterWebhookRoutes()
	}
}
