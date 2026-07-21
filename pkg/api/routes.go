package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

func (r *Router) Setup() http.Handler {
	r.mux = chi.NewRouter()
	// P2 — ops dashboard. Standalone /admin/* prefix (per D7
	// verdict: avoid SPA-side CORS overhead while keeping chi's
	// root-router middleware). Each handler is gated by
	// adminAuthMiddleware (Identity.IsAdmin()==true); non-admin
	// callers get 403, unauthenticated callers get 401.
	if r.adminStore != nil {
		r.mux.Method(http.MethodGet, "/admin/channels", adminAuthMiddleware(http.HandlerFunc(r.handleAdminChannels)))
		r.mux.Method(http.MethodGet, "/admin/channels.csv", adminAuthMiddleware(http.HandlerFunc(r.handleAdminChannelsCSV)))
		r.mux.Method(http.MethodGet, "/admin/queue", adminAuthMiddleware(http.HandlerFunc(r.handleAdminQueue)))
		r.mux.Method(http.MethodGet, "/admin/queue.csv", adminAuthMiddleware(http.HandlerFunc(r.handleAdminQueueCSV)))
		// Task 10/10 — operator-triage endpoints for dead-lettered
		// upload_jobs. Two-sibling JSON + CSV convention mirrors
		// /admin/queue.{csv} so operators can wire the same dashboard
		// + spreadsheet export pipeline they already use for the
		// stuck-job list. 500-row hard cap stays under the dashboard
		// render budget; the `error_code` + `error_message` columns
		// let the operator decide retry / cancel / ignore without
		// paging through the full DB.
		r.mux.Method(http.MethodGet, "/admin/upload_jobs/dead_letter", adminAuthMiddleware(http.HandlerFunc(r.handleAdminUploadJobsDeadLetter)))
		r.mux.Method(http.MethodGet, "/admin/upload_jobs/dead_letter.csv", adminAuthMiddleware(http.HandlerFunc(r.handleAdminUploadJobsDeadLetterCSV)))
		r.mux.Method(http.MethodGet, "/admin/health", adminAuthMiddleware(http.HandlerFunc(r.handleAdminHealth)))
		r.mux.Method(http.MethodGet, "/admin/health.csv", adminAuthMiddleware(http.HandlerFunc(r.handleAdminHealthCSV)))
		// P2 — operator-side channel onboarding surface (P2 task).
		// POST /admin/channels/import-csv: multipart CSV upload →
		// status='pending_authorization' upserts. GET
		// /admin/channels/pending: filter view of the same store
		// (hard-codes status='pending_authorization' on the
		// existing ListChannelsForOps path — no new SQL). Both
		// gated on the AdminStore wiring (no new option — reuses
		// the existing flag).
		r.mux.Method(http.MethodPost, "/admin/channels/import-csv", adminAuthMiddleware(http.HandlerFunc(r.handleAdminImportChannelsCSV)))
		r.mux.Method(http.MethodGet, "/admin/channels/pending", adminAuthMiddleware(http.HandlerFunc(r.handleAdminPendingChannels)))
		// Definition-of-Done rollout snapshot endpoint. One roundtrip
		// aggregates the 12 DoD counters in platform_accounts (FILTER
		// clauses); a second roundtrip dump-INSERTs the per-channel
		// detail into fleet_readiness_snapshot_channels. The handler
		// returns the JSON envelope -- operator diffs successive
		// snapshots via the persisted child rows.
		r.mux.Method(http.MethodGet, "/admin/youtube/fleet_readiness", adminAuthMiddleware(http.HandlerFunc(r.handleAdminYouTubeFleetReadiness)))
		// P2 — admin connect-link. POST /admin/channels/{channel_id}/connect-link
		// returns a signed OAuth URL with prompt=consent + select_account
		// + login_hint=manager_email_hint. The callback (handlers.go
		// handleCallback) detects the JWT-shaped state and refuses the
		// 422/409 mismatch cleanly. Intentional split between this
		// admin-side URL issuer (here) AND the OAuth callback (universal
		// /api/v1/auth/{provider}/callback, NOT in /admin/) — the
		// callback is per-provider, the URL issuer is per-channel.
		r.mux.Method(http.MethodPost, "/admin/channels/{channel_id}/connect-link", adminAuthMiddleware(http.HandlerFunc(r.handleAdminChannelConnectLink)))
	}

	// P1 Velox integration — service-to-service /internal/v1 routes.
	// Registered LAST in Setup() because the path prefix is most
	// specific (no share with /api/v1/*). registerInternalVeloxRoutes
	// is a no-op if either VELOX_API_TOKEN OR the destination
	// store is unwired (boot-time fail-fast per
	// internal_velox.go::registerInternalVeloxRoutes contract).
	// Production wiring in internal/bootstrap.Wire passes both.
	r.registerInternalVeloxRoutes()

	r.mux.Method(http.MethodGet, "/api/v1/health", http.HandlerFunc(r.handleHealth))
	// Blocco #5.3 — /ready is top-level + public. Readiness
	// probes never carry credentials; routers must NOT have to
	// know the /api/v1 prefix to probe. Mounted in the route
	// table BEFORE the other handlers so it's near-code at the
	// top of Setup(); the handler is invoked via the recovery
	// middleware chain (captures panic-on-probe) regardless of
	// where the route sits in mux order.
	r.mux.Method(http.MethodGet, "/ready", http.HandlerFunc(r.handleReady))

	// FASE 2.2: email/password auth routes (when configured).
	if r.authEmailSvc != nil {
		r.registerAuthEmailRoutes()
	}

	// FASE 2.3: workspace team management (when configured).
	if r.teamStore != nil {
		r.registerTeamRoutes()
	}

	// FASE 3.1: Stripe billing (when configured).
	if r.billingSvc != nil {
		r.registerBillingRoutes()
	}

	// SPRINT 4.2: webhook runtime (when configured). Endpoints
	// + manual-replay only — the actual POST work happens in the
	// background worker (cmd/server/main.go spawns it separately).
	if r.webhookStore != nil {
		r.registerWebhookRoutes()
	}

	// SPRINT 7.1 (P0#14): OAuth social routes are gated on a valid
	// InstaEdit session (Bearer or HttpOnly cookie). The middleware
	// 302s to /login?next=/connections/{provider} when the user is
	// not authenticated, so the SPA can resume the OAuth connect
	// after the user logs in. auto-create-user is removed: users
	// reach the OAuth callback only via the product onboarding
	// flow (email register / login).
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/login",
		OAuthStartLimitIfConfigured(r.rateLimitSvc)(http.HandlerFunc(r.oauthSessionRedirect(r.handleLogin))))
	r.mux.Method(http.MethodGet, "/api/v1/auth/{provider}/callback", http.HandlerFunc(r.oauthSessionRedirect(r.handleCallback)))
	r.mux.Method(http.MethodPost, "/api/v1/auth/exchange", http.HandlerFunc(r.handleExchangeCode))
	r.mux.Method(http.MethodGet, "/api/v1/auth/me", r.protected(r.handleMe))
	// SPRINT 2.1: refresh + logout live OUTSIDE the JWT middleware
	// (the cookie IS the credential). CSRF is bypassed for /refresh
	// and /logout because the act of presenting a valid refresh
	// cookie already authenticates the request.
	r.mux.Method(http.MethodPost, "/api/v1/auth/refresh", http.HandlerFunc(r.handleRefresh))
	r.mux.Method(http.MethodPost, "/api/v1/auth/logout", http.HandlerFunc(r.handleLogout))
	// /logout-all + /sessions live BEHIND the JWT middleware.
	r.mux.Method(http.MethodPost, "/api/v1/auth/logout-all", r.protected(r.handleLogoutAll))
	r.mux.Method(http.MethodGet, "/api/v1/auth/sessions", r.protected(r.handleListSessions))
	r.mux.Method(http.MethodDelete, "/api/v1/auth/sessions/{id}", r.protected(r.handleDeleteSession))
	// GET /api/v1/accounts — list the authenticated user's connected
	// social accounts across every platform. SPRINT 7.1 (P0#14):
	// must NEVER read user_id / workspace_id from body/query — both
	// come exclusively from the JWT identity deposited by the auth
	// middleware. Mounted BEFORE /accounts/{id} so chi's pattern
	// matching prefers the literal path over the parameterised one
	// (also a readability convention: list first, then by-id).
	r.mux.Method(http.MethodGet, "/api/v1/accounts", r.protected(r.handleListAccounts))
	r.mux.Method(http.MethodGet, "/api/v1/accounts/{id}", r.protected(r.handleGetAccount))
	r.mux.Method(http.MethodGet, "/api/v1/accounts/performance/summary", r.protected(r.handleGetAccountsPerformanceSummary))
	r.mux.Method(http.MethodGet, "/api/v1/accounts/{id}/performance", r.protected(r.handleGetAccountPerformance))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/validate", r.protected(r.handleValidateAccount))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/reconnect", r.protected(r.handleReconnectAccount))
	r.mux.Method(http.MethodDelete, "/api/v1/accounts/{id}", r.protected(r.handleDeleteAccount))
	r.mux.Method(http.MethodPost, "/api/v1/accounts/{id}/sync", r.protected(r.handleSyncAccount))
	r.mux.Method(http.MethodGet, "/api/v1/accounts/{id}/content", r.protected(r.handleAccountContent))
	r.mux.Method(http.MethodPatch, "/api/v1/accounts/{id}", r.protected(r.handleUpdateAccount))
	r.mux.Method(http.MethodGet, "/api/v1/metrics", http.HandlerFunc(r.handleMetrics))
	// Taglio 3.2: the old /api/v1/storage/upload-url endpoint is
	// replaced by /api/v1/media/presign (see pkg/api/media.go). The
	// new endpoint is part of a 3-step presigned upload flow that
	// removes arbitrary media_url from public post payloads.
	// SPRINT 2.2: per-endpoint media-presign budget (30/min,
	// in-memory coarse backstop). The middleware is a no-op when
	// rateLimitSvc is nil.
	var mediaPresignMw []func(http.Handler) http.Handler
	if r.rateLimitSvc != nil {
		mediaPresignMw = append(mediaPresignMw, MediaPresignLimit(r.rateLimitSvc))
	}

	r.mux.Method(http.MethodPost, "/api/v1/media/presign",
		chain(r.protected(r.handlePresignMedia), mediaPresignMw...))

	r.mux.Method(http.MethodPost, "/api/v1/media/import/drive", r.protected(r.handleDriveImport))

	// Async drive import: queue a background job to download a
	// public or authenticated Drive video and publish it later.
	r.mux.Method(http.MethodPost, "/api/v1/media/import/drive/async", r.protected(r.handleDriveImportAsync))

	// Batch drive import: list every video in a Drive folder and
	// schedule them as posts with cumulative random gaps. The first
	// job's scheduled_at is NOW so the publish_worker picks it up on
	// its next tick (≈1s). Used for "I have a folder full of videos,
	// post one every 3-4.5 hours on my Facebook Page" workflows.
	r.mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder", r.protected(r.handleDriveBatchImport))

	// P1#7 — async folder-batch producer/consumer.
	// POST /folder/async returns {batch_id, status:"queued"} immediately;
	// the background crawler (internal/worker/drive_batch_crawler.go)
	// does the Drive pagination + upload_job creation. The OLD
	// /folder endpoint above is kept as a synchronous back-compat
	// path for clients on the v1 shape; clients should migrate to
	// /folder/async for the new multi-platform semantics.
	r.mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder/async", r.protected(r.handleDriveBatchImportV2))
	r.mux.Method(http.MethodGet, "/api/v1/media/import/drive/folder/async/{id}", r.protected(r.handleDriveBatchV2Status))

	// Batch drive status: dashboard polls this for per-folder counts
	// (pending/processing/completed/failed) and min/max scheduled_at.
	// Mirrors the upload_jobs partial index on folder_id so polling
	// is one index range scan + a per-status COUNT FILTER.
	r.mux.Method(http.MethodGet, "/api/v1/media/import/drive/batch/status", r.protected(r.handleDriveBatchStatus)) // Dashboard "Programmati" surface: per-account scheduled uploads
	// + cross-account list + drag-drop reschedule + cancel.
	// Sub-router pattern keeps the route table flat without leaking
	// the new IDs into the chi-pattern matching at the top level.
	r.mux.Route("/api/v1/uploads", func(sr chi.Router) {
		// /counts MUST come before /{id} below — but the route
		// below is mounted as /, not /{id}, so order doesn't
		// matter here. We still register /counts first for
		// readability (the cheap aggregate before any heavy by-account
		// detail query).
		sr.Get("/counts", r.protected(r.handleUploadCounts))
		sr.Get("/", r.protected(r.handleListUploads))
		sr.Get("/by-account", r.protected(r.handleListUploadsByAccount))
		// Server-side batch folder import — one round-trip per
		// folder regardless of size. Auto-pages Drive's
		// next_page_token transparently (max driveBatchMaxPages
		// = 50 pages × 200 videos = 10 000 entries).
		// Idempotency-Key contract mirrors handleDriveBatchImport.
		sr.Post("/batch/by-folder", r.protected(r.handleUploadsBatchByFolder))
		sr.Patch("/{id}/reschedule", r.protected(r.handleRescheduleUpload))
		sr.Delete("/{id}", r.protected(r.handleCancelUpload))
	})

	r.mux.Method(http.MethodPost, "/api/v1/media/{id}/complete", r.protected(r.handleCompleteMedia))
	r.mux.Route("/api/v1/workspaces", func(sr chi.Router) {
		sr.Post("/", r.protected(r.handleCreateWorkspace))
		sr.Get("/", r.protected(r.handleListWorkspaces))
		sr.Get("/{id}", r.protected(r.handleGetWorkspace))
		sr.Delete("/{id}", r.protected(r.handleDeleteWorkspace))
		// SPRINT 1.1: switch active workspace. Re-issues the JWT with
		// the new ws claim and sets a fresh HttpOnly session cookie.
		sr.Post("/{id}/switch", r.protected(r.handleSwitchWorkspace))
		// P0#4 — bind a platform_account to this workspace under
		// an optional group_name tag. Idempotent (ON CONFLICT
		// DO UPDATE on the workspace_id + platform_account_id
		// composite PK). 404 on cross-tenant; 400 on missing
		// body fields. See pkg/api/workspace_channels.go.
		sr.Post("/{id}/channels", r.protected(r.handleAttachWorkspaceChannel))
		sr.Get("/{id}/channels", r.protected(r.handleListWorkspaceChannels))
		sr.Patch("/{id}/channels/{accountId}", r.protected(r.handleUpdateWorkspaceChannel))
		sr.Delete("/{id}/channels/{accountId}", r.protected(r.handleDetachWorkspaceChannel))
	})

	// TAGLIO X.Y — hierarchical groups for organizing connected
	// platform accounts. The sub-router is registered behind a
	// feature-flag nil-guard so a server that hasn't wired
	// WithGroupStore (yet) returns 501 instead of crashing. Every
	// handler enforces workspace ownership via
	// requireWorkspaceOwnership → JWT-deposited userID, so the
	// tenant boundary mirrors /api/v1/workspaces/*.
	if r.groupStore != nil {
		r.mux.Route("/api/v1/groups", func(sr chi.Router) {
			// Mount list/create BEFORE the parameterised {id}
			// routes so the order reads top-down: list → create
			// → by-id → accounts.
			sr.Get("/", r.protected(r.handleListGroups))
			sr.Post("/", r.protected(r.handleCreateGroup))
			sr.Get("/{id}", r.protected(r.handleGetGroup))
			sr.Patch("/{id}", r.protected(r.handleUpdateGroup))
			sr.Delete("/{id}", r.protected(r.handleDeleteGroup))
			sr.Get("/{id}/accounts", r.protected(r.handleListGroupAccounts))
			sr.Put("/{id}/accounts", r.protected(r.handleSetGroupAccounts))
		})
	}
	r.mux.Route("/api/v1/posts", func(sr chi.Router) {
		// SPRINT 2.2: per-workspace POST budget (60/min/workspace,
		// Postgres-backed). Outer to the auth-protected handler so
		// the identity is available when the tier resolves the
		// scope. The middleware is a no-op when rateLimitSvc is nil.
		if r.rateLimitSvc != nil {
			sr.Use(WorkspacePostLimit(r.rateLimitSvc))
		}
		sr.Post("/", r.protected(r.handleCreatePost))
		sr.Get("/", r.protected(r.handleListPosts))
		sr.Get("/workspace/{wid}", r.protected(r.handleListByWorkspace))
		sr.Get("/{id}", r.protected(r.handleGetPost))
		sr.Patch("/{id}", r.protected(r.handlePatchPost))
		sr.Delete("/{id}", r.protected(r.handleDeletePost))
		sr.Post("/{id}/publish", r.protected(r.handlePublishPostID))
		sr.Post("/{id}/schedule", r.protected(r.handleSchedulePost))
		sr.Post("/{id}/cancel", r.protected(r.handleCancelPost))
		sr.Post("/{id}/retry", r.protected(r.handleRetryPost))
		sr.Get("/{id}/targets", r.protected(r.handleGetPostTargets))
		sr.Post("/{id}/targets", r.protected(r.handleAddTarget))
	})
	r.mux.Route("/api/v1/post-targets", func(sr chi.Router) {
		sr.Post("/{id}/retry", r.protected(r.handleRetryTarget))
	})

	// /api/v1/api-keys/* — Taglio 4.6 tenant API key management.
	//
	// Middleware order on this sub-router:
	//   1. Authenticator (if wired) — authenticates sk_test_/sk_live_
	//      Bearer tokens and deposits ApiKeyIdentity in context.
	//      Pass-through for non-sk_ requests, so JWT/cookie auth runs
	//      next.
	//   2. JWT/cookie auth (existing r.auth) — authenticates JWT/cookie
	//      sessions, deposits UserIdentity in context.
	//   3. Handler — reads IdentityFromContext (works for both),
	//      dispatches on IsAPIKey / HasPermission as needed.
	//
	// Skipping Authenticator (when WithApiKeyAuthenticator was not
	// called) means API-key-only clients can't authenticate; the
	// JWT/cookie path remains available so dashboard-like flows
	// still work in dev.
	r.mux.Route("/api/v1/api-keys", func(sr chi.Router) {
		// Blocco #1.3 — CSRF double-submit is enforced on EVERY
		// unsafe method (POST/DELETE/PATCH). Mounted OUTERMOST so a
		// missing/expired csrf_token cookie is rejected with 403
		// before we spend cycles on auth. Bearer-authenticated
		// callers are exempt (the middleware detects the Bearer
		// prefix and short-circuits). The cookie-authenticated
		// POST/DELETE here is the dashboard UI minting/rotating
		// API keys from its own session; without CSRF those would
		// be trivially CSRF-able from any malicious third-party
		// page (the session cookie is HttpOnly but cross-site
		// requests still carry it).
		sr.Use(func(next http.Handler) http.Handler {
			return auth.NewCSRF(r.csrfConfig(), next)
		})
		if r.apiKeyAuth != nil {
			sr.Use(func(next http.Handler) http.Handler {
				return r.apiKeyAuth.Middleware(next)
			})
		}
		sr.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				r.auth.Middleware(next).ServeHTTP(w, req)
			})
		})
		// SPRINT 2.2: per-API-key read budget (600/min/key,
		// Postgres-backed). Mounted AFTER the auth chain so the
		// ApiKeyIdentity is in context when the tier resolves the
		// scope. The middleware is a no-op when rateLimitSvc is nil.
		if r.rateLimitSvc != nil {
			sr.Use(APIKeyReadLimit(r.rateLimitSvc))
		}
		sr.Post("/", r.handleCreateApiKey)
		sr.Get("/", r.handleListApiKeys)
		sr.Get("/{id}", r.handleGetApiKey)
		sr.Delete("/{id}", r.handleDeleteApiKey)
		sr.Post("/{id}/rotate", r.handleRotateApiKey)
	})
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
		r.rateLimiter.middleware(r.corsMiddleware(r.loggingMiddleware(r.mux))),
	)
	return r.recoverMiddleware(rateLimitAndBelow)
}
