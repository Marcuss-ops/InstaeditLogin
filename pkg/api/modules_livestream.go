package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// LivestreamHandlers groups the HTTP handler functions used by the
// livestream module. Keeping them in a nested struct keeps
// LivestreamModuleDeps readable (mirrors the GroupsHandlers pattern
// used by the groups module).
type LivestreamHandlers struct {
	ListLivestreamChannels http.HandlerFunc
	ListLivestreams        http.HandlerFunc
	CreateLivestream       http.HandlerFunc
	GetLivestream          http.HandlerFunc
	PatchLivestream        http.HandlerFunc
	DeleteLivestream       http.HandlerFunc
}

// LivestreamModuleDeps is the narrow set of dependencies the
// livestream module needs to mount its routes. The module owns ONLY
// the livestream configuration bounded context (/api/v1/livestreams);
// the handlers remain owned by the Router so their existing response
// logic and store dependencies (livestreamStore, workspaceStore,
// userRepo, vault) do not move as part of this extraction. The
// handlers' internal store nil-checks keep the pre-module 503
// semantics unchanged (routes are always mounted, exactly like the
// inline block they replace).
type LivestreamModuleDeps struct {
	// Protected is the JWT/session auth wrapper applied to every
	// livestream route.
	Protected func(http.HandlerFunc) http.HandlerFunc
	// CSRFMiddleware is applied to the mutation endpoints (POST /
	// PATCH / DELETE), mirroring the pre-module inline wiring. Nil
	// falls through to the handler directly.
	CSRFMiddleware func(http.Handler) http.Handler
	// Handlers are the concrete handler functions, injected by the
	// Router in Setup().
	Handlers LivestreamHandlers
}

// LivestreamModule mounts the livestream configuration CRUD routes.
// Extracted from Router.Setup so the livestream bounded context owns
// its own route table and middleware order; GETs stay CSRF-exempt and
// mutations are CSRF-wrapped, exactly as before.
type LivestreamModule struct {
	deps LivestreamModuleDeps
}

// NewLivestreamModule constructs the livestream route module.
func NewLivestreamModule(deps LivestreamModuleDeps) RouteModule {
	return &LivestreamModule{deps: deps}
}

// Compile-time assertion: LivestreamModule implements RouteModule.
var _ RouteModule = (*LivestreamModule)(nil)

// Register mounts the livestream routes. The static /channels
// segment is registered before /{id} so it wins the match, matching
// the pre-module registration order.
func (m *LivestreamModule) Register(mux chi.Router) {
	// GET /api/v1/livestreams — workspace rows as {items: [...]}.
	mux.Method(http.MethodGet, "/api/v1/livestreams", m.deps.Protected(m.deps.Handlers.ListLivestreams))

	// GET /api/v1/livestreams/channels — creation-wizard preflight.
	// Registered before /{id} so the static segment wins.
	mux.Method(http.MethodGet, "/api/v1/livestreams/channels", m.deps.Protected(m.deps.Handlers.ListLivestreamChannels))

	create := http.Handler(m.deps.Handlers.CreateLivestream)
	if m.deps.CSRFMiddleware != nil {
		create = m.deps.CSRFMiddleware(create)
	}
	mux.Method(http.MethodPost, "/api/v1/livestreams", m.deps.Protected(create.ServeHTTP))

	mux.Method(http.MethodGet, "/api/v1/livestreams/{id}", m.deps.Protected(m.deps.Handlers.GetLivestream))

	patch := http.Handler(m.deps.Handlers.PatchLivestream)
	if m.deps.CSRFMiddleware != nil {
		patch = m.deps.CSRFMiddleware(patch)
	}
	mux.Method(http.MethodPatch, "/api/v1/livestreams/{id}", m.deps.Protected(patch.ServeHTTP))

	del := http.Handler(m.deps.Handlers.DeleteLivestream)
	if m.deps.CSRFMiddleware != nil {
		del = m.deps.CSRFMiddleware(del)
	}
	mux.Method(http.MethodDelete, "/api/v1/livestreams/{id}", m.deps.Protected(del.ServeHTTP))
}
