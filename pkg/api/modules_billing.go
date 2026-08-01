package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// BillingModuleDeps is the narrow set of dependencies the billing
// module needs to mount its routes. All fields are required when the
// module is registered; the module is a no-op when BillingSvc is nil.
type BillingModuleDeps struct {
	BillingSvc     BillingServiceAPI
	AuthMiddleware func(http.Handler) http.Handler
	FrontendURL    string
}

// BillingModule mounts billing and Stripe webhook routes.  Registration
// is a no-op when the Router has no billing service wired.
type BillingModule struct {
	deps BillingModuleDeps
}

func NewBillingModule(deps BillingModuleDeps) RouteModule {
	return &BillingModule{deps: deps}
}

// Compile-time assertion: BillingModule implements RouteModule.
var _ RouteModule = (*BillingModule)(nil)

func (m *BillingModule) Register(mux chi.Router) {
	if m.deps.BillingSvc == nil {
		return
	}
	m.registerBillingRoutes(mux)
}
