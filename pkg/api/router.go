package api

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api/contracts"
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

// VeloxDownloadJob aliases the worker-package type so handlers and
// local test files (package api) can refer to it WITHOUT an explicit
// worker. prefix at the call site. The canonical channel-item shape
// lives in internal/worker/velox_artifact_downloader.go; pkg/api only
// re-exports the name. Type aliases (Token `=`, NOT `type X Y`) are
// structurally identical to their target so channel assignments,
// field accesses, and test unmarshalling all work identically.
type VeloxDownloadJob = worker.VeloxDownloadJob

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
	oneTimeCodes   *OneTimeCodeStore
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
	// WithAdminInviteToken(cfg.AdminInviteToken). Production
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

	// Blocco #5.3 — Sentry hub + /ready wiring.
	// sentryHub is nil when SENTRY_DSN is unset (operator-disables-
	// by-omission). When set, the recovery middleware uses
	// sentryhttp.New() against this hub; when nil, plain recover.
	sentryHub *sentry.Hub
	// workerStatus tracks per-goroutine startup flags for the
	// /ready "5 worker loops started (no deadlock)" check.
	workerStatus *WorkerStatus
	// dbForReady is the *sql.DB used by /ready for PingContext +
	// SchemaHealthy. Nil disables both (test fixture path); the
	// production wiring in cmd/server/main.go passes app.DB.
	dbForReady *sql.DB

	// veloxAPIToken (P1 Velox integration) is the static shared
	// secret used by the service-to-service /internal/v1/* routes.
	// Loaded from env VELOX_API_TOKEN via internal/config + wired
	// with api.WithVeloxAPIToken. When empty AND registerInternalVeloxRoutes
	// is called, the route refuses to register (operator-safe boot
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
	// Per-route guarded in registerInternalVeloxRoutes — the validate
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

	// downloadJobCh is the buffered channel into which POST
	// /internal/v1/deliveries fires accepted-download work. The
	// download worker pool drains it. Optional: if nil the handler
	// drops the enqueue (logged at WARN) and accepts the delivery
	// anyway; a row-level reaper handles abandoned rows later.
	// Buffer size 64 absorbs typical bursts; on overflow the handler
	// logs WARN + drops so the 500ms p99 SLA is preserved. See
	// pkg/api/internal_velox.go::handleCreateInternalDelivery for
	// the dispatch path + VeloxDownloadJob for the payload shape.
	// downloadJobCh is the buffered fan-out from /internal/v1/deliveries
	// (handleCreateInternalDelivery) into the Velox artifact-download worker
	// pool. Buffer of 64 absorbs the 9-tenant peak burst (6 tenants × 2 jittered
	// retries per the channel-import cutover). Bidirectional (not chan<-) so
	// the test harness can drain it without needing a separate handle to the
	// underlying OS pipe.
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

	downloadJobCh chan VeloxDownloadJob

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
}

// ConnectionStateStore is declared in pkg/api/connections.go (SPRINT 1.2);
// placeholder import to keep repository wired in this package so the
// above struct field typechecks.

var _ = repository.RoleAdmin

// ConnectionStateStore is the persistence contract for connection_states
// (SPRINT 1.2). Defined inline to keep pkg/api off internal/repository
// imports; main.go injects *repository.ConnectionStateRepository which
// satisfies this interface. Implementations live in pkg/api/connections.go
// once that file is materialised.
type ConnectionStateStore interface {
	Create(state *repository.ConnectionState) error
	Consume(id string, expectedNonce string, jwtWorkspaceID int64) (*repository.ConnectionState, error)
}

// SessionsStore is the contract between the HTTP layer and the SPRINT 2.1
// session lifecycle. Production wiring in cmd/server/main.go injects the
// concrete *services.SessionsService (which satisfies the interface).
// Tests inject an in-memory fake (see fakeSessionsService in
// pkg/api/auth_email_test.go and pkg/api/sessions_test.go) so handler
// tests don't need a real *sql.DB-bound SessionRepository.
//
// The methods mirror the post-Sprint-2.1 rotation/revoke contract:
//   - Start creates a session row + access/refresh cookie pair.
//   - Refresh rotates the refresh token + reuses-detection revokes
//     the entire family on reuse (Row.RevokedAt != nil).
//   - Revoke revokes a single session owned by the caller.
//   - RevokeAll revokes every active session for the caller.
//   - List returns every session (active + revoked) for the caller,
//     ordered by LastUsedAt DESC; used by GET /auth/sessions.
//   - WithdrawFromCookie is the cookie-anchored logout: revoke the
//     row whose hash matches the supplied refresh cookie value.
type SessionsStore interface {
	Start(services.StartSessionRequest) (*services.StartSessionResult, error)
	Refresh(services.RefreshRequest) (*services.StartSessionResult, error)
	Revoke(sessionID, ownerUserID int64, reason string) error
	RevokeAll(userID int64, reason string) (int64, error)
	List(userID int64) ([]repository.Session, error)
	WithdrawFromCookie(refreshPlain string) error
	// IsActive verifies that a session row exists and has not been
	// revoked. Used by the cookie-refresh middleware to reject
	// invalidated access tokens early.
	IsActive(sessionID int64) (bool, error)
}

type UserStore interface {
	// AttachPlatformAccount links an OAuth platform profile to the
	// authenticated user identified by userID. SPRINT 7.1 (P0#14)
	// closed the OAuth-auto-create gap: this method NEVER creates a
	// user, only attaches a (platform, platform_user_id) tuple to
	// an existing one. Returns ErrAccountAlreadyLinked (mapped to
	// HTTP 409 by the OAuth callback handler) when the tuple is
	// already linked to a different user.
	AttachPlatformAccount(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error)
	ListPlatformAccountsByUser(userID int64, platform string) ([]*models.PlatformAccount, error)
	// ListFilteredYouTubeAccounts returns the YouTube platform accounts for a user,
	// optionally filtered by workspace, group_name (from workspace_channels), and
	// language/manager values stored in the account metadata JSONB.
	ListFilteredYouTubeAccounts(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error)
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
	// FindPlatformAccount loads an existing platform account by its
	// provider-scoped (platform, platform_user_id) tuple. Used by
	// the OAuth callback to detect idempotent re-links for the same
	// user and to refuse account takeovers across users.
	FindPlatformAccount(platform, platformUserID string) (*models.PlatformAccount, error)
	UpdatePlatformAccount(account *models.PlatformAccount) error
	DeletePlatformAccount(id int64) error
	// FindUserIDByEmail (P2 — admin CSV import) resolves an email to
	// the underlying user_id (FK on platform_accounts). The admin
	// /channels/import-csv endpoint uses this to honour the
	// owner_email form field; the CLI (scripts/import_channels_csv.go)
	// uses the same method via a *repository.UserRepository wrapper.
	// Returns ErrUserNotFound when the email is unknown.
	FindUserIDByEmail(ctx context.Context, email string) (int64, error)
	// FinalizeAttach (P2 — admin connect-link) is invoked by the
	// OAuth callback AFTER a successful AttachPlatformAccount +
	// vault.Save. It UPSERTs the oauth_connections row (keyed on
	// (user_id, provider, provider_resource_id)) and promotes the
	// platform_account from 'pending_authorization' to 'active'.
	// The vault's token row requires oauth_connection_id to be set
	// (FK); FinalizeAttach is what stamps that FK onto the row.
	// Idempotent on re-auth for the same channel — refreshes
	// connected_at + scopes without losing the oauth_connections
	// row. Returns the oauth_connection_id used.
	//
	// As of Task 1/10 the production HTTP callback path goes
	// through services.ChannelAuthorizer.AuthorizeChannel (see
	// r.authorizer in Router) — one atomic transaction replaces
	// FinalizeAttach + vault.Save with a SINGLE roll-back-able
	// call. The method is kept on the interface for any third
	// party / future caller that wants the tx-isolated half
	// without the token write.
	FinalizeAttach(ctx context.Context, accountID int64, scopes []string) (int64, error)
	// MarkReauthRequired (Task 2/10 — channel-binding guard) flips
	// a platform_account's status to 'reauth_required' with a
	// code + message pair. Called by the OAuth callback path when
	// attachDiscoveredAccounts returns ErrYouTubeChannelMismatch
	// (the channels.list?mine=true result did not contain the
	// channel id the operator expected). Best-effort: a failure
	// here logs a warning but does NOT prevent the HTTP 422
	// response from returning — the publish_worker's next tick
	// will sweep any post_targets whose account drifted and stamp
	// blocked_auth on them independently. Idempotent on the DB
	// side (re-flips with a fresh reauth_required_at on each call).
	MarkReauthRequired(ctx context.Context, accountID int64, code, message string) error
}

type WorkspaceStore interface {
	Create(w *models.Workspace) error
	FindByID(id int64) (*models.Workspace, error)
	ListByOwner(ownerID int64) ([]models.Workspace, error)
	Delete(id int64) error
	// P0#4 — workspace_channels join surfaces. Matched 1:1 by
	// repository.WorkspaceRepository.* — owner implements every
	// method here, and mockWorkspaceStore keeps the test fixtures in
	// lockstep. Method bodies are: AttachChannel (UPSERT,
	// group_name refresh on conflict), ListChannels (newest-first,
	// bounded by workspace_id), UpdateChannel (COALESCE on
	// group_name / enabled for partial-update semantics),
	// DetachChannel (404 on no-row), FindChannel (PK-indexed
	// single-row read-back; used after UpdateChannel to avoid
	// paying the ListChannels + scan cost on every PATCH).
	AttachChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName string) (*models.WorkspaceChannel, error)
	ListChannels(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error)
	UpdateChannel(ctx context.Context, workspaceID, platformAccountID int64, groupName *string, enabled *bool) error
	DetachChannel(ctx context.Context, workspaceID, platformAccountID int64) error
	FindChannel(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error)
}

type PostStore interface {
	Create(post *models.Post, targets []*models.PostTarget) error
	FindByID(id int64) (*models.Post, error)
	Update(post *models.Post) error
	ListByWorkspace(workspaceID int64) ([]models.Post, error)
	Delete(id int64) error
	SaveTarget(target *models.PostTarget) error
	PublishPost(id int64) error
	CancelPost(id int64) error
	RetryPost(id int64) error
	RetryTarget(id int64) error
}

// GroupStore mirrors the subset of repository.GroupRepository that
// the /api/v1/groups handlers need. Same pattern as WorkspaceStore /
// PostStore: interface is local to pkg/api so test fixtures can supply
// an in-memory fake. Production wiring in internal/bootstrap/app.go
// passes *repository.GroupRepository which satisfies this contract.
//
// ValidateAccountOwnership takes (userID, workspaceID) so the API
// layer can defend against the rare "user owns this account in a
// DIFFERENT workspace" cross-attach attempt — defence-in-depth on top
// of the SQL FK chain.
type GroupStore interface {
	Create(g *models.Group) error
	FindByID(id int64) (*models.Group, error)
	Update(g *models.Group) error
	Delete(id int64) error
	ListByWorkspace(workspaceID int64) ([]models.Group, error)
	ListAccountsInGroup(groupID int64) ([]int64, error)
	// ValidateAccountOwnership returns the subset of supplied
	// accountIDs that are visible to (userID, workspaceID). The
	// PUT /api/v1/groups/{id}/accounts handler intersects the
	// caller-supplied list against this before SetAccounts so a
	// hostile payload cannot attach an account the caller does not
	// own to a foreign group. Empty slice + nil error when none
	// of the supplied ids belong to the caller.
	ValidateAccountOwnership(userID, workspaceID int64, accountIDs []int64) ([]int64, error)
	SetAccounts(groupID int64, accountIDs []int64) error
}

// ApiKeyStore mirrors the subset of repository.ApiKeyRepository that
// the API layer + Authenticator middleware actually depend on.
type ApiKeyStore interface {
	Create(key *models.ApiKey, hash []byte) error
	FindByIDForWorkspace(wsID, id int64) (*models.ApiKey, error)
	FindByHash(hash []byte) (*models.ApiKey, error)
	ListByWorkspace(wsID int64) ([]models.ApiKey, error)
	Revoke(wsID, id int64) error
	MarkUsed(wsID, id int64) error
	UpdateName(wsID, id int64, name string) error
	Rotate(wsID, oldID int64, newKey *models.ApiKey, newHash []byte) error
}

// IdempotencyStore mirrors the two methods the /api/v1/posts
// handler (handleCreatePost) needs:
//
//   - FindActiveByKey — pre-handler lookup. Returns (nil, nil)
//     on miss OR on expired rows (so the middleware treats expired
//     records as a normal miss and lets the handler run).
//   - Insert — post-handler write. Persists (workspace_id, key,
//     hash) so subsequent replays hit the same row.
//
// The contract is intentionally narrow: no Update, no Delete from
// the API layer; the table is append-only from this side. Expired
// rows are evicted by a CRON sweeper that lands in a future Taglio.
//
// Same pattern as PostStore / WorkspaceStore / ApiKeyStore: an
// interface local to pkg/api so handlers depend on the contract,
// not on the *sql.DB-bound concrete type. Tests can pass an
// in-memory fake.
type IdempotencyStore interface {
	FindActiveByKey(workspaceID int64, key string, now time.Time) (*models.IdempotencyRecord, error)
	Insert(rec *models.IdempotencyRecord) error
	// FindBatchReplay + InsertBatchReplay (migration 039, drive_batch
	// idempotency, Taglio 4.7 LEVEL 1 extension). drive_batch creates
	// up to N=200 upload_jobs in one POST so there's no single source-of-truth
	// row to re-fetch on replay; the cached response payload lives in a
	// 1:1 side table (idempotency_batch_replays) keyed on the parent
	// idempotency_record_id. The replay path is wired in
	// pkg/api/idempotency.go's replayIdempotentResource ("drive_batch"
	// branch) and the handler writes both rows via
	// insertBatchIdempotentRecord in idempotency.go.
	FindBatchReplay(idempotencyRecordID int64) (*models.BatchReplay, error)
	InsertBatchReplay(rec *models.BatchReplay) error
}

type StorageProvider interface {
	Provider() string
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error)
	// VerifyUpload (Taglio 3.2) HEADs the object and returns
	// server-reported content-type + size for /complete verification.
	VerifyUpload(ctx context.Context, key string) (contentType string, sizeBytes int64, err error)
	// AssetURL (Taglio 3.2) returns the trusted internal URL for an
	// uploaded asset. The publish flow goes through this — the
	// platform API never sees a user-controlled URL.
	AssetURL(key string) string
}

type AuditLogStore interface {
	Log(ctx context.Context, eventType, actorID string, resourceType, resourceID string, metadata map[string]interface{}) error
}

// UploadJobStore is the persistence contract for the background
// upload_jobs queue. The API layer both creates new jobs (batches)
// AND reads aggregates for the dashboard status endpoint. The
// worker claims and updates the underlying rows; the API layer does
// NOT touch status transitions from the request path.
type UploadJobStore interface {
	Create(job *models.UploadJob) error
	// ListByUser returns upload_jobs scoped to the caller (userID)
	// with optional filters (account_id / status / from-to). Backs
	// the dashboard "Programmati" view (per-account calendar) and
	// any future "pending uploads" widget. nil filter fields are
	// no-ops; the SQL is one statement with NULL-or-equal predicates
	// so the planner keeps a single plan across all combinations.
	ListByUser(userID int64, filter repository.UploadJobListFilter) ([]models.UploadJob, error)
	// PendingCountsByAccount returns one aggregate row per target
	// account the user has pending uploads on (count + earliest
	// scheduled_at). Single GROUP BY query, no row cap — exact
	// counts even when the user has 10k scheduled rows. Handler
	// maps to GET /api/v1/uploads/counts for the dashboard widget.
	PendingCountsByAccount(userID int64) ([]repository.UploadJobPendingCount, error)
	// PendingDistinctCount returns the user's total number of pending
	// upload_jobs (distinct rows, not per-target expansions). The
	// dashboard's "Pending uploads" stat reads from this — using
	// SUM(PendingCountsByAccount.count) over-counts one upload that
	// targets multiple accounts.
	PendingDistinctCount(userID int64) (int64, error)
	// Reschedule atomically updates scheduled_at for a pending
	// upload_job. Returns the updated row on success; typed
	// repository.ErrUploadJobNotFound when the id is unknown OR
	// the job has already moved past `pending` (worker claimed
	// / completed / failed). handler maps to HTTP 404.
	Reschedule(jobID, userID int64, newScheduledAt time.Time) (models.UploadJob, error)
	// Cancel atomically deletes a pending upload_job. Same state-
	// machine + authz contract as Reschedule; returns
	// repository.ErrUploadJobNotFound on missing / non-pending rows.
	Cancel(jobID, userID int64) error
	// AggregateByFolder returns the per-status counts + min/max
	// scheduled_at scoped to (folder_id, user_id). Used by
	// GET /api/v1/media/import/drive/batch/status for the
	// dashboard. Returns a zero-value BatchStatusSummary (not an
	// error) when no rows match — the handler turns that into a
	// 200 + note rather than 404.
	AggregateByFolder(folderID string, userID int64) (models.BatchStatusSummary, error)
}

type RouterOption func(*Router)

// WithConnectLinkNonceStore wires the store used to persist and
// atomically consume connect-link nonces. When nil, replay
// protection is disabled (tests and legacy deployments).
func WithConnectLinkNonceStore(store ConnectLinkNonceStore) RouterOption {
	return func(r *Router) {
		r.connectLinkNonceStore = store
	}
}

// ConnectLinkNonceStore is the persistence contract for connect-link
// jti values. Production wiring passes *repository.ConnectLinkNonceRepository.
// The stored value is the JWT's RegisteredClaims.ID (jti), which
// replaces the legacy custom "nonce" claim.
//
// Consume returns nil on success. On a known rejection it returns one
// of repository.ErrNonceMissing, repository.ErrNonceExpired, or
// repository.ErrNonceConsumed so the caller can log/metric the exact
// reason. Any other error indicates a database or transaction failure.
type ConnectLinkNonceStore interface {
	Create(jti, expectedChannelID string, expiresAt time.Time) error
	Consume(jti string) error
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
}

// P2 — ops dashboard store. AdminStore is the read-side
// contract for the /admin/* endpoints; the AdminRepository
// implementation in internal/repository/admin_repo.go owns
// all the queries. Same pattern as UploadJobStore: a local
// interface so tests can supply an in-memory fake without
// dragging the *sql.DB-bound concrete type.
type AdminStore interface {
	ChannelCounts(ctx context.Context) (repository.AdminChannelCounts, error)
	ListChannelsForOps(ctx context.Context, statusFilter, platformFilter string, limit int) ([]repository.AdminChannelRow, error)
	QueueCounts(ctx context.Context) (repository.AdminQueueCounts, error)
	InFlightPerWorker(ctx context.Context) ([]repository.AdminInFlightRow, error)
	ListStuckJobs(ctx context.Context, limit int) ([]repository.AdminStuckJobRow, error)
	// ListDeadLetterJobs (Task 10/10) surfaces upload_jobs in
	// status='dead_letter' so the operator can triage retry-budget
	// exhaustions. JSON via /admin/upload_jobs/dead_letter; CSV via
	// /admin/upload_jobs/dead_letter.csv. Bounded by 500 so the
	// response stays under the dashboard render budget.
	ListDeadLetterJobs(ctx context.Context, limit int) ([]repository.AdminDeadLetterJobRow, error)
	ErrorRatePerChannel(ctx context.Context, windowInterval, windowLabel string, limit int) ([]repository.AdminErrorRateRow, error)
	YouTubeQuotaApproximation(ctx context.Context, window time.Duration, dailyBudgetUnits, costPerUploadUnits int64) (repository.AdminYouTubeQuota, error)
	// UpsertPendingChannel (P2 — admin CSV import) bulk-upserts
	// pre-resolved channel rows into platform_accounts at
	// status='pending_authorization'. Mirrors the production
	// /admin/channels/import-csv endpoint's DB-write contract:
	// UPSERT on (platform, platform_user_id), last-write-wins,
	// status ALWAYS reset to 'pending_authorization', metadata
	// refreshed. NEVER writes tokens (the OAuth callback is the
	// only path that sets the cipher row in credentials.vault).
	//
	// Per-row DB failures surface in Result.Errors as
	// channelimport.RowError slices (not return-as-error) so
	// partial-success visibility is preserved when an operator
	// uploads 500-channel sheets.
	UpsertPendingChannel(ctx context.Context, ownerUserID int64, rows []channelimport.ImportRow) (channelimport.Result, error)
	// CreateFleetReadinessSnapshot (Definition-of-Done rollout) takes
	// an append-only snapshot of the YouTube platform_account fleet --
	// the 12 readiness counters (active / pending / reauth / etc) +
	// the per-channel "is this channel OK?" detail rows. The JSON
	// envelope is what /admin/youtube/fleet_readiness returns; the
	// per-channel rows persist to fleet_readiness_snapshot_channels
	// so successive calls produce an audit trail an operator can
	// diff to spot channels that flipped recently.
	CreateFleetReadinessSnapshot(ctx context.Context, adminUserID int64) (repository.FleetReadinessSnapshotResponse, error)
}

// SnapshotStore is the persistence contract for
// account_resource_snapshots. Defined inline to keep pkg/api off
// internal/repository imports; main.go injects
// *repository.SnapshotRepository which satisfies this interface.
type SnapshotStore interface {
	GetSnapshot(platformAccountID int64) (*repository.AccountResourceSnapshot, error)
	UpsertSnapshot(snap *repository.AccountResourceSnapshot) error
	IsSnapshotStale(platformAccountID int64, maxAge time.Duration) (bool, error)
}

// MetricHistoryStore is the persistence contract for daily account
// metrics. Defined inline to keep pkg/api off internal/repository
// imports; main.go injects *repository.AccountMetricsRepository.
type MetricHistoryStore interface {
	UpsertDaily(platformAccountID int64, date time.Time, point repository.AccountMetricPoint) error
	UpsertMonetary(platformAccountID int64, date time.Time, point repository.AccountMetricPoint) error
	GetHistory(platformAccountID int64, from, to time.Time) ([]repository.AccountMetricPoint, error)
}

func NewRouter(
	capRouter *services.CapabilityRouter,
	userRepo UserStore,
	authMgr *auth.Manager,
	frontendURL string,
	allowedOrigins []string,
	opts ...RouterOption,
) *Router {
	r := &Router{
		capabilities:  capRouter,
		userRepo:      userRepo,
		auth:          authMgr,
		oneTimeCodes:  NewOneTimeCodeStore(60 * time.Second),
		frontendURL:   frontendURL,
		allowedOrigin: allowedOrigins,
		rateLimiter:   newRateLimiter(nil), // FASE 1.2: per-IP token bucket (trusted proxies wired via option below)
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
	return r
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
