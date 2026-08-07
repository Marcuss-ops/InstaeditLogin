# Refactoring Tracker

Operational backlog for reducing large Go files without creating mechanical
file splits. This document is a planning source of truth; it does not authorize
changes to production behavior by itself.

**Snapshot:** 2026-08-07
**Branch policy:** work directly on `main`; keep each validated slice in its
own commit and push it to `origin/main`.
**LOC threshold:** 500 lines for attention, 800 lines for an immediate review.
**Measurement:** tracked `.go` files only; generated files, vendor content, and
untracked worktree files are excluded from the baseline. Counts below reflect
the working-tree snapshot at this date; refresh them before starting a slice if
local changes have altered a candidate's size.

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

The current scan found **22 tracked runtime production files above 500 lines**.
No runtime production file is above 800 lines in this snapshot. Test, E2E, and
CLI files are tracked separately below so their size does not distort runtime
priorities. Refresh this count before each new slice; it is an inventory, not a
set of automatically generated tickets. Since the last snapshot:
`internal/config/config.go` (672) split in `46b6188`, `internal/repository/group_repo.go`
(625) split in `6886c37`, `internal/bootstrap/workers_wiring.go` (630) split in
`7d03153`, and `pkg/api/auth_handlers.go` (786) + `pkg/api/posts_handlers.go`
(547) split in `cabcc39`; the auth split surfaced `pkg/api/auth_oauth.go`
(565), which is monitored below.

### Runtime production files above 500 lines

| Priority | File | Lines | Current responsibility | Next action |
|---|---|---:|---|---|
| P0 | `pkg/api/auth_handlers.go` | 786 → 3 | OAuth login/callback/exchange and session bootstrap | ✅ COMPLETED in `cabcc39`: split into `auth_oauth.go` (565), `auth_oauth_state.go` (224), `auth_account_attach.go` (239), `auth_session.go` (92); `auth_handlers.go` left as 3-line pointer. Dead `Router.handleMe` removed in `040fa04`. `auth_oauth.go` remains >500 → monitor below. |
| P0 | `internal/bootstrap/workers_wiring.go` | 630 | Dependency adapters and all worker specifications | ✅ COMPLETED in `7d03153`: specs → `workers_specs.go` (409), adapters → `workers_adapters.go` (66); `workers_wiring.go` now 121 lines with one ordered registry + `TestWorkerSpecs_PreserveLifecycleContract`. |
| P0 | `pkg/api/posts_handlers.go` | 547 | Post create, read, list, patch, delete, and response mapping | ✅ COMPLETED in `cabcc39`: split into `posts_mutations.go` (107), `posts_read.go` (240), `posts_types.go` (81); `posts_handlers.go` remains the thin router boundary. Idempotency and workspace authorization preserved. |
| P1 | `internal/config/config_types.go` | 616 | Config struct types split out of `config.go` (`46b6188`) | Monitor; split only if a second domain boundary (worker vs database structs) becomes worth isolating. |
| P1 | `pkg/api/auth_oauth.go` | 565 | OAuth login, callback, exchange, and account-attach flows (split from `auth_handlers.go` in `cabcc39`) | Monitor; split only if login vs callback families grow further or a stable test seam appears. |
| P1 | `internal/services/youtube_oauth.go` | 586 | OAuth URL/callback, token exchange, refresh, revoke, and client pool | Isolate token transport from OAuth policy and pool selection; reuse shared HTTP/error helpers. |
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
| T0 | `internal/worker/reconcile_worker_test.go` | 963 → 507 | ✅ COMPLETED in `SLICE7` (`cabcc39`+`SLICE7`): split by scenario into `reconcile_worker_test.go` (target state machine, 507), `reconcile_worker_tick_test.go` (tick+bounded batch+backoff, 243), `reconcile_worker_run_test.go` (Run/RunOnce+shutdown, 237); mock/helpers preserved in place. |
| T0 | `pkg/api/livestreams_test.go` | 953 → 528 | ✅ COMPLETED in `SLICE7`: split into `livestreams_test.go` (mocks+fixtures+shared policy+create, 528), `livestreams_list_test.go` (list/channels, 237), `livestreams_item_test.go` (get/patch/delete, 211). |
| T0 | `pkg/api/account_routes_test.go` | 935 → 232 | ✅ COMPLETED in `SLICE7`: split into `account_routes_test.go` (list, 232), `account_routes_get_test.go` (get+snapshot, 237), `account_routes_disconnect_test.go` (disconnect/delete/shared-grant, 490). |
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
remaining `config_types.go` (616) is a types-only file under monitoring.

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

**Follow-up monitor:** `pkg/api/auth_oauth.go` (565) is now the next candidate
above the 500-line threshold in the auth family.

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

After runtime slices are stable, split the three >800-line test files and the
large integration fixtures by scenario. Then organize the diagnostic CLIs.
Do not mix these changes with production behavior changes.

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
