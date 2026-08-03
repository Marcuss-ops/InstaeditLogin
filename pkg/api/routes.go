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
		YouTubeVideoEditStore:    r.youtubeVideoEditStore,
		VeloxAPIToken:            r.veloxAPIToken,
		VeloxValidateRateLimiter: r.veloxValidateRateLimiter,
		EditorBaseURL:            r.editorURL,
	}))
	reg.Register(NewVeloxBFFModule(VeloxBFFModuleDeps{
		Client:         r.veloxBFFClient,
		JobRegistry:    r.veloxJobRegistry,
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
	// Marketing strategy-call funnel (POST /api/v1/booking_events).
	// Anonymous (no JWT, no CSRF); layered with per-IP rate-limit +
	// same-origin gate + SQL-level idempotency. Mounted AFTER the
	// Integrations module so two anonymous surfaces don't share
	// middleware-ordering accidents. See BookingEventsModule for
	// the full security model.
	reg.Register(NewBookingEventsModule(BookingEventsModuleDeps{
		Store:          r.bookingEventStore,
		RateLimit:      BookingEventRateLimitIfConfigured(r.rateLimitSvc, r.trustedProxies),
		AllowedOrigins: r.allowedOrigin,
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
			ListGroupsWithAccounts:        r.handleListGroupsWithAccounts,
			CreateGroup:                   r.handleCreateGroup,
			GetGroup:                      r.handleGetGroup,
			UpdateGroup:                   r.handleUpdateGroup,
			DeleteGroup:                   r.handleDeleteGroup,
			ListGroupAccounts:             r.handleListGroupAccounts,
			SetGroupAccounts:              r.handleSetGroupAccounts,
			UpdateGroupSettings:           r.handleUpdateGroupSettings,
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
		// Taglio 5.1 step 2 — wires the polling single-target
		// GET /api/v1/post-targets/{id}. Mirrors the existing
		// handleGetPostTargets handler resolution.
		GetPostTarget:        r.handleGetSinglePostTarget,
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

	// YouTube thumbnail editor sessions. Protected via the standard
	// session cookie/JWT chain and CSRF (when configured).
	var createEditorSessionHandler http.Handler = http.HandlerFunc(r.handleCreateYouTubeEditorSession)
	if r.csrfMiddleware != nil {
		createEditorSessionHandler = r.csrfMiddleware(createEditorSessionHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions", r.protected(createEditorSessionHandler.ServeHTTP))

	// GET /api/v1/youtube/editor-sessions — dashboard "code da modificare"
	// list. Returns the non-terminal YouTube video edit sessions in the
	// caller's workspace (with optional ?account_id, ?status, ?limit
	// filters). Read-only, no CSRF (GET exempt by spec), no body.
	// ?workspace_id is REQUIRED; the handler refuses to enumerate rows
	// without an explicit workspace scope (cross-tenant probe prevention).
	var listEditorSessionsHandler http.Handler = http.HandlerFunc(r.handleListYouTubeEditorSessions)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions", r.protected(listEditorSessionsHandler.ServeHTTP))

	var updateEditorSessionHandler http.Handler = http.HandlerFunc(r.handleUpdateYouTubeEditorSession)
	if r.csrfMiddleware != nil {
		updateEditorSessionHandler = r.csrfMiddleware(updateEditorSessionHandler)
	}
	r.mux.Method(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}", r.protected(updateEditorSessionHandler.ServeHTTP))

	// P0#5: project-centric entry points for the Dark Editor. The
	// Dark Editor holds only velox_project_id in its URL; it must be
	// able to (a) fetch the session row + (b) trigger a publish
	// without first POSTing /editor-sessions to discover the
	// session_id.
	var getEditorSessionByProjectHandler http.Handler = http.HandlerFunc(r.handleGetYouTubeEditorSessionByProject)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}", r.protected(getEditorSessionByProjectHandler.ServeHTTP))

	var publishEditorSessionByProjectHandler http.Handler = http.HandlerFunc(r.handlePublishYouTubeEditorSessionByProject)
	if r.csrfMiddleware != nil {
		publishEditorSessionByProjectHandler = r.csrfMiddleware(publishEditorSessionByProjectHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish", r.protected(publishEditorSessionByProjectHandler.ServeHTTP))

	// P2 — Dark Editor auto-save endpoint. The Dark Editor PUTs the
	// form values on debounce + on-blur so an operator who closed
	// the tab mid-edit can resume the same form state on reload.
	// CSRF semantics match the publish endpoint above (middleware
	// falls through to a passthrough when not wired).
	var saveDraftByProjectHandler http.Handler = http.HandlerFunc(r.handleSaveEditorSessionDraftByProject)
	if r.csrfMiddleware != nil {
		saveDraftByProjectHandler = r.csrfMiddleware(saveDraftByProjectHandler)
	}
	r.mux.Method(http.MethodPut, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft", r.protected(saveDraftByProjectHandler.ServeHTTP))

	// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata
	// NVIDIA AI metadata generation. Read-only step BEFORE publish:
	// generates title, description, tags, languages and translations
	// via NVIDIA API. The operator reviews + optionally edits the
	// generated values before submitting through /publish. CSRF
	// semantics match the publish endpoint.
	var generateNVIDIAMetadataHandler http.Handler = http.HandlerFunc(r.handleGenerateNVIDIAMetadata)
	if r.csrfMiddleware != nil {
		generateNVIDIAMetadataHandler = r.csrfMiddleware(generateNVIDIAMetadataHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata", r.protected(generateNVIDIAMetadataHandler.ServeHTTP))

	var publishEditorSessionHandler http.Handler = http.HandlerFunc(r.handlePublishYouTubeEditorSession)
	if r.csrfMiddleware != nil {
		publishEditorSessionHandler = r.csrfMiddleware(publishEditorSessionHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/{id}/publish", r.protected(publishEditorSessionHandler.ServeHTTP))

	// GET /api/v1/youtube/editor-sessions/{id} — session-id-keyed
	// companion to GET /by-project/{velox_project_id}. Powers the
	// dark-editor SPA after the auto-provisioner
	// (POST /internal/v1/thumbnail-sessions) returns a fresh
	// ytedit_<uuid> that the SPA reads back through this endpoint.
	// Read-only; no CSRF (GET exempt by spec).
	var getEditorSessionByIDHandler http.Handler = http.HandlerFunc(r.handleGetYouTubeEditorSessionByID)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/{id}", r.protected(getEditorSessionByIDHandler.ServeHTTP))

	// Direct handoff endpoint (Blocco #5 P0 #4): callers (typically the
	// dark editor SPA after uploading the rendered thumbnail to
	// InstaEdit storage) supply a verified media_assets.id and the
	// handler atomically links it to the session. Coexists with the
	// PATCH-by-project flow (which goes through Velox) — the direct
	// handoff path skips the Velox roundtrip for the "thumbnail
	// already uploaded" case.
	var attachThumbnailHandler http.Handler = http.HandlerFunc(r.handleAttachThumbnailToEditorSession)
	if r.csrfMiddleware != nil {
		attachThumbnailHandler = r.csrfMiddleware(attachThumbnailHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/{id}/thumbnail", r.protected(attachThumbnailHandler.ServeHTTP))

	// GET /api/v1/groups/{group_id}/youtube/videos — dashboard card
	// grid. Read-only, no CSRF (GET exempt). Aggregates the
	// YouTube-listing per channel for every account in the group (and
	// its sub-groups when ?include_subgroups=true), joined with the
	// existing editor_sessions for each (account, video) tuple.
	var listGroupYouTubeVideosHandler http.Handler = http.HandlerFunc(r.handleListGroupYouTubeVideos)
	r.mux.Method(http.MethodGet, "/api/v1/groups/{group_id}/youtube/videos", r.protected(listGroupYouTubeVideosHandler.ServeHTTP))

	// Durable, idempotent batch application for private YouTube thumbnails.
	var createYouTubeThumbnailBatchHandler http.Handler = http.HandlerFunc(r.handleCreateYouTubeThumbnailBatch)
	if r.csrfMiddleware != nil {
		createYouTubeThumbnailBatchHandler = r.csrfMiddleware(createYouTubeThumbnailBatchHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/thumbnail-batches", r.protected(createYouTubeThumbnailBatchHandler.ServeHTTP))
	var getYouTubeThumbnailBatchHandler http.Handler = http.HandlerFunc(r.handleGetYouTubeThumbnailBatch)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/thumbnail-batches/{batch_id}", r.protected(getYouTubeThumbnailBatchHandler.ServeHTTP))

	// Livestream module — configuration CRUD base. A POST creates a
	// live CONFIGURATION in state draft; the state machine
	// (desired_state/actual_state) is worker-owned and the control
	// endpoints (prepare/start/stop/restart) land with the encoder
	// worker. GET /api/v1/livestreams returns the workspace's rows as
	// {items: [...]} — the sidebar badge counts actual_state == "live".
	var listLivestreamsHandler http.Handler = http.HandlerFunc(r.handleListLivestreams)
	r.mux.Method(http.MethodGet, "/api/v1/livestreams", r.protected(listLivestreamsHandler.ServeHTTP))

	// GET /api/v1/livestreams/channels — creation-wizard preflight.
	// Registered before /{id} so the static segment wins.
	var listLivestreamChannelsHandler http.Handler = http.HandlerFunc(r.handleListLivestreamChannels)
	r.mux.Method(http.MethodGet, "/api/v1/livestreams/channels", r.protected(listLivestreamChannelsHandler.ServeHTTP))

	var createLivestreamHandler http.Handler = http.HandlerFunc(r.handleCreateLivestream)
	if r.csrfMiddleware != nil {
		createLivestreamHandler = r.csrfMiddleware(createLivestreamHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/livestreams", r.protected(createLivestreamHandler.ServeHTTP))

	var getLivestreamHandler http.Handler = http.HandlerFunc(r.handleGetLivestream)
	r.mux.Method(http.MethodGet, "/api/v1/livestreams/{id}", r.protected(getLivestreamHandler.ServeHTTP))

	var patchLivestreamHandler http.Handler = http.HandlerFunc(r.handlePatchLivestream)
	if r.csrfMiddleware != nil {
		patchLivestreamHandler = r.csrfMiddleware(patchLivestreamHandler)
	}
	r.mux.Method(http.MethodPatch, "/api/v1/livestreams/{id}", r.protected(patchLivestreamHandler.ServeHTTP))

	var deleteLivestreamHandler http.Handler = http.HandlerFunc(r.handleDeleteLivestream)
	if r.csrfMiddleware != nil {
		deleteLivestreamHandler = r.csrfMiddleware(deleteLivestreamHandler)
	}
	r.mux.Method(http.MethodDelete, "/api/v1/livestreams/{id}", r.protected(deleteLivestreamHandler.ServeHTTP))

	// Blocco Carosello — unified pipeline view endpoint. Aggregates
	// Drive + storage + per-target YouTube publish + Velox editor
	// state into a single response that the SPA timeline UI consumes
	// (GET /api/v1/content/{id}/pipeline). Read-only; no CSRF needed
	// — GET requests are exempt by spec.
	r.mux.Method(http.MethodGet, "/api/v1/content/{content_id}/pipeline", r.protected(http.HandlerFunc(r.handleGetContentPipeline).ServeHTTP))

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
