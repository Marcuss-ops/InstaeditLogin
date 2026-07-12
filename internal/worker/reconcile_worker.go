// Package worker — reconcile_worker.go is the SECOND background
// goroutine alongside PublishWorker. Taglio 5.x splits the
// reconciler out of PublishWorker (where it was a tickReconcile
// method called inside runOnce) into its own type + Run loop,
// mirroring the outbox dispatcher shape (internal/outbox/dispatcher.go).
//
// Why a separate goroutine:
//
//	1. INDEPENDENT CADENCE. The publish driver ticks at 30s (default)
//	   and looks for queued→publishing transitions. The reconciler
//	   ticks faster (5s default) so an async publish's
//	   publishing→published transition is observed promptly without
//	   being coupled to the driver's cadence.
//	2. NO DOUBLE-POLL. With tickReconcile removed from PublishWorker,
//	   there is exactly ONE goroutine reading the publishing row
//	   set per replica. Two replicas each have ONE reconciler —
//	   multi-replica safety is delegated to the platform's
//	   state-string idempotency (and the publisher_idempotency_key
//	   column on post_targets for any publisher that uses the
//	   provider reconciler idempotency model).
//	3. FAILURE ISOLATION. A stuck reconciler tick does NOT block
//	   the publish driver. With the old in-runOnce shape, a slow
//	   platform API on the reconciler tick held the publish driver
//	   hostage for the duration of the platform call.
//
// Per-tick body: ListPublishing → for each row → lookup account
// → lookup AsyncPublisher capability → vault.Renew token →
// AsyncPublisher.Reconcile (single GET + transition decision). On
// PUBLISH_COMPLETE transition to status='published'; on FAILED
// (including transient 5xx under the Reconcile contract) transition
// to status='failed'; on in-flight leave for next tick.
// provider_state is written ONLY on terminal transitions (so the
// column is a terminal-state log, not a per-tick snapshot).
package worker

import (
	"context"
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
// only the read/status-transition surface. Splitting the
// interfaces compiles-in the invariant that the reconciler never
// accidentally writes the publish path (no claim here, no
// payload load, no idempotency-key stamp).
type ReconcilePostStore interface {
	// ListPublishing returns post_targets whose status='publishing'
	// AND platform_post_id IS NOT NULL. Ordered by id ASC for
	// stable iteration (reconciler picks the same rows every tick
	// until they transition out of 'publishing').
	ListPublishing() ([]models.PostTarget, error)
	// UpdateStatus persists the publishing→published|failed
	// transitions the reconciler writes. Idempotent on terminal
	// states (two reconcilers racing the same row will both write
	// the same terminal state — the second UPDATE is a no-op).
	UpdateStatus(target *models.PostTarget) error
	// UpdatePublishState stamps the provider_state column with the
	// terminal label (PUBLISH_COMPLETE or FAILED). Written ONLY on
	// terminal transitions, not on every in-flight tick — the
	// column is terminal-state observability, not per-tick
	// consistency.
	UpdatePublishState(id int64, providerState string) error
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
// Multi-replica safety is delegated to the platform's per-publish_id
// state idempotency: two reconcilers cannot transition the same row
// to two conflicting states because:
//
//  1. UpdateStatus on terminal states is idempotent (same target→
//     same status: UPDATE post_targets SET status='published' WHERE
//     id=? is a no-op the second time).
//  2. The transitions are the only writer to status at this row's
//     lifecycle stage. The publish driver already transitioned
//     queued→publishing via ClaimQueuedTarget and is no longer the
//     row's writer.
//
// Taglio 5.x: split out from PublishWorker. The dispatcher's
// outbox-based retry path is the platform-decoupled equivalent
// for failures; the per-target retry state machine (next_attempt_at
// / attempt_count columns, migration 018) is an option for async
// platforms that want at-most-N-attempts-per-row semantics inside
// the row itself.
type ReconcileWorker struct {
	postRepo ReconcilePostStore
	userRepo ReconcileUserStore
	router   *services.CapabilityRouter
	vault    credentials.VaultAPI
	interval time.Duration
	logger   *slog.Logger
}

// NewReconcileWorker wires the dependencies. interval <= 0 falls back
// to DefaultReconcileInterval (5s) to prevent tight loops from
// misconfiguration. nil logger inherits slog.Default(). router and
// vault must be non-nil; a nil will panic on the first tick
// (fail-fast for misconfigured wiring).
func NewReconcileWorker(
	postRepo ReconcilePostStore,
	userRepo ReconcileUserStore,
	router *services.CapabilityRouter,
	vault credentials.VaultAPI,
	interval time.Duration,
	logger *slog.Logger,
) *ReconcileWorker {
	if interval <= 0 {
		interval = DefaultReconcileInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ReconcileWorker{
		postRepo: postRepo,
		userRepo: userRepo,
		router:   router,
		vault:    vault,
		interval: interval,
		logger:   logger,
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
		"interval_seconds", w.interval.Seconds())
	defer w.logger.Info("reconcile worker stopped")

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

// runOnce executes one tickReconcile pass and logs the result.
// Per-tick errors are logged at WARN and the worker keeps ticking
// on the next interval — same shape as PublishWorker.runOnce.
func (w *ReconcileWorker) runOnce(ctx context.Context) {
	if reconciled, failed, err := w.tickReconcile(ctx); err != nil {
		w.logger.Warn("reconcile worker tick failed", "error", err)
	} else if reconciled > 0 || failed > 0 {
		w.logger.Info("reconcile worker tick done",
			"reconciled", reconciled, "failed", failed)
	}
}

// tickReconcile processes all targets in status='publishing'
// with a non-null platform_post_id. For each, it loads the
// platform_account, looks up the AsyncPublisher capability,
// refreshes the OAuth token, and calls Reconcile (single GET +
// transition decision). On PUBLISH_COMPLETE it transitions to
// 'published'; on FAILED (including transient 5xx under the
// Reconcile contract) it transitions to 'failed'; on any
// in-flight state it leaves the target alone for the next tick.
//
// Safety: this goroutine does NOT claim the row before reading it.
// That's safe because the only thing the reconciler MUTATES on a
// publishing target is status (terminal transitions) and
// provider_state (terminal-state log). The status transition is
// idempotent — if two reconcilers racing from different replicas
// hit the same target at the same tick, the second UPDATE simply
// overwrites the first with the same terminal state. (The publish
// driver's ClaimQueuedTarget already prevents two workers from
// racing the queued→publishing transition.)
func (w *ReconcileWorker) tickReconcile(ctx context.Context) (reconciled, failed int, err error) {
	publishing, err := w.postRepo.ListPublishing()
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

// reconcileTarget (Taglio 5.x — canonical async-publisher
// transition). Drives the per-target async state machine by
// delegating to AsyncPublisher.Reconcile, which returns one of
// three terminal-stable outcomes per its interface contract:
//
//	(*PublishResult, nil)    — PUBLISH_COMPLETE → status='published'
//	(nil, err)               — FAILED          → status='failed' (terminal).
//	                            Includes transient 5xx/network errors: the
//	                            interface contract is "errors are terminal
//	                            too, retry is the worker's responsibility".
//	                            The dispatcher's retry counter + decorrelated-
//	                            jitter backoff on outbox_events is the retry
//	                            mechanism at the platform-decoupled level;
//	                            per-target retry on this row is via the
//	                            post_targets.next_attempt_at / attempt_count
//	                            columns (Taglio 4.7 state machine).
//	(nil, nil)               — in-flight → leave alone, retry next tick.
//
// Per-capability setup (account/oauth lookup, vault.Renew) is
// unchanged from the previous Taglio 4.2 implementation. The
// state-string switch (`switch state { case "PUBLISH_COMPLETE":
// ... }`) is gone — Reconcile owns the transition decision; the
// worker just records it.
//
// provider_state column (UpdatePublishState) is now written ONLY
// on terminal transitions (PUBLISH_COMPLETE / FAILED), not on
// every in-flight tick. Without a state string from Reconcile's
// contract we can't write a fine-grained in-flight label; skipping
// it is the documented choice (the column becomes a terminal-state
// log rather than a per-tick snapshot).
//
// Returns (reconciled bool, wasFailed bool, err). reconciled and
// wasFailed let the caller increment per-tick counters without
// parsing the error.
func (w *ReconcileWorker) reconcileTarget(ctx context.Context, target *models.PostTarget) (reconciled bool, wasFailed bool, err error) {
	// 1. Load platform account.
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return false, false, fmt.Errorf("load account %d: %w", target.PlatformAccountID, err)
	}
	if account == nil {
		// Orphan target — mark failed so it doesn't loop forever.
		return w.markFailedAndReturn(target, fmt.Sprintf("platform_account %d not found", target.PlatformAccountID))
	}

	// 2. Look up AsyncPublisher capability.
	ap, ok := w.router.AsyncPublisher(account.Platform)
	if !ok {
		// Platform doesn't support async publishing — leave the target
		// alone. Sync platforms complete their publish in the publish
		// driver's tick, no polling needed.
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
		return w.markFailedAndReturn(target, fmt.Sprintf("platform %q missing OAuth capability", account.Platform))
	}
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	oauthToken, err := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		if oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher); err != nil {
			return w.markFailedAndReturn(target, "token refresh failed: "+err.Error())
		}
	}

	// 4. Delegate to platform's Reconcile (single GET + transition decision).
	res, err := ap.Reconcile(ctx, oauthToken.AccessToken, target.PlatformPostID)
	if err != nil {
		// Terminal failure — includes FAILED-state and transient 5xx
		// (the platform impl collapses both into a non-nil error per
		// the Reconcile contract; retry is up to the outbox dispatcher
		// or the post_targets retry state machine).
		w.logger.Warn("publish reconcile terminal error",
			"target_id", target.ID, "publish_id", target.PlatformPostID, "error", err)
		_ = w.postRepo.UpdatePublishState(target.ID, "FAILED")
		return w.markFailedAndReturn(target, fmt.Sprintf("publish failed: %v", err))
	}
	if res == nil {
		// In-flight — no state string available (Reconcile hides it).
		// Leave the target alone; the next tick will check again.
		return false, false, nil
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
		return false, false, nil
	}

	// 5. Success transition: persist terminal publisher_state + flip
	// the target row to 'published' with publish_id-stamped URL fields.
	_ = w.postRepo.UpdatePublishState(target.ID, "PUBLISH_COMPLETE")
	target.Status = models.PostStatusPublished
	// For TikTok, PlatformMediaID == publish_id; for other async
	// providers the value is the public-facing post id returned by
	// the platform at terminal time. Either way, res.PlatformMediaID
	// is the canonical post_target.platform_post_id at completion.
	target.PlatformPostID = res.PlatformMediaID
	now := time.Now()
	target.PublishedAt = &now
	if err := w.postRepo.UpdateStatus(target); err != nil {
		return false, false, fmt.Errorf("transition to published: %w", err)
	}
	return true, false, nil
}

// markFailedAndReturn transitions the target to status='failed' and
// returns the bookkeeping so the reconciler can increment its
// counters. The (true, true, nil) return values signal "yes, this
// target was reconciled (to failed), yes it failed, no error".
func (w *ReconcileWorker) markFailedAndReturn(target *models.PostTarget, reason string) (reconciled bool, wasFailed bool, err error) {
	target.Status = models.PostStatusFailed
	target.ErrorMessage = reason
	_ = w.postRepo.UpdateStatus(target)
	return true, true, nil
}
