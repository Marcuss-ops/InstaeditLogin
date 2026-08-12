package api

import (
	"context"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
// session lifecycle. Production wiring in internal/bootstrap/app.go injects the
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
	// ListPlatformAccountsWithSnapshotsByUser returns a user's platform
	// accounts joined with their cached resource snapshots in ONE query
	// (repository.AccountWithSnapshot; Snapshot is nil when no row
	// exists). Backs the aggregated GET /api/v1/accounts list so a page
	// load needs exactly one SQL round-trip — no per-account snapshot
	// reads, Vault access, or provider (YouTube) calls.
	ListPlatformAccountsWithSnapshotsByUser(userID int64, platform string) ([]*repository.AccountWithSnapshot, error)
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
	// FindOAuthConnectionByID loads a grant row by id. Used by the
	// YouTube login handler (R7) to decide whether a reconnect may
	// skip prompt=consent: the row's status + granted_scopes tell
	// the handler whether the existing grant is healthy, and the
	// row's oauth_client_key is reused so a healthy reconnect stays
	// on the pool client that issued the grant (no new refresh
	// token, no cross-pool drift). Returns (nil, nil) when no row
	// matches — the caller treats that as "cannot verify health"
	// and fails towards consent.
	FindOAuthConnectionByID(ctx context.Context, id int64) (*models.OAuthConnection, error)
	// CountActiveAccountsOnConnection (P0 — shared-grant disconnect)
	// returns the number of still-active sibling channels sharing the
	// account's OAuth grant (oauth_connection_id), excluding the account
	// being disconnected. 0 → the disconnect may safely revoke the grant
	// (remote provider revoke + local token delete); >0 → the grant
	// tokens MUST be preserved for the siblings (migrations 084/085).
	CountActiveAccountsOnConnection(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error)
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
	// ListByPost returns the full target fan-out for a single post
	// (Taglio 5.1 step 2 — closes the empty-array gap on GET
	// /api/v1/posts/{id}/targets). Returns (nil, nil) when the
	// post has no targets. The companion is FindTargetByID below.
	ListByPost(postID int64) ([]models.PostTarget, error)
	// FindTargetByID returns a single post_target (Taglio 5.1
	// step 2 — GET /api/v1/post-targets/{id} polling endpoint).
	// Returns (nil, nil) when no row matches the id. The handler
	// reads the parent post + workspace separately so the IDOR
	// guard stays in Go (single owner check, single round-trip).
	FindTargetByID(id int64) (*models.PostTarget, error)
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
	ListByWorkspaceWithAccounts(workspaceID int64) ([]models.GroupWithAccounts, error)
	UpdateSettings(ctx context.Context, groupID, workspaceID, userID int64, updates []models.GroupAccountLanguageUpdate) error
	// RemoveAccountFromGroupTx detaches a single account from a group in
	// one transaction and resyncs the workspace_channels binding — the
	// dedicated "rimuovi dalla cartella" endpoint contract. It must NOT
	// touch platform_accounts or OAuth grants.
	RemoveAccountFromGroupTx(ctx context.Context, groupID, workspaceID, accountID int64) error
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

type AuditLogStore interface {
	Log(ctx context.Context, eventType, actorID string, resourceType, resourceID string, metadata map[string]interface{}) error
}

// BookingEventStore is the persistence contract for the
// POST /api/v1/booking_events endpoint (anonymous lead capture
// from the marketing strategy-call modal). The Contract has
// exactly one method so the handler stays narrow — see
// pkg/api/booking_events.go for the security model (per-IP
// rate-limit + same-origin + ON CONFLICT idempotency).
//
// Production wiring in internal/bootstrap/app.go passes
// *repository.BookingEventRepository which satisfies this
// interface. The compile-time assertion below catches
// signature drift at go vet time.
type BookingEventStore interface {
	Insert(event *models.BookingEvent) error
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
	// YouTubePoolCapacity (R8 — OAuth Client Pool dashboard) returns
	// the fleet-wide pool-capacity report: per-client active-grant
	// counts + the per-Google-manager breakdown (pool client, grant
	// status, channel totals, per-channel drill-down). Backs
	// GET /admin/youtube/oauth_pool_capacity. Never contains
	// credential material — only subject IDs, client keys, statuses.
	YouTubePoolCapacity(ctx context.Context) (repository.YouTubePoolCapacityReport, error)
}

// SnapshotStore is the persistence contract for
// account_resource_snapshots. Defined inline to keep pkg/api off
// internal/repository imports; main.go injects
// *repository.SnapshotRepository which satisfies this interface.
type SnapshotStore interface {
	GetSnapshot(platformAccountID int64) (*repository.AccountResourceSnapshot, error)
	UpsertSnapshot(snap *repository.AccountResourceSnapshot) error
	IsSnapshotStale(platformAccountID int64, maxAge time.Duration) (bool, error)
	// MarkSnapshotRefreshPending stamps refresh_pending_at on the
	// account's snapshot row so the background worker knows a refresh
	// is due. Called (best-effort) by the read path instead of calling
	// the provider synchronously — opening a channel page never blocks
	// on YouTube. UpsertSnapshot clears the flag on completion.
	MarkSnapshotRefreshPending(platformAccountID int64, now time.Time) error
	// MarkAllSnapshotRefreshesPending enqueues every non-deleted account
	// owned by userID for a background refresh in ONE statement and
	// returns the count. Backs POST /accounts/sync-all ("refresh all
	// channels"): the request only stamps the queue, the sweep worker
	// performs the actual provider calls with bounded concurrency — no
	// per-account fan-out from the API layer.
	MarkAllSnapshotRefreshesPending(userID int64, now time.Time) (int64, error)
	// MarkSnapshotsRefreshPending batches stale/missing account ids into
	// the durable refresh queue without an N+1 write fan-out.
	MarkSnapshotsRefreshPending(platformAccountIDs []int64, now time.Time) error
}

// MetricHistoryStore is the persistence contract for daily account
// metrics. Defined inline to keep pkg/api off internal/repository
// imports; main.go injects *repository.AccountMetricsRepository.
type MetricHistoryStore interface {
	UpsertDaily(platformAccountID int64, date time.Time, point repository.AccountMetricPoint) error
	UpsertMonetary(platformAccountID int64, date time.Time, point repository.AccountMetricPoint) error
	GetHistory(platformAccountID int64, from, to time.Time) ([]repository.AccountMetricPoint, error)
}

// BatchMetricHistoryStore is an optional capability for aggregate
// dashboard reads. Implementations can load all account histories in one
// query; older injected stores continue to work through MetricHistoryStore.
type BatchMetricHistoryStore interface {
	GetHistoryBatch(platformAccountIDs []int64, from, to time.Time) (map[int64][]repository.AccountMetricPoint, error)
}

// ContentPipelineStore (Blocco Carosello content-pipeline endpoint) is
// the read-only contract for GET /api/v1/content/{id}/pipeline.
// One call returns a workspace-scoped fan-out covering posts +
// post_targets + youtube_target_publications + platform_accounts
// + media_assets + upload_jobs. The handler renders the entry into
// the timeline response; the API layer does NOT cache or persist
// anything on the read path.
//
// Same pattern as PostStore / UploadJobStore: a local interface
// so test fixtures can supply an in-memory fake without dragging
// *sql.DB-bound concrete types. Production wiring in
// internal/bootstrap/app.go passes *repository.ContentPipelineRepository.
type ContentPipelineStore interface {
	GetPipeline(ctx context.Context, workspaceID, postID int64) (*repository.ContentPipelineEntry, error)
}

// ContentPackageStore is the product-level editable aggregate contract. The
// concrete repository lives in internal/repository; keeping this alias here
// lets API tests provide a small in-memory implementation without widening
// the router's dependency surface.
type ContentPackageStore = repository.ContentPackageStore

type DriveInboxStore = repository.DriveInboxStore

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
