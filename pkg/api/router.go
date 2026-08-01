package api

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/contracts"
	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
)

// UserWorkspaceHelper is re-exported from pkg/api/contracts (see
// contracts/users.go for the full godoc). The type alias keeps every
// existing reference inside pkg/api and internal/* source-compatible
// while letting the actual interface declaration live in a leaf
// package. Once all call sites migrate to contracts.UserWorkspaceHelper
// the alias can collapse in a single cleanup commit.
type UserWorkspaceHelper = contracts.UserWorkspaceHelper

// repoUserWorkspaceHelper implements UserWorkspaceHelper against the
// real Postgres repositories. The methods wrap the underlying
// repository calls and project to a []int64 (one id per row).
type repoUserWorkspaceHelper struct {
	workspaceRepo *repository.WorkspaceRepository
	teamRepo      *repository.TeamRepository
}

// RepoUserWorkspaceHelper is the production constructor. Exposed
// because main.go needs to build the helper from the *sql.DB-bound
// repositories. Kept lowercase-prefixed in type name (private field
// types) but the constructor is uppercase.
func RepoUserWorkspaceHelper(w *repository.WorkspaceRepository, t *repository.TeamRepository) UserWorkspaceHelper {
	return &repoUserWorkspaceHelper{workspaceRepo: w, teamRepo: t}
}

func (h repoUserWorkspaceHelper) ListOwned(_ context.Context, userID int64) ([]int64, error) {
	owned, err := h.workspaceRepo.ListByOwner(userID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(owned))
	for _, w := range owned {
		out = append(out, w.ID)
	}
	return out, nil
}

func (h repoUserWorkspaceHelper) ListMemberships(_ context.Context, userID int64) ([]int64, error) {
	members, err := h.teamRepo.ListForUser(userID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(members))
	for _, m := range members {
		out = append(out, m.WorkspaceID)
	}
	return out, nil
}

type Router struct {
	mux              *chi.Mux
	capabilities     *services.CapabilityRouter
	userRepo         UserStore
	workspaceStore   WorkspaceStore
	postStore        PostStore
	storageProvider  StorageProvider
	mediaStore       MediaStore
	auditLogStore    AuditLogStore
	auth             *auth.Manager
	apiKeyAuth       *auth.Authenticator
	apiKeyStore      ApiKeyStore
	idempotencyStore IdempotencyStore
	vault            credentials.VaultAPI
	// authorizer (Task 1/10) is the SINGLE gate that flips a
	// platform_account to status='active' AND writes the encrypted
	// token row, atomically. Replaces the pre-atomic FinalizeAttach +
	// vault.Save sequence (kept on UserStore / VaultAPI for back-compat
	// and unit-test seams). Nil is a startup wiring mistake — every
	// router serving /api/v1/auth/... callbacks must supply one.
	authorizer     services.ChannelAuthorizer
	oneTimeCodes   OneTimeCodeStore
	frontendURL    string
	allowedOrigin  []string
	maxUploadBytes int64
	rateLimiter    *rateLimiter // FASE 1.2: per-IP token bucket
	authEmailSvc   AuthEmailStore
	teamStore      TeamStore
	billingSvc     BillingServiceAPI
	// groupStore backs /api/v1/groups/* (TAGLIO X.Y). Optional —
	// mirrors the WorkspaceStore / PostStore nil-guard pattern: if
	// not wired, every handler returns 501 Not Implemented. Wired
	// in internal/bootstrap/app.go via api.WithGroupStore(repo).
	groupStore GroupStore
	// userAndWorkspaceHelper resolves a user's active workspace during
	// OAuth callback / exchange (and switch endpoint). Wired in
	// cmd/server/main.go via WithUserWorkspaceHelper(); defaults to nil
	// so the explicit 501-shaped error in handleExchangeCode short-
	// circuits dev environments that have not yet wired the helper.
	userAndWorkspaceHelper UserWorkspaceHelper
	connectionStates       ConnectionStateStore
	// SPRINT 2.1 — revocable session lifecycle (optional). Wiring
	// via WithSessionsService. When nil, /auth/refresh, /auth/logout,
	// /auth/logout-all, /auth/sessions and DELETE /auth/sessions/{id}
	// return 501 (consistent with the nil-store pattern used by the
	// other feature flags). The /auth/{provider}/callback handler
	// refuses to mint a session when this is nil.
	// SPRINT 7.4 (P0#14-blocco-1.4): sessionsSvc is exposed via the
	// SessionsStore interface so test fixtures can supply an in-memory
	// fake (no real *sql.DB-bound SessionRepository required). The
	// production wiring in cmd/server/main.go passes
	// *services.SessionsService which satisfies the interface.
	sessionsSvc SessionsStore
	// cookieSecure is the Secure flag for cookies. Defaults to true
	// in production wiring (cmd/server/main.go) and to false in tests
	// that exercise the cookie path with httptest's in-memory server.
	cookieSecure bool
	// cookieDomain (Blocco #2.4 — CSRF cross-origin read) is the
	// Domain attribute applied EXCLUSIVELY to the csrf_token cookie
	// via auth.CSRFConfig. Session and refresh cookies NEVER receive
	// it; they're HttpOnly on the API host and adding a Domain would
	// widen the CSRF attack surface (cross-subdomain cookie reuse)
	// without any compensating control (JS still cannot read them).
	// Wired via WithCookieDomain; defaults to empty (dev-friendly).
	cookieDomain string

	// adminInviteToken gates POST /api/v1/auth/register. When empty,
	// the handler returns 403 regardless of the X-Admin-Token header
	// (registration disabled). Wired in cmd/server via
	// WithAdminInviteToken(cfg.Auth.AdminInviteToken). Production
	// deployments must set ADMIN_INVITE_TOKEN via Fly secrets; dev
	// can omit it (no public registration permitted).
	adminInviteToken string
	// SPRINT 2.2 — multi-tier rate limiter (optional). Wiring via
	// WithRateLimitService. When nil, the per-tier middleware
	// factories (WorkspacePostLimit / APIKeyReadLimit /
	// MediaPresignLimit / OAuthStartLimit) become no-ops. Required
	// in production so the per-workspace and per-API-key tiers are
	// enforced (per the user's "no in-memory for >1 replica" rule).
	rateLimitSvc *services.RateLimitService
	// SPRINT 4.2 — webhook runtime (optional). Wiring via
	// WithWebhookStore. When nil, /api/v1/webhooks/* return 501
	// (mirroring the other feature-flag nil-guard pattern).
	// The HTTP handlers only manage endpoint configuration +
	// manual replay — the actual POST work happens in a
	// background worker (internal/worker/webhook_worker.go)
	// that main.go spawns separately.
	webhookStore WebhookStore

	// uploadJobStore persists background upload jobs (public or
	// authenticated Google Drive imports). When nil, the async
	// drive-import endpoint returns 501.
	uploadJobStore UploadJobStore

	// adminStore backs the P2 ops dashboard (/admin/channels,
	// /admin/queue, /admin/health + their .csv variants). When
	// nil, every admin endpoint returns 501. Wiring happens in
	// internal/bootstrap/app.go via WithAdminStore passing the
	// production *repository.AdminRepository.
	adminStore AdminStore

	// importBatchStore persists the P1#7 header row for an async
	// folder-batch import. The producer handler (POST
	// /api/v1/media/import/drive/folder/async) inserts one row
	// IMMEDIATELY and returns {batch_id, status:"queued"}; the
	// background crawler (internal/worker/drive_batch_crawler.go)
	// claims + processes + completes the row. When nil, the
	// producer endpoint AND/OR the poll endpoint return 501.
	importBatchStore ImportBatchStore

	// snapshotStore caches remote resource data (channel stats,
	// profile, branding) so the frontend doesn't trigger a provider
	// API call on every render. Wired via WithSnapshotStore;
	// when nil, GET /accounts/{id} returns the base 6-field shape
	// (no resource details) and /accounts/{id}/sync returns 501.
	snapshotStore SnapshotStore

	// metricHistoryStore persists a daily time-series of extracted
	// account metrics. Wired via WithMetricHistoryStore; when nil,
	// snapshot refreshes do not write historical rows and
	// GET /accounts/{id}/performance returns 501.
	metricHistoryStore MetricHistoryStore

	// channelAnalyticsService is the Step-4 extracted business logic
	// for GET /accounts/{id}/performance: workspace ownership + YT
	// platform + channel-id resolution + period resolution +
	// history fetch + video lister + trending rank + DTO assembly.
	// Wired via WithChannelAnalyticsService; when nil, the handler
	// returns 501 ("channel analytics service not configured").
	channelAnalyticsService *ChannelAnalyticsService
	// analyticsClock anchors summary-handler windows. The per-channel
	// service owns its own injected clock; both are explicitly wired
	// from the same RealClock in production and FixedClock in tests.
	analyticsClock analytics.Clock

	// bookingEventStore persists the anonymous lead-capture events
	// from the marketing strategy-call modal (POST
	// /api/v1/booking_events). Wired via WithBookingEventStore;
	// when nil, the BookingEventsModule does not register its
	// route (matches the webhookStore / uploadJobStore nil-guard
	// pattern). The endpoint sits OUTSIDE the JWT auth chain
	// (it's anonymous) and OUTSIDE the CSRF chain (anonymous
	// browsers have no csrf_token cookie).
	bookingEventStore BookingEventStore

	// Blocco #5.3 — Sentry hub + /ready wiring.
	// sentryHub is nil when SENTRY_DSN is unset (operator-disables-
	// by-omission). When set, the recovery middleware uses
	// sentryhttp.New() against this hub; when nil, plain recover.
	sentryHub *sentry.Hub
	// dbForReady is the *sql.DB used by /ready for PingContext +
	// SchemaHealthy. Nil disables both (test fixture path); the
	// production wiring in cmd/server/main.go passes app.DB.
	dbForReady *sql.DB

	// veloxAPIToken (P1 Velox integration) is the static shared
	// secret used by the service-to-service /internal/v1/* routes.
	// Loaded from env VELOX_API_TOKEN via internal/config + wired
	// with api.WithVeloxAPIToken. When empty AND VeloxModule.Register
	// runs, the route refuses to register (operator-safe boot
	// fail-fast). When empty AT REQUEST TIME (the route was
	// registered), the middleware returns 503 + an error log.
	veloxAPIToken string

	// externalDestinations (P1 Velox integration) is the
	// persistence contract wired via WithExternalDestinationStore.
	// When nil, the /internal/v1 routes are not registered
	// (matches the postStore / workspaceStore nil-guard
	// pattern). Read directly from the Router field — the
	// handler does NOT go through a captured-config struct to
	// avoid an option-order trap (snapshotting r.workspaceStore /
	// r.userRepo at option-call time would capture nil if
	// wired in the wrong order).
	externalDestinations ExternalDestinationStore
	// externalDeliveries (P1 Velox integration, POST /internal/v1/deliveries)
	// is the persistence contract wired via WithExternalDeliveryStore.
	// Per-route guarded in VeloxModule.Register — the validate
	// route (destinations/{id}/validate) does NOT require it; the
	// deliveries route (POST /deliveries) REQUIRES it. When nil, only
	// the validate route is mounted (matches the per-route
	// Optional-wiring pattern used for the other feature flags).
	externalDeliveries ExternalDeliveryStore

	// connectLinkNonceStore persists the jti (RegisteredClaims.ID)
	// embedded in each admin connect-link state JWT. The jti is
	// consumed atomically on first callback so a link can only be
	// used once within its 30-minute validity window.
	connectLinkNonceStore ConnectLinkNonceStore

	// veloxBFFClient (P2 Velox BFF) is the typed client used by
	// the user-facing /api/v1/velox/* routes (jobs, workers, assets).
	// Wired via WithVeloxBFFClient. When nil, the VeloxBFFModule
	// does not mount its routes (nil-guard pattern matching the
	// other feature flags). The concrete implementation lives in
	// internal/veloxclient and signs a short-lived JWT with
	// VELOX_CONTROL_JWT_SECRET before calling the Velox master.
	veloxBFFClient veloxapi.Client

	// veloxBFFCSRFMiddleware (P2 Velox BFF) wraps the user-facing
	// /api/v1/velox/* routes with the project's canonical CSRF
	// check. Mirrors csrfMiddleware used by the destinations route.
	veloxBFFCSRFMiddleware func(http.Handler) http.Handler

	// veloxBFFAuthMiddleware (P2 Velox BFF) mirrors veloxBFFCSRFMiddleware
	// for the JWT identity layer on /api/v1/velox/*. nil when not wired;
	// tests pass passthrough stubs. cmd/server/main.go wires it via
	// WithVeloxBFFAuthMiddleware(r.auth.Middleware).
	veloxBFFAuthMiddleware func(http.Handler) http.Handler

	// veloxValidateRateLimiter (P2 Velox integration — Phase 2
	// rate-limit on the /internal/v1/destinations/{id}/validate
	// endpoint). nil → no rate limit (the closest production
	// deployment opted-out via WithVeloxValidateRateLimit(0,0) or
	// simply never wired the option). When non-nil, the handler
	// rejects with 429 + Retry-After after `limit` requests per
	// `window` per destination_id. See
	// pkg/api/internal_velox.go::validateRateLimiter.
	veloxValidateRateLimiter *validateRateLimiter

	// csrfMiddleware (P2 Velox integration — Phase 2) wraps the
	// user-facing /api/v1/integrations/velox/destinations route
	// with the project's canonical CSRF check. nil when not
	// wired; tests pass passthrough stubs. cmd/server/main.go
	// wires it via WithCsrfMiddleware(auth.NewCSRF(r.csrfConfig(),
	// _)). Production MUST wire this; the field exists so the
	// route registration can reference it without a compile
	// error.
	csrfMiddleware func(http.Handler) http.Handler

	// authMiddleware (P2 Velox integration — Phase 2) mirrors
	// csrfMiddleware for the JWT identity layer on
	// /api/v1/integrations/velox/destinations. nil when not
	// wired; tests pass passthrough stubs. cmd/server/main.go
	// wires it via WithAuthMiddleware(r.auth.Middleware).
	authMiddleware func(http.Handler) http.Handler

	// youTubeSvc (P7 — 4-step /accounts/{id}/validate pipeline) is the
	// narrow capability-subset of *services.YouTubeOAuthService that
	// handleValidateAccount's pipeline (refresh-grant → tokeninfo →
	// channel-binding → optional canary-upload) depends on. When nil
	// the handler falls back to the legacy token-freshness probe
	// (preserves the pre-C1 cross-platform behaviour for any test or
	// deployment that hasn't yet wired the option). Wired in
	// cmd/server/main.go via WithYouTubeService(svc); the handler
	// owns the routing decision.
	youTubeSvc YouTubeOAuthService

	// nvidiaMetadataSvc is the NVIDIA AI metadata generator.
	// Optional — when nil, the /generate-metadata endpoint returns
	// 503 and the manual metadata flow remains fully functional.
	// Wired via WithNvidiaMetadataService in internal/bootstrap/app.go.
	nvidiaMetadataSvc *services.MetadataGenerator

	// youtubeGroupVideosConfig controls the group-video read projection.
	// It is immutable after construction; youtubeGroupVideosCache is
	// protected by youtubeGroupVideosCacheMu because group fan-out runs
	// concurrently.
	youtubeGroupVideosConfig  YouTubeGroupVideosConfig
	youtubeGroupVideosCacheMu sync.Mutex
	youtubeGroupVideosCache   map[string]youtubeGroupVideosCacheEntry

	// youtubeVideoEditStore persists thumbnail editor sessions for
	// YouTube videos. Wired via WithYouTubeVideoEditStore.
	youtubeVideoEditStore YouTubeVideoEditStore

	// contentPipelineStore (Blocco Carosello) backs the unified
	// GET /api/v1/content/{id}/pipeline endpoint. Single workspace-
	// scoped fan-out returns posts + targets + pubs + accounts +
	// media + upload_job in 4 round-trips. When nil the route
	// returns 503 (matches the nil-store pattern used by the
	// other feature flags). Wired via WithContentPipelineStore
	// in internal/bootstrap/app.go.
	contentPipelineStore ContentPipelineStore

	// editorURL is the base URL of the dark editor SPA. When empty,
	// frontendURL is used as a fallback.
	editorURL string

	// publishingInFlightTimeout bounds how long a session can stay in
	// status='publishing' before another publish request is allowed to
	// retry it. Wired via WithPublishingInFlightTimeout; default 5m.
	publishingInFlightTimeout time.Duration

	// thumbnailDownloadClient is the HTTP client used by the thumbnail
	// publish flow to download the thumbnail bytes from storage. It
	// has a bounded timeout to prevent indefinite hangs on slow
	// storage backends. Wired via WithThumbnailDownloadClient; when
	// nil, NewRouter installs a default client with a 30s timeout.
	thumbnailDownloadClient *http.Client

	// trustedProxies contains the parsed TRUSTED_PROXIES networks.
	// When non-empty, clientIP() trusts X-Forwarded-For / X-Real-IP
	// only from these peers. Wired via WithTrustedProxies in
	// internal/bootstrap/app.go.
	trustedProxies []*net.IPNet

	// metricsUser and metricsPass gate /api/v1/metrics via basic
	// auth. Empty/incomplete values make the endpoint fail-closed
	// (503). Wired via WithMetricsAuth.
	metricsUser string
	metricsPass string

	// scheduleLimits (Blocco #2 P0) is the narrow read-only view of
	// WorkerConfig.PublishHorizonDays + VideoRetentionBufferDays
	// constructed by bootstrap from cfg.Worker and passed via
	// WithScheduleLimits. Used by handleRescheduleUpload,
	// handleDriveBatchImportV2 (heuristic cap), and
	// computeMediaAssetLifetime (all media_asset create sites).
	// Defaulted inside the helper functions to 30 / 7 so dev
	// fixtures that bypass the setter still get a sane TTL.
	scheduleLimits ScheduleLimits
}

// ConnectionStateStore is declared in pkg/api/connections.go (SPRINT 1.2);
// placeholder import to keep repository wired in this package so the
// above struct field typechecks.

var _ = repository.RoleAdmin

// Compile-time assertion that *repository.WorkspaceRepository
// satisfies the extended WorkspaceStore interface (post-P0#4
// channel surfaces). Caught at go vet time, not at runtime.
// Mirrors the team's `var _ UserStore = (*mockUserStore)(nil)`
// pattern in routes_test.go. The assertion lives in pkg/api (NOT
// in internal/repository) because the WorkspaceStore interface is
// declared here — internal/repository cannot import pkg/api (it
// would create an import cycle, since pkg/api already imports
// internal/repository).
var _ WorkspaceStore = (*repository.WorkspaceRepository)(nil)

// YouTubeOAuthService is the narrow capability-subset of
// *services.YouTubeOAuthService that the 4-step
// /accounts/{id}/validate pipeline (introduced in Commit C2) needs.
// Defined inline in pkg/api to keep tests mockable and avoid pkg/api
// directly importing internal/services for the interface ONLY (the
// service struct itself is injected via WithYouTubeService at
// production wiring time and its exported method-results are
// referenced via the interface below).
//
// The 4 steps map 1:1 onto the four interface methods:
//   - RefreshOAuthToken      → STEP 1 (refresh-grant via vault.Renew)
//   - GetTokenInfo          → STEP 2 (introspect access token + scope)
//   - ValidateChannelBinding → STEP 3 (paginated channels.list bind)
//   - CanaryUpload          → STEP 4 (optional private video + bind-reconcile)
//   - ClientID              → STEP 2 aud check (aud must equal the OAuth client
//     that issued the grant — guards against
//     Production-vs-Testing token drift)
type YouTubeOAuthService interface {
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
	GetTokenInfo(ctx context.Context, accessToken string) (*services.YouTubeTokenInfo, error)
	ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error
	CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error)
	FetchEarnings(ctx context.Context, accessToken, channelID string, days int) ([]repository.AccountMetricPoint, error)
	ClientID() string
	// GetYouTubeVideo validates that a video exists on the connected
	// YouTube channel and returns a narrow summary of its metadata.
	GetYouTubeVideo(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error)
	// ListEditableVideos (P0 group videos endpoint) returns one page
	// of processed private/unlisted videos belonging to channelID.
	// pageToken="" starts from the first page; subsequent pages are
	// fetched with the NextPageToken from the previous response. The
	// service-level filter (privacy != public AND uploadStatus =
	// processed) already filters out the long tail of public/
	// uploading/deleted rows the editor flow rejects at create time.
	ListEditableVideos(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error)
	// SetThumbnail uploads a JPEG/PNG image to YouTube and applies it
	// as the custom thumbnail for the given video. The caller must
	// supply a valid access token (retrieved from the vault).
	SetThumbnail(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error
	// UpdateVideoPrivacy changes the privacy status (and optionally the
	// snippet title/description) of an existing YouTube video via
	// videos.update. For scheduled publishing pass a future publishAt and
	// privacyStatus="private".
	UpdateVideoPrivacy(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, title, description string) error
	// PublishThumbnail uploads a thumbnail to YouTube and updates the
	// video privacy + snippet in a SINGLE videos.update(part=snippet,status)
	// call. Title / Description are still supported but moved into the
	// YouTubePublishOptions struct so the signature doesn't grow
	// unboundedly as more snippet fields (tags / localizations / default
	// languages) are added.
	//
	// Retries transient failures internally and returns the public
	// YouTube URL on success.
	PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error)
	// UpsertLocalizations sets (or replaces) one per-language
	// localization entry on a YouTube video via
	// videos.update(part=localizations). YouTube expects a single
	// language per call; the orchestrator loops over the
	// Translations map calling this once per entry after the
	// snippet+status update succeeds.
	//
	// The lang argument is a BCP-47 code (e.g. "en", "it", "pt-BR");
	// the orchestrator validates against the YouTubePublishOptions
	// sanity bounds before invoking the call so quota isn't burned
	// on a guaranteed-4xx response.
	UpsertLocalizations(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error
}

// YouTubeVideoEditStore is the persistence contract for thumbnail
// editor sessions. Defined inline in pkg/api so tests can supply a
// fake; production wiring passes *repository.YouTubeVideoEditRepository.
type YouTubeVideoEditStore interface {
	Create(ctx context.Context, edit *models.YouTubeVideoEdit) error
	FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error)
	FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	Update(ctx context.Context, edit *models.YouTubeVideoEdit) error
	// MarkPublishing (Blocco #5 P0 #2) atomically transitions the row to
	// status='publishing' WITH desired_privacy + publish_at stamped in the
	// same statement. CAS predicate (extended form): status IN
	// ('editing','failed') OR (status='publishing' AND updated_at <
	// NOW() - make_interval(secs => inFlightTimeout)). The strict
	// (inFlightTimeout <= 0) branch runs the same SQL minus the
	// orphan-recovery branch (E1 — Go-level guard). The handler maps
	// (nil, repository.ErrYouTubeVideoEditNotFound) to HTTP 409
	// (CAS-loss). Mirrors repository.YouTubeVideoEditRepository.MarkPublishing.
	MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error)
	// AttachThumbnail (Blocco #5 P0 #4) atomically links a verified
	// media asset (thumbnail) to an editor session. Single UPDATE
	// statement with CAS predicate `status IN ('editing','failed')` so
	// concurrent publish requests cannot race the link (a session in
	// 'publishing' or 'published' state will not match — handler maps
	// 0-rows to 409). Mirrors
	// repository.YouTubeVideoEditRepository.AttachThumbnail.
	AttachThumbnail(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error)
	// ListByWorkspace feeds the dashboard "code da modificare" widget.
	// Workspace-scoped + optional AccountID/Statuses filters + bounded
	// LIMIT. See repository.YouTubeEditorSessionListFilter for the full
	// semantics; the handler validates ?workspace_id and parses
	// ?account_id / ?status / ?limit, defaulting the status set to
	// YouTubeVideoEditNonTerminalStatuses when no ?status= is supplied.
	ListByWorkspace(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error)
	// ListByWorkspaceAccountIDs (P0 group videos endpoint) feeds the
	// GET /api/v1/groups/{group_id}/youtube/videos join: one SQL
	// query returns every editor session in the workspace whose
	// platform_account_id is in the supplied slice. The handler
	// caller (pkg/api/youtube_group_videos.go) joins the result onto
	// YouTube's fresh per-channel listing by (account_id, video_id)
	// tuple. See repository.YouTubeVideoEditRepository.ListByWorkspaceAccountIDs
	// for the SQL contract + index hint.
	ListByWorkspaceAccountIDs(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error)
	// FindOrCreateEditableSession (P0#3 click-idempotency) returns the
	// open (non-terminal) editor session for the given (workspace,
	// account, video) triple, or inserts a fresh one.
	FindOrCreateEditableSession(ctx context.Context, workspaceID int64, platformAccountID int64, youtubeVideoID string, sessionIDHint string, projectIDHint string) (*models.YouTubeVideoEdit, error)
	// SaveDraft (P2 — Dark Editor auto-save) atomically writes the
	// operator's mid-edit form values to youtube_video_edits.draft_*
	// AND stamps dirty_flag=false AND draft_updated_at=NOW().
	SaveDraft(ctx context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, publishAt *time.Time, draftUpdatedAt time.Time) error
	// MarkPublishedWithActualPrivacy (P0#7) atomically transitions
	// status='publishing' → 'published' AND stamps actual_privacy +
	// youtube_sync_status.
	MarkPublishedWithActualPrivacy(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error)
}

func NewRouter(
	capRouter *services.CapabilityRouter,
	userRepo UserStore,
	authMgr *auth.Manager,
	frontendURL string,
	allowedOrigins []string,
	opts ...RouterOption,
) (*Router, error) {
	r := &Router{
		capabilities: capRouter,
		userRepo:     userRepo,
		auth:         authMgr,

		frontendURL:               frontendURL,
		allowedOrigin:             allowedOrigins,
		analyticsClock:            analytics.RealClock{},
		rateLimiter:               newRateLimiter(nil), // FASE 1.2: per-IP token bucket (trusted proxies wired via option below)
		publishingInFlightTimeout: DefaultPublishingInFlightTimeout,
		thumbnailDownloadClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(r)
	}
	// Trusted proxies are applied via WithTrustedProxies above;
	// propagate them to the per-IP rate limiter so it extracts the
	// original client IP only from known proxies.
	if r.rateLimiter != nil {
		r.rateLimiter.trustedProxies = r.trustedProxies
	}
	if err := r.validateRequiredDeps(); err != nil {
		return nil, err
	}
	return r, nil
}

// MustNewRouter is a test-only convenience wrapper around NewRouter
// that panics when the router cannot be constructed (e.g. a required
// dependency is missing). It exists so that test fixtures can keep
// the familiar single-return form `r := api.MustNewRouter(...)`.
func MustNewRouter(
	capRouter *services.CapabilityRouter,
	userRepo UserStore,
	authMgr *auth.Manager,
	frontendURL string,
	allowedOrigins []string,
	opts ...RouterOption,
) *Router {
	r, err := NewRouter(capRouter, userRepo, authMgr, frontendURL, allowedOrigins, opts...)
	if err != nil {
		panic("api.MustNewRouter: " + err.Error())
	}
	return r
}

// validateRequiredDeps returns an error if a dependency that is
// considered mandatory for a correct/safe API is missing. Optional
// stores remain optional and are NOT checked here.
func (r *Router) validateRequiredDeps() error {
	if r.vault == nil {
		return errors.New("CredentialVault is required: pass WithCredentialVault(...)")
	}
	if r.authorizer == nil {
		return errors.New("ChannelAuthorizer is required: pass WithChannelAuthorizer(...)")
	}
	if r.oneTimeCodes == nil {
		return errors.New("OneTimeCodeStore is required: pass WithOneTimeCodeStore(...)")
	}
	if r.idempotencyStore == nil {
		return errors.New("IdempotencyStore is required: pass WithIdempotencyStore(...)")
	}
	if r.connectLinkNonceStore == nil {
		return errors.New("ConnectLinkNonceStore is required: pass WithConnectLinkNonceStore(...)")
	}
	return nil
}

// Compile-time assertion that *services.YouTubeOAuthService
// satisfies the narrow YouTubeOAuthService capability interface
// declared in this file. Caught by `go vet`, not at runtime. The
// assertion mirrors the existing
// `var _ WorkspaceStore = (*repository.WorkspaceRepository)(nil)`
// pattern around line ~340 in this same file; without it, a future
// prod-struct signature drift (e.g. an extra required parameter
// on RefreshOAuthToken) silently breaks the wiring at the
// injection site rather than at compile time.
var _ YouTubeOAuthService = (*services.YouTubeOAuthService)(nil)

// Compile-time assertion that *repository.YouTubeVideoEditRepository
// satisfies the YouTubeVideoEditStore interface.
var _ YouTubeVideoEditStore = (*repository.YouTubeVideoEditRepository)(nil)

// Compile-time assertion that *repository.ContentPipelineRepository
// satisfies the ContentPipelineStore interface (catches signature
// drift at go vet time, not at runtime).
var _ ContentPipelineStore = (*repository.ContentPipelineRepository)(nil)

// Compile-time assertion that *repository.BookingEventRepository
// satisfies the BookingEventStore interface. Same rationale as the
// other `var _ XxxStore = (*repository.XxxRepository)(nil)` lines
// in this file: signature drift in either the repo or the
// interface surfaces at go vet time rather than as a runtime nil
// dereference.
var _ BookingEventStore = (*repository.BookingEventRepository)(nil)

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

func (r *Router) integrationsModule() *IntegrationsModule {
	return NewIntegrationsModule(IntegrationsModuleDeps{
		ExternalDestinationStore: r.externalDestinations,
		WorkspaceStore:           r.workspaceStore,
		UserStore:                r.userRepo,
		AuditLogStore:            r.auditLogStore,
		AuthMiddleware:           r.authMiddleware,
		CSRFMiddleware:           r.csrfMiddleware,
	}).(*IntegrationsModule)
}

func (r *Router) registerInternalVeloxRoutes() {
	r.veloxModule().Register(r.mux)
}

func (r *Router) registerUserVeloxDestinations(mux chi.Router) {
	r.integrationsModule().Register(mux)
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

func (r *Router) handleCreateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleCreateIntegrationVeloxDestination(w, req)
}

func (r *Router) handleListIntegrationVeloxDestinations(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleListIntegrationVeloxDestinations(w, req)
}

func (r *Router) handleGetIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleGetIntegrationVeloxDestination(w, req)
}

func (r *Router) handleDeleteIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleDeleteIntegrationVeloxDestination(w, req)
}

func (r *Router) handleUpdateIntegrationVeloxDestination(w http.ResponseWriter, req *http.Request) {
	r.integrationsModule().handleUpdateIntegrationVeloxDestination(w, req)
}
