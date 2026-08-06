# Final Acceptance Verification

**Date:** 2026-08-06
**Branch:** `main`
**HEAD verified before this follow-up:** `d27e466` (`docs(acceptance): verify concurrent postgres claims`)
**Remote:** `main` was aligned with `origin/main` before this report was created.

This document records the final verification of the scalability acceptance criteria. It separates deterministic offline evidence from tests that require PostgreSQL, Docker, OAuth credentials, or a live provider. A passing handler benchmark is **not** presented as a live HTTP + PostgreSQL p95.

## Executive verdict

| Criterion | Result | Evidence |
|---|---|---|
| 100 channels/accounts | **PASS for tested paths** | Frontend performance test covers 10/50/100/200 accounts; 100 accounts use one accounts request and zero per-account fan-out. Backend handler benchmark covers 100 joined account/snapshot rows. This is not a live 100-channel deployment test. |
| Zero Google calls during page load | **PASS for tested read paths** | Linking performance test counts zero provider/detail calls; the stale-snapshot account handler test asserts that the provider is not called and cached data is returned. This does not prove zero calls for every internal page or live Google traffic. |
| OAuth refresh deduplicated | **PASS offline** | `go test -race` concurrent shared-grant and same-grant singleflight tests pass; one provider refresh is observed while callers are concurrent. |
| Upload streaming and bounds | **PASS for covered multipart/body paths** | Admin CSV multipart spooling/cleanup tests, explicit body-limit tests, and request-body bound/close tests pass with the race detector. This is not a constant-RSS proof for every video upload handler or a 2 GB live upload. |
| Cursor pagination | **PASS for covered endpoints** | Cursor primitives, accounts, groups, posts/jobs and related list-handler tests pass with the race detector. Coverage is endpoint-specific; this row does not claim that every list endpoint has been exhaustively exercised. |
| Safe job claim | **PASS** | SQL contract tests verify `FOR UPDATE SKIP LOCKED`; empty-queue backoff test passes; the PostgreSQL multi-worker integration test passed with 8 concurrent workers and 24 jobs under the race detector. |
| Accounts p95 below 300–500 ms | **PARTIAL** | Repeated fake-store handler benchmark runs had an approximate 95th percentile of **165,055 ns/op (0.165 ms)**. This is a percentile across benchmark-run samples, not an HTTP request p95. Real HTTP + PostgreSQL + network p95 was not measured in this environment. |
| Full live E2E | **PASS with one intentional skip** | Clean checkout with the E2E schema and Router.Setup fixes: 23 named runner pass events, no failures, and 1 intentional skip for the real browser OAuth smoke test. |

The implementation satisfies the acceptance criteria that can be proven for the tested paths in this repository. The remaining acceptance gaps are operational and coverage-related: measure `GET /api/v1/accounts` against a running PostgreSQL/API deployment and broaden upload/provider coverage if required. The previously observed E2E schema and Router.Setup race failures are resolved by the harness fixes recorded below.

## Fixes verified in this run

- `internal/database/migrations/042_account_resource_snapshots.sql` was **not modified**: it already creates the production table and migration checksums are immutable after application.
- `tests/e2e/e2e_harness_helpers.go` now creates an idempotent reduced-schema equivalent of `account_resource_snapshots`, including the production 042 columns and refresh coordination fields from migration 102, plus the relevant indexes.
- `pkg/api/router.go` and `pkg/api/routes.go` serialize accidental concurrent `Router.Setup()` calls; `tests/e2e/account_lifecycle_resilience_e2e_test.go` initializes the handler once before concurrent requests, with `pkg/api/routes_concurrency_test.go` providing the regression coverage.

## Commands and results

The commands marked as executed below were run from the repository root on `main`; commands explicitly described as available for reproduction were not necessarily run in this environment.

### 1. Accounts, stale snapshots, and pagination

```bash
go test -race ./pkg/api -run 'Test(HandleListAccounts|HandleGetAccount|.*Cursor.*|.*Pagination.*|.*Accounts.*)' -count=1
```

**Result:** PASS (`github.com/Marcuss-ops/InstaeditLogin/pkg/api`).

Focused provider-isolation checks:

```bash
go test -race ./pkg/api -run \
  'TestHandleGetAccount_(StaleSnapshot_ServesCachedWithoutProviderCall|FreshSnapshot_NoPendingMark)' \
  -count=1
```

**Result:** PASS.

These tests establish that a stale account snapshot is served immediately from local storage, is marked for background refresh, and does not call YouTube during the page-load read path.

### 2. Frontend page-load request/fan-out test

```bash
cd web
npx vitest run src/pages/internal/Linking.perf.test.tsx --reporter=verbose
```

**Result:** 4/4 tests passed.

Observed measurements:

| Accounts | API requests | Total requests | Per-account fan-out | Time to interactive |
|---:|---:|---:|---:|---:|
| 10 | 1 | 2 | 0 | 176 ms |
| 50 | 1 | 1 | 0 | 40 ms |
| 100 | 1 | 1 | 0 | 31 ms |
| 200 | 1 | 1 | 0 | 21 ms |

The test uses mocked HTTP/provider boundaries, so these values validate frontend request topology and rendering behavior, not production network latency.

### 3. OAuth refresh window and concurrency

```bash
go test -race ./internal/credentials \
  -run 'Test(RefreshWindow|Vault_Renew_Concurrent)' -count=1
```

**Result:** PASS.

The tests cover deterministic bounded refresh jitter and concurrent callers sharing an `oauth_connection_id` or grant. The provider refresh counter remains one while the leader is blocked, proving application-level singleflight occurs before duplicate advisory-lock work.

### 4. Upload streaming, limits, and temporary-file cleanup

```bash
go test -race ./pkg/api -run \
  'Test(AdminImportChannelsCSV_(RejectsBodyOverExplicitLimit|SpoolsLargePartAndCleansTemporaryFiles|CleansTemporaryFilesOnValidationError|CleansTemporaryFilesOnMalformedMultipart)|IdempotencyReadBodyBoundsAndClosesOversizedBody|WriteRequestBodyErrorMapsMaxBytesTo413)' \
  -count=1
```

**Result:** PASS.

The checks cover the exercised admin CSV multipart path: explicit request limits, multipart spooling rather than unbounded buffering, cleanup on success and validation/multipart errors, and correct 413 mapping for oversized bodies. They do not by themselves prove that every upload endpoint streams a multi-gigabyte video with constant process RSS; that broader claim requires endpoint-by-endpoint tests and a live memory measurement.

### 5. Cursor pagination

```bash
go test -race ./pkg/api -run 'Test.*(Cursor|Pagination|ListAccounts|Groups)' -count=1
```

**Result:** PASS.

The tested acceptance code paths use bounded list responses and cursor-aware handlers rather than an unbounded page-load response. The concrete tests include `TestListCursorRoundTripAndScope`, `TestParseListPageBounds`, `TestHandleListAccounts_JoinedSnapshotEnrichment`, `TestPostsWorkspaceListCursorHandler`, `TestGroupsAggregateCursorHandler`, `TestGroupsListRejectsCursorFromAnotherScope`, and `TestListJobs_CursorUsesOptionalPagerAndReturnsEnvelope`; these do not substitute for an exhaustive audit of every list resource.

### 6. Atomic claim and empty-queue backoff

```bash
go test -race ./internal/repository -run 'TestClaimBatch.*' -count=1
go test -race ./internal/worker \
  -run 'TestRunPoolLoop_EmptyQueueUsesBoundedBackoff' -count=1
```

**Result:** PASS.

`TestClaimBatch_SQLContract` verifies the production claim SQL contract, including `FOR UPDATE SKIP LOCKED`. Repository empty-claim behavior and worker bounded backoff are covered separately.

The PostgreSQL concurrency test was executed with its Docker-backed testcontainers setup:

```bash
go test -tags=integration -race ./internal/repository \
  -run '^TestClaimBatch_MultipleWorkersDoNotDoubleClaim$' -count=1 -timeout 10m
```

**Result:** PASS in 4.257s.

The test started an ephemeral PostgreSQL 17 container, applied migrations, seeded 24 pending jobs, and ran 8 concurrent workers. It observed 24 distinct claimed IDs, no duplicate claims, and 24 rows in `leased` status. The race detector reported no race.

### 7. Accounts latency benchmark

```bash
cd pkg/api
go test -run '^$' -bench '^BenchmarkHandleListAccounts_100$' \
  -benchmem -benchtime=100x -count=20
```

**Result:** PASS for the handler-only benchmark.

Across 20 runs, the measured `ns/op` values ranged from 117,250 to 167,503. The estimated local p95 was approximately **165,055 ns/op (0.165 ms)**, with 122 allocations/op.

This benchmark uses a fake repository returning 100 account/snapshot rows and directly invokes the handler. It excludes PostgreSQL query execution, connection-pool wait, HTTP middleware, serialization/network transfer outside the recorder, and production contention. Therefore it is supporting evidence, not proof of the production p95 target.

## Tagged E2E result

Command:

```bash
go test -tags=e2e -race -timeout 15m -v ./tests/e2e/...
```

Observed result from the JSON runner output in a clean checkout containing only the acceptance fixes:

- **23 named runner pass events**
- **0 failures**
- **1 intentional skip:** `Test_Z_YouTubeOAuth_EndToEnd_RealBrowser_Smoke` is disabled without live OAuth/browser credentials.

The command was:

```bash
go test -tags=e2e -race -timeout 20m -json ./tests/e2e/...
```

The clean-checkout run included the idempotent E2E schema bootstrap for `account_resource_snapshots` and the `Router.Setup()` lifecycle fix. The previously observed `account_resource_snapshots` relation-not-found failure and `Router.Setup()` race did not recur. The single skipped browser test remains expected because it requires live OAuth/browser credentials.

## Reproduction map

| Goal | Command |
|---|---|
| Page-load request topology | `cd web && npx vitest run src/pages/internal/Linking.perf.test.tsx --reporter=verbose` |
| No provider call on stale snapshot | `go test -race ./pkg/api -run 'TestHandleGetAccount_StaleSnapshot_ServesCachedWithoutProviderCall' -count=1` |
| OAuth singleflight | `go test -race ./internal/credentials -run 'TestVault_Renew_Concurrent' -count=1` |
| Upload limits/cleanup | `go test -race ./pkg/api -run 'TestAdminImportChannelsCSV_' -count=1` |
| Cursor/pagination | `go test -race ./pkg/api -run 'Test.*(Cursor|Pagination)' -count=1` |
| Claim SQL/backoff and PostgreSQL concurrency | `go test -race ./internal/repository -run 'TestClaimBatch.*' -count=1 && go test -race ./internal/worker -run 'TestRunPoolLoop_EmptyQueueUsesBoundedBackoff' -count=1`; `go test -tags=integration -race ./internal/repository -run '^TestClaimBatch_MultipleWorkersDoNotDoubleClaim$' -count=1 -timeout 10m` |
| 100-account handler benchmark | `cd pkg/api && go test -run '^$' -bench '^BenchmarkHandleListAccounts_100$' -benchmem -benchtime=100x -count=20` |
| Full tagged E2E | `go test -tags=e2e -race -timeout 15m -v ./tests/e2e/...` |

## Required follow-ups to close the remaining gaps

1. Run the accounts p95 benchmark against a staging deployment with a real PostgreSQL database, connection pool, HTTP server, and representative 100-channel data. Record p50/p95/p99 and SQL/pool metrics.
2. Keep the reduced E2E schema synchronized with production read-path tables and migrations when new columns are added.
3. Keep `Router.Setup()` as initialization and reuse the returned handler for concurrent requests; the current per-router mutex protects accidental repeated setup calls.

## Worktree safety

This report was created while unrelated local changes were present in:

- `web/src/lib/demo.ts`
- `web/src/pages/internal/Groups.test.tsx`
- `web/src/pages/internal/Groups.tsx`
- `web/src/pages/internal/groupsTypes.ts`
- `web/src/pages/internal/useGroupsData.ts`
- pre-existing deleted Vite result artifact under `node_modules/.vite/`
- unrelated in-progress repository/worker changes present during this follow-up

Those files are intentionally excluded from the acceptance report and E2E fix commits.
