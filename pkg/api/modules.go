package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// RouteModule is the shared contract every bounded-context module
// implements.  The Router acts as the registry/resolver: it owns the
// shared dependencies and passes its chi mux to each module.
type RouteModule interface {
	Register(mux chi.Router)
}

// AdminModule mounts the operator dashboard routes under /admin/*.
// Registration is a no-op when the Router has no admin store wired.
type AdminModule struct {
	r *Router
}

func NewAdminModule(r *Router) RouteModule {
	return &AdminModule{r: r}
}

func (m *AdminModule) Register(mux chi.Router) {
	if m.r.adminStore == nil {
		return
	}
	mux.Method(http.MethodGet, "/admin/channels", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminChannels)))
	mux.Method(http.MethodGet, "/admin/channels.csv", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminChannelsCSV)))
	mux.Method(http.MethodGet, "/admin/queue", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminQueue)))
	mux.Method(http.MethodGet, "/admin/queue.csv", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminQueueCSV)))
	mux.Method(http.MethodGet, "/admin/upload_jobs/dead_letter", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminUploadJobsDeadLetter)))
	mux.Method(http.MethodGet, "/admin/upload_jobs/dead_letter.csv", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminUploadJobsDeadLetterCSV)))
	mux.Method(http.MethodGet, "/admin/health", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminHealth)))
	mux.Method(http.MethodGet, "/admin/health.csv", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminHealthCSV)))
	mux.Method(http.MethodPost, "/admin/channels/import-csv", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminImportChannelsCSV)))
	mux.Method(http.MethodGet, "/admin/channels/pending", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminPendingChannels)))
	mux.Method(http.MethodGet, "/admin/youtube/fleet_readiness", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminYouTubeFleetReadiness)))
	mux.Method(http.MethodPost, "/admin/channels/{channel_id}/connect-link", adminAuthMiddleware(http.HandlerFunc(m.r.handleAdminChannelConnectLink)))
}

// VeloxModule mounts the service-to-service /internal/v1 routes.
type VeloxModule struct {
	r *Router
}

func NewVeloxModule(r *Router) RouteModule {
	return &VeloxModule{r: r}
}

func (m *VeloxModule) Register(mux chi.Router) {
	m.r.registerInternalVeloxRoutes()
}

// BillingModule mounts billing and Stripe webhook routes.  Registration
// is a no-op when the Router has no billing service wired.
type BillingModule struct {
	r *Router
}

func NewBillingModule(r *Router) RouteModule {
	return &BillingModule{r: r}
}

func (m *BillingModule) Register(mux chi.Router) {
	if m.r.billingSvc == nil {
		return
	}
	m.r.registerBillingRoutes()
}

// MediaModule mounts the presigned-upload and Drive-import routes.
type MediaModule struct {
	r *Router
}

func NewMediaModule(r *Router) RouteModule {
	return &MediaModule{r: r}
}

func (m *MediaModule) Register(mux chi.Router) {
	var mediaPresignMw []func(http.Handler) http.Handler
	if m.r.rateLimitSvc != nil {
		mediaPresignMw = append(mediaPresignMw, MediaPresignLimit(m.r.rateLimitSvc))
	}
	mux.Method(http.MethodPost, "/api/v1/media/presign", chain(m.r.protected(m.r.handlePresignMedia), mediaPresignMw...))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive", m.r.protected(m.r.handleDriveImport))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/async", m.r.protected(m.r.handleDriveImportAsync))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder", m.r.protected(m.r.handleDriveBatchImport))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder/async", m.r.protected(m.r.handleDriveBatchImportV2))
	mux.Method(http.MethodGet, "/api/v1/media/import/drive/folder/async/{id}", m.r.protected(m.r.handleDriveBatchV2Status))
	mux.Method(http.MethodGet, "/api/v1/media/import/drive/batch/status", m.r.protected(m.r.handleDriveBatchStatus))
	mux.Method(http.MethodPost, "/api/v1/media/{id}/complete", m.r.protected(m.r.handleCompleteMedia))
}

// PublishingModule mounts post, post-target and upload-job routes.
type PublishingModule struct {
	r *Router
}

func NewPublishingModule(r *Router) RouteModule {
	return &PublishingModule{r: r}
}

func (m *PublishingModule) Register(mux chi.Router) {
	mux.Route("/api/v1/posts", func(sr chi.Router) {
		if m.r.rateLimitSvc != nil {
			sr.Use(WorkspacePostLimit(m.r.rateLimitSvc))
		}
		sr.Post("/", m.r.protected(m.r.handleCreatePost))
		sr.Get("/", m.r.protected(m.r.handleListPosts))
		sr.Get("/workspace/{wid}", m.r.protected(m.r.handleListByWorkspace))
		sr.Get("/{id}", m.r.protected(m.r.handleGetPost))
		sr.Patch("/{id}", m.r.protected(m.r.handlePatchPost))
		sr.Delete("/{id}", m.r.protected(m.r.handleDeletePost))
		sr.Post("/{id}/publish", m.r.protected(m.r.handlePublishPostID))
		sr.Post("/{id}/schedule", m.r.protected(m.r.handleSchedulePost))
		sr.Post("/{id}/cancel", m.r.protected(m.r.handleCancelPost))
		sr.Post("/{id}/retry", m.r.protected(m.r.handleRetryPost))
		sr.Get("/{id}/targets", m.r.protected(m.r.handleGetPostTargets))
		sr.Post("/{id}/targets", m.r.protected(m.r.handleAddTarget))
	})
	mux.Route("/api/v1/post-targets", func(sr chi.Router) {
		sr.Post("/{id}/retry", m.r.protected(m.r.handleRetryTarget))
	})
	mux.Route("/api/v1/uploads", func(sr chi.Router) {
		sr.Get("/counts", m.r.protected(m.r.handleUploadCounts))
		sr.Get("/", m.r.protected(m.r.handleListUploads))
		sr.Get("/by-account", m.r.protected(m.r.handleListUploadsByAccount))
		sr.Post("/batch/by-folder", m.r.protected(m.r.handleUploadsBatchByFolder))
		sr.Patch("/{id}/reschedule", m.r.protected(m.r.handleRescheduleUpload))
		sr.Delete("/{id}", m.r.protected(m.r.handleCancelUpload))
	})
}

// AuthModule mounts authentication, sessions, accounts, workspaces,
// groups, API keys, team and webhook routes.  It is the broadest module
// because all of these surfaces are part of the user/workspace identity
// context.
type AuthModule struct {
	r *Router
}

func NewAuthModule(r *Router) RouteModule {
	return &AuthModule{r: r}
}

func (m *AuthModule) Register(mux chi.Router) {
	if m.r.authEmailSvc != nil {
		m.r.registerAuthEmailRoutes()
	}
	if m.r.teamStore != nil {
		m.r.registerTeamRoutes()
	}

	mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login", OAuthStartLimitIfConfigured(m.r.rateLimitSvc, m.r.trustedProxies)(http.HandlerFunc(m.r.oauthSessionRedirect(m.r.handleLogin))))
	mux.Method(http.MethodGet, "/api/v1/auth/{provider}/callback", http.HandlerFunc(m.r.oauthSessionRedirect(m.r.handleCallback)))
	mux.Method(http.MethodPost, "/api/v1/auth/exchange", http.HandlerFunc(m.r.handleExchangeCode))
	mux.Method(http.MethodGet, "/api/v1/auth/me", m.r.protected(m.r.handleMe))
	mux.Method(http.MethodPost, "/api/v1/auth/refresh", http.HandlerFunc(m.r.handleRefresh))
	mux.Method(http.MethodPost, "/api/v1/auth/logout", http.HandlerFunc(m.r.handleLogout))
	mux.Method(http.MethodPost, "/api/v1/auth/logout-all", m.r.protected(m.r.handleLogoutAll))
	mux.Method(http.MethodGet, "/api/v1/auth/sessions", m.r.protected(m.r.handleListSessions))
	mux.Method(http.MethodDelete, "/api/v1/auth/sessions/{id}", m.r.protected(m.r.handleDeleteSession))

	mux.Method(http.MethodGet, "/api/v1/accounts", m.r.protected(m.r.handleListAccounts))
	mux.Method(http.MethodGet, "/api/v1/accounts/{id}", m.r.protected(m.r.handleGetAccount))
	mux.Method(http.MethodGet, "/api/v1/accounts/performance/summary", m.r.protected(m.r.handleGetAccountsPerformanceSummary))
	mux.Method(http.MethodGet, "/api/v1/accounts/{id}/performance", m.r.protected(m.r.handleGetAccountPerformance))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/validate", m.r.protected(m.r.handleValidateAccount))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/reconnect", m.r.protected(m.r.handleReconnectAccount))
	mux.Method(http.MethodDelete, "/api/v1/accounts/{id}", m.r.protected(m.r.handleDeleteAccount))
	mux.Method(http.MethodPost, "/api/v1/accounts/{id}/sync", m.r.protected(m.r.handleSyncAccount))
	mux.Method(http.MethodGet, "/api/v1/accounts/{id}/content", m.r.protected(m.r.handleAccountContent))
	mux.Method(http.MethodPatch, "/api/v1/accounts/{id}", m.r.protected(m.r.handleUpdateAccount))

	mux.Route("/api/v1/workspaces", func(sr chi.Router) {
		sr.Post("/", m.r.protected(m.r.handleCreateWorkspace))
		sr.Get("/", m.r.protected(m.r.handleListWorkspaces))
		sr.Get("/{id}", m.r.protected(m.r.handleGetWorkspace))
		sr.Delete("/{id}", m.r.protected(m.r.handleDeleteWorkspace))
		sr.Post("/{id}/switch", m.r.protected(m.r.handleSwitchWorkspace))
		sr.Post("/{id}/channels", m.r.protected(m.r.handleAttachWorkspaceChannel))
		sr.Get("/{id}/channels", m.r.protected(m.r.handleListWorkspaceChannels))
		sr.Patch("/{id}/channels/{accountId}", m.r.protected(m.r.handleUpdateWorkspaceChannel))
		sr.Delete("/{id}/channels/{accountId}", m.r.protected(m.r.handleDetachWorkspaceChannel))
	})

	if m.r.groupStore != nil {
		mux.Route("/api/v1/groups", func(sr chi.Router) {
			sr.Get("/", m.r.protected(m.r.handleListGroups))
			sr.Post("/", m.r.protected(m.r.handleCreateGroup))
			sr.Get("/{id}", m.r.protected(m.r.handleGetGroup))
			sr.Patch("/{id}", m.r.protected(m.r.handleUpdateGroup))
			sr.Delete("/{id}", m.r.protected(m.r.handleDeleteGroup))
			sr.Get("/{id}/accounts", m.r.protected(m.r.handleListGroupAccounts))
			sr.Put("/{id}/accounts", m.r.protected(m.r.handleSetGroupAccounts))
		})
	}

	mux.Route("/api/v1/api-keys", func(sr chi.Router) {
		sr.Use(func(next http.Handler) http.Handler {
			return auth.NewCSRF(m.r.csrfConfig(), next)
		})
		if m.r.apiKeyAuth != nil {
			sr.Use(func(next http.Handler) http.Handler {
				return m.r.apiKeyAuth.Middleware(next)
			})
		}
		sr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				m.r.auth.Middleware(next).ServeHTTP(w, req)
			})
		})
		if m.r.rateLimitSvc != nil {
			sr.Use(APIKeyReadLimit(m.r.rateLimitSvc))
		}
		sr.Post("/", m.r.handleCreateApiKey)
		sr.Get("/", m.r.handleListApiKeys)
		sr.Get("/{id}", m.r.handleGetApiKey)
		sr.Delete("/{id}", m.r.handleDeleteApiKey)
		sr.Post("/{id}/rotate", m.r.handleRotateApiKey)
	})

	if m.r.webhookStore != nil {
		m.r.registerWebhookRoutes()
	}
}
