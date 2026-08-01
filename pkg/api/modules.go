package api

import (
	"github.com/go-chi/chi/v5"
)

// RouteModule is the shared contract every bounded-context module
// implements.  The Router acts as the registry/resolver: it owns the
// shared dependencies and passes its chi mux to each module.
type RouteModule interface {
	Register(mux chi.Router)
}

// RouteRegistry is the single source of truth for which modules are
// mounted by Router.Setup(). It replaces the ad-hoc list of module
// constructor calls in routes.go and is the anchor for any future
// per-module dependency injection.
type RouteRegistry struct {
	modules []RouteModule
}

// NewRouteRegistry returns an empty registry. Setup() uses this to
// register every bounded-context module in a single, explicit list.
func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{}
}

// Register adds a module to the registry. Modules are mounted in the
// order they are registered.
func (reg *RouteRegistry) Register(m RouteModule) {
	reg.modules = append(reg.modules, m)
}

// Mount iterates over the registered modules and invokes their Register
// method against the supplied chi mux.
func (reg *RouteRegistry) Mount(mux chi.Router) {
	for _, m := range reg.modules {
		m.Register(mux)
	}
}
