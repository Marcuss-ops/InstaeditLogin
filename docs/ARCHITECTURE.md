# InstaEditLogin — Architecture

## Overview

InstaEditLogin is a Go monolith with a React/Vite SPA frontend and a PostgreSQL database. It authenticates users via OAuth 2.0 against multiple social platforms and publishes content on their behalf.

## Layers

```
cmd/server/main.go          # Application entry point, wiring, graceful shutdown
cmd/seed/main.go            # Development seed command
internal/config/            # Environment configuration and validation
internal/database/          # PostgreSQL connection and migrations
internal/models/            # Domain models (user, account, post, workspace)
internal/repository/        # CRUD repositories
internal/services/          # OAuth providers, token helper, storage providers
internal/auth/              # JWT manager and middleware
internal/outbox/            # Transactional-outbox dispatcher goroutine
internal/worker/            # Background publish worker + reconciler
pkg/api/                    # HTTP router and handlers
pkg/metrics/                # Prometheus metrics
web/                        # React + Vite SPA
```

## Data Flow

1. User clicks login on a social provider.
2. Backend redirects to provider OAuth URL with a server-generated state cookie.
3. Provider redirects back to `/api/v1/auth/{provider}/callback`.
4. Backend exchanges code, fetches profile, creates/updates user and platform account.
5. Backend issues a JWT and redirects to the SPA callback.
6. SPA uses the JWT for authenticated calls to posts, accounts, workspaces.
7. Publishing creates `posts` and `post_targets`; the worker dispatches to providers.

## Async Publishing Pipeline

The publishing pipeline has TWO surfaces and TWO goroutines: a **driver** that performs the platform call (queued → publishing), and a **reconciler** that polls for the asynchronous completion of that call (publishing → published | failed). Each runs as an independent background goroutine with INDEPENDENT tick intervals — the driver defaults to 30s (`PUBLISH_WORKER_INTERVAL_SECONDS`), the reconciler to 5s (`RECONCILE_WORKER_INTERVAL_SECONDS`). Taglio 5.x split them out of the previous in-`runOnce` shape; this section documents the post-split architecture.

A THIRD background goroutine — the **outbox dispatcher** (`internal/outbox/dispatcher.go`, see [Transactional Outbox Pipeline](#transactional-outbox-pipeline)) — sits alongside the two publish-pipeline goroutines and materialises `publish_jobs` audit rows. Three goroutines in total, three independent contexts, three shutdown Done channels.

### Two goroutines, independent cadence

```
 PublishWorker.Run(ctx)   — driver:    queued → publishing
   interval = 30s default
   each tick: ListPending + per-row publishTarget

 ReconcileWorker.Run(ctx)  — reconciler: publishing → published | failed
   interval = 5s default
   each tick: ListPublishing + per-row reconcileTarget
```

Both share the same `*CredentialVault`, the same `*CapabilityRouter`, and the same `*repository.PostRepository` — production wiring (`cmd/server/main.go`) instantiates each worker from the same handles. The split is invisible to the HTTP API; the only externally observable difference vs the pre-split shape is the snappier reconciler cadence (sub-30s pickup of `publishing → published` transitions under the canonical 5s default).

### Driver: `tick()` — queued → publishing transition

The publish worker (`internal/worker/publish_worker.go::Run`) ticks every `interval` (default 30s) and on each tick calls `runOnce` → `tick`. For each `post_targets` row whose `status='queued'` AND whose parent `posts.scheduled_at <= now()`:

1. **Atomic claim** via `ClaimQueuedTarget(id)` (`UPDATE post_targets SET status='publishing' WHERE id=? AND status='queued'`). The single UPDATE uses `status='queued'` as a logical lock so 2+ worker replicas cannot double-publish. This is the verdict-§10 atomic-claim primitive; Redis-style SKIP LOCKED is not needed because each row's transition is owned by exactly one worker at a time.
2. Load parent `Post` via `FindByID`.
3. Load `PlatformAccount` via `FindPlatformAccountByID`.
4. Refresh OAuth token via `vault.Renew` (the `CredentialVault` serialises concurrent refreshes with a `pg_advisory_xact_lock`).
5. **Taglio 4.7 LEVEL 2**: stamp the deterministic `provider_idempotency_key` (`SHA-256("v1:" + post_id + ":" + account_id)[:16]`) onto the `post_targets` row so retries reuse the same key.
6. Resolve the platform's `Publisher` capability and call `Publish(ctx, token, accountUserID, payload)`, forwarding `payload.IdempotencyKey` so providers with native per-call idempotency keys (LinkedIn "X-Restli-Idempotency-Key", Twitter v2 "request_id", TikTok "idempotent" query param) drive upstream dedup; the DB-level `UNIQUE(platform_account_id, provider_idempotency_key)` constraint is the catch-all safety net.
7. **Sync platforms** (Meta, YouTube) complete the publish in the same `Publish` call → transition `status='published'`, set `PublishedAt`, set `PlatformPostID` to the final media id. The row leaves both filter sets (`queued` for the driver, `publishing` for the reconciler).
8. **Async platforms** (TikTok, Threads) return immediately with a `publish_id` → store `PlatformPostID=publish_id`, KEEP `status='publishing'`. The reconciler owns the next transition.

The driver and reconciler never both touch the same row simultaneously — **the driver owns `queued → publishing` (and the rare `publishing → failed` exits on vanished-post / missing-capability / platform-error paths), and the reconciler owns `publishing → published | failed`** under normal conditions. See [State-machine ownership](#state-machine-ownership) below for the per-transition ownership table.

### Reconciler: `tickReconcile()` — publishing → published | failed transition

The reconcile worker (`internal/worker/reconcile_worker.go::Run`) ticks every `interval` (default 5s) and on each tick calls `runOnce` → `tickReconcile`. For each `post_targets` row whose `status='publishing'` AND `platform_post_id IS NOT NULL` (`ListPublishing` query):

1. **`reconcileTarget(ctx, target)`** (`internal/worker/reconcile_worker.go`) drives the per-target state machine.
2. Load `PlatformAccount` — orphan targets (account missing) are marked `failed` so they don't loop forever.
3. **Capability lookup**: `router.AsyncPublisher(account.Platform)`. Sync platforms (no `AsyncPublisher` capability) are no-op'd: their `tick()` already completed the publish synchronously in the driver. Reconcile never touches them.
4. Refresh OAuth token via the vault. (See [Token-refresh duplication](#token-refresh-duplication-taglio-5x) for how driver + reconciler racing the same account is safe.)
5. **Delegate to `AsyncPublisher.Reconcile`** (single GET to the platform's status endpoint + transition decision). The interface contract (`internal/services/provider.go::AsyncPublisher.Reconcile`):

   | Return shape | Worker action |
   | --- | --- |
   | `(*PublishResult, nil)` | `status='published'`, `PublishedAt=now()`, `PlatformPostID=res.PlatformMediaID`. `UpdatePublishState("PUBLISH_COMPLETE")` for terminal observability. |
   | `(nil, err)` | `status='failed'`, `ErrorMessage=...`. `UpdatePublishState("FAILED")`. **Per Taglio 5.x migration**: transient 5xx errors are *terminal* here — retry is the outbox dispatcher's job at the platform-decoupled layer, NOT this reconciliation loop. (Pre-refactor: transient errors were left alone for the next tick.) |
   | `(nil, nil)` | **In-flight**: leave `status='publishing'`, no `UpdatePublishState` (no state-string exposure under `Reconcile`'s contract). Next tick retries. |
   | Defensive (Taglio 5.x): `res.PlatformMediaID==""` on success | Treated as in-flight (`false, false, nil`). Misbehaving platform impls don't silently land the row in `status=published` with `platform_post_id=""`. |

6. **Terminal-state log**: on PUBLISH_COMPLETE or FAILED, `UpdatePublishState` writes the canonical label onto `post_targets.provider_state`. On in-flight ticks, `UpdatePublishState` is intentionally NOT called — the column becomes a terminal-state log rather than a per-tick snapshot.

`tickReconcile` does NOT claim the row before reading it. That's safe because the only thing the reconciler MUTATES on a `publishing` target is `status` (terminal transitions) and `provider_state` (terminal-state log). The terminal updates are idempotent — if two reconcilers (from replica-A and replica-B) racing the same target land on it the same tick, both write the same terminal value and the second UPDATE is a no-op. No row-level lock needed at this layer.

### State-machine ownership

`post_targets.status` is the canonical lifecycle counter; each goroutine owns a non-overlapping subset of transitions. The transitions are deliberately scoped so that no two goroutines can concurrently contest the same row at the same transition:

| Transition | Owner goroutine | Atomicity / side-effects |
| --- | --- | --- |
| `queued → publishing` | `PublishWorker` (`ClaimQueuedTarget`) | DB row-level lock via `WHERE status='queued'` guard. **Verdict §10.** |
| `queued → failed` (vanished post / missing capability / platform publlish error / setKey conflict) | `PublishWorker` (`markFailed`) | Works on the row the claim already won; idempotent on the terminal update. |
| `publishing → published` | `ReconcileWorker` (`UpdateStatus`) on `AsyncPublisher.Reconcile(*PublishResult, nil)` | Idempotent terminal — second reconciler racing on the same row writes the same value, second UPDATE no-ops. |
| `publishing → failed` (terminal Reconcile error, incl. transient 5xx under the Reconcile contract) | `ReconcileWorker` (`markFailedAndReturn` via `UpdateStatus`) | Idempotent terminal — same property as above. |
| `publishing → failed` (orphan target: `platform_account` missing) | `ReconcileWorker` (`markFailedAndReturn` short-circuit before the vault/API call) | Idempotent terminal. |
| `published → …` | (none — terminal) | — |
| `failed → …` | (none — terminal) | — |

Multi-replica safety lives entirely in the row-level lock on `queued → publishing` (the only contended transition) and the idempotency of terminal updates on `publishing → {published, failed}`. The reconciler never claims the row before reading — its sole terminal UPDATE writes the same value the loser would write.

### Why `Reconcile`, not the raw `CheckPublishStatus` + state-string switch

The pre-Taglio-5.x `tickReconcile` body called `ap.CheckPublishStatus(token, publishID)` directly — a single GET returning the platform-specific state string (`PROCESSING_UPLOAD`, `PENDING_PUBLISH`, `IN_REVIEW`, `PUBLISH_COMPLETE`, `FAILED`). The worker then dispatched the state string itself.

The Taglio 5.x replacement delegates the same dispatch to `ap.Reconcile`, which wraps `CheckPublishStatus` and applies the transition-decision logic in the provider (where the platform-specific state-machine knowledge lives):

```go
func (s *TikTokOAuthService) Reconcile(ctx, accessToken, publishID) (*PublishResult, error) {
    state, err := s.CheckPublishStatus(ctx, accessToken, publishID)
    if err != nil { return nil, err }                  // transient OR FAILED-state → terminal
    switch state {
    case "PUBLISH_COMPLETE": return &PublishResult{...}, nil
    case "FAILED":          return nil, fmt.Errorf(...)
    default:                return nil, nil          // in-flight
    }
}
```

Three benefits:

1. **Worker is smaller**. The state-string switch is gone; the worker just records the operator-stable outcome (`*PublishResult`, `err`, or `(nil, nil)`).
2. **State-machine lives with the platform**. A future AsyncPublisher (Threads, Bluesky, etc.) can implement its own in-flight / terminal logic without the worker needing to know about it. The interface contract is the contract — workers and providers decouple on it.
3. **Migration is opaque to the test surface on TikTok specifically**: TikTok's `Reconcile` is a thin wrapper over `CheckPublishStatus`, so the call-by-call observable behaviour on TikTok is identical.

The trade-off is the one behavioural change flagged above: **transient 5xx now terminate the row** under `Reconcile`'s contract. The per-target retry path is owned by the post-targets retry state machine (`attempt_count`, `next_attempt_at` from migration 018) and the outbox dispatcher at the platform-decoupled layer — not this worker's tick.

### Token-refresh duplication (Taglio 5.x)

Both publish + reconcile goroutines may call `vault.Renew` on the same `account_id` per tick (driver before each `publishTarget`; reconciler before each `reconcileTarget` final transition). This is safe — the `CredentialVault` uses `pg_advisory_xact_lock` to serialise concurrent refreshes for the same account_id, so a driver-reconciler race collapses to a single round-trip (the first refresh completes; subsequent calls find the token already valid and return without work). The vault's call-count rises slightly across the two goroutines; the network / DB load stays bounded. See `internal/worker/reconcile_worker.go::reconcileTarget` step 3 for the inline callout.

### Three-way shutdown

`cmd/server/main.go` spawns all three background goroutines in parallel at startup and shuts them down **sequentially** on SIGINT/SIGTERM. Each goroutine has its own cancellable context + `Done` channel; the cancels go out as a single broadcast on the signal, then the awaits are stacked (each with its own 15s budget), followed by the HTTP server's own 30s drain:

```
go publishWorker.Run(workerCtx)        // [1] driver    — 30s tick interval
go reconcileWorker.Run(reconcileCtx)    // [2] reconciler — 5s tick interval
go dispatcher.Run(dispatcherCtx)        // [3] outbox    — SKIP LOCKED + 60s lease

<-quit (SIGINT/SIGTERM)
workerCancel(); reconcileCancel(); dispatcherCancel()            // single broadcast

select { <-workerDone,    15s }          // drain budget [1] (driver)
select { <-reconcileDone, 15s }          // drain budget [2] (reconciler)
select { <-dispatcherDone, 15s }         // drain budget [3] (outbox dispatcher)

srv.Shutdown(ctx) with 30s budget       // HTTP server drain — runs AFTER goroutine drains
```

Each goroutine performs a graceful drain on its own context: when `ctx.Done()` fires while a tick is mid-flight, the current tick completes naturally and `Run` returns only after that. A slow shutdown on one goroutine (e.g. a hung platform call in the reconciler) does NOT block the other two — each `Done` channel is independent, so the corresponding `select` returns via the timeout path while the healthy ones drain as they go.

Wall-clock bounds on shutdown:

- **Graceful drain** (default path): ms-level per goroutine. On a clean SIGTERM each goroutine returns within ms of the cancel broadcast and the three `Done` channels close at sub-second timescales. The HTTP server's 30s drain then begins.
- **Hard hangs** (e.g. platform API stuck on one tick, or a goroutine ignoring `ctx.Done()`): each governance budget fires sequentially. The stacked `<-time.After(15s)` design caps the **goroutine-drain** window at `3 × 15s = 45s` before the operator logs "drain timeout, continuing shutdown" for the still-pending goroutine. After the goroutines settle (clean or timed-out), `srv.Shutdown(30s)` kicks off another 30s budget for the HTTP server. Total worst-case wall-clock: `45s (goroutines) + 30s (HTTP) = up to 75s`.

The goroutine-drain stack and the HTTP-server drain are **sequential, not concurrent** — this matches the production wiring in `cmd/server/main.go::main` (the three `<-XxxDone` selects come before `srv.Shutdown(ctx)` in the source order). Operators tuning the shutdown budgets should bound total shutdown at the worst case (`75s`) plus any operator-imposed `kill -9` wait time.

### Cross-references

- **Driver code**: `internal/worker/publish_worker.go::Run`, `::runOnce`, `::tick`, `::publishTarget`. No longer owns `tickReconcile` / `reconcileTarget` — those moved to `reconcile_worker.go` at Taglio 5.x. The interface `PublisherPostStore` was slimmed to drop `ListPublishing` + `UpdatePublishState` (the reconciler's surface).
- **Reconciler code**: `internal/worker/reconcile_worker.go::Run`, `::runOnce`, `::tickReconcile`, `::reconcileTarget`, `::markFailedAndReturn`. Constructed via `NewReconcileWorker(postRepo, userRepo, router, vault, interval, logger)` — same shape as `NewPublishWorker` but with `cfg.ReconcileWorkerIntervalSeconds` (default 5s). The `ReconcilePostStore` interface is a strict subset of `PublisherPostStore` (3 method surface: `ListPublishing`, `UpdateStatus`, `UpdatePublishState`).
- **Interface contract**: `internal/services/provider.go::AsyncPublisher` — defines `StartPublish`, `CheckPublishStatus`, `ContinuePublish`, `Reconcile`. The `Reconcile` contract documentation is inline; the comment block above the interface spells out the three return-shape outcomes.
- **Implementation reference**: `internal/services/tiktok_oauth.go::Reconcile` — concrete TikTok implementation; demonstrates the canonical wrapper pattern (`CheckPublishStatus` + state-string dispatch). The defensive empty-`PlatformMediaID` guard (treat as in-flight) was added by commit `8eb29bb` per the review-pass HIGH-2.
- **Tests**:
  - **Driver tests** (`internal/worker/publish_worker_test.go`): `TestPublishTarget_*` (10 tests covering claim, find, set-key, publish, failed-exit, claim-loss, ordering, error paths); `TestRunOnce_TickOnly` + `TestRunOnce_TickOnly_AsyncPlatform_NoReconcile` (assert the driver NEVER reaches `CheckPublishStatus` / `Reconcile` after the Taglio 5.x split); `TestComputeProviderIdempotencyKey_*` (deterministic-key unit tests).
  - **Reconciler tests** (`internal/worker/reconcile_worker_test.go`): `TestReconcileTarget_*` (6 tests covering PublishComplete, Failed, InFlight, SyncPlatform, OrphanAccount, TransientError); `TestTickReconcile_*` (3 tests covering iterates-all / empty-list / list-error); `TestReconcileWorker_Run_*` (2 Run-loop tests: `TicksAndExitsOnCtxCancel` + `GracefulShutdown_DrainsInFlight`, mirroring the outbox dispatcher's Run test shape).
  - The transient-error behavioural change under `Reconcile`'s contract is asserted by `TestReconcileTarget_TransientError_TerminalFailure`.
- **Configuration**: `internal/config/config.go::PublishWorkerIntervalSeconds` (default 30) + `::ReconcileWorkerIntervalSeconds` (default 5). Environment variables: `PUBLISH_WORKER_INTERVAL_SECONDS`, `RECONCILE_WORKER_INTERVAL_SECONDS`. Both fall back to defaults on ≤0 inside their respective `NewXxxWorker` constructors (defensive constructor logic, not config-validation logic — operators can simply leave env unset to get the canonical defaults).
- **Two-goroutine split commit** (`ca7c879`, Taglio 5.x): extracted `tickReconcile` / `reconcileTarget` / `markFailedAndReturn` from `PublishWorker` into a new `ReconcileWorker` struct with its own `Run` goroutine, mirroring the outbox dispatcher. Verified via `git show --stat ca7c879` (touches `internal/worker/reconcile_worker.go` + `reconcile_worker_test.go` + `mocks_test.go` + slims `publish_worker.go` + `publish_worker_test.go` + adds `cfg.ReconcileWorkerIntervalSeconds` + wires 3-way shutdown in `cmd/server/main.go`).

## Transactional Outbox Pipeline

**Cross-reference: `internal/outbox/dispatcher.go`, `internal/outbox/processors/publishjobs.go`, `cmd/server/main.go`.**

`PostRepository.Create` writes `posts + post_targets + outbox_events` in one `BEGIN/COMMIT` tx. A background goroutine (`outbox.NewDispatcher`) reads `outbox_events` via `SELECT FOR UPDATE SKIP LOCKED` + heartbeat lease, then calls `processors.NewPublishJobsMaterialiser` to insert the audit row. Both run parallel to the publish worker with independent 15s drain budgets on shutdown. The PublishJob table is the audit-only appendix; `post_targets.status` remains the source of truth for current publish state.

## Security

- Tokens are encrypted at rest with AES-256-GCM.
- JWT is signed with HS256 and validated by middleware.
- OAuth state is stored in an HttpOnly, Secure, SameSite=Lax cookie.
- Strict JWT auth is enforced in production.
