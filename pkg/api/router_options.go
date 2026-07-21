package api

import (
	"database/sql"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// WithDB wires the database for the /ready handler's DB ping +
// migrations check. Production wiring in internal/bootstrap.Wire
// passes App.DB; tests pass nil (which makes /ready return "db not
// configured" so the error is visible without dragging the real
// *sql.DB into unit tests).
func WithDB(db *sql.DB) RouterOption {
	return func(r *Router) { r.dbForReady = db }
}

// WithConnectionStateStore wires *repository.ConnectionStateRepository
// into the Router. Without this option, /api/v1/connections/* return 501.
func WithConnectionStateStore(s ConnectionStateStore) RouterOption {
	return func(r *Router) { r.connectionStates = s }
}

// WithVeloxDownloadJobChannel wires the durable Velox→InstaEdit handoff
// queue. The API only enqueues; the worker process consumes it.
func WithVeloxDownloadJobChannel(ch chan VeloxDownloadJob) RouterOption {
	return func(r *Router) { r.downloadJobCh = ch }
}

func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) { r.workspaceStore = repo }
}

func WithPostStore(repo PostStore) RouterOption {
	return func(r *Router) { r.postStore = repo }
}
func WithStorageProvider(p StorageProvider) RouterOption {
	return func(r *Router) { r.storageProvider = p }
}
func WithMaxUploadBytes(n int64) RouterOption {
	return func(r *Router) { r.maxUploadBytes = n }
}
func WithAuditLogStore(store AuditLogStore) RouterOption {
	return func(r *Router) { r.auditLogStore = store }
}
func WithOneTimeCodeStore(s *OneTimeCodeStore) RouterOption {
	return func(r *Router) { r.oneTimeCodes = s }
}

// WithIdempotencyStore injects the idempotency_records persistence
// layer. The /api/v1/posts handler (handleCreatePost) consults this
// when an Idempotency-Key request header is present. Without this
// option wired, Idempotency-Key headers are silently ignored — the
// handler falls through to the no-cache path. Production wiring
// must include this option (see cmd/server/main.go).
func WithIdempotencyStore(s IdempotencyStore) RouterOption {
	return func(r *Router) { r.idempotencyStore = s }
}

// WithApiKeyAuthenticator injects the API-key middleware used on
// /api/v1/api-keys/* routes. When set, requests with Authorization:
// Bearer sk_test_…/sk_live_… are authenticated against the api_keys
// table by Authenticator.Middleware; non-sk_ requests pass through
// to the existing JWT/cookie chain. When NOT set, the API-key routes
// behave as JWT-only (existing behaviour; the apiKeyStore is
// independently wired by WithApiKeyStore).
//
// Optional in main.go today so existing dev environments without
// per-tenant API keys keep working — production deployments always
// set it (cmd/server/main.go constructs one via
// auth.NewApiKeyAuthenticator(apiKeyRepo)).
func WithApiKeyAuthenticator(a *auth.Authenticator) RouterOption {
	return func(r *Router) { r.apiKeyAuth = a }
}

// WithApiKeyStore injects the api_keys persistence layer. The
// /api/v1/api-keys/* handlers require this to be wired; otherwise
// they return 501 Not Implemented at runtime, mirroring the
// postStore / workspaceStore nil-guard pattern. The interface is
// local to this package so test fixtures can supply an in-memory
// fake without dragging the repository import into pkg/api tests.
func WithApiKeyStore(s ApiKeyStore) RouterOption {
	return func(r *Router) { r.apiKeyStore = s }
}

// WithYouTubeService wires the production YouTubeOAuthService into
// the Router. Without this option handleValidateAccount falls back
// to the legacy token-freshness probe for YouTube platforms AND for
// every other platform — preserving the pre-C1 cross-platform
// scaffolding for tests / environments that haven't wired the
// option. Required for the 4-step pipeline on YouTube; optional for
// any other platform (no change in behaviour).
func WithYouTubeService(svc YouTubeOAuthService) RouterOption {
	return func(r *Router) { r.youTubeSvc = svc }
}

// WithCredentialVault injects the central credential vault. The Router
// REQUIRES this to be set (via main.go) before serving
// handleCallback — the call site panics with a nil-pointer dereference
// if it's missing, which is the desired fail-fast behaviour for a
// misconfigured main.go. Tests
// inject a mockCredentialVault via this same option.
//
// Taglio 2.2: renamed from WithTokenService. The vault centralises
// AES-256-GCM encryption, persistence, refresh (with Postgres advisory
// locks), and revocation — no provider or consumer needs to know how
// tokens are stored.
func WithCredentialVault(v credentials.VaultAPI) RouterOption {
	return func(r *Router) { r.vault = v }
}

// WithChannelAuthorizer (Task 1/10) wires the atomic OAuth finalize
// flow. The router calls this in attachDiscoveredAccounts — the
// difference vs the previous two-call (FinalizeAttach + vault.Save)
// sequence is atomicity: a partial failure inside the authz flow
// rolls back BOTH the oauth_connections write AND the tokens write
// AND the platform_accounts status flip, so the API can never reach
// a "status='active' but no credentials" state. Bindings that go
// through disabled providers (e.g. ad-hoc test routers) may pass
// a stub that returns nil; real routers must pass a real
// *services.ChannelAuthorizationService from internal/bootstrap.
func WithChannelAuthorizer(c services.ChannelAuthorizer) RouterOption {
	return func(r *Router) { r.authorizer = c }
}

// WithAuthEmailService injects the email/password auth service for SaaS
// registration, login, and password reset endpoints.
// When not set, /api/v1/auth/register and /login return 501 Not Implemented.
func WithAuthEmailService(svc AuthEmailStore) RouterOption {
	return func(r *Router) { r.authEmailSvc = svc }
}

// WithTeamStore injects the workspace team repository for member/invite
// management. When not set, /api/v1/workspaces/{id}/members, /invites,
// and /api/v1/invites/{token} return 501 Not Implemented.
func WithTeamStore(s TeamStore) RouterOption {
	return func(r *Router) { r.teamStore = s }
}

// WithGroupStore wires the hierarchical-groups repository used by
// /api/v1/groups/* endpoints (TAGLIO X.Y). When nil, every handler
// in pkg/api/groups.go returns 501 Not Implemented (matches the
// postStore / workspaceStore / billingSvc feature-flag nil-guard
// pattern). Production wiring in internal/bootstrap/app.go passes
// repository.NewGroupRepository(db).
func WithGroupStore(s GroupStore) RouterOption {
	return func(r *Router) { r.groupStore = s }
}

// WithBillingService injects the Stripe billing service for checkout,
// customer portal, and webhook handling. When not set, /api/v1/billing/*
// endpoints return 501 Not Implemented.
func WithBillingService(svc BillingServiceAPI) RouterOption {
	return func(r *Router) { r.billingSvc = svc }
}

// WithUserWorkspaceHelper injects the resolver used by handleExchangeCode
// (and by future switch handlers) to derive the workspace_id stamped on
// freshly-issued JWTs. Required in production wiring; the helper is nil
// until this option is set, at which point handleExchangeCode fails the
// request with 500 (cf. resolveActiveWorkspace).
func WithUserWorkspaceHelper(h UserWorkspaceHelper) RouterOption {
	return func(r *Router) { r.userAndWorkspaceHelper = h }
}

// WithSessionsService wires the SPRINT 2.1 sessions service used by
// /auth/refresh, /auth/logout, /auth/logout-all, /auth/sessions,
// and the workspace-switch endpoint. When not set, the endpoints
// return 501 Not Implemented.
func WithSessionsService(svc *services.SessionsService) RouterOption {
	return func(r *Router) { r.sessionsSvc = svc }
}

// WithCookieSecure toggles the Secure flag on session cookies.
// Defaults to false (httptest-friendly); production wiring in
// cmd/server/main.go MUST set this to true.
func WithCookieSecure(secure bool) RouterOption {
	return func(r *Router) { r.cookieSecure = secure }
}

// WithCookieDomain sets the optional `Domain` attribute applied to
// the csrf_token cookie ONLY. Session and refresh cookies are NEVER
// given a Domain — they remain host-only on the API origin. The
// reason is asymmetric threat model:
//
//   - csrf_token is NON-HttpOnly and MUST be readable by JS on the
//     SPA origin. Cross-origin (app.instaedit.org reading the
//     api.instaedit.org cookie) only works when the cookie's Domain
//     is set to a parent the SPA's host falls under, OR when the
//     SPA is reverse-proxied through the API same-host.
//
//   - session / refresh cookies are HttpOnly. JS can never read them
//     regardless of origin; the browser only attaches them on
//     subsequent requests to the API origin. Setting Domain on these
//     widens the cross-subdomain attack surface without any security
//     upside — the SPA cannot read them anyway.
//
// Pass an empty string to disable (dev / localhost default).
// Production wiring passes cfg.CookieDomain directly so COOKIE_DOMAIN
// env controls the scope at deploy time.
func WithCookieDomain(domain string) RouterOption {
	return func(r *Router) { r.cookieDomain = domain }
}

// WithAdminInviteToken wires the shared secret that gates the public
// registration endpoint (POST /api/v1/auth/register). The handler
// performs a constant-time compare between this value and the
// X-Admin-Token request header; an empty value disables registration
// entirely. See internal/config.AdminInviteToken for the env surface.
func WithAdminInviteToken(token string) RouterOption {
	return func(r *Router) { r.adminInviteToken = token }
}

// WithRateLimitService wires the SPRINT 2.2 multi-tier rate
// limiter. Required in production wiring so the per-workspace
// POST /posts (60/min/workspace) and per-API-key reads
// (600/min/key) are enforced across replicas via the Postgres
// rate_limit_counters table. The per-IP (OAuth start) and
// per-endpoint (media presign) tiers stay in-memory per-replica
// as coarse backstops; the real per-IP gate is the edge tier
// (Cloudflare / reverse proxy — see docs/OPERATIONS.md).
func WithRateLimitService(svc *services.RateLimitService) RouterOption {
	return func(r *Router) { r.rateLimitSvc = svc }
}

// WithWebhookStore wires the SPRINT 4.2 webhook runtime. The
// HTTP handlers use it to CRUD endpoint configuration + manual
// replay; the background worker (spawned separately by
// cmd/server/main.go) uses the same repo to claim + process
// deliveries. When not wired, /api/v1/webhooks/* return 501.
func WithWebhookStore(s WebhookStore) RouterOption {
	return func(r *Router) { r.webhookStore = s }
}

// WithUploadJobStore wires the background upload_jobs queue used by
// POST /api/v1/media/import/drive/async. When nil, the endpoint
// returns 501.
func WithUploadJobStore(s UploadJobStore) RouterOption {
	return func(r *Router) { r.uploadJobStore = s }
}

// WithAdminStore wires the P2 ops dashboard store. When nil,
// every /admin/* handler returns 501 (mirroring the
// PostStore / WorkspaceStore nil-guard pattern).
func WithAdminStore(s AdminStore) RouterOption {
	return func(r *Router) { r.adminStore = s }
}

// WithImportBatchStore wires the P1#7 async folder-batch header
// table. When nil, POST /api/v1/media/import/drive/folder/async and
// GET /api/v1/media/import/drive/folder/async/{id} return 501. The
// background crawler is wired separately (see
// internal/bootstrap.Wire — that's where the *repository.ImportBatchRepository
// is also injected).
func WithImportBatchStore(s ImportBatchStore) RouterOption {
	return func(r *Router) { r.importBatchStore = s }
}

// WithSnapshotStore wires the account resource snapshot cache. When
// nil, GET /accounts/{id} returns the base 6-field shape and
// /accounts/{id}/sync returns 501.
func WithSnapshotStore(s SnapshotStore) RouterOption {
	return func(r *Router) { r.snapshotStore = s }
}

// WithMetricHistoryStore wires the account metric history store.
// When nil, snapshot refreshes do not persist historical rows and
// GET /accounts/{id}/performance returns 501.
func WithMetricHistoryStore(s MetricHistoryStore) RouterOption {
	return func(r *Router) { r.metricHistoryStore = s }
}
