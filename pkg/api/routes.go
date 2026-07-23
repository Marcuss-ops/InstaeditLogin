package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Setup wires the application's route table.  Route registration is
// now delegated to bounded-context modules; this method only keeps the
// top-level cross-cutting concerns (health/readiness, metrics, CORS,
// rate-limiting, logging, recovery and security headers).
func (r *Router) Setup() http.Handler {
	r.mux = chi.NewRouter()

	// Build the route registry in the order the modules should mount.
	// This is the single place that decides which modules are wired
	// into the router; individual modules only declare their routes.
	reg := NewRouteRegistry()
	reg.Register(NewAdminModule(AdminModuleDeps{
		AdminStore:            r.adminStore,
		AuthManager:           r.auth,
		UserStore:             r.userRepo,
		WorkspaceStore:        r.workspaceStore,
		Capabilities:          r.capabilities,
		ConnectLinkNonceStore: r.connectLinkNonceStore,
	}))
	reg.Register(NewVeloxModule(VeloxModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		ExternalDeliveryStore:    r.externalDeliveries,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		VeloxAPIToken:            r.veloxAPIToken,
		VeloxValidateRateLimiter: r.veloxValidateRateLimiter,
	}))
	reg.Register(NewVeloxBFFModule(VeloxBFFModuleDeps{
		Client:         r.veloxBFFClient,
		AuthMiddleware: r.veloxBFFAuthMiddleware,
		CSRFMiddleware: r.veloxBFFCSRFMiddleware,
	}))
	reg.Register(NewIntegrationsModule(IntegrationsModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		AuditLogStore:            r.auditLogStore,
		AuthMiddleware:           r.authMiddleware,
		CSRFMiddleware:           r.csrfMiddleware,
	}))

	// Public / health probes are mounted before the auth module so the
	// route table stays easy to scan top-down.
	r.mux.Method(http.MethodGet, "/api/v1/health", http.HandlerFunc(r.handleHealth))

	r.mux.Method(http.MethodGet, "/ready", http.HandlerFunc(r.handleReady))

	var apiKeyAuthMw func(http.Handler) http.Handler
	if r.apiKeyAuth != nil {
		apiKeyAuthMw = r.apiKeyAuth.Middleware
	}
	var authMiddleware func(http.Handler) http.Handler
	if r.auth != nil {
		authMiddleware = r.auth.Middleware
	}
	reg.Register(NewAuthModule(AuthModuleDeps{
		AuthEmailSvc:            r.authEmailSvc,
		TeamStore:               r.teamStore,
		GroupStore:              r.groupStore,
		WebhookStore:            r.webhookStore,
		RateLimitSvc:            r.rateLimitSvc,
		AuthMiddleware:          authMiddleware,
		ApiKeyAuthMiddleware:    apiKeyAuthMw,
		Protected:               r.protected,
		CsrfConfig:              r.csrfConfig,
		OAuthStartLimiter:       OAuthStartLimitIfConfigured(r.rateLimitSvc, r.trustedProxies),
		OAuthSessionRedirect:    r.oauthSessionRedirect,
		RegisterAuthEmailRoutes: r.registerAuthEmailRoutes,
		RegisterTeamRoutes:      r.registerTeamRoutes,
		RegisterWebhookRoutes:   r.registerWebhookRoutes,
		Handlers: AuthHandlers{Login: r.handleLogin,
			Callback:                      r.handleCallback,
			ExchangeCode:                  r.handleExchangeCode,
			Refresh:                       r.handleRefresh,
			Logout:                        r.handleLogout,
			LogoutAll:                     r.handleLogoutAll,
			ListSessions:                  r.handleListSessions,
			DeleteSession:                 r.handleDeleteSession,
			ListAccounts:                  r.handleListAccounts,
			GetAccount:                    r.handleGetAccount,
			GetAccountsPerformanceSummary: r.handleGetAccountsPerformanceSummary,
			GetAccountPerformance:         r.handleGetAccountPerformance,
			ValidateAccount:               r.handleValidateAccount,
			ReconnectAccount:              r.handleReconnectAccount,
			DeleteAccount:                 r.handleDeleteAccount,
			SyncAccount:                   r.handleSyncAccount,
			AccountContent:                r.handleAccountContent,
			UpdateAccount:                 r.handleUpdateAccount,
			CreateWorkspace:               r.handleCreateWorkspace,
			ListWorkspaces:                r.handleListWorkspaces,
			GetWorkspace:                  r.handleGetWorkspace,
			DeleteWorkspace:               r.handleDeleteWorkspace,
			SwitchWorkspace:               r.handleSwitchWorkspace,
			AttachWorkspaceChannel:        r.handleAttachWorkspaceChannel,
			ListWorkspaceChannels:         r.handleListWorkspaceChannels,
			UpdateWorkspaceChannel:        r.handleUpdateWorkspaceChannel,
			DetachWorkspaceChannel:        r.handleDetachWorkspaceChannel,
			ListGroups:                    r.handleListGroups,
			CreateGroup:                   r.handleCreateGroup,
			GetGroup:                      r.handleGetGroup,
			UpdateGroup:                   r.handleUpdateGroup,
			DeleteGroup:                   r.handleDeleteGroup,
			ListGroupAccounts:             r.handleListGroupAccounts,
			SetGroupAccounts:              r.handleSetGroupAccounts,
			CreateApiKey:                  r.handleCreateApiKey,
			ListApiKeys:                   r.handleListApiKeys,
			GetApiKey:                     r.handleGetApiKey,
			DeleteApiKey:                  r.handleDeleteApiKey,
			RotateApiKey:                  r.handleRotateApiKey,
		},
	}))
	reg.Register(NewMediaModule(MediaModuleDeps{
		RateLimitSvc:       r.rateLimitSvc,
		Protected:          r.protected,
		PresignMedia:       r.handlePresignMedia,
		DriveImport:        r.handleDriveImport,
		DriveImportAsync:   r.handleDriveImportAsync,
		DriveBatchImport:   r.handleDriveBatchImport,
		DriveBatchImportV2: r.handleDriveBatchImportV2,
		DriveBatchV2Status: r.handleDriveBatchV2Status,
		DriveBatchStatus:   r.handleDriveBatchStatus,
		CompleteMedia:      r.handleCompleteMedia,
	}))
	reg.Register(NewPublishingModule(PublishingModuleDeps{
		RateLimitSvc:         r.rateLimitSvc,
		Protected:            r.protected,
		CreatePost:           r.handleCreatePost,
		ListPosts:            r.handleListPosts,
		ListPostsByWorkspace: r.handleListByWorkspace,
		GetPost:              r.handleGetPost,
		PatchPost:            r.handlePatchPost,
		DeletePost:           r.handleDeletePost,
		PublishPost:          r.handlePublishPostID,
		SchedulePost:         r.handleSchedulePost,
		CancelPost:           r.handleCancelPost,
		RetryPost:            r.handleRetryPost,
		GetPostTargets:       r.handleGetPostTargets,
		AddPostTarget:        r.handleAddTarget,
		RetryTarget:          r.handleRetryTarget,
		UploadCounts:         r.handleUploadCounts,
		ListUploads:          r.handleListUploads,
		ListUploadsByAccount: r.handleListUploadsByAccount,
		UploadsBatchByFolder: r.handleUploadsBatchByFolder,
		RescheduleUpload:     r.handleRescheduleUpload,
		CancelUpload:         r.handleCancelUpload,
	}))
	reg.Register(NewBillingModule(BillingModuleDeps{
		BillingSvc:     r.billingSvc,
		AuthMiddleware: r.auth.Middleware,
		FrontendURL:    r.frontendURL,
	}))

	r.mux.Method(http.MethodGet, "/api/v1/metrics", http.HandlerFunc(r.handleMetrics))

	// Mount every registered module against the chi mux.
	reg.Mount(r.mux)

	// FASE 1.2: rate limiter is the outermost middleware so it
	// protects ALL routes (public + protected) from abuse.
	//
	// Blocco #5.3 — the panic-catching recovery wrapper sits
	// OUTSIDE the rate-limit + CORS + logging chain so panics
	// inside ANY of those middleware bodies (not just the
	// terminal handler) get caught. The wrapper is a no-op for
	// happy-path requests (passthrough to rate-limiter) and
	// recovers + writes 500 only on panic.
	// securityHeaders is OUTSIDE the rate-limit + CORS + logging chain
	// so its decisions are independent of those middlewares' behaviour.
	// It is INSIDE recover so a panic inside its handler still gets
	// caught + logged + translated to a 500.
	rateLimitAndBelow := r.securityHeadersMiddleware(
		r.rateLimiter.middleware(r.corsMiddleware(r.requestIDMiddleware(r.loggingMiddleware(r.mux)))),
	)
	return r.recoverMiddleware(rateLimitAndBelow)
}

// requestIDMiddleware ensures every request carries a request_id in its
// context. It reuses an incoming X-Request-ID header when present, or
// generates a fresh crypto-random id otherwise, and mirrors it back in
// the X-Request-ID response header so clients can correlate logs with
// the generic 500 messages they receive.
func (r *Router) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := req.Header.Get("X-Request-ID")
		if !isValidRequestID(id) {
			id = generateRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, withRequestID(req, id))
	})
}
