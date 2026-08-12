package api

import (
	"database/sql"
	"net"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
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

func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) { r.workspaceStore = repo }
}

// WithThumbnailProjectStore wires the autonomous thumbnail project store.
// It is independent from YouTube editor-session persistence and optional
// until the database migration is deployed.
func WithThumbnailProjectStore(store ThumbnailProjectStore) RouterOption {
	return func(r *Router) { r.thumbnailProjectStore = store }
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
func WithOneTimeCodeStore(s OneTimeCodeStore) RouterOption {
	return func(r *Router) { r.oneTimeCodes = s }
}

// WithIdempotencyStore injects the idempotency_records persistence
// layer. The /api/v1/posts handler (handleCreatePost) consults this
// when an Idempotency-Key request header is present. Without this
// option wired, Idempotency-Key headers are silently ignored — the
// handler falls through to the no-cache path. Production wiring
// must include this option (see the canonical bootstrap).
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
// set it (the canonical bootstrap constructs one via
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
// WithYouTubeCredentialResolver wires the shared OAuth grant resolver used
// by account validation and pre-action YouTube credential resolution.
func WithYouTubeCredentialResolver(resolver *services.YouTubeCredentialResolver) RouterOption {
	return func(r *Router) { r.youtubeCredentialResolver = resolver }
}

func WithYouTubeService(svc YouTubeOAuthService) RouterOption {
	return func(r *Router) {
		r.youTubeSvc = svc
		// Discover the narrower YouTubeRevoker capability (used by the
		// account disconnect flow) at wiring time. Kept separate from the
		// broad validation interface so existing test providers remain
		// compatible.
		if revoker, ok := svc.(YouTubeRevoker); ok {
			r.youtubeRevoker = revoker
		}
		if revoker, ok := svc.(services.OAuthGrantRevoker); ok {
			r.oauthGrantRevoker = revoker
		}
	}
}

// WithUserStore wires the user-store on the Router. The store satisfies
// the UserStore interface declared in router.go (covers
// FindPlatformAccountByID, etc.). Missing previously: tests that needed
// to override the empty default mockStore had to mutate *Router fields
// post-construction; this option makes the wiring declarative and
// matches the pattern of WithWorkspaceStore / WithYouTubeService.
func WithUserStore(s UserStore) RouterOption {
	return func(r *Router) { r.userRepo = s }
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
// the canonical bootstrap MUST set this to true.
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
// Production wiring passes cfg.HTTP.CookieDomain directly so COOKIE_DOMAIN
// env controls the scope at deploy time.
func WithCookieDomain(domain string) RouterOption {
	return func(r *Router) { r.cookieDomain = domain }
}

// WithAdminInviteToken wires the shared secret that gates the public
// registration endpoint (POST /api/v1/auth/register). The handler
// performs a constant-time compare between this value and the
// X-Admin-Token request header; an empty value disables registration
// entirely. See internal/config.Auth.AdminInviteToken for the env surface.
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
// the canonical bootstrap) uses the same repo to claim + process
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

// WithContentPackageStore wires the editable Content Package aggregate. The
// resolver is built from the same store so preview and preparation share one
// precedence/readiness implementation.
func WithContentPackageStore(s ContentPackageStore) RouterOption {
	return func(r *Router) {
		r.contentPackageStore = s
		if s != nil {
			r.publicationResolver = services.NewPublicationResolver(s)
		}
	}
}

func WithDriveInboxStore(s DriveInboxStore) RouterOption {
	return func(r *Router) { r.driveInboxStore = s }
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

// WithRouterAnalyticsClock wires the shared clock used by analytics
// handlers outside ChannelAnalyticsService, including the aggregate
// summary route. Production passes analytics.RealClock{}; tests pass
// analytics.FixedClock.
func WithRouterAnalyticsClock(clock analytics.Clock) RouterOption {
	return func(r *Router) {
		if !isNilAnalyticsClock(clock) {
			r.analyticsClock = clock
		}
	}
}

// WithChannelAnalyticsService wires the Step-4 extracted business
// logic for GET /accounts/{id}/performance. The service owns
// workspace ownership + YouTube platform + channel-id resolution
// + period + history + video fetch + trending rank + DTO assembly;
// the handler is a thin delegator. When nil, the handler returns
// 501 ("channel analytics service not configured").
//
// Production wiring: NewChannelAnalyticsService(r.userRepo,
// r.metricHistoryStore). The option is exposed so a future
// variant — e.g. a service that adds a cache layer or a metrics
// decorator — can be injected without touching the bootstrap.
func WithChannelAnalyticsService(s *ChannelAnalyticsService) RouterOption {
	return func(r *Router) { r.channelAnalyticsService = s }
}

type RouterOption func(*Router)

// WithConnectLinkNonceStore wires the store used to persist and
// atomically consume connect-link nonces (and the signed oauth-flow
// state nonces issued by handleLogin when a YouTube OAuth Client Pool
// is configured). When nil, replay protection is disabled (tests and
// legacy deployments).
func WithConnectLinkNonceStore(store ConnectLinkNonceStore) RouterOption {
	return func(r *Router) {
		r.connectLinkNonceStore = store
	}
}

// WithYouTubeOAuthClientRegistry wires the YouTube OAuth Client Pool
// registry used by handleLogin to select the pool client that issues
// the next grant and by handleCallback to resolve the oauth_client_key
// carried in the signed state. When nil (the default), the legacy
// single-client flow is preserved: cookie-backed CSRF state + the
// cfg.Auth.YouTubeClientID exchange path. Production wiring builds the
// registry via services.NewYouTubeOAuthClientRegistryFromConfig(cfg)
// whenever YOUTUBE_OAUTH_CLIENT_A/B_* env vars are configured.
func WithYouTubeOAuthClientRegistry(reg *services.YouTubeOAuthClientRegistry) RouterOption {
	return func(r *Router) {
		r.youtubeOAuthClientRegistry = reg
	}
}

// WithTrustedProxies configures the list of networks (IP or CIDR)
// that are allowed to supply X-Forwarded-For / X-Real-IP headers.
// When empty (the default), clientIP extraction falls back to the
// direct peer address.
func WithTrustedProxies(proxies []*net.IPNet) RouterOption {
	return func(r *Router) {
		r.trustedProxies = proxies
	}
}

// WithMetricsAuth wires the basic-auth credentials used by
// /api/v1/metrics. If either value is empty the endpoint is
// fail-closed (503 Service Unavailable).
func WithMetricsAuth(user, pass string) RouterOption {
	return func(r *Router) {
		r.metricsUser = user
		r.metricsPass = pass
	}
}

// WithYouTubeVideoEditStore wires the repository used to persist
// YouTube thumbnail editor sessions. When nil, the
// /api/v1/youtube/editor-sessions endpoint returns 503.
func WithYouTubeVideoEditStore(store YouTubeVideoEditStore) RouterOption {
	return func(r *Router) {
		r.youtubeVideoEditStore = store
	}
}

// WithYouTubeThumbnailBatchStore wires durable YouTube thumbnail batch
// persistence. When nil, the batch endpoints return 503.
func WithYouTubeThumbnailBatchStore(store YouTubeThumbnailBatchStore) RouterOption {
	return func(r *Router) {
		r.youtubeThumbnailBatchStore = store
	}
}

// WithLivestreamStore wires livestream configuration persistence.
// When nil, the /api/v1/livestreams/* endpoints return 503 (matches
// the nil-store feature-flag pattern).
func WithLivestreamStore(store LivestreamStore) RouterOption {
	return func(r *Router) {
		r.livestreamStore = store
	}
}

// WithContentPipelineStore wires the consolidated read-side repo
// used by GET /api/v1/content/{id}/pipeline. When nil, the route
// returns 503 (matches the rest of the nil-store feature flags).
// Production wiring in internal/bootstrap/app.go passes
// repository.NewContentPipelineRepository(app.DB).
func WithContentPipelineStore(store ContentPipelineStore) RouterOption {
	return func(r *Router) {
		r.contentPipelineStore = store
	}
}

// WithYouTubeCopyrightAlertStore wires the workspace-scoped read side used
// by Calendar to display post-upload YouTube copyright alerts.
func WithYouTubeCopyrightAlertStore(store YouTubeCopyrightAlertStore) RouterOption {
	return func(r *Router) { r.youtubeCopyrightAlertStore = store }
}

// WithEditorURL wires the base URL of the separately deployed InstaEditor SPA.
// An empty value is preserved as unavailable; it never falls back to the
// InstaEdit frontend or the legacy EDITOR_URL variable.
func WithEditorURL(url string) RouterOption {
	return func(r *Router) {
		r.editorURL = url
	}
}

// WithEditorService wires the provider-neutral editor lifecycle service.
// The service is intentionally separate from the legacy project-bridge
// handoff routes so callers can migrate to CreateProject/OpenProject/
// GetProjectStatus/RequestRender without exposing provider details.
func WithEditorService(service services.EditorService) RouterOption {
	return func(r *Router) {
		r.editorService = service
	}
}

// WithPublishingInFlightTimeout configures the guard window used by the
// YouTube thumbnail publish handler to treat a session with
// status='publishing' as still in-flight. The default is 5 minutes;
// non-positive values are ignored and the default is used instead.
func WithPublishingInFlightTimeout(d time.Duration) RouterOption {
	return func(r *Router) {
		if d > 0 {
			r.publishingInFlightTimeout = d
		}
	}
}

// WithThumbnailDownloadClient wires the HTTP client used to download
// thumbnail bytes from storage before publishing to YouTube. When
// nil, NewRouter keeps its default 30s client.
func WithThumbnailDownloadClient(client *http.Client) RouterOption {
	return func(r *Router) {
		if client != nil {
			r.thumbnailDownloadClient = client
			// Test and integration callers commonly inject one transport
			// that handles both storage reads and writes. Keep that
			// compatibility while allowing production wiring (or a later
			// WithThumbnailUploadClient option) to use a separate timeout.
			r.thumbnailUploadClient = client
		}
	}
}

// WithThumbnailUploadClient wires the HTTP client used for rendered
// thumbnail PUTs. When nil, NewRouter keeps its default 2-minute client.
func WithThumbnailUploadClient(client *http.Client) RouterOption {
	return func(r *Router) {
		if client != nil {
			r.thumbnailUploadClient = client
		}
	}
}

// WithBookingEventStore wires the repository used to persist the
// strategy-call marketing events. Optional (nil → route not
// registered) — matches the webhookStore / uploadJobStore pattern.
func WithBookingEventStore(store BookingEventStore) RouterOption {
	return func(r *Router) { r.bookingEventStore = store }
}

// WithNvidiaMetadataService wires the NVIDIA AI metadata generator
// used by the /generate-metadata endpoint. When nil (the default),
// the endpoint returns 503 and manual metadata entry still works.
// Production wiring in internal/bootstrap/app.go passes
// services.NewMetadataGenerator(cfg.AI.NVIDIAAPIKey,
// services.WithModel(cfg.AI.NVIDIAModel)).
func WithNvidiaMetadataService(svc *services.MetadataGenerator) RouterOption {
	return func(r *Router) {
		r.nvidiaMetadataSvc = svc
	}
}

// WithMetadataGenerationStore wires the async metadata generation job
// store (migration 113). When nil (the default), the
// /generate-metadata endpoints return 503. Production wiring passes
// repository.NewMetadataGenerationJobRepository(app.DB).
func WithMetadataGenerationStore(store MetadataGenerationStore) RouterOption {
	return func(r *Router) {
		r.metadataGenerationStore = store
	}
}

// WithYouTubeGroupVideosConfig wires the limits and short-lived cache
// used by GET /groups/{group_id}/youtube/videos. The option is applied
// at router construction time so tests and production share the same
// handler behavior without reading environment variables per request.
func WithYouTubeGroupVideosConfig(cfg YouTubeGroupVideosConfig) RouterOption {
	return func(r *Router) {
		r.youtubeGroupVideosConfig = cfg.normalized()
	}
}
