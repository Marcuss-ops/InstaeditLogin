// Package worker implements background processes that run alongside the
// HTTP server. The canonical worker entrypoint supervises these goroutines:
//
//   - PublishWorker.publishTarget  — driver: queued → publishing
//     → published|failed. Picks up scheduled post_targets whose
//     scheduled_at <= NOW() and dispatches them through the
//     per-platform Publisher capability registered in the
//     CapabilityRouter.
//   - ReconcileWorker.reconcile    — reconciler: publishing →
//     published|failed. Polls ListPublishing every interval and
//     calls AsyncPublisher.Reconcile on each row.
//
// Both run as INDEPENDENT goroutines with INDEPENDENT tick intervals
// and ctx-cancellable lifecycles. The worker registry starts and shuts
// them down in parallel under the shared drain budget.
//
// The split mirror's the outbox dispatcher's shape (commit 20ad05f,
// internal/outbox/dispatcher.go) — each major background process is
// its own struct, its own Run loop, its own Done channel. Multi-
// replica safety for both is delegated to the underlying Postgres
// state (the publish driver's atomic claim + the outbox dispatcher's
// SKIP LOCKED); no per-process coordination between replicas is
// required.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// providerIdempotencyKeyPrefix is the namespace marker baked into the
// SHA-256 input so a v2 (or any future revision) of the deterministic
// key generator can yield different outputs for the same (post_id,
// platform_account_id) tuple. Bumping the prefix is the migration:
// change the prefix, run a backfill to recompute all rows, and v2
// keys fully replace v1.
const providerIdempotencyKeyPrefix = "v1:"

// providerIdempotencyKeyLen is the hex-prefix length chosen for the
// worker-stamped key. 16 hex characters = 64 bits of entropy, more
// than enough to keep collision probability negligible for the life of
// a workspace (a 32 hex / 128-bit alternative is overkill for a
// per-(post,account) tuple).
const providerIdempotencyKeyLen = 16

// computeProviderIdempotencyKey returns the deterministic hex prefix
// for (postID, platformAccountID). Stable across processes and time,
// so retries on the same target produce the same key — the platform's
// native API dedup catches the duplicate publish on its end.
//
// The prefix-encoding is the migration boundary: introduce v2 keys by
// changing the prefix string, not by changing the SHA-256 layout. Old
// v1 keys remain readable until the backfill completes.
func computeProviderIdempotencyKey(postID, platformAccountID int64) string {
	h := sha256.New()
	h.Write([]byte(providerIdempotencyKeyPrefix))
	fmt.Fprintf(h, "%d:%d", postID, platformAccountID)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:providerIdempotencyKeyLen]
}

// PublisherPostStore is the narrow slice of the post + post_targets
// repository the *driver* (PublishWorker.publishTarget) depends on.
// Distinct from ReconcilePostStore because the driver needs the
// claim/find-by-id/stamp-key surface while the reconciler needs only
// the read/status-transition surface. Splitting the interfaces
// compiles-in the invariant that the two goroutines can't
// accidentally hit the other's data path.
//
// Defined here (not in repository package) so the worker can be
// unit-tested with a small in-memory mock without touching sql.DB
// or sqlmock. The concrete *PostRepository satisfies it via duck-
// typing at the wireup site (main.go).
type PublisherPostStore interface {
	// ListPending returns due child publication jobs in fair round-robin
	// order across parent posts. Each row is an independently claimable
	// target job; the parent post is only the aggregate status owner.
	ListPending(before time.Time) ([]models.PostTarget, error)
	// FindByID loads the parent post for the publish payload (caption/title/media_url).
	FindByID(id int64) (*models.Post, error)
	// UpdateStatus persists the publishing→published|failed
	// transitions. Lease-aware terminal writes use the optional
	// LeaseAwarePublisherPostStore contract so test doubles and legacy
	// callers remain source-compatible.
	UpdateStatus(target *models.PostTarget) error
	// SetProviderIdempotencyKey (Taglio 4.7 LEVEL 2, migration 022)
	// writes the worker-computed deterministic per-target
	// idempotency_key onto the post_target row. The worker calls
	// this AFTER ClaimQueuedTargetWithLease succeeds and BEFORE the
	// publish call so retries reuse the same key. Errors:
	//   * ErrProviderIdempotencyConflict: another target on the same
	//     account already has this key — degenerate/duplicate, the
	//     worker treats as failed and lets the operator reconcile.
	//   * ErrPostTargetNotFound: id is stale.
	//   * Other: wrapped DB error.
	SetProviderIdempotencyKey(id int64, key string) error

	// GetMetadata (Task 7/10) — post metadata JSON column accessor.
	GetMetadata(id int64) (json.RawMessage, error)
	// SetTargetCanaryVideoID (Task 7/10) — stamps canary upload video id.
	SetTargetCanaryVideoID(targetID int64, videoID string) error
}

// PublisherUserStore is the narrow slice of the user /
// platform_accounts repository the *driver* depends on. Just enough
// to resolve the platform_account for a pending post_target
// without dragging in the full UserRepository surface. ReconcileWorker
// uses the same type via the ReconcileUserStore alias defined in
// reconcile_worker.go.

// LeaseAwarePublisherPostStore is implemented by the SQL repository for
// independent child-job ownership. The lease-aware claim
// (ClaimQueuedTargetWithLease) is the ONLY claim path the publish
// driver uses. MarkRateLimitedRetryWithLease is the requeue path after
// the platform's final publish call answered 429/Retry-After — the
// legacy lease-less MarkRateLimitedRetry was removed (the worker
// uses a narrow interface assertion so test doubles only need the
// single method they exercise).
type LeaseAwarePublisherPostStore interface {
	ClaimQueuedTargetWithLease(id int64, ownerID string, leaseTTL time.Duration) (bool, error)
	UpdateStatusWithLease(target *models.PostTarget, ownerID string) error
	MarkRetrying(id int64, ownerID string, lastError string, nextAttemptAt time.Time) error
	MarkDeadLetter(id int64, ownerID string, lastError string) error
	MarkRateLimitedRetryWithLease(id int64, ownerID string, nextAttemptAt time.Time, lastError string) error
	UpdatePublishProgress(id int64, ownerID string, uploadOffset int64, providerState string, leaseTTL time.Duration) error
	ReclaimExpiredLeases(ownerID string) (int64, error)
}

// ContentPackageStateSynchronizer updates the product-level package state
// from the existing post/target publication rows. It does not own provider
// execution and is intentionally optional for backwards-compatible worker tests.
type ContentPackageStateSynchronizer interface {
	SyncPublicationState(ctx context.Context, packageID int64) error
}

type PublisherUserStore interface {
	// FindPlatformAccountByID returns (nil, nil) when no row matches, matching
	// the codebase's repository convention (nil/nil not-found, no ErrNoRows).
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
	// MarkReauthRequired (P0#3 server-side channel binding check)
	// atomically flips the platform_account's status to
	// 'reauth_required', stamps reauth_required_at with NOW(), and
	// records the failure code + message. Called by the publish
	// worker when a YouTube pre-upload channel binding check fails
	// (or any future per-platform credential rotation that is not
	// transient). Other platforms (Twitter, LinkedIn, etc.) may add
	// similar paths using the same method. Idempotent: re-flags with
	// a fresh reauth_required_at on each call (caller does NOT need
	// to read-then-write).
	MarkReauthRequired(ctx context.Context, id int64, code, message string) error
}

// PublishWorker periodically dispatches scheduled posts to their
// target platforms. One struct, one goroutine (its Run method),
// ctx-cancellable. The 3-step status transition (`queued` →
// `publishing` → `published | failed`) acts as a logical lock so two
// worker instances cannot double-publish the same target.
//
// Taglio 2.2: the worker depends on the CapabilityRouter
// (per-capability lookups: OAuthProvider for refresh, Publisher for
// the actual call) and a CredentialVault (for the encrypt + store +
// refresh-with-advisory-lock). The OAuthProvider is adapted to a
// credentials.TokenRefresher closure at the call site so the vault
// has zero knowledge of per-platform types.
//
// Taglio 5.x: the async-publish side of the state machine (publishing
// → published|failed) was extracted to ReconcileWorker (its own Run
// goroutine with independent tick interval). PublishWorker now only
// owns the queued → publishing transition.
type PublishWorker struct {
	postRepo      PublisherPostStore
	userRepo      PublisherUserStore
	router        *services.CapabilityRouter
	vault         credentials.VaultAPI
	throttle      *PlatformThrottle       // FASE 1.3: per-platform rate limiter
	workerID      string                  // per-process id, threaded via constructor (no global)
	memoryLimiter *services.MemoryLimiter // explicit DI; nil-safe in tests
	interval      time.Duration
	logger        *slog.Logger
	// resolver (migration 080 + MediaDownloadResolver followup) mints
	// a fresh presigned GET URL just-in-time at publish time so the
	// per-platform API call sees a valid signature. Production wiring
	// supplies services.NewMediaDownloadResolver(
	//     storageProvider, repository.NewMediaAssetRepository(db), log);
	// operational paths fail closed when it is absent.
	resolver services.MediaDownloadResolver
	// deliveryRegistry (Task 7/10) — post-completion dispatch hook.
	// Optional: nil means dispatch is a no-op (pre-existing test
	// rigidity). Wires through WithDeliveryRegistry, never through
	// NewPublishWorker (the existing constructor's positional args
	// are pinned by every test rig in this package).
	deliveryRegistry *services.DeliveryRegistry

	// canonicalCanaryUploader (Task 7/10) — the YouTube canary pre-flight
	// capability wired at startup via SetCanonicalCanaryUploader (test
	// harness uses that setter). Nil-safe at runtime: a nil uploader
	// makes the canary block warn + fall through to markPublishBlockedAuth.
	canonicalCanaryUploader services.YouTubeCanaryUploader

	// ytPubLookup (Blocco #1 followup — P1 Migration 077/Phase-2
	// bypass) — the YouTube target-publication store used by the
	// publish worker to discover the existing youtube_video_id that
	// Phase 1 (upload_worker.MarkYouTubeUploaded) stamped. When non-nil
	// and the row exists with youtube_upload_status='youtube_uploaded',
	// publishTarget routes through YouTubePrivacyUpdater.UpdateVideoPrivacy
	// (videos.update) instead of doing a fresh publisher.Publish
	// (videos.insert). Wired at startup via SetYouTubeTargetPublicationStore.
	// Nil at startup means the bypass is silently disabled — the
	// pre-fix behaviour is preserved for callers that don't wire it.
	ytPubLookup YouTubeTargetPublicationLookup
	// packageStateSync projects per-target publication completion back onto
	// the Content Package aggregate. It is optional for legacy test fixtures.
	packageStateSync ContentPackageStateSynchronizer

	// nvidiaTranslator (per-channel-language posting) translates the
	// post title/caption into each target channel's language at
	// publish time (account.Metadata["language"]). nil = feature off
	// (the original text is published unchanged). Wired at startup via
	// SetNvidiaMetadataTranslator.
	nvidiaTranslator ChannelTranslator
	// translationCache memoizes (post, language) → localized post so
	// sibling targets of the same post sharing a language trigger a
	// single NVIDIA call. Bounded by posts × distinct languages.
	translationCache sync.Map
}

// NewPublishWorker wires the dependencies. interval <= 0 falls back to
// a safe default of 30s to prevent tight loops from misconfiguration.
// nil logger inherits slog.Default(). router and vault must be
// non-nil; a nil will panic on the first tick (fail-fast for
// misconfigured wiring).
//
// Commit DI refactor: workerID and memoryLimiter are now explicit
// constructor arguments (no global metrics.WorkerID() read, no
// sync.Once-protected MemoryLimiter lookup). Both are nil/empty-safe:
// an empty workerID is recorded as "unset" so log lines still
// appear; a nil memoryLimiter is acceptable for workers that don't
// yet consume rate-limit signals (today: publish / reconcile).
func NewPublishWorker(
	postRepo PublisherPostStore,
	userRepo PublisherUserStore,
	router *services.CapabilityRouter,
	vault credentials.VaultAPI,
	resolver services.MediaDownloadResolver,
	workerID string,
	memoryLimiter *services.MemoryLimiter,
	interval time.Duration,
	logger *slog.Logger,
) *PublishWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	if workerID == "" {
		workerID = "unset"
	}
	return &PublishWorker{
		postRepo:      postRepo,
		userRepo:      userRepo,
		router:        router,
		vault:         vault,
		throttle:      NewPlatformThrottle(), // FASE 1.3
		workerID:      workerID,
		memoryLimiter: memoryLimiter,
		interval:      interval,
		logger:        logger,
		resolver:      resolver,
	}
}
