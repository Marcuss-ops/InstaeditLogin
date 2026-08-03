package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxjobs"
	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
)

// VeloxBFFModuleDeps is the narrow set of dependencies the Velox
// BFF module needs to mount its routes.
type VeloxBFFModuleDeps struct {
	Client         veloxapi.Client
	JobRegistry    *veloxjobs.Registry
	AuthMiddleware func(http.Handler) http.Handler
	CSRFMiddleware func(http.Handler) http.Handler
}

// VeloxBFFModule mounts the user-facing /api/v1/velox/* BFF routes
// that proxy a bounded subset of Velox operations to the browser.
// Registration is a no-op when the Router has no veloxBFFClient wired
// (matches the AdminModule / VeloxModule nil-guard pattern).
type VeloxBFFModule struct {
	deps VeloxBFFModuleDeps
}

func NewVeloxBFFModule(deps VeloxBFFModuleDeps) RouteModule {
	return &VeloxBFFModule{deps: deps}
}

// Compile-time assertion: VeloxBFFModule implements RouteModule.
var _ RouteModule = (*VeloxBFFModule)(nil)

func (m *VeloxBFFModule) Register(mux chi.Router) {
	if m.deps.Client == nil {
		return
	}
	veloxapi.Register(mux, veloxapi.Deps{
		Client:         m.deps.Client,
		JobRegistry:    m.deps.JobRegistry,
		AuthMiddleware: m.deps.AuthMiddleware,
		CSRFMiddleware: m.deps.CSRFMiddleware,
	})
}

// WithVeloxBFFClient wires the typed Velox client used by the
// user-facing /api/v1/velox/* BFF routes. When omitted, the
// VeloxBFFModule does not mount its routes (nil-guard pattern).
// Production wiring in cmd/server/main.go passes the
// internal/veloxclient.Client constructed from VELOX_CONTROL_URL +
// VELOX_CONTROL_JWT_SECRET.
func WithVeloxBFFClient(c veloxapi.Client) RouterOption {
	return func(r *Router) { r.veloxBFFClient = c }
}

// WithVeloxJobRegistry wires the central technical job-type registry used
// by POST /api/v1/jobs. When omitted, the BFF module uses the default
// built-in registry for backwards-compatible test and local wiring.
func WithVeloxJobRegistry(registry *veloxjobs.Registry) RouterOption {
	return func(r *Router) { r.veloxJobRegistry = registry }
}

// WithVeloxBFFAuthMiddleware wires the JWT auth middleware for the
// /api/v1/velox/* routes. Typically r.auth.Middleware.
func WithVeloxBFFAuthMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.veloxBFFAuthMiddleware = mw }
}

// WithVeloxBFFCSRFMiddleware wires the CSRF middleware for the
// /api/v1/velox/* routes. Typically auth.NewCSRF(r.csrfConfig(), _).
func WithVeloxBFFCSRFMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.veloxBFFCSRFMiddleware = mw }
}
