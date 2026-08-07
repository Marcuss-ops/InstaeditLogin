package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// HealthModuleDeps is the narrow contract for the public health and
// readiness probes. The handlers remain owned by Router so their existing
// response logic and dependencies do not move as part of this extraction.
type HealthModuleDeps struct {
	Health http.HandlerFunc
	Ready  http.HandlerFunc
}

// HealthModule mounts the unauthenticated operational probes. Keeping these
// routes together gives the router a small, stable boundary for liveness and
// readiness without mixing them into an auth or business-domain module.
type HealthModule struct {
	deps HealthModuleDeps
}

// NewHealthModule constructs the public health/readiness route module.
func NewHealthModule(deps HealthModuleDeps) RouteModule {
	return &HealthModule{deps: deps}
}

var _ RouteModule = (*HealthModule)(nil)

// Register mounts the probes without authentication, CSRF, or feature-store
// guards. Cross-cutting middleware applied by Router.Setup remains unchanged.
func (m *HealthModule) Register(mux chi.Router) {
	mux.Method(http.MethodGet, "/api/v1/health", m.deps.Health)
	mux.Method(http.MethodGet, "/ready", m.deps.Ready)
}
