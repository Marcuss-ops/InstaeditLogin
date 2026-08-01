package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// AdminModuleDeps is the narrow set of dependencies the admin
// module needs to mount its routes.
type AdminModuleDeps struct {
	AdminStore            AdminStore
	AuthManager           *auth.Manager
	UserStore             UserStore
	WorkspaceStore        WorkspaceStore
	Capabilities          *services.CapabilityRouter
	ConnectLinkNonceStore ConnectLinkNonceStore
}

// AdminModule mounts the operator dashboard routes under /admin/*.
// Registration is a no-op when the Router has no admin store wired.
type AdminModule struct {
	deps AdminModuleDeps
}

func NewAdminModule(deps AdminModuleDeps) RouteModule {
	return &AdminModule{deps: deps}
}

// Compile-time assertion: AdminModule implements RouteModule.
var _ RouteModule = (*AdminModule)(nil)

func (m *AdminModule) Register(mux chi.Router) {
	if m.deps.AdminStore == nil {
		return
	}
	mux.Method(http.MethodGet, "/admin/channels", m.admin(http.HandlerFunc(m.handleAdminChannels)))
	mux.Method(http.MethodGet, "/admin/channels.csv", m.admin(http.HandlerFunc(m.handleAdminChannelsCSV)))
	mux.Method(http.MethodGet, "/admin/queue", m.admin(http.HandlerFunc(m.handleAdminQueue)))
	mux.Method(http.MethodGet, "/admin/queue.csv", m.admin(http.HandlerFunc(m.handleAdminQueueCSV)))
	mux.Method(http.MethodGet, "/admin/upload_jobs/dead_letter", m.admin(http.HandlerFunc(m.handleAdminUploadJobsDeadLetter)))
	mux.Method(http.MethodGet, "/admin/upload_jobs/dead_letter.csv", m.admin(http.HandlerFunc(m.handleAdminUploadJobsDeadLetterCSV)))
	mux.Method(http.MethodGet, "/admin/health", m.admin(http.HandlerFunc(m.handleAdminHealth)))
	mux.Method(http.MethodGet, "/admin/health.csv", m.admin(http.HandlerFunc(m.handleAdminHealthCSV)))
	mux.Method(http.MethodPost, "/admin/channels/import-csv", m.admin(http.HandlerFunc(m.handleAdminImportChannelsCSV)))
	mux.Method(http.MethodGet, "/admin/channels/pending", m.admin(http.HandlerFunc(m.handleAdminPendingChannels)))
	mux.Method(http.MethodGet, "/admin/youtube/fleet_readiness", m.admin(http.HandlerFunc(m.handleAdminYouTubeFleetReadiness)))
	mux.Method(http.MethodPost, "/admin/channels/{channel_id}/connect-link", m.admin(http.HandlerFunc(m.handleAdminChannelConnectLink)))
}

// admin composes the JWT/cookie auth middleware with the admin-only
// authorization check. The /admin/* routes were previously wrapped only
// with adminAuthMiddleware, which expects an Identity in context; this
// helper ensures the auth manager extracts and validates the identity
// first. A missing auth manager returns 401.
func (m *AdminModule) admin(next http.HandlerFunc) http.Handler {
	if m.deps.AuthManager == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
	return m.deps.AuthManager.Middleware(adminAuthMiddleware(next))
}
