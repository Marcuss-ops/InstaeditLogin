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

The publishing pipeline has two distinct surfaces — a **driver** that performs the platform call, and a **reconciler** that polls for the asynchronous completion of that call. Both run on the publish worker's `Run` goroutine and share the same interval. This section describes the whats, the whys, and the cross-references.

### Driver: `tick()` — queued → publishing transition

The publish worker (`internal/worker/publish_worker.go::Run`) ticks every `interval` (default 30s) and calls `runOnce`, which executes two phases in order:

1. **`tick()`** — for each `post_targets` row whose `status='queued'` AND whose parent `posts.scheduled_at <= now()`:
   1. **Atomic claim** via `ClaimQueuedTarget(id)` (`UPDATE post_targets SET status='publishing' WHERE id=? AND status='queued'`). The single UPDATE uses `status='queued'` as a logical lock so 2+ worker replicas cannot double-publish.
   2. Load parent `Post` via `FindByID`.
   3. Load `PlatformAccount` via `FindPlatformAccountByID`.
   4. Refresh OAuth token via `vault.Renew` (the `CredentialVault` serialises concurrent refreshes with a `pg_advisory_xact_lock`).
   5. Taglio 4.7 LEVEL 2: stamp the deterministic `provider_idempotency_key` (`SHA-256("v1:" + post_id + ":" + account_id)[:16]`).
   6. Resolve the platform's `Publisher` capability and call `Publish(ctx, token, accountUserID, payload)`.
   7. **Sync platforms** (Meta, YouTube) complete the publish in the same call → transition `status='published'`, set `PublishedAt`, set `PlatformPostID` to the final media id.
   8. **Async platforms** (TikTok, Threads) return immediately with a `publish_id` → store `PlatformPostID=publish_id`, KEEP `status='publishing'`, leave transition to the reconciler. The outbox dispatcher also writes a `publish_jobs` audit row alongside the post + outbox_events inside the same `PostRepository.Create` tx — see [Transactional Outbox Pipeline](#transactional-outbox-pipeline) below.

### Reconciler: `tickReconcile()` — publishing → published | failed transition

`runOnce` invokes `tickReconcile()` AFTER `tick()`: for each `post_targets` row whose `status='publishing'` AND `platform_post_id IS NOT NULL` (`ListPublishing` query):

1. **`reconcileTarget(ctx, target)`** (`internal/worker/publish_worker.go`) drives the per-target state machine.
2. Load `PlatformAccount` — orphan targets (account missing) are marked `failed`.
3. **Capability lookup**: `router.AsyncPublisher(account.Platform)`. Sync platforms (no `AsyncPublisher` capability) are no-op'd: their `tick()` already completed the publish synchronously.
4. Refresh OAuth token via the vault.
5. **Delegate to `AsyncPublisher.Reconcile`** (single GET to the platform's status endpoint + transition decision). The interface contract (`internal/services/provider.go::AsyncPublisher.Reconcile`):

   | Return shape | Worker action |
   | --- | --- |
   | `(*PublishResult, nil)` | `status='published'`, `PublishedAt=now()`, `PlatformPostID=res.PlatformMediaID`. `UpdatePublishState("PUBLISH_COMPLETE")` for terminal observability. |
   | `(nil, err)` | `status='failed'`, `ErrorMessage=...`. `UpdatePublishState("FAILED")`. **Per Taglio 5.x migration**: transient 5xx errors are *terminal* here — retry is the outbox dispatcher's job at the platform-decoupled layer, NOT this reconciliation loop. (Pre-refactor: transient errors were left alone for the next tick.) |
   | `(nil, nil)` | **In-flight**: leave `status='publishing'`, no `UpdatePublishState` (no state-string exposure under `Reconcile`'s contract). Next tick retries. |
   | Defensive (Taglio 5.x): `res.PlatformMediaID==""` on success | Treated as in-flight (`false, false, nil`). Misbehaving platform impls don't silently land the row in `status=published` with `platform_post_id=""`. |

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

### Why `tickReconcile` runs in-band with `tick()` today

A single goroutine (`PublishWorker.Run`) executes both phases in sequence on the same interval. Three reasons:

1. **Sequential, predictable ordering**. Rows that `tick()` JUST transitioned to `publishing` (with a fresh `platform_post_id` from the platform's `StartPublish`/`Publish` response) become visible to `tickReconcile()` in the same `runOnce` sweep — a brand-new async publish starts being polled immediately, not on the next interval (sub-30s pickup).
2. **Resource / DB-load simplicity**. One goroutine vs two halves the lock contention on `pg_advisory_xact_lock` (vault refresh per-target), halves the per-target OAuth refresh fan-out, and keeps the canonical metrics column `tick counter` single-source-of-truth.
3. **Independent cadence is a future-extraction**. The reconciler CAN evolve into a separate `ReconcileWorker.Run` goroutine with its own tick interval (e.g. 5s, decoupled from the publish driver's 30s) without changing the underlying contract. The dispatch path from `tick()` → outbox `OutboxEvent` → dispatcher `ClaimNext` → materialiser is independent enough that the reconciler could even transition to "subscribe to outbox_events tagged `publish_job_completed`" for event-driven reconcile. Today's in-band shape is the simpler v1.

### Cross-references

- **Worker code**: `internal/worker/publish_worker.go::Run`, `::runOnce`, `::tick`, `::tickReconcile`, `::reconcileTarget`. The Taglio 5.x migration is the commit `183b0e2` (refactor to `Reconcile`) and `8eb29bb` (defensive `PlatformMediaID` guard).
- **Interface contract**: `internal/services/provider.go::AsyncPublisher` — defines `StartPublish`, `CheckPublishStatus`, `ContinuePublish`, `Reconcile`. The `Reconcile` contract documentation is inline; the comment block above the interface spells out the three return-shape outcomes.
- **Implementation reference**: `internal/services/tiktok_oauth.go::Reconcile` — concrete TikTok implementation; demonstrates the canonical wrapper pattern (`CheckPublishStatus` + state-string dispatch).
- **Tests**: `internal/worker/publish_worker_test.go::TestReconcileTarget_*` (six reconciler tests) + `::TestTickReconcile_*` + `::TestRunOnce_BothTicksAndReconcile`. The transient-error behavioural change is asserted by `TestReconcileTarget_TransientError_TerminalFailure`.

## Transactional Outbox Pipeline

**Cross-reference: `internal/outbox/dispatcher.go`, `internal/outbox/processors/publishjobs.go`, `cmd/server/main.go`.**

`PostRepository.Create` writes `posts + post_targets + outbox_events` in one `BEGIN/COMMIT` tx. A background goroutine (`outbox.NewDispatcher`) reads `outbox_events` via `SELECT FOR UPDATE SKIP LOCKED` + heartbeat lease, then calls `processors.NewPublishJobsMaterialiser` to insert the audit row. Both run parallel to the publish worker with independent 15s drain budgets on shutdown. The PublishJob table is the audit-only appendix; `post_targets.status` remains the source of truth for current publish state.

## Security

- Tokens are encrypted at rest with AES-256-GCM.
- JWT is signed with HS256 and validated by middleware.
- OAuth state is stored in an HttpOnly, Secure, SameSite=Lax cookie.
- Strict JWT auth is enforced in production.
