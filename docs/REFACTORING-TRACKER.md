# Refactoring Tracker

Operational backlog for reducing large Go files without creating mechanical
file splits. This document is a planning source of truth; it does not authorize
changes to production behavior by itself.

**Snapshot:** 2026-08-07
**Branch policy:** work directly on `main`; keep each validated slice in its
own commit and push it to `origin/main`.
**LOC threshold:** 500 lines for attention, 800 lines for an immediate review.
**Measurement:** tracked `.go`, `.ts`, `.tsx` runtime production files; generated
files, vendor content, test files, and untracked worktree files are excluded
from the runtime inventory (tests are tracked in their own table below).
Counts below reflect the working-tree snapshot at this date; refresh them
before starting a slice if local changes have altered a candidate's size.

## Policy

A large file is not automatically a refactoring defect. Split only when the
change does at least one of the following:

1. creates a stable test seam;
2. removes duplicated policy or wiring;
3. isolates a responsibility with a clear owner;
4. reduces dependency direction or initialization complexity; or
5. makes the resulting unit easier to reason about without changing its
   public contract.

Do not split a file into arbitrary line-count fragments. Preserve exported
APIs unless a repository-wide search and the affected tests are updated in the
same slice. Keep SQL transactions, authorization boundaries, retry semantics,
and worker ordering intact.

## Current inventory

The current scan (`scripts/loc-report.sh -t 500 -n 60`) found **30 tracked
runtime production files above 500 lines** (20 Go + 10 TS/TSX). One runtime
file is above 700 lines — `web/src/pages/internal/AdminDashboard.tsx` (760)
is the only **P0** in this snapshot. Test, E2E, and CLI files are tracked
separately below so their size does not distort runtime priorities. Since the
last snapshot: `internal/config/config.go` (672) split in `46b6188`,
`internal/repository/group_repo.go` (625) split in `6886c37`,
`internal/bootstrap/workers_wiring.go` (630) split in `7d03153`, and
`pkg/api/auth_handlers.go` (786) + `pkg/api/posts_handlers.go` (547) split in
`cabcc39`; the auth split surfaced `pkg/api/auth_oauth.go` (565), monitored
below. **New:** `web/src/pages/internal/CoverEditor.tsx` (1240) split in
`d83d09e9` into 12 editor files; `web/src/pages/internal/AdminDashboard.tsx`
(760), `Covers.tsx` (587), `ScheduledByAccount.tsx` (562), `Groups.tsx` (545),
`livestreamWizardStep2.tsx` (527), `Compose.tsx` (506), and the publishing
wizard steps (`ChannelMetadataStep.tsx` 516, `ConfirmationStep.tsx` 510) join
`Programs.tsx` (513) as the frontend runtime inventory — the tracker now
measures TS/TSX too.

### Runtime production files above 500 lines

| Priority | File | Lines | Current responsibility | Next action |
|---|---|---:|---|---|
| P0 | `web/src/pages/internal/AdminDashboard.tsx` | 760 | Admin dashboard: KPI cards, performance charts, account tables, admin actions | **Next split:** separate the dashboard sections (KPI cards / channel-performance charts / account tables / admin CSV actions) into focused presentational components; preserve the shared data-hook wiring. |
| P0 | `pkg/api/auth_handlers.go` | 786 → 3 | OAuth login/callback/exchange and session bootstrap | ✅ COMPLETED in `cabcc39`: split into `auth_oauth.go` (565), `auth_oauth_state.go` (224), `auth_account_attach.go` (239), `auth_session.go` (92); `auth_handlers.go` left as 3-line pointer. Dead `Router.handleMe` removed in `040fa04`. `auth_oauth.go` remains >500 → monitor below. |
| P0 | `internal/bootstrap/workers_wiring.go` | 630 | Dependency adapters and all worker specifications | ✅ COMPLETED in `7d03153`: specs → `workers_specs.go` (409), adapters → `workers_adapters.go` (66); `workers_wiring.go` now 121 lines with one ordered registry + `TestWorkerSpecs_PreserveLifecycleContract`. |
| P0 | `pkg/api/posts_handlers.go` | 547 | Post create, read, list, patch, delete, and response mapping | ✅ COMPLETED in `cabcc39`: split into `posts_mutations.go` (107), `posts_read.go` (240), `posts_types.go` (81); `posts_handlers.go` remains the thin router boundary. Idempotency and workspace authorization preserved. |
| P1 | `internal/config/config_types.go` | 616 → 230 | Config struct types split out of `config.go` (`46b6188`) | ✅ COMPLETED in `18c9aa71`: split by domain into `config_types_database.go` (45), `config_types_storage.go` (29), `config_types_auth.go` (100), `config_types_integrations.go` (43), `config_types_server.go` (53), `config_types_worker.go` (122); `config_types.go` keeps only the `Config` aggregate root. Centralized env-var validation in `field_specs.go` untouched; 12 structs preserved byte-identical. |
| P1 | `internal/services/youtube_oauth.go` | 586 | OAuth URL/callback, token exchange, refresh, revoke, and client pool | Isolate token transport from OAuth policy and pool selection; reuse shared HTTP/error helpers. |
| P1 | `pkg/api/auth_oauth.go` | 565 → 45 | OAuth login, callback, exchange, and account-attach flows (split from `auth_handlers.go` in `cabcc39`) | ✅ COMPLETED in `3bd437c9`: split by flow into `auth_oauth_login.go` (189, `handleLogin`), `auth_oauth_callback.go` (176, `handleCallback` + `resolveCallbackState`), `auth_oauth_exchange.go` (176, `exchangeOAuthCode` + `callbackAttach*` + `writeCallbackSuccess`); `auth_oauth.go` keeps the pool-aware contract types + `HandleOAuthCallbackRouteForTest` as a 45-line boundary. State verification (connect-link/oauth-flow/CSRF paths, single-use nonce consumption) and pool client-cookie idempotency preserved. |
| P1 | `internal/repository/thumbnail_project_repo.go` | 558 | Project CRUD, snapshots, revisions, restore, and CAS status | Extract revision/snapshot persistence behind focused helpers; retain CAS and revision-number transaction guarantees. |
| P1 | `pkg/api/accounts_read_handlers.go` | 552 | Account listing, account detail, content, and earnings reads | Split read endpoints by query family; keep account ownership loading in one shared helper. |
| P1 | `internal/services/provider_error.go` | 549 | Provider error types, classification, and retry/rate-limit metadata | Separate stable error contracts from classifiers only if call sites remain behaviorally identical. |
| P1 | `internal/models/external_delivery.go` | 549 | External delivery domain model and delivery state data | Review whether model declarations and conversion helpers can be isolated without duplicating the contract. |
| P1 | `pkg/metrics/collector.go` | 546 | Periodic DB-backed metric collection and gauge updates | Extract query-specific collectors while preserving advisory-lock single-flight and zero-fill semantics. |
| P1 | `internal/repository/asset_repo.go` | 545 | Media assets, visibility, probe state, expiration, and cleanup | Separate lifecycle/probe operations from retention cleanup; preserve workspace predicates and deletion eligibility. |
| P1 | `internal/repository/post_repo_retry.go` | 535 | Claims, leases, retries, rate limits, DLQ, and reclamation | Consolidate retry/lease policy helpers; do not create another facade layer over the existing post repository split. |
| P1 | `internal/services/provider.go` | 534 | Capability interfaces and provider routing contracts | Treat as a public internal contract; refactor only to remove duplicated capability lookup or clarify interfaces with compile-time assertions. |
| P1 | `internal/services/threads_oauth.go` | 528 | Threads OAuth and publishing integration | Compare with the shared Meta OAuth seams before extracting provider-specific policy. |
| P1 | `internal/repository/post_repo_aggregate.go` | 527 | Post aggregate/list queries and scans | Extract scan/projection helpers only when they are reused; keep query ordering and pagination contracts unchanged. |
| P1 | `pkg/api/router.go` | 528 | Central router state, dependency validation, and route assembly | Keep registration thin; extract only remaining dependency-validation or route-family seams without duplicating module wiring. |
| P1 | `internal/repository/post_repo_post.go` | 507 | Post and target creation/update persistence | Keep aggregate transactions and idempotency boundaries intact; extract only shared scan or mutation policy. |
| P1 | `internal/worker/reconcile_worker.go` | 525 | Async publish reconciliation and terminal state transitions | Refactor only after the `AsyncPublisher` contract is stable; keep in-flight, terminal, and retry semantics in one tested state machine. |
| P1 | `pkg/api/accounts_write_handlers.go` | 521 | Account validation, sync, and write-side orchestration | Verify existing `accounts_validate.go`/`accounts_sync.go` seams first; remove only remaining orchestration duplication. |
| P1 | `web/src/pages/internal/Covers.tsx` | 587 | Cover project library: list, cards, filters, create/delete dialogs | **Next split (frontend):** separate the library list/card rendering from the create/delete dialog wiring, mirroring the CoverEditor split pattern. |
| P1 | `web/src/pages/internal/ScheduledByAccount.tsx` | 562 | Scheduled-by-account list and filters | Split list row/filter presentational components from the page's data wiring. |
| P1 | `web/src/pages/internal/Groups.tsx` | 545 | Groups management tree/detail | Extract tree rows and detail panels into presentational components (GroupsDetailPanels already split). |
| P1 | `web/src/pages/internal/livestreamWizardStep2.tsx` | 527 | Livestream wizard step 2 | Split step sub-forms into focused components once the step grows another responsibility. |
| P1 | `web/src/features/publishing/wizard/ChannelMetadataStep.tsx` | 516 | Publishing wizard channel metadata step | Monitor; split only if the step's form sections grow independent validation. |
| P1 | `web/src/pages/Programs.tsx` | 513 | Programs listing page | Monitor; split list/card rendering from page wiring if it grows. |
| P1 | `web/src/features/publishing/wizard/ConfirmationStep.tsx` | 510 | Publishing wizard confirmation step | Monitor; split summary/confirm sections if the step grows. |
| P1 | `web/src/pages/internal/Compose.tsx` | 506 | Compose/publish form page | Monitor; split form sections once compose grows a second concern. |
| P2 | `internal/services/youtube_validate.go` | 503 | YouTube validation and upload contract checks | Monitor; split only if validation and transport policy continue to grow. |
| P2 | `internal/services/youtube_live_gateway.go` | 503 | YouTube live gateway operations | Monitor; first document the gateway contract and test seam before extracting. |
| P2 | `internal/repository/webhook_repo.go` | 502 | Webhook delivery persistence | Monitor; extract only if delivery lease/query policy is duplicated elsewhere. |
| P2 | `internal/repository/delivery_session_repo.go` | 502 | Delivery session persistence | Monitor; prioritize only when a clear session lifecycle seam appears. |

> The table intentionally prioritizes responsibility and operational risk over
> raw size. `internal/services/provider.go`, the post-repository family, and
> worker state machines should not be split merely to get below 500 lines.

### Tooling files above 500 lines

These commands are not part of the runtime production path. Refactor them only
when their operator workflows need a clearer seam; do not mix their changes
with API, worker, or repository behavior changes.

| Priority | File | Lines | Planned organization |
|---|---|---:|---|
| T2 | `cmd/batch-import-drive-folder/main.go` | 531 | Separate CLI parsing from import execution and output formatting; keep command behavior stable. |
| T2 | `cmd/yttest/main.go` | 588 | Separate command setup from diagnostic probes; keep this lower priority than runtime paths. |
| T2 | `cmd/test-youtube-upload/main.go` | 550 | Extract safe output and probe helpers only after runtime refactors stabilize. |

### Test and support hotspots

These files are large but should be organized by scenario or fixture reuse,
not treated as runtime architecture work:

| Priority | File | Lines | Planned organization |
|---|---|---:|---|
| T0 | `internal/worker/reconcile_worker_test.go` | 963 → 505 | ✅ COMPLETED in `9b80e102`: split by scenario into `reconcile_worker_test.go` (target state machine, 505), `reconcile_worker_tick_test.go` (tick+bounded batch+backoff, 243), `reconcile_worker_run_test.go` (Run/RunOnce+shutdown, 237); mock/helpers preserved in place. |
| T0 | `pkg/api/livestreams_test.go` | 953 → 386 | ✅ COMPLETED in `9b80e102` + `40760ca2`: split into `livestreams_test.go` (shared policy+create, 386), `livestreams_fixtures_test.go` (mocks+fixtures, 153), `livestreams_list_test.go` (list/channels, 237), `livestreams_item_test.go` (get/patch/delete, 211); 16 tests restored in `b33def5e` after `40760ca2` dropped them. |
| T0 | `pkg/api/account_routes_test.go` | 935 → 232 | ✅ COMPLETED in `9b80e102`: split into `account_routes_test.go` (list, 232), `account_routes_get_test.go` (get+snapshot, 237), `account_routes_disconnect_test.go` (disconnect/delete/shared-grant, 490). |
| T1 | `web/src/pages/internal/CoverEditor.test.tsx` | 865 | CoverEditor page contract tests (13 tests: autosave, conflict, export flush, save-as-copy, link) | Now the largest test file in the repo; split by scenario (autosave/conflict / export+link / media+load) preserving the 13-test data-testid/aria-label contract. |
| T1 | `internal/worker/publish_reconcile_integration_test.go` | 785 | Separate integration fixtures from publish/reconcile assertions. |
| T1 | `internal/repository/upload_job_pool_test.go` | 774 | Split claim/lease, reclaim, heartbeat, and concurrency cases. |
| T1 | `internal/worker/publish_worker_publish_youtube_test.go` | 780 | Split upload, idempotency, failure, and retry scenarios. |
| T1 | `pkg/api/posts_test.go` | 708 | Align test files with post lifecycle/query handler boundaries. |
| T1 | `internal/repository/post_repo_test.go` | 705 | Split aggregate, target, retry, and idempotency coverage. |
| T1 | `pkg/api/accounts_performance_assembler_test.go` | 683 | Split aggregation, pagination, and missing-data cases. |
| T1 | `tests/e2e/oauth_callback_binding_e2e_test.go` | 685 | Separate callback binding, re-auth, and failure-path scenarios. |
| T2 | Other `*_test.go` files above 500 | current scan | Split only when a scenario boundary is already clear or a test fixture is reusable. |

Test splits must preserve package-level helpers, avoid duplicate fixtures, and
run the narrow package suite before the full Go suite.

### Watchlist

Files between 450 and 500 lines are monitored but do not receive an automatic
issue or refactoring slice. Refresh the count before starting work; several
files previously listed in the watchlist have since crossed 500 or moved below
it. Use:

```bash
./scripts/loc-report.sh -t 500 -n 100
```

## Completed slices

These slices were implemented, tested, committed, and pushed directly to
`main`. They are recorded here as concrete architectural outcomes; no issue
number is assigned unless an actual external ticket exists.

| Slice | Architectural outcome | Verification | Commit |
|---|---|---|---|
| Configuration boundaries | Private field-spec resolvers centralize repeated DB pool and YouTube OAuth env mapping; domain validation remains in `config_validation.go`. | Config tests, full Go tests, vet, build | `a78484c` |
| Group repository seams | Shared membership/aggregate helpers remove repeated workspace-aware repository policy while preserving transaction boundaries. | Repository/API tests, full Go tests, vet, build | `2bbaa66` |
| Worker lifecycle wiring | One canonical ordered worker registry with shared lifecycle handling; no second registry introduced. | Wiring/runtime tests, full Go tests, vet, build | `d89cc25` |
| Livestream command policy | Shared command policy/resolver seams preserve livestream HTTP contracts. | Contract tests, full Go tests, vet, build | `c71b0a4` |
| Post handler policies | Shared cursor, route-ID, and workspace-ownership policies remove handler duplication without changing HTTP shapes or idempotency order. | Contract tests, full Go tests, vet, build | `315bd6e` |
| YouTube OAuth policy | Shared OAuth scope/policy seam separates repeated policy from transport and credential resolution. | OAuth contract tests, full Go tests, vet, build | `218a84b` |
| Retry/sampler primitives | Uniform semi-open duration sampling is shared only where RNG ownership and interval semantics are identical; distinct backoff policies remain separate. | Range/distribution tests, full Go tests, vet, build | `3148d94` |
| Configuration boundaries (final) | `internal/config/config.go` split into `config_types.go` (structs), `config_load.go` (resolution), `config_validation.go`, `config_database.go`; `Load()` output unchanged. | Config tests, full Go tests, vet, build | `46b6188` |
| Group repository seams (final) | `internal/repository/group_repo.go` split into `group_repo_helpers.go`, `group_repo_membership.go`, `group_repo_settings.go`, `group_repo_page.go`; ownership and cycle checks preserved. | Repository/API tests, full Go tests, vet, build | `6886c37` |
| Worker wiring registry (final) | `internal/bootstrap/workers_wiring.go` split into `workers_specs.go` + `workers_adapters.go`; one ordered registry + lifecycle-contract test retained. | Wiring/runtime tests, full Go tests, vet, build | `7d03153` |
| Auth and post HTTP handlers | `pkg/api/auth_handlers.go` (786) split into `auth_oauth.go` / `auth_oauth_state.go` / `auth_account_attach.go` / `auth_session.go` (dead `Router.handleMe` removed in `040fa04`); `pkg/api/posts_handlers.go` (547) split into `posts_mutations.go` / `posts_read.go` / `posts_types.go`. | API tests, full Go tests (33 pkgs), vet, build, loc-check | `cabcc39` |
| CoverEditor frontend split | `web/src/pages/internal/CoverEditor.tsx` (1240) split in `d83d09e9` into `features/thumbnailProjects/editor/snapshot.ts` + `objects.ts`, `components/editor/` (CanvasStage, LayersPanel, Inspector, RevisionPanel, EditorHeader, ConflictBanner, EditorToolbar, CanvasSettingsPanel, AssignmentsPanel), and `hooks/useCoverEditorMutations.ts`; page now 497 lines. | vitest (13 tests), tsc -b, oxlint, vite build | `d83d09e9` |
| Config type domains | `internal/config/config_types.go` (616) split in `18c9aa71` by domain into `config_types_database.go` (45) / `config_types_storage.go` (29) / `config_types_auth.go` (100) / `config_types_integrations.go` (43) / `config_types_server.go` (53) / `config_types_worker.go` (122); `config_types.go` keeps only the `Config` aggregate root (230). Byte-exact struct moves — 12 types preserved, `field_specs.go` centralized validation untouched. | Config tests, full Go tests (33 pkgs), vet, build, loc-check | `18c9aa71` |

## Remaining execution order

The remaining inventory below is a watchlist of responsibility boundaries, not
a preassigned ticket queue. Start a slice only after refreshing line counts and
finding an actual duplicated policy or stable test seam.

### Next execution order

### Slice 1 — Configuration boundaries (P0) ✅ COMPLETED

Split in `46b6188` (final) after the earlier `a78484c` seam: `config.go` →
`config_types.go` (structs) + `config_load.go` (resolution) +
`config_validation.go` + `config_database.go` + `field_specs.go`. `Load()`
output unchanged; config tests, full Go tests, vet, build all green. The
remaining `config_types.go` (616) was a types-only file under monitoring —
its domain boundary was confirmed in `18c9aa71` (per-domain struct blocks:
database-pool vs storage vs auth vs integrations vs server vs worker) and
split into six `config_types_*.go` files; `config_types.go` now holds only
the `Config` aggregate root (230 lines).

### Slice 2 — Group repository transaction seams (P0) ✅ COMPLETED

Split in `6886c37` (final) after the earlier `2bbaa66` seam: `group_repo.go` →
`group_repo_helpers.go` + `group_repo_membership.go` + `group_repo_settings.go`
+ `group_repo_page.go`. Ownership checks, cycle checks, and `workspace_channels`
resync semantics preserved; repository/API tests, full Go tests, vet, build all
green.

### Slice 3 — Worker wiring registry (P0) ✅ COMPLETED

Split in `7d03153`: `workers_wiring.go` (630) → `workers_specs.go` (409, one
spec per cohesive grouping) + `workers_adapters.go` (66, shared adapters);
`workers_wiring.go` now 121 lines with one canonical ordered registry and
`TestWorkerSpecs_PreserveLifecycleContract` retained. Wiring/runtime tests,
full Go tests, vet, build green.

### Slice 4 — Auth and post HTTP handlers (P0) ✅ COMPLETED

Split in `cabcc39` + dead-code cleanup in `040fa04`:

- `pkg/api/auth_handlers.go` (786) → `auth_oauth.go` (565: login/callback/
exchange flows) + `auth_oauth_state.go` (224: signed-state and client-cookie
helpers) + `auth_account_attach.go` (239: account attach/discovery) +
`auth_session.go` (92: exchange-code + workspace resolution); `auth_handlers.go`
is now a 3-line pointer.
- Dead `Router.handleMe` removed in `040fa04` (the live `/api/v1/auth/me` route
is mounted from `AuthModule.handleMe` in `modules_auth.go`; repo-wide search
confirmed zero `Router`-method call sites).
- `pkg/api/posts_handlers.go` (547) → `posts_mutations.go` (107) +
`posts_read.go` (240) + `posts_types.go` (81); `posts_handlers.go` remains the
thin registration boundary.

Auth/session/cookie/CSRF contracts, YouTube client-pool state semantics, post
idempotency, and workspace authorization all preserved; `go test ./...` (33
packages), `go vet`, `go build`, and `loc-check` green.

**Follow-up monitor:** the auth family is now fully split — `auth_oauth.go`
(565) was split by flow in `3bd437c9` into `auth_oauth_login.go` (189),
`auth_oauth_callback.go` (176), `auth_oauth_exchange.go` (176); no auth file
remains above 500 lines.

### Slice 5 — YouTube OAuth policy/transport (P1)

**Targets:** `internal/services/youtube_oauth.go`,
`internal/services/threads_oauth.go`, related tests.

- isolate token HTTP transport, callback policy, refresh/revoke, and OAuth pool
  selection;
- reuse shared provider error and HTTP client abstractions;
- preserve redirect URI, refresh-token, and client-pool contracts.

**Done when:** policy tests do not need to exercise raw HTTP transport and
refresh/revoke behavior remains covered by focused tests.

### Slice 6 — Repository and metrics cleanup (P1)

**Targets:** thumbnail/asset/post retry/aggregate repositories and
`pkg/metrics/collector.go`.

- extract only shared scan, lease, retry, or query-policy helpers;
- preserve tenant predicates, SQL ordering, advisory locks, and zero-filled
  metric labels;
- update repository/metrics tests with each extraction.

**Done when:** no new facade or duplicate SQL policy is introduced and the
operational contracts remain explicit.

### Slice 7 — Test and CLI organization (T1/T2)

The three >900-line test files (`reconcile_worker_test.go`, `livestreams_test.go`,
`account_routes_test.go`) are now split (see Completed slices, `9b80e102`).
Remaining: split `web/src/pages/internal/CoverEditor.test.tsx` (865, the
largest test file) by scenario, plus the other T1 test hotspots and the large
integration fixtures. Then organize the diagnostic CLIs. Do not mix these
changes with production behavior changes.

### Slice 8 — Frontend page splits (P0/P1, web)

**Targets:** `web/src/pages/internal/AdminDashboard.tsx` (760, the only
runtime >700), then `Covers.tsx` (587), `ScheduledByAccount.tsx` (562),
`Groups.tsx` (545), `livestreamWizardStep2.tsx` (527), `Compose.tsx` (506),
and the publishing wizard steps (`ChannelMetadataStep.tsx` 516,
`ConfirmationStep.tsx` 510).

- separate presentational sections (cards, tables, forms, panels) from page
  data wiring, mirroring the CoverEditor split (`d83d09e9`);
- preserve data-testid/aria-label contracts covered by vitest;
- validate each slice with `npx vitest run <affected-tests>` + `npm run build`
  + `npm run lint`.

**Done when:** no runtime web file sits above 500 lines and every page's test
suite still passes unchanged.

## Per-slice checklist

Before editing:

- refresh `scripts/loc-report.sh`;
- inspect all references to exported symbols being moved;
- confirm the working tree is clean or explicitly preserve unrelated changes;
- write the intended responsibility boundary in the commit description.

After editing:

```bash
gofmt -w <changed-go-files>
go test ./<affected-packages>/...
go test ./...
go vet ./...
go build ./...
./scripts/loc-check.sh -t 500 -a origin/main
```

For routing or runtime wiring changes also run:

```bash
make verify-entrypoint-topology
go test -race ./...
```

Commit only the intended slice, push directly to `main`, and verify:

```bash
git diff --cached --check
git show --stat --oneline HEAD
git status --short --branch
git ls-remote origin refs/heads/main
```

## Completed work and historical context

Previously completed splits remain useful examples, but are not active tasks.
Notable patterns include:

- `pkg/api/modules.go` split into focused module files;
- `pkg/api/uploads_handlers.go` split into lifecycle/list/schedule handlers;
- `internal/auth/jwt.go` split into issue/verify/middleware helpers;
- `internal/services/youtube_oauth.go` partially split with shared YouTube
  types;
- `internal/outbox/dispatcher.go` split for backoff/mark policy;
- large E2E and integration files split by scenario.

Use the current code, not historical line counts, to decide whether another
split is still needed. When a slice is completed, update this tracker with the
new line counts, tests run, commit SHA, and any new seam that future work should
reuse.
