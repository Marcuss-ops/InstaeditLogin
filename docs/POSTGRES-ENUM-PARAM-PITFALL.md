# PostgreSQL 42P08 — Enum parameter type-deduction pitfall in the publish/reconcile CAS queries

> Fixed in commit `78b28f54` ("fix: restore production deployment gates",
> 2026-08-08). This document explains **why** Postgres rejects the same
> `$1` parameter with two different type deductions and what the cast
> pattern in `internal/repository/queries.go` does about it.

---

## 1. Failure signature

The worker integration lane (`make test-integration-worker`) failed on every
push from 2026-08-07 19:23 onward (25+ consecutive `integration-fast` runs),
with:

```text
transition to published: update reconcile status for target 1:
  pq: inconsistent types deduced for parameter $1 at position 2:15 (42P08)
```

- The error surfaced in `PostRepository.UpdateReconcileStatusWithLease`
  (`internal/repository/post_repo_reconcile_lease.go`), which executes
  `qUpdateTargetStatusWithReconcileLease` inside a transaction while the
  reconciler drives a `post_targets` row to a terminal state.
- `position 2:15` points at **line 2, character 15** of the SQL statement —
  i.e. the first `$1` in `SET status = $1, ...`. That is the deduction
  origin of the conflict (see below).

### Impact on production deploys

`integration-fast` is the **deploy-gating** workflow: `deploy.yml` only runs
when it concludes `success`, so every red run **skipped the Vercel deploy**.
Production therefore kept serving the last green bundle for hours — which in
the 2026-08-08 incident was a build that still carried the retired
"Copertine" sidebar entry. Green CI is a hard precondition for shipping the
frontend; never assume a push "deploys itself" while this gate is red.

---

## 2. Root cause — why Postgres deduces inconsistent types for one parameter

`post_targets.status` is a PostgreSQL **enum** type (`post_status`). In the
extended query protocol, every `$n` placeholder must resolve to **exactly one
type** before the planner runs. Postgres resolves a parameter's type from the
contexts in which it appears; if two contexts deduce *different* types for the
same placeholder, the query is rejected with SQLSTATE **42P08**.

The old `qUpdateTargetStatusWithReconcileLease` used `$1` in two contexts:

```sql
UPDATE post_targets
 SET status = $1,                          -- context A: assignment to enum column
     ...
     completed_at = CASE WHEN $1 IN ('failed', 'dlq', 'blocked_auth')
                         THEN COALESCE(completed_at, NOW())
                         ELSE completed_at END,   -- context B: compared to string literals
 WHERE ...
```

- **Context A** — `status = $1`: the column is `post_status` (enum), so the
  parameter is deduced as `post_status`.
- **Context B** — `$1 IN ('failed', 'dlq', 'blocked_auth')`: the literals are
  unknown-typed strings, so `$1` is deduced as `text`.

Two deductions, one parameter → `42P08 inconsistent types deduced for
parameter $1`. The same class of bug existed in the sibling query
`qUpdateTargetStatusWithLease`, whose `CASE WHEN $1 = 'publishing'` conditions
created the identical enum-vs-text conflict against `SET status = $1`.

Note that the error reports the *origin* position of the first conflicting
usage (`SET status = $1`), not the `IN` list — a useful debugging hint:
"position 2:15" means "go look at `$1` in the SET clause", even though the
conflict is between that SET clause and a later expression.

---

## 3. The fix — pin both usages to text with explicit casts

```sql
-- qUpdateTargetStatusWithReconcileLease (and qUpdateTargetStatusWithLease)
SET status = $1::text::post_status,        -- parameter = text, then cast to enum
    ...
    completed_at = CASE WHEN $1::text IN ('failed', 'dlq', 'blocked_auth')
                        THEN COALESCE(completed_at, NOW()) ELSE completed_at END,
    lease_owner_id = CASE WHEN $1::text = 'publishing' THEN ... END,
```

- `$1::text` **pins** the parameter to `text` — an explicit cast overrides
  type deduction, so Postgres no longer infers `post_status` from context A.
- `$1::text::post_status` (the column write) casts the text value to the enum
  for the assignment. The double cast is deliberate: `::text` pins the
  parameter, `::post_status` satisfies the enum column.
- Every other usage in the same statement uses `$1::text`, so all deductions
  agree on `text`.

### Do-not rules

These queries are regression-prone by construction:

- **Do not** remove the casts (they look redundant and are not).
- **Do not** add new bare `$1 = '...'` / `$1 IN (...)` conditions to these
  statements — wrap them in `$1::text` (or reuse an existing casted usage).
- If you touch these constants, the sqlmock expectations in
  `internal/repository/post_repo_reconcile_lease_test.go` pin the **exact**
  SQL text and will fail loudly on drift.

---

## 4. Verification

- `PREPARE` + `EXECUTE` of both queries against a real Postgres (16) confirms
  the prepared statements plan without 42P08.
- Worker integration tests pass locally and in CI:
  - `TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished`
  - `TestPublishAndReconcileWorkers_InFlightRetriesAcrossTicks`
- `make verify`, `go build ./...`, `make test-integration-worker` green.
- After the fix, `integration-fast` → `success`, `deploy.yml` → `success`
  (head `78b28f54`), and the frontend bundle reached production.

### Related: in-flight timing assertions

The same commit realigned the in-flight integration test's wall-clock
assertions. In-flight retries call `scheduleInFlight` with
`incrementAttempt=false`, so `reconcile_attempt` stays 0 and every in-flight
poll reuses the **first** backoff slot:

```go
var reconcileBackoffSchedule = [...]time.Duration{ 5s, 15s, 30s, 60s, ... }
```

The transition lower bound in
`TestPublishAndReconcileWorkers_InFlightRetriesAcrossTicks` therefore moved
from 19s (two escalating backoffs) to 9s (two fixed 5s in-flight polls), and
the doc-comment wall-clock map was updated to match. The two tests together
cover every terminal-stable branch of `AsyncPublisher.Reconcile` on a real
DB (happy path + `(nil, nil)` leave-alone in-flight path).

---

## 5. Where this lives in the code

| Item | Location |
| --- | --- |
| Fixed queries | `internal/repository/queries.go` → `qUpdateTargetStatusWithLease`, `qUpdateTargetStatusWithReconcileLease` (with `ENUM PARAM PITFALL` comments inline) |
| Repo call sites | `internal/repository/post_repo_reconcile_lease.go` (`UpdateReconcileStatusWithLease`), publish driver lease CAS |
| Unit pin | `internal/repository/post_repo_reconcile_lease_test.go` (sqlmock exact-SQL expectations) |
| Integration tests | `internal/worker/publish_reconcile_integration_test.go` (testcontainers Postgres + real TikTok service against httptest) |
| Deploy gate | `.github/workflows/integration-fast.yml` (worker lane → `make test-integration-worker`) → `.github/workflows/deploy.yml` (Vercel `--prod`) |
