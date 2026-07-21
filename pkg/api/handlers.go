package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
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
}

// WithDB wires the database for the /ready handler's DB ping +
// migrations check. Production wiring in internal/bootstrap.Wire
// passes App.DB; tests pass nil (which makes /ready return "db not
// configured" so the error is visible without dragging the real
// *sql.DB into unit tests).
func WithDB(db *sql.DB) RouterOption {
	return func(r *Router) { r.dbForReady = db }
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
}

// WithConnectionStateStore wires *repository.ConnectionStateRepository
// into the Router. Without this option, /api/v1/connections/* return 501.
func WithConnectionStateStore(s ConnectionStateStore) RouterOption {
	return func(r *Router) { r.connectionStates = s }
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

// WithVeloxDownloadJobChannel wires the durable Velox→InstaEdit handoff
// queue. The API only enqueues; the worker process consumes it.
func WithVeloxDownloadJobChannel(ch chan VeloxDownloadJob) RouterOption {
	return func(r *Router) { r.downloadJobCh = ch }
}

func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) { r.workspaceStore = repo }
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
	ClientID() string
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

// WithSnapshotStore wires the account resource snapshot cache. When
// nil, GET /accounts/{id} returns the base 6-field shape and
// /accounts/{id}/sync returns 501.
func WithSnapshotStore(s SnapshotStore) RouterOption {
	return func(r *Router) { r.snapshotStore = s }
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
		rateLimiter:   newRateLimiter(), // FASE 1.2: per-IP token bucket
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

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

// chain composes a list of middlewares around a final handler.
// chain(h, m1, m2) yields m1(m2(h)) — the first arg is the
// innermost handler, subsequent args wrap it in order. No-op
// identity when no middlewares are supplied.
func chain(handler http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	composed := handler
	// Apply in reverse so the first middleware in the slice is
	// the outermost wrapper at request time.
	for i := len(mws) - 1; i >= 0; i-- {
		composed = mws[i](composed)
	}
	return composed
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

// ----------------------------------------------------------------------- Helpers

func parsePathIDAsInt64(w http.ResponseWriter, req *http.Request, paramName string) (int64, bool) {
	s := req.PathValue(paramName)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+paramName+": "+s)
		return 0, false
	}
	return n, true
}

func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
	uid, ok := auth.UserIDFromContext(req.Context())
	if !ok || uid <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	return uid, true
}

func logAndError(w http.ResponseWriter, msg string, err error, kv ...any) {
	slog.Error(msg, append([]any{"error", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, msg+": "+err.Error())
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
