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
	r.setupMu.Lock()
	defer r.setupMu.Unlock()

	r.mux = chi.NewRouter()
	// Register inside chi so the route context is populated before metrics
	// labels are derived. Applying this middleware outside the mux would
	// observe only the raw URL and lose the bounded route pattern.
	r.mux.Use(r.metricsMiddleware)

	// Build the route registry in the order the modules should mount.
	// This is the single place that decides which modules are wired
	// into the router; individual modules only declare their routes.
	reg := NewRouteRegistry()
	reg.Register(NewAdminModule(AdminModuleDeps{
		AdminStore:                 r.adminStore,
		AuthManager:                r.auth,
		UserStore:                  r.userRepo,
		WorkspaceStore:             r.workspaceStore,
		Capabilities:               r.capabilities,
		ConnectLinkNonceStore:      r.connectLinkNonceStore,
		YouTubeOAuthClientRegistry: r.youtubeOAuthClientRegistry,
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
	reg.Register(NewEditorBFFModule(EditorBFFModuleDeps{
		Client:                r.editorBFFClient,
		AuthMiddleware:        r.editorBFFAuthMiddleware,
		CSRFMiddleware:        r.editorBFFCSRFMiddleware,
		YouTubeVideoEditStore: r.youtubeVideoEditStore,
		WorkspaceStore:        r.workspaceStore,
		TeamStore:             r.teamStore,
		LaunchTokenIssuer:     r.editorLaunchTokenIssuer,
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

	// Agent Gateway run bookkeeping (/api/v1/agent/runs*). The gateway
	// records every run + tool step through this REST surface; it never
	// touches the database directly. Protected via the standard
	// JWT/API-key chain; workspace_id + actor_key_id are derived from
	// the authenticated identity. When the store is nil the module
	// registers no routes.
	reg.Register(NewAgentRunsModule(AgentRunsModuleDeps{
		Store:     r.agentRunStore,
		Protected: r.protected,
	}))

	// Public / health probes are mounted before the auth module so the
	// route table stays easy to scan top-down. Their small module owns
	// only route registration; the existing Router handlers keep the
	// response logic and dependencies unchanged.
	reg.Register(NewHealthModule(HealthModuleDeps{
		Health: r.handleHealth,
		Ready:  r.handleReady,
	}))

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
			GetDashboardAnalytics:         r.handleGetDashboardAnalytics,
			ValidateAccount:               r.handleValidateAccount,
			ReconnectAccount:              r.handleReconnectAccount,
			DeleteAccount:                 r.handleDeleteAccount,
			DisconnectAccount:             r.handleDisconnectAccount,
			DeleteAccountData:             r.handleDeleteAccountData,
			DeleteOAuthGrant:              r.handleDeleteOAuthGrant,
			SyncAccount:                   r.handleSyncAccount,
			SyncAllAccounts:               r.handleSyncAllAccounts,
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
			CreateApiKey:                  r.handleCreateApiKey,
			ListApiKeys:                   r.handleListApiKeys,
			GetApiKey:                     r.handleGetApiKey,
			DeleteApiKey:                  r.handleDeleteApiKey,
			RotateApiKey:                  r.handleRotateApiKey,
		},
	}))
	// Hierarchical-groups bounded context (/api/v1/groups/*). Owns
	// its own GroupStore dependency + route table; extracted from
	// AuthModule so the identity module stops depending on the
	// groups store. When GroupStore is nil the module registers no
	// routes (404 at the chi level), preserving the pre-module
	// WithGroupStore mount contract (see
	// TestWithGroupStore_RouteMounting).
	reg.Register(NewGroupsModule(GroupsModuleDeps{
		GroupStore: r.groupStore,
		Protected:  r.protected,
		Handlers: GroupsHandlers{
			ListGroups:             r.handleListGroups,
			ListGroupsWithAccounts: r.handleListGroupsWithAccounts,
			CreateGroup:            r.handleCreateGroup,
			GetGroup:               r.handleGetGroup,
			UpdateGroup:            r.handleUpdateGroup,
			DeleteGroup:            r.handleDeleteGroup,
			ListGroupAccounts:      r.handleListGroupAccounts,
			SetGroupAccounts:       r.handleSetGroupAccounts,
			UpdateGroupSettings:    r.handleUpdateGroupSettings,
			RemoveGroupAccount:     r.handleRemoveGroupAccount,
		},
	}))
	reg.Register(NewThumbnailProjectsModule(
		r.protected,
		r.handleCreateThumbnailProject,
		r.handleListThumbnailProjects,
		r.handleGetThumbnailProject,
		r.handleUpdateThumbnailProject,
		r.handleSaveThumbnailProjectSnapshot,
		r.handleListThumbnailProjectRevisions,
		r.handleGetThumbnailProjectRevision,
		r.handleRestoreThumbnailProjectRevision,
		r.handleArchiveThumbnailProject,
		r.handleDeleteThumbnailProject,
		r.handleRenderThumbnailProject,
		r.handleGetThumbnailExport,
		r.handleAddThumbnailProjectAsset,
		r.handleListThumbnailProjectAssets,
		r.handleDeleteThumbnailProjectAsset,
		r.handleCreateThumbnailAssignments,
		r.handleListThumbnailAssignments,
		r.handleResolveThumbnailProjectMedia,
		r.handleCreateVeloxProjectBridge,
		r.handleGetVeloxProjectBridge,
		r.handleDeleteVeloxProjectBridge,
	))
	if r.coverLibraryStore != nil {
		coverLibraryRead := func(handler http.Handler) http.Handler { return r.protected(handler.ServeHTTP) }
		coverLibraryMutation := func(handler http.Handler) http.Handler {
			if r.csrfMiddleware != nil {
				handler = r.csrfMiddleware(handler)
			}
			return r.protected(handler.ServeHTTP)
		}
		r.mux.Method(http.MethodGet, "/api/v1/cover-library", coverLibraryRead(http.HandlerFunc(r.handleListCoverLibrary)))
		r.mux.Method(http.MethodGet, "/api/v1/template-library", coverLibraryRead(http.HandlerFunc(r.handleListCoverTemplates)))
		r.mux.Method(http.MethodGet, "/api/v1/template-library/{template_id}/versions", coverLibraryRead(http.HandlerFunc(r.handleListCoverTemplateVersions)))
		r.mux.Method(http.MethodPost, "/api/v1/template-library", coverLibraryMutation(http.HandlerFunc(r.handleCreateCoverTemplate)))
		r.mux.Method(http.MethodPost, "/api/v1/template-library/{template_id}/versions", coverLibraryMutation(http.HandlerFunc(r.handleCreateCoverTemplateVersion)))
		r.mux.Method(http.MethodPost, "/api/v1/template-library/{template_id}/archive", coverLibraryMutation(http.HandlerFunc(r.handleArchiveCoverTemplate)))
	}
	reg.Register(NewMediaModule(MediaModuleDeps{
		RateLimitSvc:           r.rateLimitSvc,
		Protected:              r.protected,
		EditorSessionProtected: r.editorSessionProtectedUnscoped,
		PresignMedia:           r.handlePresignMedia,
		DriveImport:            r.handleDriveImport,
		DriveImportAsync:       r.handleDriveImportAsync,
		DriveBatchImport:       r.handleDriveBatchImport,
		DriveBatchImportV2:     r.handleDriveBatchImportV2,
		DriveBatchV2Status:     r.handleDriveBatchV2Status,
		DriveBatchStatus:       r.handleDriveBatchStatus,
		CompleteMedia:          r.handleCompleteMedia,
		ListMedia:              r.handleListMediaAssets,
		GetMedia:               r.handleGetMediaAsset,
		ListDriveAssets:        r.handleListDriveAssets,
		GetDriveAsset:          r.handleGetDriveAsset,
	}))
	// Livestream configuration CRUD base. The state machine
	// (desired_state/actual_state) is worker-owned; the control
	// endpoints (prepare/start/stop/restart) land with the encoder
	// worker. GET /api/v1/livestreams returns the workspace's rows as
	// {items: [...]} — the sidebar badge counts actual_state == "live".
	// Extracted from Setup's inline block so the bounded context owns
	// its route table + CSRF order (see LivestreamModule).
	reg.Register(NewLivestreamModule(LivestreamModuleDeps{
		Protected:      r.protected,
		CSRFMiddleware: r.csrfMiddleware,
		Handlers: LivestreamHandlers{
			ListLivestreamChannels: r.handleListLivestreamChannels,
			ListLivestreams:        r.handleListLivestreams,
			CreateLivestream:       r.handleCreateLivestream,
			GetLivestream:          r.handleGetLivestream,
			PatchLivestream:        r.handlePatchLivestream,
			DeleteLivestream:       r.handleDeleteLivestream,
		},
	}))
	reg.Register(NewPublishingModule(PublishingModuleDeps{
		RateLimitSvc:                  r.rateLimitSvc,
		Protected:                     r.protected,
		ProtectedWithAPIKeyPermission: r.protectedWithAPIKeyPermission,
		ListPublishingTargets:         r.handleListPublishingTargets,
		CreatePost:                    r.handleCreatePost,
		ListPosts:                     r.handleListPosts,
		ListPostsByWorkspace:          r.handleListByWorkspace,
		GetPost:                       r.handleGetPost,
		PatchPost:                     r.handlePatchPost,
		DeletePost:                    r.handleDeletePost,
		PublishPost:                   r.handlePublishPostID,
		SchedulePost:                  r.handleSchedulePost,
		CancelPost:                    r.handleCancelPost,
		RetryPost:                     r.handleRetryPost,
		GetPostTargets:                r.handleGetPostTargets,
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
		EditScheduledUpload:  r.handleEditScheduledUpload,
		CancelUpload:         r.handleCancelUpload,
	}))
	// Content Packages are the editable product aggregate. This bounded
	// context is mounted only when its store is wired; creating/editing a
	// package never touches the publishing queue.
	if r.contentPackageStore != nil {
		contentMutation := func(handler http.Handler) http.Handler {
			if r.csrfMiddleware != nil {
				handler = r.csrfMiddleware(handler)
			}
			return r.protected(handler.ServeHTTP)
		}
		contentRead := func(handler http.Handler) http.Handler { return r.protected(handler.ServeHTTP) }
		r.mux.Method(http.MethodPost, "/api/v1/content-packages", contentMutation(http.HandlerFunc(r.handleCreateContentPackage)))
		r.mux.Method(http.MethodGet, "/api/v1/content-packages/{id}", contentRead(http.HandlerFunc(r.handleGetContentPackage)))
		r.mux.Method(http.MethodPatch, "/api/v1/content-packages/{id}", contentMutation(http.HandlerFunc(r.handlePatchContentPackage)))
		r.mux.Method(http.MethodPut, "/api/v1/content-packages/{id}/targets", contentMutation(http.HandlerFunc(r.handleContentTargets)))
		r.mux.Method(http.MethodPost, "/api/v1/content-packages/{id}/metadata", contentMutation(http.HandlerFunc(r.handleContentMetadata)))
		r.mux.Method(http.MethodPost, "/api/v1/content-packages/{id}/translations", contentMutation(http.HandlerFunc(r.handleContentTranslations)))
		r.mux.Method(http.MethodGet, "/api/v1/content-packages/{id}/preview", contentRead(http.HandlerFunc(r.handleContentPreview)))
		r.mux.Method(http.MethodPost, "/api/v1/content-packages/{id}/schedule", contentMutation(http.HandlerFunc(r.handleScheduleContentPackage)))
		r.mux.Method(http.MethodPost, "/api/v1/content-packages/{id}/cancel", contentMutation(http.HandlerFunc(r.handleCancelContentPackage)))
		r.mux.Method(http.MethodGet, "/api/v1/content-packages/{id}/activity", contentRead(http.HandlerFunc(r.handleContentActivity)))
	}
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
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions", r.protectedWithAPIKeyPermission("write", createEditorSessionHandler.ServeHTTP))

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
	r.mux.Method(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}", r.editorSessionProtected(updateEditorSessionHandler.ServeHTTP))

	// P0#5: project-centric entry points for InstaEditor. The
	// InstaEditor holds only velox_project_id in its URL; it must be
	// able to (a) fetch the session row + (b) trigger a publish
	// without first POSTing /editor-sessions to discover the
	// session_id.
	var getEditorSessionByProjectHandler http.Handler = http.HandlerFunc(r.handleGetYouTubeEditorSessionByProject)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}", r.editorSessionProtected(getEditorSessionByProjectHandler.ServeHTTP))

	var publishEditorSessionByProjectHandler http.Handler = http.HandlerFunc(r.handlePublishYouTubeEditorSessionByProject)
	if r.csrfMiddleware != nil {
		publishEditorSessionByProjectHandler = r.csrfMiddleware(publishEditorSessionByProjectHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish", r.editorSessionProtected(publishEditorSessionByProjectHandler.ServeHTTP))

	// P2 — InstaEditor auto-save endpoint. The InstaEditor PUTs the
	// form values on debounce + on-blur so an operator who closed
	// the tab mid-edit can resume the same form state on reload.
	// CSRF semantics match the publish endpoint above (middleware
	// falls through to a passthrough when not wired).
	var saveDraftByProjectHandler http.Handler = http.HandlerFunc(r.handleSaveEditorSessionDraftByProject)
	if r.csrfMiddleware != nil {
		saveDraftByProjectHandler = r.csrfMiddleware(saveDraftByProjectHandler)
	}
	r.mux.Method(http.MethodPut, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft", r.editorSessionProtected(saveDraftByProjectHandler.ServeHTTP))

	// POST /api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata
	// ASYNC NVIDIA AI metadata generation kick-off (migration 113):
	// enqueues a metadata_generation_jobs row and returns 202
	// immediately — the POST never blocks on the 60-180s NVIDIA call.
	// The operator polls GET /generate-metadata/jobs/{job_id} until
	// completed, reviews + optionally edits the generated title,
	// description, tags, languages and translations, then submits
	// through /publish. CSRF semantics match the publish endpoint.
	var generateNVIDIAMetadataHandler http.Handler = http.HandlerFunc(r.handleGenerateNVIDIAMetadata)
	if r.csrfMiddleware != nil {
		generateNVIDIAMetadataHandler = r.csrfMiddleware(generateNVIDIAMetadataHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata", r.protected(generateNVIDIAMetadataHandler.ServeHTTP))

	// GET /api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}
	// Poll endpoint for the async metadata generation job. Read-only;
	// no CSRF (GET exempt by spec). Ownership is verified via the
	// job's workspace (enumerable BIGSERIAL ids never leak cross-
	// tenant — non-owners get 404).
	var getMetadataGenerationJobHandler http.Handler = http.HandlerFunc(r.handleGetMetadataGenerationJob)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}", r.protected(getMetadataGenerationJobHandler.ServeHTTP))

	var publishEditorSessionHandler http.Handler = http.HandlerFunc(r.handlePublishYouTubeEditorSession)
	if r.csrfMiddleware != nil {
		publishEditorSessionHandler = r.csrfMiddleware(publishEditorSessionHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/{id}/publish", r.protectedWithAPIKeyPermission("publish", publishEditorSessionHandler.ServeHTTP))

	// GET /api/v1/youtube/editor-sessions/{id} — session-id-keyed
	// companion to GET /by-project/{velox_project_id}. Powers the
	// InstaEditor SPA after the auto-provisioner
	// (POST /internal/v1/thumbnail-sessions) returns a fresh
	// ytedit_<uuid> that the SPA reads back through this endpoint.
	// Read-only; no CSRF (GET exempt by spec).
	var getEditorSessionByIDHandler http.Handler = http.HandlerFunc(r.handleGetYouTubeEditorSessionByID)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/{id}", r.protectedWithAPIKeyPermission("write", getEditorSessionByIDHandler.ServeHTTP))

	// Direct handoff endpoint (Blocco #5 P0 #4): callers (typically the
	// InstaEditor SPA after uploading the rendered thumbnail to
	// InstaEdit storage) supply a verified media_assets.id and the
	// handler atomically links it to the session. Coexists with the
	// PATCH-by-project flow (which goes through Velox) — the direct
	// handoff path skips the Velox roundtrip for the "thumbnail
	// already uploaded" case.
	var attachThumbnailHandler http.Handler = http.HandlerFunc(r.handleAttachThumbnailToEditorSession)
	if r.csrfMiddleware != nil {
		attachThumbnailHandler = r.csrfMiddleware(attachThumbnailHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/editor-sessions/{id}/thumbnail", r.protectedWithAPIKeyPermission("write", attachThumbnailHandler.ServeHTTP))

	// GET /api/v1/groups/{group_id}/youtube/videos — dashboard card
	// grid. Read-only, no CSRF (GET exempt). Aggregates the
	// YouTube-listing per channel for every account in the group (and
	// its sub-groups when ?include_subgroups=true), joined with the
	// existing editor_sessions for each (account, video) tuple.
	var listGroupYouTubeVideosHandler http.Handler = http.HandlerFunc(r.handleListGroupYouTubeVideos)
	r.mux.Method(http.MethodGet, "/api/v1/groups/{group_id}/youtube/videos", r.protected(listGroupYouTubeVideosHandler.ServeHTTP))

	// PATCH /api/v1/groups/{group_id}/youtube/videos/{video_id} — the
	// single metadata update endpoint (title / description / category)
	// for a group video. The body carries platform_account_id (the
	// owning channel) + the partial patch; the service merges over the
	// current canonical snippet so tags / omitted fields survive.
	var patchGroupYouTubeVideoMetadataHandler http.Handler = http.HandlerFunc(r.handlePatchGroupYouTubeVideoMetadata)
	if r.csrfMiddleware != nil {
		patchGroupYouTubeVideoMetadataHandler = r.csrfMiddleware(patchGroupYouTubeVideoMetadataHandler)
	}
	r.mux.Method(http.MethodPatch, "/api/v1/groups/{group_id}/youtube/videos/{video_id}", r.protected(patchGroupYouTubeVideoMetadataHandler.ServeHTTP))

	// POST /api/v1/groups/{group_id}/youtube/videos/{video_id}/thumbnail
	// — the cover-save flow. The authorized InstaEdit backend receives
	// the rendered cover (thumbnail_media_id), verifies group/owner/
	// video, calls thumbnails.set (PNG/JPEG ≤ 2 MB) and invalidates the
	// account's cached videos so the card refreshes. Thumbnail-ONLY: it
	// never touches privacy or snippet metadata (that is the full
	// editor-sessions /publish pipeline).
	var publishGroupVideoThumbnailHandler http.Handler = http.HandlerFunc(r.handlePublishGroupVideoThumbnail)
	if r.csrfMiddleware != nil {
		publishGroupVideoThumbnailHandler = r.csrfMiddleware(publishGroupVideoThumbnailHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/groups/{group_id}/youtube/videos/{video_id}/thumbnail", r.protected(publishGroupVideoThumbnailHandler.ServeHTTP))
	// Same thumbnail-only flow for YouTube Studio private videos, which
	// may not belong to a selected group.
	var publishAccountVideoThumbnailHandler http.Handler = http.HandlerFunc(r.handlePublishAccountVideoThumbnail)
	if r.csrfMiddleware != nil {
		publishAccountVideoThumbnailHandler = r.csrfMiddleware(publishAccountVideoThumbnailHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{account_id}/youtube/videos/{video_id}/thumbnail", r.protected(publishAccountVideoThumbnailHandler.ServeHTTP))

	// GET /api/v1/youtube/video-categories — centralized YouTube video
	// categories resource (videoCategories.list proxy) shared by every
	// category select (group-video metadata drawer, livestreams wizard).
	// Read-only, no CSRF (GET exempt). Resolves the first active YouTube
	// account of the caller's workspaces and mints its token from the
	// vault; ?region_code is an optional ISO 3166-1 alpha-2 code.
	var listYouTubeVideoCategoriesHandler http.Handler = http.HandlerFunc(r.handleListYouTubeVideoCategories)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/video-categories", r.protected(listYouTubeVideoCategoriesHandler.ServeHTTP))

	// GET /api/v1/groups/{group_id}/covers — Copertine hub covers
	// grid. Read-only, no CSRF (GET exempt). Returns the cover
	// projects (thumbnail projects) linked to the group's accounts —
	// current + archived history — in one SQL round-trip via
	// ListCoversByGroupAccounts.
	var listGroupCoversHandler http.Handler = http.HandlerFunc(r.handleListGroupCovers)
	r.mux.Method(http.MethodGet, "/api/v1/groups/{group_id}/covers", r.protected(listGroupCoversHandler.ServeHTTP))

	// Durable, idempotent batch application for private YouTube thumbnails.
	var createYouTubeThumbnailBatchHandler http.Handler = http.HandlerFunc(r.handleCreateYouTubeThumbnailBatch)
	if r.csrfMiddleware != nil {
		createYouTubeThumbnailBatchHandler = r.csrfMiddleware(createYouTubeThumbnailBatchHandler)
	}
	r.mux.Method(http.MethodPost, "/api/v1/youtube/thumbnail-batches", r.protected(createYouTubeThumbnailBatchHandler.ServeHTTP))
	var getYouTubeThumbnailBatchHandler http.Handler = http.HandlerFunc(r.handleGetYouTubeThumbnailBatch)
	r.mux.Method(http.MethodGet, "/api/v1/youtube/thumbnail-batches/{batch_id}", r.protected(getYouTubeThumbnailBatchHandler.ServeHTTP))

	// Blocco Carosello — unified pipeline view endpoint. Aggregates
	// Drive + storage + per-target YouTube publish + Velox editor
	// state into a single response that the SPA timeline UI consumes
	// (GET /api/v1/content/{id}/pipeline). Read-only; no CSRF needed
	// — GET requests are exempt by spec.
	r.mux.Method(http.MethodGet, "/api/v1/content/{content_id}/pipeline", r.protected(http.HandlerFunc(r.handleGetContentPipeline).ServeHTTP))
	r.mux.Method(http.MethodGet, "/api/v1/youtube/copyright-alerts", r.protected(http.HandlerFunc(r.handleListYouTubeCopyrightAlerts).ServeHTTP))
	r.mux.Method(http.MethodGet, "/api/v1/youtube/copyright-check", r.protected(http.HandlerFunc(r.handleCheckYouTubeCopyright).ServeHTTP))

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
	compressedRoutes := gzipJSONMiddleware(r.mux)
	rateLimitAndBelow := r.securityHeadersMiddleware(
		r.rateLimiter.middleware(r.corsMiddleware(r.requestIDMiddleware(r.loggingMiddleware(compressedRoutes)))),
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
