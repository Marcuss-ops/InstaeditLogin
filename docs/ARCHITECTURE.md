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

## Frontend (web/) — public pages and the AI Compose scope

The React + Vite SPA in `web/` serves three classes of routes. The status of "AI Compose" — the marketing surface that promises stock-footage curation, SFX selection, AI image placement, auto-crop, auto-hashtags, and per-platform adaptation — differs sharply from what is actually production today. This section makes the truthful status of each claim explicit so investors and new collaborators do not conflate copy with capability.

### Status legend (used throughout this section)

| Marker             | Meaning                                                                  |
|--------------------|--------------------------------------------------------------------------|
| **Operative**      | In production code, covered by tests                                     |
| **UI prototype**   | Rendered in the SPA, no production backend; pure visual proof            |
| **Roadmap**        | Listed in copy / design docs but NOT engineered                          |

### Routes at a glance

| Path                              | Auth          | Owner component                              | Purpose                                          | Status                  |
|-----------------------------------|---------------|----------------------------------------------|--------------------------------------------------|-------------------------|
| `/`                               | public        | `web/src/pages/Landing.tsx`                  | Marketing landing                                | Operative (UI shipped)  |
| `/editor`                         | public        | `web/src/pages/Editor.tsx`                   | AI Compose showcase page                         | **UI prototype**        |
| `/login`                          | public        | `web/src/pages/Login.tsx`                    | Email + magic-link auth                          | Operative               |
| `/privacy`, `/terms`, `/data-deletion.html` | public | `web/src/pages/{PrivacyPolicy,TermsOfService}.tsx` | Legal pages                              | Operative               |
| `/app/compose`                    | JWT-required  | `web/src/pages/internal/Compose.tsx`         | Auth-protected composer (real upload + publish)  | Operative               |
| `/app/*`                          | JWT-required  | `web/src/pages/internal/*`                   | Dashboard, accounts, posts, linking              | Operative               |

The actual *publishing* work — caption/title/media_url → 1-of-N platform APIs via the Postgres-backed publish pipeline — lives at `/app/compose` and is driven server-side by `internal/worker/publish_worker.go` (driver) + `internal/worker/reconcile_worker.go` (terminal-update reconciler). The page at `/editor` does NOT exercise this path; it is a marketing demonstrator and not a working editor.

### `/editor` claim → status (proof-of-positioning only)

The `/editor` page (`web/src/pages/Editor.tsx`) is wholly client-side rendering. Every visible element is a hardcoded constant or decorative component — there is no `fetch`, no backend call, no AI API call. The page is intentional copy-driven proof of the product positioning; readers should not mistake it for a working editor.

| Capability claimed in `/editor` copy                                                          | Status          | Evidence in `web/src/pages/Editor.tsx`                                                                                                                                                                                                                                                                            |
|------------------------------------------------------------------------------------------------|-----------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `9:16` short-form renders (YouTube Shorts, Instagram Reels, TikTok)                              | **UI prototype**| `SHORT_DEMOS` (top of file) is a 2-element array of hardcoded YouTube `id` strings (`MVwXsmRLnwM`, `XCIWzK2BuRo`); each is rendered as an `<iframe>` at `aspect-[9/16]`. Static URLs — no algorithm picks stock footage or SFX                                                |
| `16:9` long-form renders (YouTube, Facebook, Instagram, LinkedIn)                                | **UI prototype**| `LONGFORM_DEMOS` is a 4-element array of hardcoded YouTube `id` strings (`fLhv7d6N_3c`, `iA1WT69NFbw`, `R18AVWQ92fs`, `lpKX9SKqSMw`); each rendered at `aspect-[16/9]`. Same model — proof of pixel, not proof of pipeline                                          |
| "Engine researches the best stock footage and SFX"                                              | **Roadmap**     | No production code path generates a video from a brief anywhere in the repo (`web/`, `internal/services/`, `internal/worker/` are all search-tested clean for stock-footage / SFX API references)                                                                                |
| "Generates supporting AI images"                                                                | **Roadmap**     | No image-generation API call exists in the codebase                                                                                                                                                                                                                                                              |
| "Placed at exactly the right moment"                                                            | **Roadmap**     | No narration-alignment or beat-detection logic. The per-platform `OutputCard` (renders `PLATFORM_REGISTRY`) reads from a hardcoded `sample` object with literal `format` strings ("9:16 · Reels", "1.91:1 · Post", etc.) — no engine picks the per-clip moment                          |
| "Human-in-the-loop — After Effects-level quality in minutes"                                    | **Roadmap**     | `web/src/pages/internal/Compose.tsx` exposes a real single-upload UI on top of the publish worker, but there is no per-cut creative review, no AI grading pass, no automatic scene-split                                                                                                                                  |
| One render → 7 native posts (per-platform aspect/format adaptation)                            | **Operative (backend only)** | The platform-aware dispatch (`CapabilityRouter`, `AsyncPublisher` capability) is implemented in `internal/services/provider.go` and exercised by `PublishWorker.publishTarget`. The publish worker correctly routes each `post_targets` row to its platform's native API without a creative pass — but the `/editor` UI's "7 native posts" rendering is the marketing wrapper around this backend capability, NOT evidence that an AI produces 7 render variants |
| `1:1`, `1.91:1`, `4:5`, `9:16`, `16:9` aspect-ratio auto-crop per platform                        | **Roadmap**     | Per-platform aspect knowledge lives only in `Editor.tsx` as hardcoded `format` strings. No `ffmpeg` invocation, no crop codepath anywhere — `internal/worker/upload_worker.go` streams source bytes to S3 unchanged                                                |
| Auto-hashtag generation (per-platform tokenisation, per-post trending tag extraction)          | **Roadmap**     | `internal/worker/publish_worker.go::publishTarget` builds the `PublishPayload` by forwarding `post.Caption` verbatim — no tokenisation, no per-platform hashtag block, no LLM rewriter                                                    |
| Per-platform caption-tone adaptation (formal/quirky thread-style voice for X vs LinkedIn, etc.) | **Roadmap**     | `/app/compose` accepts a single caption field per post; no platform-aware rewriter                                                                                                                                                                                                                            |

### What `/editor` is FOR (and what it is NOT)

The page is **intentionally not a working editor**. It is a marketing demonstrator for the AI-assist positioning: the page team is committed to the auto-cut, AI image, stock-footage, and SFX curve, and the right-hand side of the page is the funnel into `/login` → `/app/compose` where the actual single-platform publish work happens (the publish-worker + capability-router backend, which **is** operative).

When the auto-crop, hashtag generation, AI image placement, stock-footage curation, or LLM caption rewriter modules are merged into the codebase, this section's table is the audit — each roadmap row flips from Roadmap → Operative at the moment its codepath lands and its tests go green.

### Honesty guard (for investors / collaborators)

> "Does InstaEdit do AI Compose today?"
>
> - The marketing surface at `/editor` ships as written and is a genuine positioning page.
> - The backend one-render → 7-platforms publish pipeline (PublishWorker + ReconcileWorker + CapabilityRouter in `internal/worker/` and `internal/services/provider.go`) is **operative** — single multi-platform fan-out from one user-supplied raw render works today.
> - The creative-AI pipeline (stock footage, SFX, AI images, auto-crop, hashtag generation, per-platform caption rewriter) is **NOT built**. The copy at `/editor` reflects design intent, not engineering reality. Plan for that gap when evaluating the AI Compose roadmap.

## Background workers and Async Publishing Pipeline

`internal/bootstrap/app.go::RunWorkers` starts exactly **seven independent background goroutines**, mirrored by the `cmd/worker` binary and by the `cmd/server` dev wrapper (the production topology runs `cmd/api` + `cmd/worker` as separate pods, plus a one-shot `cmd/migrate` before deploy). Each goroutine owns its own cancellable context, tick interval, and `Done` channel; the boot log line confirms it: `7 background goroutines started: publish / reconcile / outbox / webhook / metrics / sessions_cleanup / upload`.

> **Documentation drift (Taglio 5.x)**: earlier versions of this document described the runtime as a "two- / three-goroutine" pipeline because only the publish + reconcile + outbox triple was tracked in the indexed case study. The other four (`webhook`, `metrics`, `sessions_cleanup`, `upload`) have been part of the boot surface since Blocco #2.1 — readers should treat the canonical table below as authoritative and ignore the older "TWO/THREE/5" references that may still appear in commit-message archaeology or `cmd/server/main.go` comments.

### Authoritative goroutine list (mirrors `pkg/api/worker_status.go::WorkerNames`)

| # | Name              | Component                              | Default tick                       | Env var                              | Responsibility                                                                 | Drain budget |
|---|-------------------|----------------------------------------|------------------------------------|--------------------------------------|--------------------------------------------------------------------------------|--------------|
| 1 | `publish`         | `worker.PublishWorker`                 | 30s                                | `PUBLISH_WORKER_INTERVAL_SECONDS`    | Driver: claim `post_targets` (queued → publishing) + sync-platform dispatch    | 15s          |
| 2 | `reconcile`       | `worker.ReconcileWorker`               | 5s                                 | `RECONCILE_WORKER_INTERVAL_SECONDS`  | Reconciler: terminal `publishing → published \| failed` via `AsyncPublisher`   | 15s          |
| 3 | `outbox`          | `outbox.Dispatcher`                    | 5s tick + 60s lease + 20s heartbeat | n/a (constants)                      | Materialise `publish_jobs` audit rows via `SELECT FOR UPDATE SKIP LOCKED`       | 15s          |
| 4 | `webhook`         | `worker.WebhookWorker`                 | 5s                                 | `WEBHOOK_WORKER_INTERVAL_SECONDS`     | Drain `webhook_deliveries` (HMAC sign + HTTP POST + retry)                     | 15s          |
| 5 | `metrics`         | `metrics.RunPeriodicCollector`         | 10s                                | n/a (`DefaultCollectorInterval`)     | Refresh Prometheus gauges (queue depth, age, publish state counts)             | 15s          |
| 6 | `sessions_cleanup`| `worker.SessionsCleanupWorker`         | 300s                               | `SESSION_CLEANUP_INTERVAL_SECONDS`   | Retention-policy hard delete on `sessions` (revoked > 30d OR refresh expired > 7d) | 15s          |
| 7 | `upload`          | `worker.UploadWorker`                  | 30s                                | `UPLOAD_WORKER_INTERVAL_SECONDS`     | Stream `upload_jobs` (queued) → fetch Google Drive → S3 → posts + publish queue | 15s          |

Every goroutine flips an `atomic.Bool` on its first executable line via `WorkerStatus.Mark(name)`; the `/ready` endpoint aggregates the same set. The `publish` + `reconcile` + `outbox` triple drives the publishing pipeline detailed below; the other four are documented in their own package files (`internal/worker/`, `internal/outbox/`, `pkg/metrics/`).

### Pipeline-specific cadence (publish + reconcile)

```
 PublishWorker.Run(ctx)   — driver:    queued → publishing
   interval = 30s default
   each tick: ListPending + per-row publishTarget

 ReconcileWorker.Run(ctx)  — reconciler: publishing → published | failed
   interval = 5s default
   each tick: ListPublishing + per-row reconcileTarget
```

Both share the same `*CredentialVault`, the same `*CapabilityRouter`, and the same `*repository.PostRepository` — production wiring (`internal/bootstrap/app.go::Wire`) instantiates each worker from the same handles. The split is invisible to the HTTP API; the only externally observable difference vs the pre-split shape is the snappier reconciler cadence (sub-30s pickup of `publishing → published` transitions under the canonical 5s default).

### Capability-based dispatch (no hardcoded provider lists)

Publish-path classification per `post_targets` row is **resolved dynamically** through the `*CapabilityRouter` (`internal/services/provider.go`), NOT through a hardcoded list of provider names in the worker. The router holds all registered providers keyed by platform name; each registered provider is type-asserted against the capability interfaces at registration time. The publish worker consults the router on every tick — there is no per-platform `switch` in worker code:

```
router.AsyncPublisher(platform)?
   ├── false → sync path:   Publish() returns the final media id inline
   │                       (provider implements Publisher only)
   └── true  → async path:  Publish() returns a publish_id, row stays in
                           status='publishing', Reconcile picks it up
                           (provider implements Publisher AND AsyncPublisher)
```

The authoritative mapping "which platforms currently satisfy which capability" lives in [`docs/PROVIDER_MATRIX.md`](./PROVIDER_MATRIX.md). Adding a new platform, or moving an existing one from sync to async, is exactly two changes: (a) the new provider struct's `var _ AsyncPublisher = (*X)(nil)` toggles the capability at registration, (b) the corresponding ○/● mark in the matrix flips — no worker code changes. **If `router.AsyncPublisher(platform)` returns `(nil, false)` at runtime**, the row still publishes via the provider's `Publisher.Publish(...)` method on the sync path and transitions straight to `published`; the reconciler no-ops that target entirely (no `AsyncPublisher` capability to reconcile against).

### Driver: `tick()` — queued → publishing transition

The publish worker (`internal/worker/publish_worker.go::Run`) ticks every `interval` (default 30s) and on each tick calls `runOnce` → `tick`. For each `post_targets` row whose `status='queued'` AND whose parent `posts.scheduled_at <= now()`:

1. **Atomic claim** via `ClaimQueuedTarget(id)` (`UPDATE post_targets SET status='publishing' WHERE id=? AND status='queued'`). The single UPDATE uses `status='queued'` as a logical lock so 2+ worker replicas cannot double-publish. This is the verdict-§10 atomic-claim primitive; Redis-style SKIP LOCKED is not needed because each row's transition is owned by exactly one worker at a time.
2. Load parent `Post` via `FindByID`.
3. Load `PlatformAccount` via `FindPlatformAccountByID`.
4. Refresh OAuth token via `vault.Renew` (the `CredentialVault` serialises concurrent refreshes with a `pg_advisory_xact_lock`).
5. **Taglio 4.7 LEVEL 2**: stamp the deterministic `provider_idempotency_key` (`SHA-256("v1:" + post_id + ":" + account_id)[:16]`) onto the `post_targets` row so retries reuse the same key.
6. Resolve the platform's `Publisher` capability and call `Publish(ctx, token, accountUserID, payload)`, forwarding `payload.IdempotencyKey` so providers with native per-call idempotency keys (LinkedIn "X-Restli-Idempotency-Key", Twitter v2 "request_id", TikTok "idempotent" query param) drive upstream dedup; the DB-level `UNIQUE(platform_account_id, provider_idempotency_key)` constraint is the catch-all safety net.
7. **Sync-path row** — when `router.AsyncPublisher(platform)` returned `(nil, false)` during setup (see [Capability-based dispatch](#capability-based-dispatch-no-hardcoded-provider-lists)), the provider's `Publish(...)` returns the final media id inline → transition `status='published'`, set `PublishedAt`, set `PlatformPostID` to the final media id. The row leaves both filter sets (`queued` for the driver, `publishing` for the reconciler). **The current set of providers on this path is documented in [PROVIDER_MATRIX.md](./PROVIDER_MATRIX.md); do not maintain a parallel list in worker code.**
8. **Async-path row** — when `router.AsyncPublisher(platform)` returned a non-nil implementation, `Publish(...)` returns a publish_id immediately → store `PlatformPostID=publish_id`, KEEP `status='publishing'`. The reconciler owns the next transition. **The current set of providers on this path is documented in [PROVIDER_MATRIX.md](./PROVIDER_MATRIX.md); do not maintain a parallel list in worker code.**

The driver and reconciler never both touch the same row simultaneously — **the driver owns `queued → publishing` (and the rare `publishing → failed` exits on vanished-post / missing-capability / platform-error paths), and the reconciler owns `publishing → published | failed`** under normal conditions. See [State-machine ownership](#state-machine-ownership) below for the per-transition ownership table.

### Reconciler: `tickReconcile()` — publishing → published | failed transition

The reconcile worker (`internal/worker/reconcile_worker.go::Run`) ticks every `interval` (default 5s) and on each tick calls `runOnce` → `tickReconcile`. For each `post_targets` row whose `status='publishing'` AND `platform_post_id IS NOT NULL` (`ListPublishing` query):

1. **`reconcileTarget(ctx, target)`** (`internal/worker/reconcile_worker.go`) drives the per-target state machine.
2. Load `PlatformAccount` — orphan targets (account missing) are marked `failed` so they don't loop forever.
3. **Capability lookup**: `router.AsyncPublisher(account.Platform)`. The current per-platform ○/● mapping is documented in [`docs/PROVIDER_MATRIX.md`](./PROVIDER_MATRIX.md); workers MUST NOT carry their own platform name lists. When the lookup returns `(nil, false)` (sync-path provider — the row already transitioned to `published` in the driver's `Publish` call), this target is no-op'd: there is nothing to reconcile.
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

### Rate limiting and retry semantics

Two independent mechanisms govern how the system handles backpressure from upstream platforms, plus an explicit gap that is still open on the publish path. Earlier versions of this document carried a vaguer claim ("the worker implements exponential backoff on 429") which understated the truth — the actual layering is:

| Layer | Where | Behaviour | Scope |
|---|---|---|---|
| Preventive throttle | `internal/worker/throttle.go` (`PlatformThrottle.Wait`) | Token bucket per platform name, `defaultBurst=1`, pure spacing | Per worker replica, NOT distributed |
| Async retry with jitter + DLQ | `internal/outbox/dispatcher.go` (`Dispatcher.computeBackoff`, `processOne`) | Decorrelated jitter, `MaxAttempts=5`, `BaseDelay=1s`, `CapDelay=1h`, `ErrTerminal→DLQ-immediate` | Outbox events audit-row materialisation |
| Webhook outbound delivery | `internal/worker/webhook_worker.go` | Retries `5xx/408/425/429/timeout` up to `MaxAttempts` with backoff | Operator-configured webhook sinks |
| **Gap** — final publish call retry | — | `publisher.Publish(...)` errors short-circuit to `markFailed` today; no `next_attempt_at` reschedule from the worker | To be added |

#### (a) Per-process throttle (pre-call)

`PublishWorker.publishTarget` blocks on `PlatformThrottle.Wait(ctx, account.Platform)` before calling `publisher.Publish(...)`. The bucket has `defaultBurst = 1` so there is no burst headroom — every call must wait its turn — and the per-platform rates are tuned conservatively against the documented platform limits:

| Platform   | Default rate | Source constraint                                             |
|------------|--------------|---------------------------------------------------------------|
| instagram  | 2 req/s      | Meta Graph ~200 calls/user/hour ≈ 1 per 18s                   |
| facebook   | 2 req/s      | same                                                          |
| threads    | 2 req/s      | same                                                          |
| tiktok     | 0.5 req/s    | documented `video.publish` limit ~1 per 2s                    |
| youtube    | 0.33 req/s   | Data API v3 daily quota; uploads cost 1 bucket unit each (YouTube 2026 bucket model: default 100/day, expandable to 300-400/day for 200-channel rollout)        |
| twitter    | 1 req/s      | v2 user-endpoint ~300 tweets/3h                               |
| linkedin   | 0.5 req/s    | Posts API ~100 calls/day per app                              |

> **The throttle is per worker replica, NOT distributed across pods.** Operators running N replicas get N independent buckets so the aggregate rate can exceed the platform's per-app limit; the throttle is a best-effort smoothing layer, not a global SLA. A cross-replica distributed limiter (Postgres counter or Redis would be the natural backends) is a future enhancement — not implemented today.

#### (b) Outbox retry with decorrelated jitter + DLQ

The canonical async retry curve lives in the **outbox dispatcher goroutine** (`internal/outbox/dispatcher.go`), NOT in the publish worker. Per `outbox_event` row the dispatcher applies:

- **Lease**: `SELECT FOR UPDATE SKIP LOCKED` + heartbeat lease (default lease TTL 60s, heartbeat 20s, tick 5s).
- **Retry curve** (`Dispatcher.computeBackoff`): AWS-style decorrelated jitter with `MaxAttempts = 5`, `BaseDelay = 1s`, `CapDelay = 1h`. The formula is `temp = min(cap, prev * 3)`, `sleep = uniform(base..temp)` where `prev = base * 2^(attempt-1)`. After 5 failed attempts the row is marked `dead_letter` and surfaces for operator triage.
- **Terminal opt-out**: a provider can `fmt.Errorf("%w: …", services.ErrTerminal)` to skip retries and go straight to DLQ — used for unrecoverable conditions (schema mismatch, payload too large, business-rule violation). Anything else is treated as transient → `MarkFailed` with backoff.

The transactional outbox table is the audit-only appendix to `post_targets.status`; the outbox dispatcher is a separate goroutine from the publish/reconcile workers, running on its own 5s tick with a 15s drain budget on shutdown. See [Transactional Outbox Pipeline](#transactional-outbox-pipeline).

#### (c) Webhook outbound retry

`internal/worker/webhook_worker.go` (seventh goroutine in the **Authoritative goroutine list** subsection above) has its own retry curve for outbound webhooks to operator-configured HTTP sinks. Status codes `5xx / 408 / 425 / 429 / timeout` are rescheduled up to `MaxAttempts`; `2xx` is success; other `4xx` (non-408/425/429) is dead. This is a separate domain from platform publishing and is fully documented in `webhook_worker.go`.

#### (d) **OPEN GAP — retry on 429/Retry-After/5xx at the final publish-platform call**

The publish worker's call to `publisher.Publish(ctx, ...)` does **not** retry on platform backpressure today. The call's error path in `internal/worker/publish_worker.go::publishTarget` immediately routes through `markFailed`:

```go
result, err := publisher.Publish(ctx, oauthToken.AccessToken, account.PlatformUserID, payload)
if err != nil {
    return w.markFailed(target, err.Error()) // terminal — no attempt_count bump, no next_attempt_at re-stamp
}
```

The `post_targets` table has an `attempt_count` / `next_attempt_at` column (introduced by migration 018 — the per-target retry state machine), but no code path on the publish worker today bumps those columns on a transient platform error. The typed detection helper already exists — `internal/services/provider.go::IsRateLimitError` recognises both `*services.RateLimitError{RetryAfter: …}` and `*services.ProviderError{Code: ErrorCodeRateLimited}`, and `ParseRetryAfter` understands RFC 7231 `Retry-After` headers plus the de-facto `X-RateLimit-Reset` epoch-seconds convention. The per-process throttle in (a) already smooths steady-state rate such that `429`s should be rare in practice, but when they DO happen they currently surface as terminal `failed` rows that the operator must requeue.

**Closing the gap** would mean wiring the rate-limit detection into the worker as: on `*services.RateLimitError`, stamp `next_attempt_at = NOW() + RetryAfter`, bump `attempt_count`, leave `status='queued'`, and skip the publish call this tick so a future tick picks it up. The columns and helpers are ready; the worker-side branch is the missing glue. Until that lands, treat any `429` row as operator-requeue rather than expect automatic retry.

### Seven-way shutdown

`internal/bootstrap/app.go::RunWorkers` spawns all seven background goroutines in parallel at startup and shuts them down **sequentially** on SIGINT/SIGTERM. Each goroutine has its own cancellable context + `Done` channel; the cancels go out as a single broadcast on the signal, then the awaits are stacked (each with its own 15s budget), followed by the HTTP server's own 30s drain (`cmd/api` and `cmd/server` paths):

```
go publishWorker.Run(workerCtx)         // [1] driver                — 30s tick
go reconcileWorker.Run(reconcileCtx)     // [2] reconciler            — 5s tick
go dispatcher.Run(dispatcherCtx)         // [3] outbox                — SKIP LOCKED + 60s lease
go webhookWorker.Run(webhookCtx)         // [4] webhook               — 5s tick
go metricsCollector.Run(metricsCtx)      // [5] metrics               — 10s tick
go sessionsCleanupWorker.Run(sessionsCtx)// [6] sessions_cleanup     — 300s tick
go uploadWorker.Run(uploadCtx)           // [7] upload                — 30s tick

<-ctx.Done() (SIGINT/SIGTERM)
workerCancel(); reconcileCancel(); dispatcherCancel(); webhookCancel()
metricsCancel(); sessionsCleanupCancel(); uploadCancel()            // single broadcast

select { <-workerDone,            15s }    // drain budget [1]
select { <-reconcileDone,         15s }    // drain budget [2]
select { <-dispatcherDone,        15s }    // drain budget [3]
select { <-webhookDone,           15s }    // drain budget [4]
select { <-metricsDone,           15s }    // drain budget [5]
select { <-sessionsCleanupDone,   15s }    // drain budget [6]
select { <-uploadDone,            15s }    // drain budget [7]
srv.Shutdown(ctx) with 30s budget          // HTTP server drain — runs AFTER goroutine drains
```

Each goroutine performs a graceful drain on its own context: when `ctx.Done()` fires while a tick is mid-flight, the current tick completes naturally and `Run` returns only after that. A slow shutdown on one goroutine (e.g. a hung platform call in the reconciler, or a hung S3 PUT in the upload worker) does NOT block the others — each `Done` channel is independent, so the corresponding `select` returns via the timeout path while the healthy ones drain as they go.

Wall-clock bounds on shutdown:

- **Graceful drain** (default path): ms-level per goroutine. On a clean SIGTERM each goroutine returns within ms of the cancel broadcast and all seven `Done` channels close at sub-second timescales. The HTTP server's 30s drain then begins.
- **Hard hangs** (e.g. platform API stuck on one tick, or a goroutine ignoring `ctx.Done()`): each governance budget fires sequentially. The stacked `<-time.After(15s)` design caps the **goroutine-drain** window at `7 × 15s = 105s` before the operator logs "drain timeout, continuing shutdown" for the still-pending goroutine(s). After the goroutines settle (clean or timed-out), `srv.Shutdown(30s)` kicks off another 30s budget for the HTTP server. Total worst-case wall-clock: `105s (goroutines) + 30s (HTTP) = up to 135s`.

The goroutine-drain stack and the HTTP-server drain are **sequential, not concurrent** — this matches the production wiring in `internal/bootstrap/app.go::RunWorkers` and `cmd/server/main.go::main` (the seven `<-XxxDone` selects come before `srv.Shutdown(ctx)` in the source order). Operators tuning the shutdown budgets should bound total shutdown at the worst case (`135s`) plus any operator-imposed `kill -9` wait time.

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
- **Driver/reconciler split commit** (`ca7c879`, Taglio 5.x): extracted `tickReconcile` / `reconcileTarget` / `markFailedAndReturn` from `PublishWorker` into a new `ReconcileWorker` struct with its own `Run` goroutine, mirroring the outbox dispatcher. Verified via `git show --stat ca7c879` (touches `internal/worker/reconcile_worker.go` + `reconcile_worker_test.go` + `mocks_test.go` + slims `publish_worker.go` + `publish_worker_test.go` + adds `cfg.ReconcileWorkerIntervalSeconds`). The pre-Blocco #5.x wiring collapsed the whole shutdown into a 3-way stack; the post-Blocco #2.1 / Taglio 5.x runtime is a 7-goroutine stack (see "Seven-way shutdown" above).

## Transactional Outbox Pipeline

**Cross-reference: `internal/outbox/dispatcher.go`, `internal/outbox/processors/publishjobs.go`, `cmd/server/main.go`.**

`PostRepository.Create` writes `posts + post_targets + outbox_events` in one `BEGIN/COMMIT` tx. A background goroutine (`outbox.NewDispatcher`) reads `outbox_events` via `SELECT FOR UPDATE SKIP LOCKED` + heartbeat lease, then calls `processors.NewPublishJobsMaterialiser` to insert the audit row. Both run parallel to the publish worker with independent 15s drain budgets on shutdown. The PublishJob table is the audit-only appendix; `post_targets.status` remains the source of truth for current publish state.

## Google Drive folder import (POST `/api/v1/media/import/drive/folder`)

The Drive import endpoint fans a (public or authenticated) Google Drive folder out into a staggered schedule of `upload_jobs` that the upload worker (seventh goroutine, **Authoritative goroutine list** subsection above) drains in the background. It is **NOT** a publish API — it queues and spreads the work across time, then exits. The endpoint, the request body, and the CLI that drives it from an operator shell are documented below.

### Endpoints

| Method | Path                                                | Handler                                                | Purpose                                                                                                       |
|--------|-----------------------------------------------------|--------------------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| POST   | `/api/v1/media/import/drive/folder`                 | `pkg/api/drive_batch.go::handleDriveBatchImport`         | Schedule a folder import with cumulative stagger per job. Returns 202 with the DriveBatchImportResponse body. |
| GET    | `/api/v1/media/import/drive/batch/status`           | `pkg/api/drive_batch.go::handleDriveBatchStatus`        | Dashboard-friendly aggregate (pending/processing/completed/failed) per folder id, scoped to the caller.        |
| POST   | `/api/v1/media/import/drive/async`                  | `pkg/api/drive_import_async.go::handleDriveImportAsync` | Same source code path as `/folder`; surfaced when the SPA cannot hold a long blocking HTTP connection.       |
| POST   | `/api/v1/media/import/drive`                        | `pkg/api/drive_import.go::handleDriveImport`             | Single-file import — different surface, same `upload_jobs` queue.                                            |

> The legacy path string `/api/v1/drive/batch-import` does **not** exist in the current router. The canonical folder-batch path is `/api/v1/media/import/drive/folder`. Any docs or runbook that says otherwise is stale.

### Request body for `POST /api/v1/media/import/drive/folder`

```jsonc
{
  "folder_id":           "1Kssuh0eQ7Wmg8uMg29aI7fShXSLCaw3x", // required (string)
  "drive_account_id":    1234,   // optional int64 — use the user's linked Drive OAuth grant to list private/shared folders
  "workspace_id":        42,     // required int64  — owns the scheduled upload_jobs
  "facebook_account_id": 7,      // required int64  — platform_accounts.id of the target Facebook Page
  "title":               "",     // optional       — empty = per-post title = Drive filename
  "caption_prefix":      "",     // optional       — prepended to every caption with ` — ` separator
  "min_jitter_seconds":  10800,  // optional int   — see "Stagger windows" below; min 60
  "max_jitter_seconds":  16200,  // optional int   — must be >= min_jitter_seconds
  "page_token":          "",     // optional       — Drive nextPageToken for folders > 200 items
  "cursor_scheduled_at": null    // optional time  — continuation cursor across pages (RFC3339)
}
```

### Stagger windows: API defaults vs CLI defaults

The two surfaces have **deliberately different default windows** because they target different use cases:

| Surface                                          | `min_jitter_seconds`                       | `max_jitter_seconds`                       | Source os.Getenv / const                 |
|-------------------------------------------------|-------------------------------------------|-------------------------------------------|------------------------------------------|
| API `POST /api/v1/media/import/drive/folder`    | `10800` (3h)                               | `16200` (4.5h)                             | `pkg/api/drive_batch.go` constants injected when both fields are zero in the body |
| CLI `cmd/link-drive-and-import`                  | `DRIVE_SCHEDULE_MIN_HOURS` → `14400` (4h)  | `DRIVE_SCHEDULE_MAX_HOURS` → `21600` (6h)  | `cmd/link-drive-and-import/main.go` (envFloat calls at lines 79–83)        |

> **Both surfaces contract**: jitter is sampled per-job from `crypto/rand` uniformly on `[min, max]`; the API enforces `min_jitter_seconds >= 60` and rejects mismatches with HTTP 422; the CLI exits with a non-zero status on the same invalid ranges. **The legacy 3–4.5h vs 4–6h difference is by design** — the API tunes to "shipping cadence" for the dashboard SPA, the CLI to a wider non-blocking window for ops/sandbox imports.

### What the staggering does (and what it does NOT)

The stagger is **configurable load distribution** — it spreads queued publishes evenly across a window so the platform APIs see a natural spacing instead of a burst. The root cause is the rates documented in [Rate limiting and retry semantics](#rate-limiting-and-retry-semantics): the per-platform `defaultPlatformLimits` are conservative but still below the published rate ceilings the providers monitor (Meta `video.upload` burst, TikTok `video.publish` per-app-per-second, YouTube Data API v3 daily quota units, etc.). A folder of 30 videos queued at the same minute would otherwise publish 30 in a single worker tick and trip the per-account rate counter on the provider side.

What it does NOT do:

- It does **not** simulate a human posting cadence. The pacing is randomised against `[min, max]` per job, but the rate-limit reason stands regardless of perceived "humanness".
- It does **not** "avoid shadowban"; that phrasing has no technical basis in this codebase. The actual mitigation is the configurable jitter uniform against the per-platform perforated rate budgets in `internal/worker/throttle.go`.
- It does **not** guarantee delivery — it only queues `upload_jobs`. The upload worker owns the actual streaming-to-S3 + publish hand-off. Provider-side `*services.RateLimitError` responses are caught at the worker layer (see the **OPEN GAP — retry on 429/Retry-After/5xx** under [Rate limiting and retry semantics](#rate-limiting-and-retry-semantics)).

### CLI: `cmd/link-drive-and-import` env vars

The operator-facing CLI for one-off folder imports (no HTTP server involved). Reads from the environment:

| Env var                       | Required | Default                | Purpose                                                                                                            |
|-------------------------------|----------|------------------------|--------------------------------------------------------------------------------------------------------------------|
| `INSTAEDIT_USER_ID`           | YES      | —                      | The `users.id` of the workspace owner.                                                                             |
| `INSTAEDIT_WORKSPACE_ID`      | YES      | —                      | The `workspaces.id` owning the scheduled `upload_jobs`.                                                            |
| `FACEBOOK_PLATFORM_ACCOUNT_ID`| YES      | —                      | The `platform_accounts.id` of the target Facebook Page.                                                            |
| `DRIVE_FOLDER_ID` **OR** `DRIVE_FOLDER_URL` | YES at least one | hard-coded `defaultFolderID` constant | Drive folder id (raw) OR share URL — the URL form is parsed for the `/folders/<id>/` segment by `driveFolderID()`. |
| `DRIVE_SCHEDULE_MIN_HOURS`    | NO       | `4`                    | Lower bound of the cumulative stagger (in hours).                                                                  |
| `DRIVE_SCHEDULE_MAX_HOURS`    | NO       | `6`                    | Upper bound; must be `>= MIN`.                                                                                    |

Both `DRIVE_FOLDER_ID` and `DRIVE_FOLDER_URL` are accepted. The URL parser strips the query string and the trailing slash: `https://drive.google.com/drive/folders/<id>?usp=sharing` resolves to `<id>`. When neither is set, the CLI falls back to the compile-time constant `defaultFolderID` in `cmd/link-drive-and-import/main.go` — operators should set one explicitly to avoid silent surprises.

### What the endpoint cannot do (and what it does in those cases)

- **Private folders require the user's linked Drive OAuth grant.** Set `drive_account_id > 0` in the body. Without it the server falls back to public listing via `GOOGLE_DRIVE_API_KEY`. If neither is configured, the response is **HTTP 200** with `needs_drive_account: true` and/or `needs_google_drive_api_key: true` plus a `note` field explaining what to configure — NOT a fatal error.
- **Folder paging is at most 200 per response** (Google's quirk). The response always carries `next_page_token` — an empty string means "you got everything". To continue a multi-page import, re-call the endpoint with `page_token` set **and** `cursor_scheduled_at` set to the previous response's `last_scheduled_at` (RFC3339). Sending `cursor_scheduled_at` empty collapses the random gap at page boundaries (the page would publish back-to-back).
- **7-day silent cap on cumulative schedule** (`driveBatchJitterMaxSeconds` const in `pkg/api/drive_batch.go`). Cumulative schedules past 7 days are clamped without telling the caller; bump the constant (and surface the clamp in the response `note` if you do) if your folder workflow demands 2-week+ stagger horizons.

## Security

- Tokens are encrypted at rest with AES-256-GCM.
- JWT is signed with HS256 and validated by middleware.
- OAuth state is stored in an HttpOnly, Secure, SameSite=Lax cookie.
- Strict JWT auth is enforced in production.
