package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/deliveries"
)

// VeloxModuleDeps is the narrow set of dependencies the Velox
// (service-to-service /internal/v1) module needs to mount its routes.
type VeloxModuleDeps struct {
	ExternalDestinationStore ExternalDestinationStore
	ExternalDeliveryStore    ExternalDeliveryStore
	WorkspaceStore           WorkspaceStore
	UserStore                UserStore
	// GroupStore backs the OPTIONAL POST
	// /internal/v1/destinations/resolve-target endpoint (group
	// expansion branch). When omitted, the route is not
	// registered (matches the postStore / workspaceStore
	// nil-guard pattern documented at router.go top). Wired in
	// internal/bootstrap/app.go via the same repository that
	// powers /api/v1/groups/* so the resolve-target handler
	// reuses the production GroupRepository.
	GroupStore               GroupStore
	YouTubeVideoEditStore    YouTubeVideoEditStore
	VeloxAPIToken            string
	VeloxValidateRateLimiter *validateRateLimiter
	// EditorBaseURL is the configured base URL of the separately
	// deployed InstaEditor. It is intentionally empty when the editor
	// is unavailable; handlers then return an empty editor_url rather
	// than falling back to the InstaEdit frontend or a fabricated host.
	EditorBaseURL string
}

// VeloxModule mounts the service-to-service /internal/v1 routes.
type VeloxModule struct {
	deps           VeloxModuleDeps
	targetResolver *deliveries.TargetResolver
}

func NewVeloxModule(deps VeloxModuleDeps) RouteModule {
	m := &VeloxModule{deps: deps}
	// Construct the unified TargetResolver at module-creation time
	// (not lazily) to avoid a data race on the resolver field under
	// concurrent requests. All deps are available at construction;
	// nil stores are handled fail-closed by the resolver itself.
	m.targetResolver = deliveries.NewTargetResolver(deliveries.TargetResolverDeps{
		DestinationStore: deps.ExternalDestinationStore,
		WorkspaceStore:   deps.WorkspaceStore,
		UserStore:        deps.UserStore,
		GroupStore:       deps.GroupStore,
	})
	return m
}

// resolver returns the pre-constructed TargetResolver shared across
// validate and resolve-target handlers, eliminating the duplicated
// workspace/account/binding/eligibility checks the audit flagged.
func (m *VeloxModule) resolver() *deliveries.TargetResolver {
	return m.targetResolver
}

// Compile-time assertion: VeloxModule implements RouteModule.
var _ RouteModule = (*VeloxModule)(nil)

func (m *VeloxModule) Register(mux chi.Router) {
	if m.deps.ExternalDestinationStore == nil || m.deps.VeloxAPIToken == "" {
		return
	}
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate",
		internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleValidateInternalDestination)))
	// Resolve-target is the body-based target-descriptor validator
	// (POST /internal/v1/destinations/resolve-target) — gated on
	// the per-feature nil-guard pattern: the route is registered
	// ONLY if GroupStore + WorkspaceStore + UserStore are all
	// wired. Production wiring (internal/bootstrap/app.go) sets
	// all three from the same *sql.DB-backed repositories, so the
	// guard is purely a dependency-injection signal.
	if m.deps.GroupStore != nil && m.deps.WorkspaceStore != nil && m.deps.UserStore != nil {
		mux.Method(http.MethodPost, "/internal/v1/destinations/resolve-target",
			internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleResolveTargetInternalDestination)))
	}
	if m.deps.ExternalDeliveryStore != nil {
		mux.Method(http.MethodPost, "/internal/v1/deliveries",
			internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleCreateInternalDelivery)))
		mux.Method(http.MethodGet, "/internal/v1/deliveries/{id}",
			internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleGetInternalDelivery)))
	}
	// Thumbnail-session auto-provisioner. Called by Velox after
	// PRIVATE_UPLOADED (state machine §10 in
	// docs/velox-instaedit-contract.md). Creates a
	// youtube_video_edits row with editor_session_id in the
	// ytedit_<uuid> format; the InstaEditor SPA reads it back via
	// GET /api/v1/youtube/editor-sessions/{id}.
	if m.deps.YouTubeVideoEditStore != nil {
		// Keep the route mounted when the editor URL is missing so the
		// authenticated caller receives an explicit 503 misconfiguration
		// response instead of an indistinguishable 404.
		mux.Method(http.MethodPost, "/internal/v1/thumbnail-sessions",
			internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleCreateThumbnailSession)))
	}
}

// --- Router thin wrappers (test compatibility) ------------------------------
//
// The following thin wrappers keep existing unit tests (which call the
// handlers directly on *Router) compiling while the public module
// constructors receive typed deps. They simply forward to a module
// instance built from the Router's current fields.
//
// TODO: These wrappers exist only for test compatibility. Migrate the
// affected tests to use the typed VeloxModule / IntegrationsModule
// constructors and then delete the wrappers. Do NOT add new production
// code here; new routes should use the typed modules.

func (r *Router) veloxModule() *VeloxModule {
	return NewVeloxModule(VeloxModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		ExternalDeliveryStore:    r.externalDeliveries,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		GroupStore:               r.groupStore,
		VeloxAPIToken:            r.veloxAPIToken,
		VeloxValidateRateLimiter: r.veloxValidateRateLimiter,
	}).(*VeloxModule)
}

func (r *Router) registerInternalVeloxRoutes() {
	r.veloxModule().Register(r.mux)
}

func (r *Router) handleValidateInternalDestination(w http.ResponseWriter, req *http.Request) {
	r.veloxModule().handleValidateInternalDestination(w, req)
}

func (r *Router) handleResolveTargetInternalDestination(w http.ResponseWriter, req *http.Request) {
	r.veloxModule().handleResolveTargetInternalDestination(w, req)
}

func (r *Router) handleCreateInternalDelivery(w http.ResponseWriter, req *http.Request) {
	r.veloxModule().handleCreateInternalDelivery(w, req)
}

func (r *Router) handleGetInternalDelivery(w http.ResponseWriter, req *http.Request) {
	r.veloxModule().handleGetInternalDelivery(w, req)
}
