package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
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

// VeloxModuleDeps is the narrow set of dependencies the Velox
// (service-to-service /internal/v1) module needs to mount its routes.
type VeloxModuleDeps struct {
	ExternalDestinationStore ExternalDestinationStore
	ExternalDeliveryStore    ExternalDeliveryStore
	WorkspaceStore           WorkspaceStore
	UserStore                UserStore
	VeloxAPIToken            string
	VeloxValidateRateLimiter *validateRateLimiter
}

// VeloxModule mounts the service-to-service /internal/v1 routes.
type VeloxModule struct {
	deps VeloxModuleDeps
}

func NewVeloxModule(deps VeloxModuleDeps) RouteModule {
	return &VeloxModule{deps: deps}
}

// Compile-time assertion: VeloxModule implements RouteModule.
var _ RouteModule = (*VeloxModule)(nil)

func (m *VeloxModule) Register(mux chi.Router) {
	if m.deps.ExternalDestinationStore == nil || m.deps.VeloxAPIToken == "" {
		return
	}
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate",
		internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleValidateInternalDestination)))
	if m.deps.ExternalDeliveryStore != nil {
	mux.Method(http.MethodPost, "/internal/v1/deliveries",
		internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleCreateInternalDelivery)))
	mux.Method(http.MethodGet, "/internal/v1/deliveries/{id}",
		internalVeloxAuthMiddleware(m.deps.VeloxAPIToken, http.HandlerFunc(m.handleGetInternalDelivery)))
	}
}

// VeloxBFFModuleDeps is the narrow set of dependencies the Velox
// BFF module needs to mount its routes.
type VeloxBFFModuleDeps struct {
	Client         veloxapi.Client
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

// MediaModuleDeps is the narrow set of dependencies the media module
// needs to mount its routes.
type MediaModuleDeps struct {
	RateLimitSvc     *services.RateLimitService
	Protected        func(http.HandlerFunc) http.HandlerFunc
	PresignMedia     http.HandlerFunc
	DriveImport      http.HandlerFunc
	DriveImportAsync http.HandlerFunc
	DriveBatchImport http.HandlerFunc
	DriveBatchImportV2 http.HandlerFunc
	DriveBatchV2Status http.HandlerFunc
	DriveBatchStatus http.HandlerFunc
	CompleteMedia    http.HandlerFunc
}

// MediaModule mounts the presigned-upload and Drive-import routes.
type MediaModule struct {
	deps MediaModuleDeps
}

func NewMediaModule(deps MediaModuleDeps) RouteModule {
	return &MediaModule{deps: deps}
}

// Compile-time assertion: MediaModule implements RouteModule.
var _ RouteModule = (*MediaModule)(nil)

func (m *MediaModule) Register(mux chi.Router) {
	var mediaPresignMw []func(http.Handler) http.Handler
	if m.deps.RateLimitSvc != nil {
		mediaPresignMw = append(mediaPresignMw, MediaPresignLimit(m.deps.RateLimitSvc))
	}
	mux.Method(http.MethodPost, "/api/v1/media/presign", chain(m.deps.Protected(m.deps.PresignMedia), mediaPresignMw...))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive", m.deps.Protected(m.deps.DriveImport))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/async", m.deps.Protected(m.deps.DriveImportAsync))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder", m.deps.Protected(m.deps.DriveBatchImport))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder/async", m.deps.Protected(m.deps.DriveBatchImportV2))
	mux.Method(http.MethodGet, "/api/v1/media/import/drive/folder/async/{id}", m.deps.Protected(m.deps.DriveBatchV2Status))
	mux.Method(http.MethodGet, "/api/v1/media/import/drive/batch/status", m.deps.Protected(m.deps.DriveBatchStatus))
	mux.Method(http.MethodPost, "/api/v1/media/{id}/complete", m.deps.Protected(m.deps.CompleteMedia))
}

// PublishingModuleDeps is the narrow set of dependencies the
// publishing module needs to mount its routes.
type PublishingModuleDeps struct {
	RateLimitSvc        *services.RateLimitService
	Protected           func(http.HandlerFunc) http.HandlerFunc
	CreatePost          http.HandlerFunc
	ListPosts           http.HandlerFunc
	ListPostsByWorkspace http.HandlerFunc
	GetPost             http.HandlerFunc
	PatchPost           http.HandlerFunc
	DeletePost          http.HandlerFunc
	PublishPost         http.HandlerFunc
	SchedulePost        http.HandlerFunc
	CancelPost          http.HandlerFunc
	RetryPost           http.HandlerFunc
	GetPostTargets      http.HandlerFunc
	AddPostTarget       http.HandlerFunc
	RetryTarget         http.HandlerFunc
	UploadCounts        http.HandlerFunc
	ListUploads         http.HandlerFunc
	ListUploadsByAccount http.HandlerFunc
	UploadsBatchByFolder http.HandlerFunc
	RescheduleUpload    http.HandlerFunc
	CancelUpload        http.HandlerFunc
}

// PublishingModule mounts post, post-target and upload-job routes.
type PublishingModule struct {
	deps PublishingModuleDeps
}

func NewPublishingModule(deps PublishingModuleDeps) RouteModule {
	return &PublishingModule{deps: deps}
}

// Compile-time assertion: PublishingModule implements RouteModule.
var _ RouteModule = (*PublishingModule)(nil)

func (m *PublishingModule) Register(mux chi.Router) {
	mux.Route("/api/v1/posts", func(sr chi.Router) {
		if m.deps.RateLimitSvc != nil {
			sr.Use(WorkspacePostLimit(m.deps.RateLimitSvc))
		}
		sr.Post("/", m.deps.Protected(m.deps.CreatePost))
		sr.Get("/", m.deps.Protected(m.deps.ListPosts))
		sr.Get("/workspace/{wid}", m.deps.Protected(m.deps.ListPostsByWorkspace))
		sr.Get("/{id}", m.deps.Protected(m.deps.GetPost))
		sr.Patch("/{id}", m.deps.Protected(m.deps.PatchPost))
		sr.Delete("/{id}", m.deps.Protected(m.deps.DeletePost))
		sr.Post("/{id}/publish", m.deps.Protected(m.deps.PublishPost))
		sr.Post("/{id}/schedule", m.deps.Protected(m.deps.SchedulePost))
		sr.Post("/{id}/cancel", m.deps.Protected(m.deps.CancelPost))
		sr.Post("/{id}/retry", m.deps.Protected(m.deps.RetryPost))
		sr.Get("/{id}/targets", m.deps.Protected(m.deps.GetPostTargets))
		sr.Post("/{id}/targets", m.deps.Protected(m.deps.AddPostTarget))
	})
	mux.Route("/api/v1/post-targets", func(sr chi.Router) {
		sr.Post("/{id}/retry", m.deps.Protected(m.deps.RetryTarget))
	})
	mux.Route("/api/v1/uploads", func(sr chi.Router) {
		sr.Get("/counts", m.deps.Protected(m.deps.UploadCounts))
		sr.Get("/", m.deps.Protected(m.deps.ListUploads))
		sr.Get("/by-account", m.deps.Protected(m.deps.ListUploadsByAccount))
		sr.Post("/batch/by-folder", m.deps.Protected(m.deps.UploadsBatchByFolder))
		sr.Patch("/{id}/reschedule", m.deps.Protected(m.deps.RescheduleUpload))
		sr.Delete("/{id}", m.deps.Protected(m.deps.CancelUpload))
	})
}

// AuthHandlers groups the HTTP handler functions used by the auth
// module. Keeping them in a nested struct keeps AuthModuleDeps readable.
type AuthHandlers struct {	Login                       http.HandlerFunc
	Callback                    http.HandlerFunc
	ExchangeCode                http.HandlerFunc
	Refresh                     http.HandlerFunc
	Logout              http.HandlerFunc
	LogoutAll           http.HandlerFunc
	ListSessions        http.HandlerFunc
	DeleteSession       http.HandlerFunc
	ListAccounts        http.HandlerFunc
	GetAccount          http.HandlerFunc
	GetAccountsPerformanceSummary http.HandlerFunc
	GetAccountPerformance http.HandlerFunc
	ValidateAccount     http.HandlerFunc
	ReconnectAccount    http.HandlerFunc
	DeleteAccount       http.HandlerFunc
	SyncAccount         http.HandlerFunc
	AccountContent      http.HandlerFunc
	UpdateAccount       http.HandlerFunc
	CreateWorkspace     http.HandlerFunc
	ListWorkspaces      http.HandlerFunc
	GetWorkspace        http.HandlerFunc
	DeleteWorkspace     http.HandlerFunc
	SwitchWorkspace     http.HandlerFunc
	AttachWorkspaceChannel http.HandlerFunc
	ListWorkspaceChannels http.HandlerFunc
	UpdateWorkspaceChannel http.HandlerFunc
	DetachWorkspaceChannel http.HandlerFunc
	ListGroups          http.HandlerFunc
	CreateGroup         http.HandlerFunc
	GetGroup            http.HandlerFunc
	UpdateGroup         http.HandlerFunc
	DeleteGroup         http.HandlerFunc
	ListGroupAccounts   http.HandlerFunc
	SetGroupAccounts    http.HandlerFunc
	CreateApiKey        http.HandlerFunc
	ListApiKeys         http.HandlerFunc
	GetApiKey           http.HandlerFunc
	DeleteApiKey        http.HandlerFunc
	RotateApiKey        http.HandlerFunc
}

// AuthModuleDeps is the narrow set of dependencies the auth module
// needs to mount its routes.
type AuthModuleDeps struct {
	AuthEmailSvc        AuthEmailStore
	TeamStore           TeamStore
	GroupStore          GroupStore
	WebhookStore        WebhookStore
	RateLimitSvc        *services.RateLimitService
	AuthMiddleware      func(http.Handler) http.Handler
	ApiKeyAuthMiddleware func(http.Handler) http.Handler
	Protected           func(http.HandlerFunc) http.HandlerFunc
	CsrfConfig          func() auth.CSRFConfig
	OAuthStartLimiter   func(http.Handler) http.Handler
	OAuthSessionRedirect func(http.HandlerFunc) http.HandlerFunc
	RegisterAuthEmailRoutes func()
	RegisterTeamRoutes  func()
	RegisterWebhookRoutes func()
	Handlers            AuthHandlers
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
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":      id.UserID(),
		"workspace_id": id.WorkspaceID(),
		"is_admin":     id.IsAdmin(),
	})
}

func (m *AuthModule) Register(mux chi.Router) {
	if m.deps.AuthEmailSvc != nil {
		m.deps.RegisterAuthEmailRoutes()
	}
	if m.deps.TeamStore != nil {
		m.deps.RegisterTeamRoutes()
	}

	mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login", m.deps.OAuthStartLimiter(http.HandlerFunc(m.deps.OAuthSessionRedirect(m.deps.Handlers.Login))))
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
	mux.Method(http.MethodDelete, "/api/v1/accounts/{id}", m.deps.Protected(m.deps.Handlers.DeleteAccount))
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
			sr.Post("/", m.deps.Protected(m.deps.Handlers.CreateGroup))
			sr.Get("/{id}", m.deps.Protected(m.deps.Handlers.GetGroup))
			sr.Patch("/{id}", m.deps.Protected(m.deps.Handlers.UpdateGroup))
			sr.Delete("/{id}", m.deps.Protected(m.deps.Handlers.DeleteGroup))
			sr.Get("/{id}/accounts", m.deps.Protected(m.deps.Handlers.ListGroupAccounts))
			sr.Put("/{id}/accounts", m.deps.Protected(m.deps.Handlers.SetGroupAccounts))
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
