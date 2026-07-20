package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
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
//                              that issued the grant — guards against
//                              Production-vs-Testing token drift)
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

// ----------------------------------------------------------------------- Handlers

func (r *Router) handleHealth(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"service":   "InstaEditLogin",
		"version":   "2.0.0",
		"platforms": r.capabilities.Names(),
	})
}

func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.capabilities.OAuth(provider)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}
	// Translate ?mode=add|reconnect into OAuthLoginOptions.
	// "add" forces account selection (Google account picker).
	// "reconnect" forces consent re-approval.
	mode := req.URL.Query().Get("mode")
	var options services.OAuthLoginOptions
	switch mode {
	case "add":
		options.SelectAccount = true
		options.ForceConsent = true
	case "reconnect":
		options.ForceConsent = true
	}
	// YouTube-only: ?expected_channel_id=UC... tells the server which
	// channel the operator intends to bind the OAuth grant to. Without
	// it, a Google account with N>1 channels cannot be attached safely
	// (channels.list(mine=true) returns every Brand Account under the
	// grant, and the bearer token is bound to one channel per
	// Brand-Account selection). The hint round-trips through a sibling
	// HttpOnly cookie (NOT the URL state param — Google echoes the URL
	// state verbatim, and we keep it a pure CSRF nonce).
	expectedChannelID := ""
	if raw := req.URL.Query().Get("expected_channel_id"); raw != "" {
		if provider == models.PlatformYouTube && isValidYouTubeChannelID(raw) {
			expectedChannelID = raw
			// expected_channel_id ALWAYS implies account picker +
			// consent so a previously-cached grant cannot bind to a
			// different Brand Account.
			options.SelectAccount = true
			options.ForceConsent = true
		}
	}

	state, err := generateOAuthState(w, provider, expectedChannelID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start oauth flow")
		return
	}

	http.Redirect(w, req, p.GetLoginURLWithOptions(state, options), http.StatusFound)
}

func (r *Router) handleCallback(w http.ResponseWriter, req *http.Request) {
	provider := req.PathValue("provider")
	p, ok := r.capabilities.OAuth(provider)
	if !ok {
		writeError(w, http.StatusNotFound, "unsupported provider: "+provider)
		return
	}
	code := req.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "missing authorization code")
		return
	}
	state := req.URL.Query().Get("state")
	if state == "" {
		writeError(w, http.StatusBadRequest, "missing state parameter")
		return
	}
	// P2 — admin connect-link. When the state param is JWT-shaped
	// (2 dots: header.payload.sig), it was issued by the admin
	// POST /admin/channels/{channel_id}/connect-link handler and
	// already carries the expected_channel_id, signed HS256 with
	// the same secret as the auth JWTs. We re-verify here so the
	// callback can refuse forged / replayed connect-link state
	// without involving the CSRF state-cookie row (the connect
	// flow has the manager browser, not the admin's). The
	// boolean return is threaded down so the ErrYouTubeChannelMismatch
	// mapping at the bottom of this handler can switch its status
	// code from 409 (legacy cookie path) to 422 (P2 connect-link
	// per the operator's intent).
	expectedChannelID := ""
	fromConnectLinkState := false
	var stateErr error
	if strings.Count(state, ".") == 2 {
		expectedChannelID, stateErr = r.auth.VerifyConnectLinkState(state)
		if stateErr != nil {
			writeError(w, http.StatusBadRequest, "invalid connect-link state: "+stateErr.Error())
			return
		}
		fromConnectLinkState = true
	} else {
		expectedChannelID, stateErr = verifyOAuthState(w, req, provider, state)
		if stateErr != nil {
			writeError(w, http.StatusBadRequest, "invalid state: "+stateErr.Error())
			return
		}
	}
	profile, tokenData, err := p.HandleCallback(req.Context(), state, code)
	if err != nil {
		metrics.RecordOAuthLoginError(provider, metrics.ErrorKind(err))
		writeError(w, http.StatusInternalServerError, "authentication failed: "+err.Error())
		return
	}
	metrics.RecordOAuthLoginSuccess(provider)

	// SPRINT 7.1 (P0#14): session requirement is enforced by the
	// oauthSessionRedirect middleware mounted in Setup(). The user
	// is guaranteed to exist here.
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil {
		// Defence-in-depth: the middleware should have redirected,
		// but if it didn't (e.g. wired without the new option in a
		// test fixture), refuse the connect with 401 rather than
		// silently auto-creating users.
		writeError(w, http.StatusUnauthorized, "oauth social requires an InstaEdit session")
		return
	}
	userID := identity.UserID()

	// Providers that expose AccountDiscoverer (Facebook Pages) expand
	// one OAuth grant into N platform accounts. For those providers we
	// discover the pages, create one PlatformAccount per page, and
	// persist the per-page access token. Otherwise we fall back to the
	// single-account attach path.
	var account *models.PlatformAccount
	if discoverer, ok := r.capabilities.Discoverer(provider); ok {
		account, err = r.attachDiscoveredAccounts(req.Context(), userID, provider, discoverer, tokenData, expectedChannelID)
		if err != nil {
			// YouTube-only typed errors surface as 409 Conflict so the
			// SPA knows to ask the operator to disambiguate before
			// retrying. Other discoverer failures stay 500 (genuine
			// server / DB problems).
			if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			if errors.Is(err, ErrYouTubeChannelMismatch) {
				// Task 2/10: best-effort flip
				// platform_account.status to 'reauth_required'
				// so the operator dashboard surfaces the
				// failure immediately. The publish_worker's
				// next tick will also flip the per-target
				// rows to PostStatusBlockedAuth via
				// markPublishBlockedAuth, but we want UI
				// visibility before the next tick fires.
				// Soft error: a MarkReauthRequired failure
				// does NOT prevent the 422/409 writeError
				// from returning (publish_worker is the
				// authoritative sweep on a longer horizon).
				if account != nil && r.userRepo != nil {
					if flagErr := r.userRepo.MarkReauthRequired(req.Context(), account.ID, "youtube_channel_mismatch", err.Error()); flagErr != nil {
						slog.WarnContext(req.Context(), "could not flag platform_account reauth_required after youtube channel mismatch",
							"platform_account_id", account.ID, "error", flagErr)
					}
				}
				// P2 — connect-link refinement: 422 when the state
				// was a JWT issued by /admin/channels/{id}/connect-link
				// (the operator bound a specific channel_id via
				// the admin dashboard; mismatch is a semantic
				// contradiction, prefer 422). Legacy path
				// (?expected_channel_id=UC… cookie) keeps 409 for
				// backwards-compat with operators wired before
				// the connect-link flow landed.
				if fromConnectLinkState {
					writeError(w, http.StatusUnprocessableEntity, err.Error())
					return
				}
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to attach discovered accounts: "+err.Error())
			return
		}
	} else {
		// Attach to the authenticated user — never auto-create.
		account, err = r.userRepo.AttachPlatformAccount(userID, profile, provider)
		if err != nil {
			if errors.Is(err, repository.ErrAccountAlreadyLinked) {
				// Operator runbook: the legal owner of the link must
				// disconnect via DELETE /api/v1/accounts/{id} before
				// re-link is possible.
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to attach platform account: "+err.Error())
			return
		}

		// Task 1/10 — atomic OAuth finalize. We use the
		// services.ChannelAuthorizer (wired via WithChannelAuthorizer
		// in internal/bootstrap.Wire) for the non-discoverer branch
		// too: passing expectedChannelID="" tells the service to
		// skip the channels.list(mine=true) YouTube-only pre-tx
		// guard, but the (UPSERT oauth_connections + INSERT tokens
		// via SaveTokenTx + UPDATE platform_accounts.status='active')
		// atomic flow still applies. Any partial failure rolls back
		// BOTH writes plus the status flip so a process crash
		// between AttachPlatformAccount (commits row at pending_authorization)
		// and this AuthorizeChannel call leaves the account in
		// pending_authorization, never in the legacy "active but
		// no cipher row" failure mode.
		//
		// expectedChannelID "" → no YouTube binder call (binder
		// may still be wired for other providers' flows). The
		// service's empty-string short-circuit is the documented
		// no-op for non-YouTube paths (Facebook Pages, Threads,
		// TikTok, …).
		if r.authorizer == nil {
			// Fail-fast on misconfiguration (mirrors the postStore /
			// workspaceStore nil-guard pattern). A misconfigured
			// main.go that forgets WithChannelAuthorizer would never
			// have been caught by Wire() but would silently leave
			// platform_accounts in pending_authorization forever
			// on every callback — the operator's dashboard would
			// show a stuck "needs reconnect" storm. Fail-fast
			// surfaces the wiring mistake at first-callback time.
			writeError(w, http.StatusInternalServerError, "channel authorizer not configured")
			return
		}
		if _, err := r.authorizer.AuthorizeChannel(req.Context(), account.ID, "", tokenData.Scopes, tokenData); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to authorize channel: "+err.Error())
			return
		}
	}

	// SPRINT 7.1 redirect target: the SPA's account-linking page. No
	// one-time code is needed — the session cookie validated at the
	// top of this handler IS the active session.
	if r.frontendURL != "" {
		q := url.Values{}
		q.Set("provider", provider)
		q.Set("status", "connected")
		http.Redirect(w, req, strings.TrimRight(r.frontendURL, "/")+"/app/linking?"+q.Encode(), http.StatusFound)
		return
	}
	// CLI / test mode (no FRONTEND_URL): typed JSON response so
	// callers can pipeline the result without following a redirect.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "connected",
		"provider":   provider,
		"user_id":    userID,
		"account_id": account.ID,
	})
}

// attachDiscoveredAccounts is used by handleCallback for providers that
// expose AccountDiscoverer (Facebook Pages, YouTube Channels). It creates
// one PlatformAccount per discovered account and persists tokens.
//
// Token strategy per provider:
//   - YouTube: every discovered channel receives the root OAuth bearer
//     token (the same token is shared across all channels from one grant).
//     SupplementalTokens is nil/empty for YouTube.
//   - Facebook Pages: each Page carries a SupplementalToken
//     (TokenTypePageAccess) with the per-Page Page Access Token, plus the
//     root long-lived user token stored as TokenTypeLongLived on every
//     discovered page (so refresh can re-exchange from any page).
//
// The generalized flow:
//  1. Discover accounts via the provider's DiscoverAccounts.
//  2. For each DiscoveredAccount, AttachPlatformAccount (idempotent).
//  3. Save metadata from DiscoveredAccount.Metadata on the account row.
//  4. Save the root token on every discovered account.
//  5. Save every DiscoveredAccount.SupplementalTokens entry as an
//     additional token in the vault. This replaces the old provider-
//     specific hack that checked for Metadata["page_access_token"].
//
// ErrYouTubeAmbiguousAuthorization is returned by attachDiscoveredAccounts
// when a YouTube OAuth grant's channels.list(mine=true) returns >1
// channel AND no expected_channel_id was supplied at login time.
//
// P0: a single Google account can own multiple YouTube channels
// (Brand Accounts, multi-channel networks). YouTube's OAuth grant is
// bound to ONE channel per Brand-Account selection at consent time.
// Cloning the root bearer token across every channel silently
// violates Google's YouTube Data API contract and misroutes uploads
// to whatever channel the grant happens to target. The operator must
// re-authorize via /api/v1/auth/youtube/login with
// ?expected_channel_id=UC... so channels.list can be filtered to a
// single channel before any token is saved. Handler maps this to
// HTTP 409 Conflict so the SPA can ask the operator to disambiguate.
var ErrYouTubeAmbiguousAuthorization = errors.New("youtube authorization is ambiguous: re-authorize with expected_channel_id")

// ErrYouTubeChannelMismatch is returned when expected_channel_id was
// supplied but channels.list(mine=true) does NOT contain that ID. The
// operator authenticated the wrong Google account, mistyped the ID,
// or a Brand Account was added since the inventory was imported. We
// refuse to attach ANY account because saving the root token on a
// different channel would silently misroute uploads. Handler maps
// this to HTTP 409 Conflict.
var ErrYouTubeChannelMismatch = errors.New("youtube authorized channel does not match expected channel")

func (r *Router) attachDiscoveredAccounts(ctx context.Context, userID int64, provider string, discoverer services.AccountDiscoverer, tokenData *models.TokenData, expectedChannelID string) (*models.PlatformAccount, error) {
	accounts, err := discoverer.DiscoverAccounts(ctx, tokenData.AccessToken, "")
	if err != nil {
		return nil, fmt.Errorf("discover accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts discovered for provider %s", provider)
	}

	// YouTube enforces a 1:1 OAuth-grant-to-channel mapping. The
	// root bearer token is bound to whichever Brand Account the
	// operator selected in Google's consent screen; cloning it
	// across every channel silently misroutes uploads. Other
	// AccountDiscoverer providers (Facebook Pages, Instagram
	// Business Accounts) intentionally fan the root token out to
	// every discovered account — that path stays unchanged.
	if provider == models.PlatformYouTube {
		if expectedChannelID != "" {
			filtered := accounts[:0]
			matched := 0
			for _, acc := range accounts {
				if acc.Profile.PlatformUserID == expectedChannelID {
					filtered = append(filtered, acc)
					matched++
				}
			}
			if matched == 0 {
				return nil, fmt.Errorf("%w: %q is not in channels.list(mine=true) result", ErrYouTubeChannelMismatch, expectedChannelID)
			}
			if matched > 1 {
				// Defensive against channels.list returning duplicates
				// for the same resource; the first match wins.
				filtered = filtered[:1]
			}
			accounts = filtered
		} else if len(accounts) != 1 {
			return nil, fmt.Errorf("%w: channels.list returned %d channels for this grant", ErrYouTubeAmbiguousAuthorization, len(accounts))
		}
	}

	var first *models.PlatformAccount
	for _, acc := range accounts {
		profile := &models.PlatformProfile{
			PlatformUserID: acc.Profile.PlatformUserID,
			Username:       acc.Profile.Username,
		}
		created, err := r.userRepo.AttachPlatformAccount(userID, profile, provider)
		if err != nil {
			if errors.Is(err, repository.ErrAccountAlreadyLinked) {
				// Already linked to this user — load the existing row so
				// we can update its token below.
				existing, findErr := r.userRepo.FindPlatformAccount(provider, acc.Profile.PlatformUserID)
				if findErr != nil {
					return nil, fmt.Errorf("find existing account: %w", findErr)
				}
				if existing == nil {
					return nil, fmt.Errorf("account already linked but not found")
				}
				created = existing
			} else {
				return nil, fmt.Errorf("attach account %s: %w", acc.Profile.PlatformUserID, err)
			}
		}

		if first == nil {
			first = created
		}

		// Persist metadata from discovery (handle, avatar, stats, etc.)
		if len(acc.Metadata) > 0 {
			if created.Metadata == nil {
				created.Metadata = make(models.Metadata)
			}
			for k, v := range acc.Metadata {
				// Do not overwrite existing metadata keys.
				if _, exists := created.Metadata[k]; !exists {
					created.Metadata[k] = v
				}
			}
			if err := r.userRepo.UpdatePlatformAccount(created); err != nil {
				return nil, fmt.Errorf("update metadata for account %d: %w", created.ID, err)
			}
		}

		// P2 — admin connect-link: Task 1/10 atomic flip. The
		// previous two-call sequence (FinalizeAttach + vault.Save
		// + supplemental vault.Save) could leave the platform_account
		// row in status='active' WITHOUT a tokens row if the vault
		// save failed AFTER FinalizeAttach committed. The new
		// services.ChannelAuthorizer.AuthorizeChannel merges those
		// writes into ONE transaction inside services/
		// channel_authorization.go: any failure rolls every write
		// back, keeping the platform_account row in its pre-call
		// state (typically 'pending_authorization').
		// Equivalent codes behaviour preserved:
		//   - ErrYouTubeChannelMismatch → 422 (via the binder
		//     guard inside AuthorizeChannel)
		//   - Eligibility-gate reject → 422 (status not in
		//     pending_authorization / active / reauth_required)
		//   - DB write failure → 5xx (wrapped, retryable)
		// The principal token + every supplemental token are
		// persisted inside the SAME tx so a Page Access Token
		// (Facebook) failure rolls back its principal user token
		// write AND the oauth_connections row too.
		channelTokens := make([]*models.TokenData, 0, 1+len(acc.SupplementalTokens))
		channelTokens = append(channelTokens, tokenData)
		channelTokens = append(channelTokens, acc.SupplementalTokens...)
		if r.authorizer == nil {
			// Fail-fast on misconfiguration (symmetric to the
			// non-discoverer branch). Mirrors the postStore /
			// workspaceStore nil-guard pattern. Without this,
			// a misconfigured main.go (missing
			// WithChannelAuthorizer) would silently leave every
			// discovered-discoverer account stuck at
			// pending_authorization with no encrypted token
			// row, even though AttachPlatformAccount's commit
			// looks successful. The fail-fast 500 surfaces the
			// wiring mistake at first-callback time.
			return nil, errors.New("channel authorizer not configured")
		}
		if _, err := r.authorizer.AuthorizeChannel(ctx, created.ID, expectedChannelID, tokenData.Scopes, channelTokens...); err != nil {
			return nil, fmt.Errorf("authorize channel for account %d: %w", created.ID, err)
		}
	}

	return first, nil
}

// handleExchangeCode exchanges a one-time code (from /auth/callback?code=...)
// for a fresh session row + access JWT + refresh token. The code is
// single-use and 60s TTL; on success both cookies are set and 204 is
// returned. The SPA's /auth/callback page calls this immediately on
// mount, then redirects to /dashboard.
//
// SPRINT 1.1: the JWT MUST carry the user's active workspace.
// Resolution order: ExplicitWorkspaceID (set by /api/v1/connections/{p}/start
// in Sprint 1.2 future work — currently always nil) > first owned
// workspace > workspace_members. If none, we create a personal workspace
// and add the user as admin so the JWT can be issued.
//
// SPRINT 7.4 (P0#14-blocco-1.4): JWT issuance migrated to
// SessionsService.Start. Previously this handler called
// r.auth.Issue(payload.UserID, activeWS) which minted a
// sessionID=0 JWT — incompatible with Manager.Verify post-Sprint-2.1
// hardening. The single SessionsService.Start call now creates the
// session row AND binds the row's positive ID to the access JWT.
func (r *Router) handleExchangeCode(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.sessionsSvc == nil {
		writeError(w, http.StatusInternalServerError, "sessions service not configured (Blocco #1.4 migration requires it)")
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Code == "" {
		writeError(w, http.StatusBadRequest, "missing code")
		return
	}
	payload, err := r.oneTimeCodes.Consume(body.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or expired code")
		return
	}
	activeWS, err := r.resolveActiveWorkspace(req.Context(), payload.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve active workspace: "+err.Error())
		return
	}
	result, err := r.sessionsSvc.Start(services.StartSessionRequest{
		UserID:      payload.UserID,
		WorkspaceID: activeWS,
		UserAgent:   req.UserAgent(),
		IP:          clientIP(req),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start session: "+err.Error())
		return
	}
	metrics.IncJWTIssued()
	r.setSessionCookie(w, req, result)
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the current user identity, including the active
// workspace_id stamped on the JWT. Used by the SPA on every page load
// to learn who's logged in (no JWT in localStorage anymore) and to
// align the dashboard's "current workspace" indicator with the server's
// view.
func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"user_id":      id.UserID(),
		"workspace_id": id.WorkspaceID(),
	})
}

// resolveActiveWorkspace returns the workspace_id which should be
// stamped on a freshly-issued JWT for the given user. Shared by
// /auth/exchange (OAuth callback) and the switch endpoint's re-bind
// after token rotation. Strategy (SPRINT 1.1):
//
//  1. Owned workspaces: pick most recent (ListByOwner desc).
//  2. Memberships: pick most recent (ListForUser desc).
//  3. None → auto-create a "Personal" workspace + admin membership.
//
// Step 3 is required so OAuth users who never went through the
// email/password onboarding still receive a JWT carrying a valid
// workspace claim (Manager.Issue refuses to sign without one).
func (r *Router) resolveActiveWorkspace(ctx context.Context, userID int64) (int64, error) {
	if r.userAndWorkspaceHelper == nil {
		return 0, fmt.Errorf("user workspace helper not configured")
	}
	if r.workspaceStore == nil || r.teamStore == nil {
		return 0, fmt.Errorf("workspace or team store not configured")
	}
	// owned
	if owned, err := r.userAndWorkspaceHelper.ListOwned(ctx, userID); err == nil && len(owned) > 0 {
		return owned[0], nil
	}
	// membership
	if memberships, err := r.userAndWorkspaceHelper.ListMemberships(ctx, userID); err == nil && len(memberships) > 0 {
		return memberships[0], nil
	}
	// Create personal workspace on the fly.
	ws := &models.Workspace{Name: "Personal", OwnerID: userID}
	if err := r.workspaceStore.Create(ws); err != nil {
		return 0, fmt.Errorf("create personal workspace on oauth exchange: %w", err)
	}
	if err := r.teamStore.AddMember(ws.ID, userID, repository.RoleAdmin); err != nil {
		return 0, fmt.Errorf("add oauth user as admin: %w", err)
	}
	return ws.ID, nil
}

// handleLogout is defined in pkg/api/sessions.go (SPRINT 2.1).
// It withdraws the session row matching the refresh-token cookie
// and clears all session cookies in one step. The route
// registration in Setup() resolves to that method directly.

// csrfConfig returns the CSRF config that matches the
// session_cookie defaults: Secure=r.cookieSecure, SameSite=None
// (required for cross-origin SPA + cross-site cookie; browsers
// require Secure when SameSite=None), Path=/, HttpOnly=false
// (SPA reads via document.cookie).
//
// Blocco #1.3 — the csrf_token cookie is set by every endpoint that
// mints a session (handleExchangeCode, handleRegister,
// handleLoginEmail, handleRefresh) so the SPA can immediately echo
// it on the next unsafe request. The token is regenerated on
// every successful login to ensure the post-login token cannot be
// guessed by a pre-login attacker (see internal/auth/csrf.go).
func (r *Router) csrfConfig() auth.CSRFConfig {
	return auth.CSRFConfig{
		Secure:       r.cookieSecure,
		Path:         "/",
		CookieDomain: r.cookieDomain,
		SameSite:     http.SameSiteNoneMode,
	}
}

// protected wraps an http.HandlerFunc with the CSRF double-submit
// check (outermost) and the JWT/cookie auth.Middleware (inner).
// Failure modes:
//   - safe methods (GET/HEAD/OPTIONS) skip CSRF and reach auth.Middleware
//     (which 401s on missing/invalid session).
//   - Authorization Bearer-prefixed requests skip CSRF (JWT or API-key
//     paths) and reach auth.Middleware.
//   - cookie-authenticated unsafe requests MUST carry a csrf_token
//     cookie equal to the X-CSRF-Token request header — otherwise 403.
//
// Other helpers in this file also use r.csrfConfig() to issue the
// csrf_token cookie on login / refresh / exchange / register so the
// SPA's first post-login POST can succeed.
func (r *Router) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		csrfHandler := auth.NewCSRF(r.csrfConfig(), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			r.auth.Middleware(next).ServeHTTP(w, req)
		}))
		csrfHandler.ServeHTTP(w, req)
	}
}

// oauthSessionRedirect validates the session (Bearer or HttpOnly
// cookie) BEFORE running the wrapped OAuth handler, but unlike
// `protected` it does not write a 401 on failure: it 302-redirects
// to ${frontendURL}/login?next=/connections/{provider} so the SPA
// can show the login UI and resume the OAuth connect after the user
// authenticates. SPRINT 7.1 (P0#14) — OAuth social is now a
// "connect an account to an existing product session" operation,
// not a registration pathway. The handleLogin and handleCallback
// routes both mount this middleware so the OAuth dialog is never
// reachable without an InstaEdit session.
//
// When frontendURL is empty (CLI / test mode) the helper falls
// back to writeError(401) so callers can still rely on a typed
// error response — the SPA path is irrelevant in CLI mode anyway.
func (r *Router) oauthSessionRedirect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		identity := r.extractSessionIdentity(req)
		if identity == nil {
			if r.frontendURL != "" {
				provider := req.PathValue("provider")
				nextURL := url.QueryEscape("/connections/" + provider)
				http.Redirect(w, req,
					strings.TrimRight(r.frontendURL, "/")+"/login?next="+nextURL,
					http.StatusFound)
				return
			}
			writeError(w, http.StatusUnauthorized, "missing user identity (OAuth social requires an InstaEdit session — post /api/v1/auth/register or /login first)")
			return
		}
		ctx := auth.WithIdentity(req.Context(), identity)
		next(w, req.WithContext(ctx))
	}
}

// extractSessionIdentity returns the UserIdentity from the request's
// Bearer token or `session` HttpOnly cookie, or nil when no valid
// identity is present. Mirrors auth.Manager.Middleware's verification
// logic but returns a typed result instead of writing a response,
// so the caller can decide between 401 (protected endpoints) and
// 302→/login (OAuth endpoints). API-key Bearer tokens are NOT
// considered valid for OAuth social — OAuth is a human flow that
// requires a JWT-path session (sessionID > 0).
func (r *Router) extractSessionIdentity(req *http.Request) auth.Identity {
	if r.auth == nil {
		return nil
	}
	// Bearer path.
	if header := req.Header.Get("Authorization"); header != "" {
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) {
			return nil
		}
		raw := strings.TrimSpace(header[len(prefix):])
		if auth.IsApiKeyBearer(raw) {
			return nil
		}
		uid, wsID, sid, err := r.auth.Verify(raw)
		if err != nil || uid <= 0 || wsID <= 0 || sid <= 0 {
			return nil
		}
		return auth.NewUserIdentity(uid, wsID, sid)
	}
	// Cookie path (`session` HttpOnly).
	if c, err := req.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		uid, wsID, sid, err := r.auth.Verify(c.Value)
		if err != nil || uid <= 0 || wsID <= 0 || sid <= 0 {
			return nil
		}
		return auth.NewUserIdentity(uid, wsID, sid)
	}
	return nil
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

// OAuthStartLimitIfConfigured is a no-op identity when the rate
// limiter is not wired; otherwise it wraps with OAuthStartLimit.
// Used by Setup() so the OAuth start route registration stays
// unconditional (no nil-guard branching in the route table).
func OAuthStartLimitIfConfigured(svc *services.RateLimitService) func(http.Handler) http.Handler {
	if svc == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return OAuthStartLimit(svc)
}

// accountListItem is the wire shape returned by handleListAccounts.
// We deliberately do NOT return the PlatformAccount struct directly:
// it leaks user_id, last_error_code/message, metadata blob, and
// every internal audit column the SPA does not need. The 6 fields
// below are the SPEC'd response contract: id, platform,
// platform_user_id, username, status, created_at.
type accountListItem struct {
	ID             int64     `json:"id"`
	Platform       string    `json:"platform"`
	PlatformUserID string    `json:"platform_user_id"`
	Username       string    `json:"username"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// handleListAccounts returns the authenticated user's connected
// social accounts. SPRINT 7.1 (P0#14) closure: identity comes ONLY
// from the JWT (deposited by r.protected → r.auth.Middleware); never
// from query params, body, or path. WorkspaceID from the identity
// is captured for tenant-scoping future work (Taglio 1.4 audit
// log) but is NOT used as a SQL filter — PlatformAccount is currently
// user-scoped in the schema (a single social identity serves every
// workspace the user is a member of; this matches the Taglio 2.4
// "OAuth is one identity per user, not per workspace" contract).
//
// Response always uses the {"accounts": [...]} wrapper so the SPA's
// JSON decoder can iterate unconditionally — never nil-vs-empty,
// always an array (possibly empty).
func (r *Router) handleListAccounts(w http.ResponseWriter, req *http.Request) {
	id := auth.IdentityFromContext(req.Context())
	if id == nil || id.UserID() <= 0 {
		// Defence-in-depth: r.protected() should have already
		// rejected this with 401. If a future refactor accidentally
		// wires this handler without the middleware, refuse the
		// request rather than silently returning any user's data.
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	_ = id.WorkspaceID() // tenancy captured for audit; not used as SQL filter (see godoc)

	accounts, err := r.userRepo.ListPlatformAccountsByUser(id.UserID(), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list accounts: "+err.Error())
		return
	}
	items := make([]accountListItem, 0, len(accounts))
	for _, a := range accounts {
		items = append(items, accountListItem{
			ID:             a.ID,
			Platform:       a.Platform,
			PlatformUserID: a.PlatformUserID,
			Username:       a.Username,
			Status:         a.Status,
			CreatedAt:      a.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"accounts": items})
}

// ----------------------------------------------------------------
// /accounts/{id} handlers (Taglio 1.4) — full implementations.
//
// Each handler enforces the same workspace-isolation contract: the
// account must be owned by the authenticated user (account.UserID ==
// identity.UserID()). Cross-tenant probes return 404 (not 403) so
// the existence of accounts in other user boundaries is never
// leakable. All four handlers share loadOwnAccountByID for the auth
// + load + ownership check; the handler-specific logic below
// handles the platform-side action.
// ----------------------------------------------------------------

// loadOwnAccountByID centralises the auth + load + ownership check
// shared by all four /accounts/{id} handlers. Returns the loaded
// account + identity on success; writes 401/404/500 directly to w
// and returns (nil, nil, false) on failure. The 404 (not 403) for
// cross-tenant probes is critical: a malicious probe MUST NOT be
// able to enumerate which account ids exist in other users by
// observing the 403 vs 404 response shape.
func (r *Router) loadOwnAccountByID(w http.ResponseWriter, req *http.Request, id int64) (*models.PlatformAccount, auth.Identity, bool) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return nil, nil, false
	}
	account, err := r.userRepo.FindPlatformAccountByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find account: "+err.Error())
		return nil, nil, false
	}
	if account == nil || account.UserID != identity.UserID() {
		// No existence leak: 404 covers both nil and cross-tenant.
		writeError(w, http.StatusNotFound, "account not found")
		return nil, nil, false
	}
	return account, identity, true
}

// isTokenExpired matches the canonical error string produced by
// vault.Get on a stored-but-expired token. The vault's internal
// isExpiryError helper (lowercase, package-private) is the source
// of truth; we probe with substring equality rather than introducing
// a typed sentinel to avoid an interface dependency in the HTTP
// layer.
func isTokenExpired(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "expired")
}

// auditAccountEvent fires a typed audit log entry, nil-safe (the
// auditLogStore is optional in tests / dev). Captures the
// WHO/WHAT/WHEN trio an operator needs to reconstruct the action.
// eventType is one of {account.reauth_required, account.disconnected}.
func (r *Router) auditAccountEvent(ctx context.Context, eventType string, identity auth.Identity, account *models.PlatformAccount) {
	if r.auditLogStore == nil {
		return
	}
	actor := strconv.FormatInt(identity.UserID(), 10)
	resource := strconv.FormatInt(account.ID, 10)
	_ = r.auditLogStore.Log(ctx, eventType, actor, "platform_account", resource, map[string]interface{}{
		"platform":         account.Platform,
		"platform_user_id": account.PlatformUserID,
	})
}

// handleGetAccount returns a single platform account owned by the
// authenticated user. When the provider implements AccountDetailsProvider
// and a cached snapshot exists, the response includes a "resource" field
// with rich details (metrics, branding, stats). The base 6-field shape
// is always present for backward compatibility.
func (r *Router) handleGetAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	type accountMetric struct {
		Key          string `json:"key"`
		Label        string `json:"label"`
		Value        int64  `json:"value"`
		DisplayValue string `json:"display_value"`
	}
	type accountResource struct {
		ResourceType string          `json:"resource_type"`
		ExternalID   string          `json:"external_id"`
		DisplayName  string          `json:"display_name"`
		Handle       string          `json:"handle,omitempty"`
		Description  string          `json:"description,omitempty"`
		AvatarURL    string          `json:"avatar_url,omitempty"`
		BannerURL    string          `json:"banner_url,omitempty"`
		PublicURL    string          `json:"public_url,omitempty"`
		Metrics      []accountMetric `json:"metrics"`
		Properties   map[string]any  `json:"properties,omitempty"`
		FetchedAt    time.Time       `json:"fetched_at"`
	}
	type accountDetailResponse struct {
		accountListItem
		Resource *accountResource `json:"resource,omitempty"`
	}

	resp := accountDetailResponse{
		accountListItem: accountListItem{
			ID:             account.ID,
			Platform:       account.Platform,
			PlatformUserID: account.PlatformUserID,
			Username:       account.Username,
			Status:         account.Status,
			CreatedAt:      account.CreatedAt,
		},
	}

	const snapshotMaxAge = 10 * time.Minute

	// Shortcut: no snapshot store wired → return base account without resource.
	if r.snapshotStore == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Try to enrich with cached snapshot data. When the snapshot is fresh
	// (< 10 min) we serve it directly; when it's stale or missing, we
	// reach out to the provider, persist a fresh snapshot, and serve that.
	stale, err := r.snapshotStore.IsSnapshotStale(account.ID, snapshotMaxAge)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "snapshot freshness check failed: "+err.Error())
		return
	}

	if stale {
		// Cache miss or stale — fetch fresh details from the provider.
		if detailsProvider, ok := r.capabilities.AccountDetails(account.Platform); ok {
			token, tokenErr := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
			if tokenErr != nil {
				token, tokenErr = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
				if tokenErr != nil {
					token, tokenErr = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
				}
			}
			if tokenErr == nil {
				details, detailsErr := detailsProvider.GetAccountDetails(req.Context(), token.AccessToken, account.PlatformUserID)
				if detailsErr == nil {
					// Build and persist the snapshot.
					snap := &repository.AccountResourceSnapshot{
						PlatformAccountID: account.ID,
						ResourceType:      details.ResourceType,
						Profile: map[string]any{
							"display_name": details.DisplayName,
							"handle":       details.Handle,
							"description":  details.Description,
							"avatar_url":   details.AvatarURL,
							"banner_url":   details.BannerURL,
							"public_url":   details.PublicURL,
							"external_id":  details.ExternalID,
						},
						FetchedAt: details.FetchedAt,
					}
					stats := make(map[string]any)
					for _, m := range details.Metrics {
						stats[m.Key] = map[string]any{
							"label":         m.Label,
							"value":         m.Value,
							"display_value": m.DisplayValue,
						}
					}
					snap.Statistics = stats
					if details.Properties != nil {
						snap.Content = details.Properties
					}
					// Best-effort save — if it fails we're already holding the
					// fresh data in memory and can serve it.
					_ = r.snapshotStore.UpsertSnapshot(snap)

					// Build resource from the fresh details.
					res := &accountResource{
						ResourceType: details.ResourceType,
						ExternalID:   details.ExternalID,
						DisplayName:  details.DisplayName,
						Handle:       details.Handle,
						Description:  details.Description,
						AvatarURL:    details.AvatarURL,
						BannerURL:    details.BannerURL,
						PublicURL:    details.PublicURL,
						FetchedAt:    details.FetchedAt,
					}
					for _, m := range details.Metrics {
						res.Metrics = append(res.Metrics, accountMetric{
							Key:          m.Key,
							Label:        m.Label,
							Value:        m.Value,
							DisplayValue: m.DisplayValue,
						})
					}
					if details.Properties != nil {
						res.Properties = details.Properties
					}
					resp.Resource = res
					writeJSON(w, http.StatusOK, resp)
					return
				}
			}
		}
		// Fall through: provider call failed or platform doesn't support
		// details — serve whatever stale snapshot (if any) is still in the DB.
	}

	// Serve from cache (fresh snapshot, or stale snapshot as fallback).
	snap, snapErr := r.snapshotStore.GetSnapshot(account.ID)
	if snapErr == nil && snap != nil {
		res := &accountResource{
			ResourceType: snap.ResourceType,
			FetchedAt:    snap.FetchedAt,
		}
		if v, ok := snap.Profile["external_id"].(string); ok {
			res.ExternalID = v
		}
		if v, ok := snap.Profile["display_name"].(string); ok {
			res.DisplayName = v
		}
		if v, ok := snap.Profile["handle"].(string); ok {
			res.Handle = v
		}
		if v, ok := snap.Profile["description"].(string); ok {
			res.Description = v
		}
		if v, ok := snap.Profile["avatar_url"].(string); ok {
			res.AvatarURL = v
		}
		if v, ok := snap.Profile["banner_url"].(string); ok {
			res.BannerURL = v
		}
		if v, ok := snap.Profile["public_url"].(string); ok {
			res.PublicURL = v
		}

		for key, val := range snap.Statistics {
			if m, ok := val.(map[string]any); ok {
				am := accountMetric{Key: key}
				if v, ok := m["label"].(string); ok {
					am.Label = v
				}
				if v, ok := m["value"].(float64); ok {
					am.Value = int64(v)
				}
				if v, ok := m["display_value"].(string); ok {
					am.DisplayValue = v
				}
				res.Metrics = append(res.Metrics, am)
			}
		}

		if snap.Content != nil {
			res.Properties = snap.Content
		}

		resp.Resource = res
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleValidateAccount probes token freshness via vault.Get. The
// handler stamps last_validated_at + flips the account status to
// reflect reality (active | expired | reauth_required). It does
// NOT rotate or revoke tokens (the reconnect flow handles that)
// and does NOT call the provider (no remote API call; the endpoint
// is cheap and rate-limit-safe for dashboards that auto-poll).
//
// Returns 200 either way — the validation IS the answer; the caller
// reads status to decide what to do. The HTTP layer doesn't surface
// the token error to the client (operators see the canonical
// latency/error dashboards; the API only reports status changes).
// validateAccountRequest is the JSON body handler handleValidateAccount
// decodes. The only field today is Canary (bool, body key "canary");
// when false the 4-step pipeline defaults to the cheap path (steps 1-3
// only). Tests that don't supply a body pass the empty / unknown-path
// branch harmlessly (json.Decode error is silently ignored).
type validateAccountRequest struct {
	Canary bool `json:"canary,omitempty"`
}

// validateAccountResponse is the 200 OK body handler handleValidateAccount
// writes on the 4-step pipeline's success path. The embedded
// accountListItem shape mirrors every other /accounts/{id} response
// surface so the SPA can render the same shape on every code path.
// CanaryVideoID + CanaryUploadedChannelID are populated only when the
// caller set body.canary=true AND step 4 succeeded end-to-end (i.e.
// the canary was uploaded AND snippet.channelId matched the platform
// account row's expected channel).
type validateAccountResponse struct {
	accountListItem
	CanaryVideoID           string `json:"canary_video_id,omitempty"`
	CanaryUploadedChannelID string `json:"canary_uploaded_channel_id,omitempty"`
}

// handleValidateAccount runs the 4-step /accounts/{id}/validate pipeline
// (the operator's "is this YouTube OAuth grant REALLY ready to upload?"
// check) on YouTube platforms, falling back to the pre-C2 token-
// freshness probe for any non-YouTube platform OR for any test /
// deployment that hasn't yet wired WithYouTubeService.
//
// The 4 steps, in order, are:
//
//  1. refresh-grant  — vault.Renew exchanges the stored refresh token
//     for a fresh access token. invalid_grant → 422 +
//     status='reauth_required' + MarkReauthRequired on platform_account.
//     Transient (network, 5xx) → 500, leave status unchanged.
//
//  2. tokeninfo      — GetTokenInfo on the fresh access token (Google's
//     oauth2/v3/tokeninfo public introspection endpoint). Three hard
//     reauth signals: Google's 400 invalid_token, info.Aud ≠
//     cfg.YouTubeClientID (Production-vs-Testing drift), info
//     missing youtube.upload OR youtube.readonly. Transient (network,
//     decode) → 500.
//
//  3. channel-binding — ValidateChannelBinding paginated
//     channels.list(mine=true) comparison against
//     platform_account.platform_user_id. ErrYouTubeChannelMismatch →
//     422 + reauth; transient → 500.
//
//  4. canary (opt-in via body.canary=true) — uploads a private
//     INSTAEDIT-OAUTH-CANARY-{channel}-{ts} probe video via the
//     resumable upload protocol, then verifies snippet.channelId
//     equals the platform_account's expected channel. Bind-mismatch
//     OR ErrYouTubeCanaryRejected → 422 + reauth; transient → 500.
//
// On any 422, MarkReauthRequired stamps the platform_account row with
// the failing step's code + wrapped message, auditAccountEvent tags
// the request, and the response carries the structured error in
// writeError.
//
// On success, status flips back to 'active', reauth_required_at is
// cleared (caller could be re-flipped on next failure), and the
// canary fields (when applicable) surface to the SPA so the operator
// can audit the YouTube-Studio video id.
func (r *Router) handleValidateAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	var body validateAccountRequest
	if req.ContentLength > 0 {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}

	// 4-step pipeline today is YouTube-only. Non-YouTube platforms +
	// test setups that haven't wired WithYouTubeService fall back to
	// the legacy token-freshness probe (preserves the pre-C2 contract).
	if r.youTubeSvc == nil || account.Platform != models.PlatformYouTube {
		r.handleValidateAccountLegacy(w, req, account)
		return
	}

	ctx := req.Context()

	// === STEP 1: refresh-grant ===
	refreshed, err := r.vault.Renew(ctx, account.ID, models.TokenTypeBearer,
		r.youTubeSvc.RefreshOAuthToken) // method value = credentials.TokenRefresher
	if err != nil {
		if isInvalidGrantError(err) {
			r.flagReauthAndRespond(w, ctx, account, identity, "refresh_grant_invalid", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "vault renew failed: "+err.Error())
		return
	}
	accessToken := refreshed.AccessToken

	// === STEP 2: tokeninfo scope + aud check ===
	info, tiErr := r.youTubeSvc.GetTokenInfo(ctx, accessToken)
	if tiErr != nil {
		if isGoogleTokenInfoRejection(tiErr) {
			r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_rejected", tiErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "youtube tokeninfo failed: "+tiErr.Error())
		return
	}
	if info.Aud != r.youTubeSvc.ClientID() {
		r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_aud_mismatch",
			fmt.Sprintf("tokeninfo.aud=%q cfg.YouTubeClientID=%q", info.Aud, r.youTubeSvc.ClientID()))
		return
	}
	if !info.HasUpload || !info.HasReadonly {
		r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_scope_missing",
			fmt.Sprintf("HasUpload=%v HasReadonly=%v scope=%q", info.HasUpload, info.HasReadonly, info.Scope))
		return
	}

	// === STEP 3: paginated channel binding ===
	if cbErr := r.youTubeSvc.ValidateChannelBinding(ctx, accessToken, account.PlatformUserID); cbErr != nil {
		if errors.Is(cbErr, services.ErrYouTubeChannelMismatch) {
			r.flagReauthAndRespond(w, ctx, account, identity, "channel_binding_mismatch", cbErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "youtube channel binding failed: "+cbErr.Error())
		return
	}

	// === STEP 4: optional canary upload ===
	var canary *services.CanaryUploadResult
	if body.Canary {
		canary, err = r.youTubeSvc.CanaryUpload(ctx, accessToken, account.PlatformUserID)
		if err != nil {
			if errors.Is(err, services.ErrYouTubeChannelMismatch) ||
				errors.Is(err, services.ErrYouTubeCanaryRejected) {
				r.flagReauthAndRespond(w, ctx, account, identity, "canary_rejected", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "youtube canary upload failed: "+err.Error())
			return
		}
	}

	// ALL STEPS PASS — flip last_validated_at + status='active' + clear reauth flags.
	now := time.Now()
	account.LastValidatedAt = &now
	account.Status = models.AccountStatusActive
	account.ReauthRequiredAt = nil
	account.LastErrorCode = ""
	account.LastErrorMessage = ""
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}

	resp := validateAccountResponse{
		accountListItem: accountListItem{
			ID:             account.ID,
			Platform:       account.Platform,
			PlatformUserID: account.PlatformUserID,
			Username:       account.Username,
			Status:         account.Status,
			CreatedAt:      account.CreatedAt,
		},
	}
	if canary != nil {
		resp.CanaryVideoID = canary.VideoID
		resp.CanaryUploadedChannelID = canary.UploadedChannelID
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleValidateAccountLegacy preserves the pre-C2 token-freshness
// probe. Called when r.youTubeSvc is nil (test setup) OR
// account.Platform is not YouTube. Behaviour — including the
// active/expired/reauth_required status mapping, the per-provider
// TokenPolicy lookup, and the audit / persist pairing — is
// byte-identical to the pre-C2 handler so every pre-existing
// TestHandleValidateAccount_* test passes unchanged.
func (r *Router) handleValidateAccountLegacy(w http.ResponseWriter, req *http.Request, account *models.PlatformAccount) {
	now := time.Now()
	account.LastValidatedAt = &now

	var tokenTypes []string
	if tp, ok := r.capabilities.TokenPolicy(account.Platform); ok {
		tokenTypes = tp.PreferredTokenTypes()
	} else {
		tokenTypes = services.DefaultTokenTypes()
	}
	active := false
	expired := false
	for _, tt := range tokenTypes {
		_, err := r.vault.Get(req.Context(), account.ID, tt)
		switch {
		case err == nil:
			active = true
		case isTokenExpired(err):
			expired = true
		}
	}
	switch {
	case active:
		account.Status = models.AccountStatusActive
	case expired:
		account.Status = models.AccountStatusExpired
	default:
		account.Status = models.AccountStatusReauthRequired
	}
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accountListItem{
		ID:             account.ID,
		Platform:       account.Platform,
		PlatformUserID: account.PlatformUserID,
		Username:       account.Username,
		Status:         account.Status,
		CreatedAt:      account.CreatedAt,
	})
}

// flagReauthAndRespond is the 422-mapping helper for every 4-step failure.
// Stamps the platform_account row with status='reauth_required' +
// reauth_required_at = NOW (via MarkReauthRequired on UserStore) +
// last_error_code/message (structured) for the operator dashboard; emits
// the canonical "account.reauth_required" audit event (idempotent); and
// writes the structured error body. Best-effort: a MarkReauthRequired
// failure is logged at WARN but does not block the 422 response. Mirrors
// the existing pre-C2 attachDiscoveredAccounts → MarkReauthRequired
// pattern at line ~1377 so the SPA-side rendering stays consistent.
func (r *Router) flagReauthAndRespond(w http.ResponseWriter, ctx context.Context,
	account *models.PlatformAccount, identity auth.Identity,
	code string, message string) {
	if err := r.userRepo.MarkReauthRequired(ctx, account.ID, code, message); err != nil {
		slog.WarnContext(ctx, "handleValidateAccount: MarkReauthRequired failed (best-effort)",
			"account_id", account.ID, "code", code, "error", err)
	}
	r.auditAccountEvent(ctx, "account.reauth_required", identity, account)

	now := time.Now()
	account.LastValidatedAt = &now
	account.Status = models.AccountStatusReauthRequired
	account.ReauthRequiredAt = &now
	account.LastErrorCode = code
	account.LastErrorMessage = message
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		slog.WarnContext(ctx, "handleValidateAccount: UpdatePlatformAccount failed after reauth flag",
			"account_id", account.ID, "error", err)
	}

	writeError(w, http.StatusUnprocessableEntity,
		fmt.Sprintf("account validation failed (%s): %s", code, message))
}

// isInvalidGrantError classifies a vault.Renew / refresh failure as
// "the operator must re-consent". Substring match on Google's
// canonical "invalid_grant" error code (RFC 6749 §5.2). Same
// fragility pattern as isHardRejection4xxStatus in the services
// package: prefers stable error-shape strings to typed sentinels
// because the upstream credential vault emits wrapped errors from
// many sub-layers. Long-term fix: have vault.Renew return a
// typed sentinel ErrInvalidGrant so callers can switch on errors.Is.
// Tracked as follow-up; the string match is correct enough for
// the 4-step pipeline's correctness today.
func isInvalidGrantError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid_grant")
}

// isGoogleTokenInfoRejection classifies a GetTokenInfo failure as
// "Google said the token is bad" (HTTP 400 invalid_token) versus
// "the request never reached Google" (network / decode). The
// substring "400" matches the upstream's `fmt.Errorf("youtube
// tokeninfo returned %d: %s", resp.StatusCode, string(body))`
// shape. Same fragility pattern as isInvalidGrantError; same
// long-term fix (typed sentinel `ErrGoogleTokenInfoInvalid`).
func isGoogleTokenInfoRejection(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "400")
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

// handleReconnectAccount flags the account as needing reauth. The
// SPA reads status='reauth_required' on /connections and surfaces
// a "Reconnect to <Platform>" CTA. The actual OAuth round-trip
// happens via /api/v1/auth/{provider}/login → callback, which
// (because of SPRINT 7.1 idempotency in AttachPlatformAccount)
// re-binds the existing platform_accounts row in place — no
// duplicate row, no POST /accounts leak.
func (r *Router) handleReconnectAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	now := time.Now()
	account.Status = models.AccountStatusReauthRequired
	account.ReauthRequiredAt = &now
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	r.auditAccountEvent(req.Context(), "account.reauth_required", identity, account)
	writeJSON(w, http.StatusOK, accountListItem{
		ID:             account.ID,
		Platform:       account.Platform,
		PlatformUserID: account.PlatformUserID,
		Username:       account.Username,
		Status:         account.Status,
		CreatedAt:      account.CreatedAt,
	})
}

// handleDeleteAccount soft-disconnects a platform account. Steps:
//
//  1. loadOwnAccountByID (auth + ownership + 404 on cross-tenant).
//  2. vault.Revoke → deletes every encrypted token row for the
//     account. Idempotent: the vault swallows ErrTokenNotFound.
//  3. Soft-disconnect: status='disconnected' on the account row +
//     last_error_code='DISCONNECTED' for operator dashboards. The
//     row stays so the audit trail (user_id, platform, platform_user_id,
//     connected_at) is preserved for compliance — a future Taglio adds
//     the workspace-level "data deletion" endpoint that hard-deletes
//     the row + scrubs the encrypted tokens.
//  4. Audit log (account.disconnected), nil-safe.
//
// post_targets that referenced this account remain unchanged in the
// schema: the publish driver will surface a "token revoked" failure
// on the next tick and stamp post_targets.status='failed' through
// the existing error-classification path. No handler-side bulk
// transition is needed (Taglio 1.4 contract is implicit failure via
// worker, not synchronous transition via handler).
//
// Best-effort remote revoke at the provider is NOT attempted here:
// no Revoker capability interface exists today. A future Taglio 1.4
// follow-up adds internal/services/provider.go's Revoker interface
// plus a concrete implementation per provider that supports it
// (Meta has /me/permissions; Twitter has POST oauth2/invalidate_token;
// Google has https://oauth2.googleapis.com/revoke).
func (r *Router) handleDeleteAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	if err := r.vault.Revoke(req.Context(), account.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "vault revoke failed: "+err.Error())
		return
	}
	account.Status = models.AccountStatusDisconnected
	account.ConnectedAt = nil
	account.LastErrorCode = "DISCONNECTED"
	account.LastErrorMessage = "account disconnected by user"
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	r.auditAccountEvent(req.Context(), "account.disconnected", identity, account)
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncAccount forces a refresh of the remote resource snapshot
// for the given account. The snapshot caches channel stats, profile,
// and branding so the frontend doesn't trigger a provider API call on
// every render. POST /accounts/{id}/sync bypasses the 10-minute cache.
//
// When snapshotStore is nil, returns 501. When the provider does not
// implement AccountDetailsProvider, returns 400.
func (r *Router) handleSyncAccount(w http.ResponseWriter, req *http.Request) {
	if r.snapshotStore == nil {
		writeError(w, http.StatusNotImplemented, "snapshot store not configured")
		return
	}

	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	detailsProvider, ok := r.capabilities.AccountDetails(account.Platform)
	if !ok {
		writeError(w, http.StatusBadRequest, "platform "+account.Platform+" does not support account details")
		return
	}

	// Retrieve the access token from the vault.
	token, err := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
	if err != nil {
		// Fall back to other token types.
		token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	details, err := detailsProvider.GetAccountDetails(req.Context(), token.AccessToken, account.PlatformUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch account details: "+err.Error())
		return
	}

	// Build the snapshot from the details response.
	snap := &repository.AccountResourceSnapshot{
		PlatformAccountID: account.ID,
		ResourceType:      details.ResourceType,
		Profile: map[string]any{
			"display_name": details.DisplayName,
			"handle":       details.Handle,
			"description":  details.Description,
			"avatar_url":   details.AvatarURL,
			"banner_url":   details.BannerURL,
			"public_url":   details.PublicURL,
			"external_id":  details.ExternalID,
		},
		FetchedAt: details.FetchedAt,
	}

	// Metrics → statistics JSONB.
	stats := make(map[string]any)
	for _, m := range details.Metrics {
		stats[m.Key] = map[string]any{
			"label":         m.Label,
			"value":         m.Value,
			"display_value": m.DisplayValue,
		}
	}
	snap.Statistics = stats

	// Platform-specific properties → content JSONB.
	if details.Properties != nil {
		snap.Content = details.Properties
	}

	if err := r.snapshotStore.UpsertSnapshot(snap); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save snapshot: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, details)
}

// handleAccountContent returns a paginated list of content items
// (videos, posts) for a connected account. The provider must implement
// AccountContentProvider. Supports ?cursor and ?query.limit parameters.
func (r *Router) handleAccountContent(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	contentProvider, ok := r.capabilities.AccountContent(account.Platform)
	if !ok {
		writeError(w, http.StatusBadRequest, "platform "+account.Platform+" does not support account content")
		return
	}

	// Retrieve the access token from the vault.
	token, err := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
	if err != nil {
		token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	cursor := req.URL.Query().Get("cursor")
	limit := 20
	if l := req.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	page, err := contentProvider.ListAccountContent(req.Context(), token.AccessToken, account.PlatformUserID, cursor, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list account content: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, page)
}

// ----------------------------------------------------------------------- Middleware

func (r *Router) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		slog.Info("HTTP request", "method", req.Method, "path", req.URL.Path, "remote_addr", req.RemoteAddr)
		next.ServeHTTP(w, req)
	})
}

func (r *Router) handleMetrics(w http.ResponseWriter, req *http.Request) {
	user := os.Getenv("METRICS_BASIC_AUTH_USER")
	pass := os.Getenv("METRICS_BASIC_AUTH_PASS")
	if user != "" && pass != "" {
		u, p, ok := req.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="metrics", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	metrics.Handler().ServeHTTP(w, req)
}

func (r *Router) corsMiddleware(next http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(r.allowedOrigin))
	for _, o := range r.allowedOrigin {
		allowed[strings.TrimSpace(o)] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if origin := req.Header.Get("Origin"); origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				// Taglio 1.2: include Cookie so the browser is allowed to
				// send the HttpOnly session cookie. Access-Control-Allow-Credentials
				// is required when the browser uses credentials:'include'.
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Cookie")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if req.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// securityHeadersMiddleware applies the standard hardened HTTP response
// headers to every response (defence-in-depth on top of whatever the
// upstream proxy/CDN also sets). The choices:
//
//   - default-src 'none' on Content-Security-Policy is the strict
//     default for an API-only JSON server: it blocks scripts,
//     styles, images, fonts, media, frames from any source unless
//     explicitly allowed. It also forbids <form> submissions to
//     third parties (form-action 'none') and embeds (frame-ancestors).
//     The SPA's index.html is served from the static host (Vite dev
//     / Vercel in prod), NOT from this server, so the SPA's CSP is
//     NOT here — its index.html / vercel.json / Nginx header config is
//     what carries the SPA-relevant CSP. This server only needs CSP
//     because some endpoints return redirect responses (OAuth
//     callback → /auth/callback redirect) and a redirect from a
//     strict-CSP origin shouldn't become a script-execution vector.
//   - X-Content-Type-Options: nosniff blocks MIME-sniffing (mostly
//     cosmetic for a JSON server but it's a single header so apply).
//   - X-Frame-Options: DENY blocks iframe embedding of API routes
//     (defence vs clickjacking if a malicious 3p page tries to load
//     our JSON responses in an iframe to read cross-origin responses
//     via same-origin network errors).
//   - Referrer-Policy: strict-origin-when-cross-origin keeps the
//     Referer header trustworthy but doesn't leak full paths.
//   - Strict-Transport-Security is ONLY emitted when the request
//     arrived over HTTPS (TLS or via a known TLS-terminating proxy:
//     Fly / Render / Cloudflare all set the X-Forwarded-Proto=https
//     header). HSTS over plain HTTP would break the connection.
//
// Placed OUTSIDE CORS / rate-limit so the headers apply to every
// response regardless of those middleware short-circuits. Placed
// INSIDE recover so a panic during header-writing is still caught
// (the headers will be reset by writeJSON 500 below).
func (r *Router) securityHeadersMiddleware(next http.Handler) http.Handler {
	apiCSP := strings.Join([]string{
		"default-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'none'",
		"base-uri 'none'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", apiCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if isTLSRequest(req) {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, req)
	})
}

// isTLSRequest reports whether the request reached the server over an
// encrypted transport. Falls back to X-Forwarded-Proto when TLS is
// terminated upstream (every managed deploy we ship uses one). This
// is the gate for the HSTS header so a plain-HTTP sandbox doesn't
// advertise a permanent HTTPS-only contract to browsers.
func isTLSRequest(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		pp := strings.ToLower(strings.TrimSpace(p))
		if i := strings.Index(pp, ","); i > 0 {
			pp = strings.TrimSpace(pp[:i])
		}
		return pp == "https"
	}
	if strings.EqualFold(req.Header.Get("X-Forwarded-Ssl"), "on") {
		return true
	}
	return false
}

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

const (
	oauthStateCookiePrefix = "oauth_state_"
	oauthStateMaxAge       = 10 * time.Minute
)

// oauthStateExpectedChannelSuffix is appended to oauth_state_{provider}
// to form the sibling cookie that round-trips an optional
// expected_channel_id across the OAuth callback. Kept distinct from the
// state cookie (which holds the pure-CSRF nonce) so the URL state param
// remains a 32-byte base64url random — verified by
// TestHandleLogin_RedirectsToProviderURL (length 43 invariant).
const oauthStateExpectedChannelSuffix = "_expected_channel"

func OAuthStateCookieName(provider string) string { return oauthStateCookiePrefix + provider }

// OAuthStateExpectedChannelCookieName returns the sibling cookie name used
// when /api/v1/auth/{provider}/login is invoked with
// ?expected_channel_id=. The cookie is HttpOnly Secure SameSite=Lax with
// MaxAge matching the state cookie; it's deleted together with the state
// cookie on successful verifyOAuthState. Kept outside the URL state
// parameter (which Google echoes back verbatim, so we keep it a pure
// CSRF nonce).
func OAuthStateExpectedChannelCookieName(provider string) string {
	return oauthStateCookiePrefix + provider + oauthStateExpectedChannelSuffix
}

// isValidYouTubeChannelID returns true for strings that look like a
// YouTube channel ID (e.g. UC_x5XG1OV2P6uZZ5FSM9Ttw): "UC" + 22 chars,
// drawn from the URL-safe alphabet [A-Za-z0-9_-]. Used server-side to
// reject malformed expected_channel_id query params before storing them
// in the round-trip cookie. Failure mode: silently drop the hint — the
// OAuth flow still proceeds without the binding assertion; the actual
// binding check happens inside attachDiscoveredAccounts.
func isValidYouTubeChannelID(s string) bool {
	if len(s) != 24 || !strings.HasPrefix(s, "UC") {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func generateOAuthState(w http.ResponseWriter, provider, expectedChannelID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth state rand failed: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: state, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(oauthStateMaxAge.Seconds()),
	})
	if expectedChannelID != "" {
		// Sibling cookie carries the operator-supplied binding hint.
		// The URL state param stays a pure CSRF nonce (Google echoes
		// it back verbatim) and this HttpOnly cookie is the only path
		// for the hint to round-trip. Issued only when handleLogin
		// saw a validated ?expected_channel_id=; deleted on
		// verifyOAuthState.
		//
		// Value format: "<state_nonce>:<channelID>". The state prefix
		// binds the channel hint to the SAME flow — a stale sibling
		// cookie from a previous OAuth round-trip cannot silently
		// leak into a new one (e.g., operator clicked Connect without
		// ?expected_channel_id= after a previous abandoned flow).
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateExpectedChannelCookieName(provider), Value: state + ":" + expectedChannelID, Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			MaxAge: int(oauthStateMaxAge.Seconds()),
		})
	}
	return state, nil
}

// verifyOAuthState checks the CSRF nonce against the
// oauth_state_{provider} cookie and (if present) reads + deletes the
// sibling oauth_state_{provider}_expected_channel cookie. The returned
// expectedChannelID is "" when no hint was set; a non-empty value means
// the operator told us which channel/resource the OAuth grant must
// bind to.
func verifyOAuthState(w http.ResponseWriter, req *http.Request, provider, stateParam string) (string, error) {
	c, err := req.Cookie(OAuthStateCookieName(provider))
	if err != nil {
		return "", fmt.Errorf("oauth state cookie missing for provider %q", provider)
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(stateParam)) != 1 {
		return "", fmt.Errorf("oauth state mismatch for provider %q (CSRF protection)", provider)
	}
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
	expectedChannelID := ""
	if ec, ecErr := req.Cookie(OAuthStateExpectedChannelCookieName(provider)); ecErr == nil && ec.Value != "" {
		// Strip the "<state_nonce>:" prefix; only return the channel ID
		// when it matches the current flow's just-verified state
		// nonce. A stale sibling cookie from a previous OAuth
		// round-trip (different state) is silently ignored — the
		// operator must re-issue ?expected_channel_id= to bind it
		// explicitly. Defence-in-depth on top of the bearer-validated
		// channels.list(mine=true) check inside attachDiscoveredAccounts.
		// Also run the extracted channel ID through the same
		// isValidYouTubeChannelID gate handleLogin uses, so a malformed
		// value (e.g. someone forged "<state>:<bogus>:<extra>") cannot
		// pass through here — it would always 409 via the channels.list
		// mismatch anyway, but the gate keeps the error surface clean.
		if id, ok := strings.CutPrefix(ec.Value, stateParam+":"); ok && isValidYouTubeChannelID(id) {
			expectedChannelID = id
		}
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateExpectedChannelCookieName(provider), Value: "", Path: "/",
			HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
			MaxAge: -1, Expires: time.Unix(1, 0),
		})
	}
	return expectedChannelID, nil
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
