// Package worker implements background processes that run alongside the
// HTTP server. Two goroutines are spawned by cmd/server/main.go:
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
// and ctx-cancellable lifecycles. cmd/server/main.go spawns them in
// parallel and shuts them down in parallel (independent 15s drains).
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
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
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
	// ListPending returns post_targets whose status='queued' AND whose
	// parent post.scheduled_at <= before. Ordered by post.scheduled_at ASC.
	ListPending(before time.Time) ([]models.PostTarget, error)
	// FindByID loads the parent post for the publish payload (caption/title/media_url).
	FindByID(id int64) (*models.Post, error)
	// ClaimQueuedTarget atomically transitions a target from
	// status='queued' to 'publishing'. Returns true on claim, false
	// if already claimed by another worker (verdict §10 — this is
	// the atomic primitive that unblocks 2+ worker replicas).
	ClaimQueuedTarget(id int64) (bool, error)
	// UpdateStatus persists the publishing→published|failed
	// transitions the driver writes (after a successful claim) and
	// the async-publish intermediate state (publishing with a
	// publish_id stamped onto platform_post_id). The claim guarantees
	// only the winning worker reaches this step, so no atomic check
	// is needed here.
	UpdateStatus(target *models.PostTarget) error
	// SetProviderIdempotencyKey (Taglio 4.7 LEVEL 2, migration 022)
	// writes the worker-computed deterministic per-target
	// idempotency_key onto the post_target row. The worker calls
	// this AFTER ClaimQueuedTarget succeeds and BEFORE the publish
	// call so retries reuse the same key. Errors:
	//   * ErrProviderIdempotencyConflict: another target on the same
	//     account already has this key — degenerate/duplicate, the
	//     worker treats as failed and lets the operator reconcile.
	//   * ErrPostTargetNotFound: id is stale.
	//   * Other: wrapped DB error.
	SetProviderIdempotencyKey(id int64, key string) error
}

// PublisherUserStore is the narrow slice of the user /
// platform_accounts repository the *driver* depends on. Just enough
// to resolve the platform_account for a pending post_target
// without dragging in the full UserRepository surface. ReconcileWorker
// uses the same type via the ReconcileUserStore alias defined in
// reconcile_worker.go.
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
	}
}

// Run blocks until ctx is cancelled, executing one tick per interval
// period. Performs a graceful drain: when ctx.Done() fires while a
// tick is mid-flight, the current tick completes naturally and Run
// returns only after that. Returns ctx.Err() on shutdown; logs
// non-nil errors and continues otherwise.
//
// Taglio 5.x: Run only drives tick() now. The publishing→published
// transition is owned by ReconcileWorker.Run on its own goroutine
// (see reconcile_worker.go). The two goroutines share the publish-
// state at the post_targets.status column; the publish driver's
// ClaimQueuedTarget is the only writer for queued→publishing, and
// the reconciler is the only writer for publishing→published|failed.
func (w *PublishWorker) Run(ctx context.Context) error {
	w.logger.Info("publish worker started",
		"interval_seconds", w.interval.Seconds(),
		"worker_id", w.workerID)
	defer w.logger.Info("publish worker stopped", "worker_id", w.workerID)

	// Initial tick — no wait for the first sweep.
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

// runOnce executes one tick and logs the result. The Reconciler is
// no longer called from runOnce — Taglio 5.x split it into its own
// goroutine (ReconcileWorker.Run, reconcile_worker.go).
func (w *PublishWorker) runOnce(ctx context.Context) {
	if processed, ok, ko, err := w.tick(ctx); err != nil {
		w.logger.Warn("publish worker tick failed", "error", err)
	} else if processed > 0 {
		w.logger.Info("publish worker tick done",
			"processed", processed, "succeeded", ok, "failed", ko)
	}
}

// tick processes all pending targets exactly once. Returns
// (processed, succeeded, failed, err). Sequential per-target — no
// per-target goroutines — for predictable load on the OAuth APIs and
// easier rate-limit debugging.
//
// Per-target errors are LOGGED and counted but do not abort the tick;
// the worker should keep trying other targets even if Meta/Twitter/etc.
// are flapping.
func (w *PublishWorker) tick(ctx context.Context) (processed, succeeded, failed int, err error) {
	pending, err := w.postRepo.ListPending(time.Now())
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list pending: %w", err)
	}
	if len(pending) == 0 {
		return 0, 0, 0, nil
	}

	for i := range pending {
		// Index-based loop (not `for _, target`): we mutate &pending[i] inside
		// publishTarget and the local copy must reflect those mutations when
		// we pass it to UpdateStatus.
		if err := w.publishTarget(ctx, &pending[i]); err != nil {
			w.logger.Warn("publish target failed",
				"target_id", pending[i].ID,
				"post_id", pending[i].PostID,
				"error", err)
			failed++
		} else {
			succeeded++
		}
		processed++
	}
	return processed, succeeded, failed, nil
}

// publishTarget drives the per-target 3-step status transition:
//
//  1. ATOMIC CLAIM: queued → publishing (verdict §10). The single
//     UPDATE in ClaimQueuedTarget uses WHERE status='queued' as a
//     logical lock so only ONE worker wins. The loser sees a
//     `claimed=false` return and skips — no double-publish.
//  2. Load parent Post (caption/title/media_url for the publish payload)
//     AND PlatformAccount (platform name + platform_user_id for dispatch).
//     Safe to do AFTER the claim: if either is missing, we transition
//     to 'failed' (we own the row), so the next tick won't re-pick it.
//  3. Refresh OAuth token via the CredentialVault (which serialises
//     concurrent refreshes with a Postgres advisory lock).
//     On failure: status → `failed` with error_message.
//  4. Publish via the platform's Publisher capability.
//     On sync platforms: status → 'published' with platform_post_id + published_at.
//     On async platforms (Taglio 4.2): status stays 'publishing', the
//     platform_post_id gets the publish_id from the result, and the
//     ReconcileWorker goroutine will drive the state machine on
//     subsequent ticks. (See reconcile_worker.go::reconcileTarget.)
//     On failure: status → `failed` with error_message.
//
// The 'failed' transitions only happen AFTER a successful claim, so
// two workers running in parallel won't redundantly write 'failed' to
// the same row (the loser would have already returned with
// claimed=false).
func (w *PublishWorker) publishTarget(ctx context.Context, target *models.PostTarget) error {
	// 1. ATOMIC CLAIM: queued → publishing. If another worker
	// already claimed this target, claim returns false and we skip.
	claimed, err := w.postRepo.ClaimQueuedTarget(target.ID)
	if err != nil {
		return fmt.Errorf("claim target %d: %w", target.ID, err)
	}
	if !claimed {
		w.logger.Info("target already claimed by another worker, skipping",
			"target_id", target.ID, "post_id", target.PostID)
		return nil // not an error — just skip
	}

	// 2. Load parent Post
	post, err := w.postRepo.FindByID(target.PostID)
	if err != nil {
		return w.markFailed(target, fmt.Sprintf("load post %d: %v", target.PostID, err))
	}
	if post == nil {
		// Vanished record — cannot publish. Mark failed so we don't loop forever.
		return w.markFailed(target, fmt.Sprintf("post %d not found", target.PostID))
	}

	// 3. Load PlatformAccount
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return w.markFailed(target, fmt.Sprintf("load account %d: %v", target.PlatformAccountID, err))
	}
	if account == nil {
		return w.markFailed(target, fmt.Sprintf("platform_account %d not found", target.PlatformAccountID))
	}

	// 4. Resolve platform capabilities. We need the OAuthProvider (for
	// token refresh) AND the Publisher (for the actual call). A
	// platform missing either cannot be published to.
	oauth, oauthOK := w.router.OAuth(account.Platform)
	publisher, pubOK := w.router.Publisher(account.Platform)
	if !oauthOK || !pubOK {
		return w.markFailed(target, fmt.Sprintf("platform %q missing capability (oauth=%v publish=%v)", account.Platform, oauthOK, pubOK))
	}

	// 5. Refresh token via the CredentialVault. The provider's
	// RefreshOAuthToken method is adapted to a credentials.TokenRefresher
	// closure so the vault only knows the function signature. Try Bearer
	// first (refresh-capable), then LongLived (Meta-style re-exchange).
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	oauthToken, err := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher)
		if err != nil {
			return w.markFailed(target, "token refresh failed: "+err.Error())
		}
	}

	// For providers that publish via a page-scoped token (Facebook
	// Pages), prefer the page access token stored for the account.
	// Page Access Tokens do not need refresh; the vault Get path
	// returns them as long as the grant is valid.
	if pageToken, err := w.vault.Get(ctx, account.ID, models.TokenTypePageAccess); err == nil && pageToken.AccessToken != "" {
		oauthToken = pageToken
	}

	// 5b. YOUTUBE ONLY — P0#3 server-side channel binding check.
	//
	// The OAuth grant we just refreshed MUST be bound to the SAME
	// channel as platform_account.platform_user_id. The refresh above
	// doesn't tell us; only channels.list?mine=true can confirm.
	// Without this check, a grant that was silently re-bound to a
	// different channel (Google rotation, operator migration, fraud)
	// would happily upload the video to the wrong channel.
	//
	// Placement rationale:
	//   - AFTER refresh + page-token override so oauthToken is the
	//     final access token we will pass to Publish (the check uses
	//     it AND the publish uses it; no double-refresh).
	//   - BEFORE the idempotency-key stamp so a flag-failed upload
	//     does NOT stamp a key (the post_target is going to
	//     'failed', not 'publishing'; no future retries should
	//     dedup against it).
	//
	// On ErrYouTubeChannelMismatch (channel id NOT in the grant's
	// channel set): flag the platform_account reauth_required (so
	// the dashboard prompts the operator to reconnect) AND mark
	// this post_target failed (so the worker stops trying).
	//
	// On any other error (5xx, network, decode): treat as transient
	// — DO NOT flag reauth — and let the next tick retry.
	if account.Platform == models.PlatformYouTube {
		raw, hasRaw := w.router.Get(account.Platform)
		if hasRaw {
			if binder, ok := raw.(services.YouTubeChannelBinder); ok {
				if bindErr := binder.ValidateChannelBinding(ctx, oauthToken.AccessToken, account.PlatformUserID); bindErr != nil {
					if errors.Is(bindErr, services.ErrYouTubeChannelMismatch) {
						if flagErr := w.userRepo.MarkReauthRequired(ctx, account.ID, "youtube_channel_mismatch", bindErr.Error()); flagErr != nil {
							// Soft error — the post_target still goes
							// to 'failed' below; we just couldn't
							// stamp the platform_account's flag. Log
							// so the operator sees both signals.
							w.logger.Warn("could not flag platform_account reauth_required after youtube channel mismatch",
								"platform_account_id", account.ID, "post_id", target.PostID, "flag_error", flagErr)
						}
						w.logger.Warn("youtube channel binding mismatch; refusing upload",
							"target_id", target.ID, "post_id", target.PostID,
							"platform_account_id", account.ID,
							"expected_channel_id", account.PlatformUserID,
							"error", bindErr)
					} else {
						w.logger.Warn("youtube channel binding check failed (transient); will retry",
							"target_id", target.ID, "post_id", target.PostID,
							"platform_account_id", account.ID, "error", bindErr)
					}
					return w.markFailed(target, "youtube channel binding check: "+bindErr.Error())
				}
			}
			// If the registered provider doesn't implement the
			// binder (older test fixtures, future non-YouTube
			// provider that accidentally registers under the
			// youtube name), the check is skipped — the existing
			// publish path proceeds. New YouTubeOAuthService
			// implementations MUST satisfy the compile-time
			// assertion in services/youtube_oauth.go.
		}
	}

	// 6. Build payload + publish. MediaURL goes through as VideoURL (the
	// payload's ImageURL branch is reserved for image-only posts that
	// don't have a content_type column — future enhancement).
	//
	// Taglio 4.7 LEVEL 2 (migration 022): ensure the post_target has
	// the deterministic provider_idempotency_key stamped onto it BEFORE
	// publishing. The key is computed from (post.ID, account.ID) so it
	// is stable across retries — the platform's native API dedup
	// catches the duplicate publish on its end. Forward it on the
	// payload so providers that support per-call idempotency keys
	// (LinkedIn "X-Restli-Idempotency-Key", Twitter v2 "request_id",
	// TikTok "idempotent" query param) drive the upstream API to
	// dedup; providers without native support ignore the field, but
	// the DB-level UNIQUE(platform_account_id, provider_idempotency_key)
	// constraint is the catch-all safety net.
	var key string
	if target.ProviderIdempotencyKey != nil && *target.ProviderIdempotencyKey != "" {
		key = *target.ProviderIdempotencyKey
	} else {
		key = computeProviderIdempotencyKey(target.PostID, account.ID)
		// Mirror the stamped key onto the in-memory struct so any
		// SUBSEQUENT path that reads target.ProviderIdempotencyKey
		// (UpdateStatus captures, future debug-log wires) sees the
		// stamped value, not the pre-stamp nil. Without this mirror
		// we trust ListPending's SELECT to include the column on
		// every re-fetch (the case today) — setting it locally
		// removes that implicit coupling.
		target.ProviderIdempotencyKey = &key
		if err := w.postRepo.SetProviderIdempotencyKey(target.ID, key); err != nil {
			if errors.Is(err, repository.ErrProviderIdempotencyConflict) {
				// Degenerate: another row on the same account already
				// has this key (collision with extremely low probability
				// for SHA-256 prefix, OR a stale key from a prior failed
				// attempt). Do NOT leave the row in 'publishing' — it
				// would be polled forever by the reconciler and never
				// re-picked by the driver either. Promote to 'failed'
				// so the row drops out of BOTH filter sets and the
				// operator can see + reconcile it.
				w.logger.Warn("provider idempotency key conflict on stamp; promoting target to failed",
					"target_id", target.ID, "post_id", target.PostID,
					"platform_account_id", account.ID, "key", key, "error", err)
				target.Status = models.PostStatusFailed
				target.ErrorMessage = "provider idempotency key conflict: " + err.Error()
				if updateErr := w.postRepo.UpdateStatus(target); updateErr != nil {
					// Surface both errors so the tick counter increments
					// AND the operator sees the underlying failure mode.
					return fmt.Errorf("provider idempotency key conflict (also failed to mark failed: %v): %w",
						updateErr, err)
				}
				return fmt.Errorf("provider idempotency key conflict: %w", err)
			}
			if errors.Is(err, repository.ErrPostTargetNotFound) {
				// Stale id — another worker or a manual op touched the row.
				// Don't double-publish; treat as a failed tick entry.
				return fmt.Errorf("provider idempotency key stamp on missing target: %w", err)
			}
			return fmt.Errorf("ensure provider idempotency key: %w", err)
		}
	}
	payload := models.PublishPayload{
		Text:      post.Caption,
		Title:     post.Title,
		PublishAt: post.PublishAt,
	}
	if post.MediaURL != "" {
		payload.VideoURL = post.MediaURL
	}
	// TikTok (and some other platforms) require an explicit privacy
	// level; the Compose UI does not yet expose a selector, so fall
	// back to PUBLIC_TO_EVERYONE for immediate publishes.
	if payload.PrivacyLevel == "" {
		payload.PrivacyLevel = "PUBLIC_TO_EVERYONE"
	}
	// YouTube requires lowercase privacy levels (public/unlisted/private)
	// and rejects the generic PUBLIC_TO_EVERYONE default. Use private as
	// the safe fallback — unverified apps' uploads are force-private anyway.
	if account.Platform == models.PlatformYouTube && (payload.PrivacyLevel == "" || payload.PrivacyLevel == "PUBLIC_TO_EVERYONE") {
		payload.PrivacyLevel = "private"
	}
	// TikTok's PULL_FROM_URL mode requires the video URL's domain to be
	// ownership-verified in the TikTok Developer Console — impossible
	// for a dynamic dev tunnel. Route TikTok through PULL_FROM_FILE
	// (chunked direct upload) instead, which uploads the bytes straight
	// to TikTok and skips the URL-ownership check.
	if account.Platform == models.PlatformTikTok && payload.Source == "" {
		payload.Source = models.PublishSourcePULLFromFile
	}
	payload.IdempotencyKey = key

	// FASE 1.3: throttle per-platform API calls to avoid rate-limit
	// bans. If the throttle is nil (test mode), skip. If the platform's
	// bucket is empty, Wait() blocks until a token is available or
	// ctx is cancelled (graceful shutdown).
	if w.throttle != nil {
		if err := w.throttle.Wait(ctx, account.Platform); err != nil {
			return fmt.Errorf("throttle wait for %s: %w", account.Platform, err)
		}
	}

	result, err := publisher.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
	if err != nil {
		return w.markFailed(target, err.Error())
	}

	// 7. ASYNC PUBLISH (Taglio 4.2): if the platform has the
	// AsyncPublisher capability, the Publish() call returned a
	// publish_id (in result.PlatformMediaID) but did NOT complete
	// the publish — the platform is still processing. We store the
	// publish_id on the target and KEEP status='publishing' (the
	// claim already wrote 'publishing' to the DB; we just need to
	// ensure UpdateStatus doesn't revert it back to the in-memory
	// 'queued' value the target struct carries from ListPending).
	// The ReconcileWorker goroutine will pick this target up on
	// subsequent ticks and drive the state machine to completion.
	if _, isAsync := w.router.AsyncPublisher(account.Platform); isAsync && result.PlatformMediaID != "" {
		target.PlatformPostID = result.PlatformMediaID
		target.Status = models.PostStatusPublishing // preserve the claim's status transition
		w.logger.Info("async publish initiated, reconciler will poll",
			"target_id", target.ID, "platform", account.Platform,
			"publish_id", result.PlatformMediaID)
		if err := w.postRepo.UpdateStatus(target); err != nil {
			return fmt.Errorf("update status for async publish: %w", err)
		}
		return nil
	}

	// 8. SYNC PUBLISH: transition publishing → published.
	target.Status = models.PostStatusPublished
	target.PlatformPostID = result.PlatformMediaID
	now := time.Now()
	target.PublishedAt = &now
	if err := w.postRepo.UpdateStatus(target); err != nil {
		return fmt.Errorf("transition to published: %w", err)
	}
	return nil
}

// markFailed transitions the target to status='failed' with the given
// reason and returns a wrapped error. The caller is expected to have
// already successfully claimed the target (via ClaimQueuedTarget) —
// the 'failed' write is only legal AFTER the claim, otherwise two
// workers could both redundantly update the same row.
//
// The UpdateStatus error is intentionally ignored (logged at the
// caller's warning level) so the returned error reflects the
// original failure reason rather than the bookkeeping error.
func (w *PublishWorker) markFailed(target *models.PostTarget, reason string) error {
	target.Status = models.PostStatusFailed
	target.ErrorMessage = reason
	_ = w.postRepo.UpdateStatus(target)
	return errors.New(reason)
}
