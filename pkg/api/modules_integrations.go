package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// IntegrationsModuleDeps is the narrow set of dependencies the
// user-facing integrations module needs to mount its routes.
type IntegrationsModuleDeps struct {
	ExternalDestinationStore ExternalDestinationStore
	WorkspaceStore           WorkspaceStore
	UserStore                UserStore
	AuditLogStore            AuditLogStore
	AuthMiddleware           func(http.Handler) http.Handler
	CSRFMiddleware           func(http.Handler) http.Handler
}

// IntegrationsModule mounts user-facing integration routes
// (currently the Velox destination endpoints under
// /api/v1/integrations/velox/destinations). It is separate from
// VeloxBFFModule because these routes are part of the workspace
// integration surface, not the Velox BFF proxy.
type IntegrationsModule struct {
	deps IntegrationsModuleDeps
}

// NewIntegrationsModule creates the integrations module.
func NewIntegrationsModule(deps IntegrationsModuleDeps) RouteModule {
	return &IntegrationsModule{deps: deps}
}

// Compile-time assertion: IntegrationsModule implements RouteModule.
var _ RouteModule = (*IntegrationsModule)(nil)

func (m *IntegrationsModule) Register(mux chi.Router) {
	if mux == nil {
		return
	}
	if m.deps.ExternalDestinationStore == nil || m.deps.WorkspaceStore == nil {
		return
	}

	wrap := func(h http.HandlerFunc) http.Handler {
		var handler http.Handler = h
		if m.deps.CSRFMiddleware != nil {
			handler = m.deps.CSRFMiddleware(handler)
		}
		if m.deps.AuthMiddleware != nil {
			handler = m.deps.AuthMiddleware(handler)
		}
		return handler
	}

	if m.deps.UserStore != nil && m.deps.AuditLogStore != nil {
		mux.Method(http.MethodPost, "/api/v1/integrations/velox/destinations",
			wrap(m.handleCreateIntegrationVeloxDestination))
	} else {
		mux.Method(http.MethodPost, "/api/v1/integrations/velox/destinations",
			wrap(func(w http.ResponseWriter, req *http.Request) {
				writeError(w, http.StatusNotImplemented, "destination creation not configured")
			}))
	}

	mux.Method(http.MethodGet, "/api/v1/integrations/velox/destinations",
		wrap(m.handleListIntegrationVeloxDestinations))
	mux.Method(http.MethodGet, "/api/v1/integrations/velox/destinations/{id}",
		wrap(m.handleGetIntegrationVeloxDestination))
	mux.Method(http.MethodDelete, "/api/v1/integrations/velox/destinations/{id}",
		wrap(m.handleDeleteIntegrationVeloxDestination))
	mux.Method(http.MethodPatch, "/api/v1/integrations/velox/destinations/{id}",
		wrap(m.handleUpdateIntegrationVeloxDestination))
}

// --- Router thin wrappers (test compatibility) ------------------------------
//
// Thin *Router forwarders that keep existing unit tests (which call the
// handlers directly on *Router) compiling while the public module
// constructors receive typed deps. They delegate to a module instance
// built from the Router's current fields. See the matching TODO in
// modules_velox.go: migrate the affected tests and delete these
// wrappers; do NOT add new production code here.

func (r *Router) integrationsModule() *IntegrationsModule {
	return NewIntegrationsModule(IntegrationsModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		AuditLogStore:            r.auditLogStore,
		AuthMiddleware:           r.authMiddleware,
		CSRFMiddleware:           r.csrfMiddleware,
	}).(*IntegrationsModule)
}

func (r *Router) registerUserVeloxDestinations(mux chi.Router) {
	r.integrationsModule().Register(mux)
}

func (r *Router) handleCreateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleCreateIntegrationVeloxDestination(w, req)
}

func (r *Router) handleListIntegrationVeloxDestinations(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleListIntegrationVeloxDestinations(w, req)
}

func (r *Router) handleGetIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleGetIntegrationVeloxDestination(w, req)
}

func (r *Router) handleDeleteIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleDeleteIntegrationVeloxDestination(w, req)
}

func (r *Router) handleUpdateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleUpdateIntegrationVeloxDestination(w, req)
}
