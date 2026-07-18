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
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
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
	oneTimeCodes     *OneTimeCodeStore
	frontendURL      string
	allowedOrigin    []string
	maxUploadBytes   int64
	rateLimiter      *rateLimiter // FASE 1.2: per-IP token bucket
	authEmailSvc     AuthEmailStore
	teamStore        TeamStore
	billingSvc       BillingServiceAPI
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
}

type WorkspaceStore interface {
	Create(w *models.Workspace) error
	FindByID(id int64) (*models.Workspace, error)
	ListByOwner(ownerID int64) ([]models.Workspace, error)
	Delete(id int64) error
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

	// Batch drive status: dashboard polls this for per-folder counts
	// (pending/processing/completed/failed) and min/max scheduled_at.
	// Mirrors the upload_jobs partial index on folder_id so polling
	// is one index range scan + a per-status COUNT FILTER.
	r.mux.Method(http.MethodGet, "/api/v1/media/import/drive/batch/status", r.protected(r.handleDriveBatchStatus))	// Dashboard "Programmati" surface: per-account scheduled uploads
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
	state, err := generateOAuthState(w, provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start oauth flow")
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
	if err := verifyOAuthState(w, req, provider, state); err != nil {
		writeError(w, http.StatusBadRequest, "invalid state: "+err.Error())
		return
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
		account, err = r.attachDiscoveredAccounts(req.Context(), userID, provider, discoverer, tokenData)
		if err != nil {
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

		// Taglio 2.2: token persistence goes through CredentialVault.Save.
		if err := r.vault.Save(req.Context(), account.ID, tokenData); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save token: "+err.Error())
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
func (r *Router) attachDiscoveredAccounts(ctx context.Context, userID int64, provider string, discoverer services.AccountDiscoverer, tokenData *models.TokenData) (*models.PlatformAccount, error) {
	accounts, err := discoverer.DiscoverAccounts(ctx, tokenData.AccessToken, "")
	if err != nil {
		return nil, fmt.Errorf("discover accounts: %w", err)
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("no accounts discovered for provider %s", provider)
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

		// Save the root OAuth token for this account. For YouTube this
		// is the bearer token; for Facebook this is the long-lived user
		// token. The vault prunes older rows per (account_id, token_type).
		if err := r.vault.Save(ctx, created.ID, tokenData); err != nil {
			return nil, fmt.Errorf("save root token for account %d: %w", created.ID, err)
		}

		// Save every supplemental token the provider declared.
		// Facebook Pages carry a Page Access Token here; YouTube
		// channels carry none (the root bearer token is shared).
		for _, supplemental := range acc.SupplementalTokens {
			if err := r.vault.Save(ctx, created.ID, supplemental); err != nil {
				return nil, fmt.Errorf("save supplemental token (%s) for account %d: %w", supplemental.TokenType, created.ID, err)
			}
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

	// Try to enrich with cached snapshot data.
	if r.snapshotStore != nil {
		snap, err := r.snapshotStore.GetSnapshot(account.ID)
		if err == nil && snap != nil {
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

			// Convert statistics JSONB to metrics slice.
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
func (r *Router) handleValidateAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	now := time.Now()
	account.LastValidatedAt = &now

	// Every token type the platform may store is checked. A platform
	// having any non-expired stored token is "active"; all-found-
	// tokens-expired is "expired"; neither found nor "expired" (i.e.
	// decrypt error or DB unreachable) is "reauth_required".
	//
	// Token types:
	//   - short_lived  — YouTube / Twitter / TikTok (legacy)
	//   - long_lived   — Meta
	//   - bearer       — YouTube (canonical)
	//   - page_access  — Facebook Page Access Tokens
	tokenTypes := []string{
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
		models.TokenTypeBearer,
		models.TokenTypePageAccess,
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

func OAuthStateCookieName(provider string) string { return oauthStateCookiePrefix + provider }

func generateOAuthState(w http.ResponseWriter, provider string) (string, error) {
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
	return state, nil
}

func verifyOAuthState(w http.ResponseWriter, req *http.Request, provider, stateParam string) error {
	c, err := req.Cookie(OAuthStateCookieName(provider))
	if err != nil {
		return fmt.Errorf("oauth state cookie missing for provider %q", provider)
	}
	if subtle.ConstantTimeCompare([]byte(c.Value), []byte(stateParam)) != 1 {
		return fmt.Errorf("oauth state mismatch for provider %q (CSRF protection)", provider)
	}
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
	return nil
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
