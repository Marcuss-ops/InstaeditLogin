// Package worker — reconcile_worker.go is the second background
// goroutine alongside PublishWorker. The reconciler is a separate
// type with its own Run loop, mirroring the outbox dispatcher shape
// (internal/outbox/dispatcher.go).
//
// Why a separate goroutine:
//
//  1. INDEPENDENT CADENCE. The publish driver ticks at 30s (default)
//     and looks for queued→publishing transitions. The reconciler
//     ticks faster (5s default) so an async publish's
//     publishing→published transition is observed promptly without
//     being coupled to the driver's cadence.
//  2. NO DOUBLE-POLL. With tickReconcile removed from PublishWorker,
//     there is exactly ONE goroutine reading the publishing row set per
//     replica. Durable per-target leases then serialize provider polling
//     across replicas; publisher idempotency remains a second safety net.
//  3. FAILURE ISOLATION. A stuck reconciler tick does NOT block
//     the publish driver. With the old in-runOnce shape, a slow
//     platform API on the reconciler tick held the publish driver
//     hostage for the duration of the platform call.
//
// Per-tick body: drain a bounded dirty-post repair queue, then
// ListPublishing → for each row → lookup account → lookup AsyncPublisher
// capability → vault.Renew token → AsyncPublisher.Reconcile (single GET +
// transition decision). On PUBLISH_COMPLETE transition to status='published';
// on explicit provider FAILED/permanent errors transition to status='failed';
// on transient errors schedule a bounded retry; on in-flight schedule the
// next poll without consuming the transient-failure budget.
// provider_state is written ONLY on terminal transitions (so the
// column is a terminal-state log, not a per-tick snapshot).

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// DefaultReconcileInterval is the default tick interval for the
// reconciler goroutine. Smaller than the publish driver's default
// (30s) so async publishes' publishing→published latency is bounded
// by a snappy 5s ceiling under typical load. Operators can override
// via RECONCILE_WORKER_INTERVAL_SECONDS (config.go).
const DefaultReconcileInterval = 5 * time.Second

// ReconcilePostStore is the narrow slice of PostRepository the
// reconciler depends on. Defined here (not in repository package)
// so the worker can be unit-tested with a small in-memory mock
// without touching *sql.DB / sqlmock. The concrete *PostRepository
// satisfies it via duck-typing at the wireup site (main.go).
//
// Distinct from PublisherPostStore because the driver needs the
// claim/find-by-id/stamp-key surface while the reconciler needs
// only the read/status-transition surface plus its own claim.
// Splitting the interfaces compiles-in the invariant that the
// reconciler never accidentally writes the publish path (no
// payload load, no idempotency-key stamp).
//
// SKIP LOCKED: ClaimPublishingTarget is the reconciler's atomic
// row-ownership check. Before calling AsyncPublisher.Reconcile,
// the reconciler claims the row so two reconciler replicas racing
// the same publishing target don't both spend an API call.
type ReconcilePostStore interface {
	// ListPublishing returns at most limit ready publishing targets whose
	// next_reconcile_at is due and whose provider publish ID is non-empty.
	ListPublishing(limit int) ([]models.PostTarget, error)
	// ClaimPublishingTarget atomically stamps a durable reconciler lease
	// using FOR UPDATE SKIP LOCKED. The lease owner is held across the
	// external provider call and must be supplied to every write below.
	ClaimPublishingTarget(id int64, ownerID string, leaseTTL time.Duration) (bool, error)
	// HeartbeatReconcileTarget extends the active owner lease via CAS.
	HeartbeatReconcileTarget(id int64, ownerID string, leaseTTL time.Duration) error
	// ReleaseReconcileTarget clears the active owner lease via CAS.
	ReleaseReconcileTarget(id int64, ownerID string) error
	// UpdateReconcileStatusWithLease persists a terminal transition only
	// while ownerID still owns a non-expired lease, then releases it.
	UpdateReconcileStatusWithLease(target *models.PostTarget, ownerID string) error
	// ScheduleNextReconcileWithLease advances adaptive polling using owner +
	// attempt CAS, optionally increments the transient-failure budget, records
	// diagnostics, and releases the lease.
	ScheduleNextReconcileWithLease(id int64, ownerID string, expectedAttempt int, next time.Time, incrementAttempt bool, errorCode, errorMessage string) error
	// ListDirtyAggregatePostIDs returns a bounded snapshot of parent posts
	// marked dirty by post-target transitions. It must never scan all posts.
	ListDirtyAggregatePostIDs(limit int) ([]int64, error)
	// RepairDirtyAggregatePost repairs one queued parent and removes its queue
	// row atomically. Failures leave the row queued for a later retry.
	RepairDirtyAggregatePost(postID int64) error
}

// ReconcileUserStore is the reconciler's narrow view of the user /
// platform_accounts repository. Kept as a type alias for
// PublisherUserStore because the dependency is identical today
// (resolver for orphan-account detection on the publishing path);
// the alias preserves intent at the wireup site (`var userRepo
// ReconcileUserStore` reads as "the user store the reconciler
// needs").
type ReconcileUserStore = PublisherUserStore

// ReconcileWorker drives the async-publishing state machine
// (publishing → published | failed) by polling ListPublishing
// every interval and calling AsyncPublisher.Reconcile on each
// target. One struct, one goroutine (its Run method), ctx-cancellable.
// Multi-replica safety is provided by the durable reconciler lease: the
// claim stamps owner and expiry before provider I/O, heartbeats extend it,
// and terminal/scheduling writes require owner-and-unexpired-lease CAS.
// Publisher idempotency remains a defense in depth for provider retries.
//
// The dispatcher's outbox-based retry path is the
// platform-decoupled equivalent for failures; the per-target retry
// state machine (next_attempt_at / attempt_count columns, migration
// 018) is an option for async platforms that want
// at-most-N-transient-failures-per-row semantics inside the row itself.
type ReconcileWorker struct {
	postRepo      ReconcilePostStore
	userRepo      ReconcileUserStore
	router        *services.CapabilityRouter
	vault         credentials.VaultAPI
	workerID      string                  // per-process id, threaded via constructor (no global)
	memoryLimiter *services.MemoryLimiter // explicit DI; nil-safe in tests
	interval      time.Duration
	logger        *slog.Logger
}

// NewReconcileWorker wires the dependencies. interval <= 0 falls back
// to DefaultReconcileInterval (5s) to prevent tight loops from
// misconfiguration. nil logger inherits slog.Default(). router and
// vault must be non-nil; a nil will panic on the first tick
// (fail-fast for misconfigured wiring).
//
// Commit DI refactor: workerID and memoryLimiter are now explicit
// constructor arguments (no global lookup). workerID=="" is normalised
// to "unset" so log lines stay meaningful; memoryLimiter may be nil in
// test rigs that don't exercise rate-limit signals.
func NewReconcileWorker(
	postRepo ReconcilePostStore,
	userRepo ReconcileUserStore,
	router *services.CapabilityRouter,
	vault credentials.VaultAPI,
	workerID string,
	memoryLimiter *services.MemoryLimiter,
	interval time.Duration,
	logger *slog.Logger,
) *ReconcileWorker {
	if interval <= 0 {
		interval = DefaultReconcileInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	if workerID == "" {
		workerID = "unset"
	}
	return &ReconcileWorker{
		postRepo:      postRepo,
		userRepo:      userRepo,
		router:        router,
		vault:         vault,
		workerID:      workerID,
		memoryLimiter: memoryLimiter,
		interval:      interval,
		logger:        logger,
	}
}

// Run blocks until ctx is cancelled, executing one reconcile pass
// per interval. Performs a graceful drain: when ctx.Done() fires
// while a tick is mid-flight, the current tick completes naturally
// and Run returns only after that. Returns ctx.Err() on shutdown;
// logs non-nil errors and continues otherwise.
//
// Initial reconcile runs before the first ticker tick so a freshly
// spawned worker doesn't wait up to `interval` before observing
// any already-publishing rows. Matches the outbox dispatcher's
// initial-drain-then-ticker shape (internal/outbox/dispatcher.go::Run).
func (w *ReconcileWorker) Run(ctx context.Context) error {
	w.logger.Info("reconcile worker started",
		"interval_seconds", w.interval.Seconds(),
		"worker_id", w.workerID)
	defer w.logger.Info("reconcile worker stopped", "worker_id", w.workerID)

	// Initial reconcile — no wait for the first sweep.
	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

const (
	dirtyAggregateRepairBatchSize = 100
	reconcilePollingBatchSize     = 100
	reconcileBackoffCap           = 120 * time.Second
	reconcileLeaseTTL             = 60 * time.Second
	// A target is dead-lettered only after this many reconciler retries.
	// Explicit provider terminal states and permanent errors fail immediately;
	// transient transport/provider errors get this bounded retry budget.
	reconcileMaxAttempts = 5
)

var reconcileBackoffSchedule = [...]time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
	60 * time.Second,
	120 * time.Second,
}

// runOnce executes one bounded dirty-post repair pass and one reconcile pass.
// Per-tick errors are logged at WARN and the worker keeps ticking on the next
// interval — same shape as PublishWorker.runOnce. A dirty row is removed only
// by RepairDirtyAggregatePost after the targeted transaction succeeds.
func (w *ReconcileWorker) runOnce(ctx context.Context) {
	if repaired, err := w.repairDirtyAggregates(); err != nil {
		w.logger.Warn("reconcile dirty aggregate repair failed", "error", err)
	} else if repaired > 0 {
		w.logger.Info("reconcile dirty aggregate repair done", "repaired", repaired)
	}
	if reconciled, failed, err := w.tickReconcile(ctx); err != nil {
		w.logger.Warn("reconcile worker tick failed", "error", err)
	} else if reconciled > 0 || failed > 0 {
		w.logger.Info("reconcile worker tick done",
			"reconciled", reconciled, "failed", failed)
	}
}

func (w *ReconcileWorker) repairDirtyAggregates() (int, error) {
	postIDs, err := w.postRepo.ListDirtyAggregatePostIDs(dirtyAggregateRepairBatchSize)
	if err != nil {
		return 0, fmt.Errorf("list dirty aggregate posts: %w", err)
	}
	repaired := 0
	for _, postID := range postIDs {
		if err := w.postRepo.RepairDirtyAggregatePost(postID); err != nil {
			return repaired, fmt.Errorf("repair dirty aggregate post %d: %w", postID, err)
		}
		repaired++
	}
	return repaired, nil
}

// tickReconcile processes all targets in status='publishing'
// with a non-null platform_post_id. For each, it loads the
// platform_account, looks up the AsyncPublisher capability,
// refreshes the OAuth token, and calls Reconcile (single GET +
// transition decision). On PUBLISH_COMPLETE it transitions to
// 'published'; explicit provider FAILED/permanent errors transition to
// 'failed'; transient errors are retried with backoff; in-flight states
// are rescheduled without consuming the transient-failure budget.
//
// Safety: this goroutine claims each row via ClaimPublishingTarget
// before reading it (SKIP LOCKED). If another reconciler replica
// already claimed the row, we skip it. The winner has
// exclusive ownership for the duration of the reconcileTarget call.
// If the lease expires, a successor may take over; all writes from the
// stale owner then fail their owner-and-expiry CAS instead of overwriting
// the successor's result.
func (w *ReconcileWorker) tickReconcile(ctx context.Context) (reconciled, failed int, err error) {
	publishing, err := w.postRepo.ListPublishing(reconcilePollingBatchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("list publishing: %w", err)
	}
	if len(publishing) == 0 {
		return 0, 0, nil
	}

	for i := range publishing {
		// Index-based loop (not `for _, target`): we mutate &publishing[i]
		// inside reconcileTarget and the local copy must reflect those
		// mutations when we pass it to UpdateStatus.
		target := &publishing[i]
		ok, wasFailed, err := w.reconcileTarget(ctx, target)
		if err != nil {
			w.logger.Warn("reconcile target failed",
				"target_id", target.ID,
				"post_id", target.PostID,
				"error", err)
			failed++
			continue
		}
		if wasFailed {
			failed++
		}
		if ok {
			reconciled++
		}
	}
	return reconciled, failed, nil
}

// reconcileTarget drives the per-target async-publisher state
// machine by
// delegating to AsyncPublisher.Reconcile, which returns one of
// three terminal-stable outcomes per its interface contract:
//
//	(*PublishResult, nil)    — PUBLISH_COMPLETE → status='published'
//	(nil, err)               — explicit provider terminal/permanent failure →
//	                            status='failed'; transient transport/provider
//	                            errors → retry with backoff until the bounded
//	                            retry budget is exhausted, then status='dlq'.
//	(nil, nil)               — in-flight → schedule the next check without
//	                            consuming the transient-failure budget.
//
// Per-capability setup (account/oauth lookup, vault.Renew) is performed
// before delegation. The state-string switch (`switch state { case "PUBLISH_COMPLETE":
// ... }`) is gone — Reconcile owns the transition decision; the
// worker just records it.
//
// provider_state is written ONLY on terminal transitions
// (PUBLISH_COMPLETE / FAILED) via UpdateStatus, not on every
// in-flight tick. Without a state string from Reconcile's contract
// we can't write a fine-grained in-flight label; skipping it is the
// documented choice (the column becomes a terminal-state log
// rather than a per-tick snapshot).
//
// Returns (reconciled bool, wasFailed bool, err). reconciled and
// wasFailed let the caller increment per-tick counters without
// parsing the error.
func (w *ReconcileWorker) reconcileTarget(ctx context.Context, target *models.PostTarget) (reconciled bool, wasFailed bool, err error) {
	// SKIP LOCKED: claim the publishing row before doing any work.
	// If another reconciler replica already claimed
	// this row, we skip it — the winner will drive the state
	// machine to completion. This prevents two replicas from
	// spending duplicate API calls on the same publish_id.
	claimed, claimErr := w.postRepo.ClaimPublishingTarget(target.ID, w.workerID, w.reconcileLeaseTTL())
	if claimErr != nil {
		return false, false, fmt.Errorf("claim publishing target %d: %w", target.ID, claimErr)
	}
	if !claimed {
		// Another reconciler owns this row — skip, it'll be
		// processed by the winner.
		return false, false, nil
	}

	// Keep ownership alive while account/token/provider work is in flight.
	// The terminal and scheduling writes below remain owner-CAS guarded.
	leaseCtx, stopLease := startReconcileLeaseHeartbeat(ctx, w.postRepo, target.ID, w.workerID, reconcileLeaseTTL)
	defer stopLease()
	defer func() { _ = w.postRepo.ReleaseReconcileTarget(target.ID, w.workerID) }()

	// 1. Load platform account.
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return false, false, fmt.Errorf("load account %d: %w", target.PlatformAccountID, err)
	}

	if account == nil {
		// Orphan target — mark failed so it doesn't loop forever.
		return w.markFailedAndReturn(target, w.workerID, fmt.Sprintf("platform_account %d not found", target.PlatformAccountID))
	}
	// A grant-wide invalid_grant transition can affect publishing rows that
	// were already in flight. Stop them before OAuth refresh or provider I/O;
	// reconnecting the shared grant is the only recovery path.
	if account.Platform == models.PlatformYouTube &&
		(account.Status == models.AccountStatusReauthRequired || account.ReauthRequiredAt != nil) {
		return w.markBlockedAuthAndReturn(target, w.workerID, youtubeReauthReason())
	}

	// 2. Look up AsyncPublisher capability.
	ap, ok := w.router.AsyncPublisher(account.Platform)
	if !ok {
		// Platform doesn't support async publishing — leave the target
		// alone. Sync platforms complete their publish in the publish
		// driver's tick, no polling or schedule advancement is needed.
		return false, false, nil
	}

	// 3. Refresh OAuth token via the vault.
	//
	// Token-refresh DUPLICATION note: the publish driver
	// (PublishWorker.publishTarget) also calls vault.Renew for the
	// same account on its tick. This is safe — the CredentialVault
	// uses pg_advisory_xact_lock to serialise concurrent refreshes
	// for the same account_id, so a driver-reconciler race collapses
	// to a single round-trip (the first refresh completes; subsequent
	// calls find the token already valid and return without work).
	// Per-account Vault.Renew call count rises slightly across the
	// two goroutines; network/DB load stays bounded. Tags this as a
	// known side-effect so a future reviewer doesn't rediscover the
	// property.
	oauth, oauthOK := w.router.OAuth(account.Platform)
	if !oauthOK {
		return w.markFailedAndReturn(target, w.workerID, fmt.Sprintf("platform %q missing OAuth capability", account.Platform))
	}
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	var oauthToken *models.OAuthToken
	if account.Platform == models.PlatformYouTube {
		oauthToken, err = credentials.RenewYouTubeToken(leaseCtx, w.vault, account.ID, refresher, w.logger)
	} else {
		oauthToken, err = w.vault.Renew(leaseCtx, account.ID, models.TokenTypeBearer, refresher)
		if errors.Is(err, credentials.ErrModernGrantMissing) {
			oauthToken, err = w.vault.Renew(leaseCtx, account.ID, models.TokenTypeLongLived, refresher)
		}
	}
	if err != nil {
		if account.Platform == models.PlatformYouTube && errors.Is(err, credentials.ErrYouTubeInvalidGrant) {
			w.markYouTubeGrantReauth(ctx, account)
			return w.markBlockedAuthAndReturn(target, w.workerID, youtubeReauthReason())
		}
		return w.markFailedAndReturn(target, w.workerID, "token refresh failed")
	}

	// 4. Delegate to platform's Reconcile (single GET + transition decision).
	res, err := ap.Reconcile(leaseCtx, oauthToken.AccessToken, target.PlatformPostID)
	if err != nil {
		isRateLimit := services.IsRateLimitError(err)
		if !isRateLimit && w.isPermanentReconcileError(err) {
			w.logger.Warn("publish reconcile permanent error",
				"target_id", target.ID, "publish_id", target.PlatformPostID, "error", err)
			return w.markFailedAndReturn(target, w.workerID, fmt.Sprintf("publish failed: %v", err))
		}
		// Transport failures, timeouts, 5xx/provider-unavailable and
		// rate limits are not proof that the remote publish failed. Keep
		// the target publishing and use the shared retry-after carrier or
		// adaptive backoff instead of turning a transient outage terminal.
		// Rate limits are provider-directed waiting, not a failed
		// reconcile attempt. They record diagnostics and Retry-After,
		// but never consume the transient budget or trigger DLQ.
		if !isRateLimit && target.ReconcileAttempt >= reconcileMaxAttempts-1 {
			return w.markDeadLetterAndReturn(target, w.workerID,
				fmt.Sprintf("publish retry budget exhausted after %d transient failures: %v", reconcileMaxAttempts, err))
		}
		return w.scheduleRetry(target, services.RetryAfterFromError(err), err)
	}
	if res == nil {
		// In-flight — no state string available (Reconcile hides it).
		// Leave the target alone and schedule the next check with adaptive
		// backoff instead of revisiting it on every worker tick.
		return w.scheduleInFlight(target)
	}
	// Defensive guard: a successful Reconcile result with an empty
	// PlatformMediaID is a misbehaving platform impl (the canonical
	// contract returns res.PlatformMediaID == publish_id or the
	// public-facing id — both non-empty). Treat as transient so the
	// row stays in 'publishing' and the next tick retries. Per-target
	// backoff is the post_targets retry state machine (or, longer-
	// term, the outbox dispatcher's max-attempts). This branch is
	// dead for TikTok's specific impl (always populates the field)
	// but defensive for future AsyncPublisher implementations.
	if res.PlatformMediaID == "" {
		w.logger.Warn("publish reconcile empty PlatformMediaID (treated as transient)",
			"target_id", target.ID, "publish_id", target.PlatformPostID)
		return w.scheduleInFlight(target)
	}

	// 5. Success transition: persist terminal publisher_state + flip
	// the target row to 'published' with publish_id-stamped URL fields.
	target.Status = models.PostStatusPublished
	target.ProviderState = "PUBLISH_COMPLETE"
	// For TikTok, PlatformMediaID == publish_id; for other async
	// providers the value is the public-facing post id returned by
	// the platform at terminal time. Either way, res.PlatformMediaID
	// is the canonical post_target.platform_post_id at completion.
	target.PlatformPostID = res.PlatformMediaID
	now := time.Now()
	target.PublishedAt = &now
	if err := w.postRepo.UpdateReconcileStatusWithLease(target, w.workerID); err != nil {
		return false, false, fmt.Errorf("transition to published: %w", err)
	}
	return true, false, nil
}

// isPermanentReconcileError centralises the provider/transport policy for
// errors returned by AsyncPublisher.Reconcile. Explicit provider terminal
// states and typed permanent errors fail immediately. Canonical provider
// errors marked non-retryable (4xx/auth/content rejection) also fail
// immediately. Rate limits and provider-unavailable errors are retryable;
// untyped transport errors (timeout, reset, EOF) are conservatively treated
// as transient because they do not prove the remote publish failed.
func (w *ReconcileWorker) isPermanentReconcileError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, services.ErrPublishTerminal) ||
		errors.Is(err, services.ErrPublishPermanent) ||
		errors.Is(err, services.ErrPermanentUpload) {
		return true
	}
	if providerErr, ok := services.IsProviderError(err); ok {
		return !providerErr.Retryable
	}
	return false
}

// scheduleRetry applies the shared Retry-After policy. A positive provider
// hint is honored as-is; absent or invalid hints use the bounded adaptive
// schedule. The target remains in publishing and the repository CAS advances
// only the transient-failure budget, records diagnostics, and releases the lease.
func (w *ReconcileWorker) scheduleRetry(target *models.PostTarget, retryAfter time.Duration, cause error) (reconciled bool, wasFailed bool, err error) {
	attempt := target.ReconcileAttempt
	if attempt < 0 {
		attempt = 0
	}
	if retryAfter <= 0 {
		retryAfter = reconcileBackoffForAttempt(attempt)
	}
	next := time.Now().Add(retryAfter)
	incrementAttempt := !services.IsRateLimitError(cause)
	if err := w.postRepo.ScheduleNextReconcileWithLease(target.ID, w.workerID, attempt, next, incrementAttempt, reconcileErrorCode(cause), cause.Error()); err != nil {
		return false, false, fmt.Errorf("schedule retry for target %d: %w", target.ID, err)
	}
	if incrementAttempt {
		target.ReconcileAttempt = attempt + 1
	}
	target.NextReconcileAt = &next
	return false, false, nil
}

func reconcileErrorCode(err error) string {
	if services.IsRateLimitError(err) {
		return "RATE_LIMITED"
	}
	return "RECONCILE_TRANSIENT"
}

func reconcileBackoffForAttempt(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt >= len(reconcileBackoffSchedule) {
		return reconcileBackoffSchedule[len(reconcileBackoffSchedule)-1]
	}
	return reconcileBackoffSchedule[attempt]
}

func (w *ReconcileWorker) markDeadLetterAndReturn(target *models.PostTarget, ownerID, reason string) (reconciled bool, wasFailed bool, err error) {
	deadTarget := *target
	deadTarget.Status = models.PostStatusDLQ
	deadTarget.ProviderState = "FAILED"
	deadTarget.LastErrorCode = "RECONCILE_RETRY_EXHAUSTED"
	deadTarget.ErrorMessage = reason
	if updateErr := w.postRepo.UpdateReconcileStatusWithLease(&deadTarget, ownerID); updateErr != nil {
		return false, false, fmt.Errorf("transition to dlq: %w", updateErr)
	}
	*target = deadTarget
	w.logger.Warn("reconcile target moved to dead letter queue", "target_id", target.ID, "post_id", target.PostID, "reason", reason)
	return true, true, nil
}

// markFailedAndReturn transitions the target to status='failed' and
// returns the bookkeeping so the reconciler can increment its
// counters. The (true, true, nil) return values signal "yes, this
// target was reconciled (to failed), yes it failed, no error".
func (w *ReconcileWorker) reconcileLeaseTTL() time.Duration {
	return 60 * time.Second
}

func (w *ReconcileWorker) scheduleInFlight(target *models.PostTarget) (reconciled bool, wasFailed bool, err error) {
	attempt := target.ReconcileAttempt
	if attempt < 0 {
		attempt = 0
	}
	backoff := reconcileBackoffForAttempt(attempt)
	if backoff > reconcileBackoffCap {
		backoff = reconcileBackoffCap
	}
	next := time.Now().Add(backoff)
	if err := w.postRepo.ScheduleNextReconcileWithLease(target.ID, w.workerID, attempt, next, false, "", ""); err != nil {
		return false, false, fmt.Errorf("schedule next reconcile for target %d: %w", target.ID, err)
	}
	target.NextReconcileAt = &next
	return false, false, nil
}

func (w *ReconcileWorker) markBlockedAuthAndReturn(target *models.PostTarget, ownerID, reason string) (reconciled bool, wasFailed bool, err error) {
	blockedTarget := *target
	blockedTarget.Status = models.PostStatusBlockedAuth
	blockedTarget.ProviderState = "REAUTH_REQUIRED"
	blockedTarget.LastErrorCode = "blocked_auth"
	blockedTarget.ErrorMessage = reason
	if updateErr := w.postRepo.UpdateReconcileStatusWithLease(&blockedTarget, ownerID); updateErr != nil {
		return false, false, fmt.Errorf("transition to blocked_auth: %w", updateErr)
	}
	*target = blockedTarget
	return true, true, nil
}

func (w *ReconcileWorker) markFailedAndReturn(target *models.PostTarget, ownerID, reason string) (reconciled bool, wasFailed bool, err error) {
	w.logger.Warn("reconcile target marked failed",
		"target_id", target.ID,
		"post_id", target.PostID,
		"reason", reason)

	// Mutate a copy so the caller's target stays unchanged if the DB
	// write fails (avoids leaking a stale in-memory failed state).
	failedTarget := *target
	failedTarget.Status = models.PostStatusFailed
	failedTarget.ProviderState = "FAILED"
	failedTarget.ErrorMessage = reason
	if failedTarget.LastErrorCode == "" {
		failedTarget.LastErrorCode = "RECONCILE_FAILED"
	}
	if updateErr := w.postRepo.UpdateReconcileStatusWithLease(&failedTarget, ownerID); updateErr != nil {
		return false, false, fmt.Errorf("transition to failed: %w", updateErr)
	}
	*target = failedTarget
	return true, true, nil
}
