# Integration-slow E2E audit — 2026-07-31

## Status

**Audit classification: unresolved / follow-up required.** This report records the
public GitHub evidence and local verification available on 2026-07-31. It does
not close, relabel, or declare any `flaky-or-broken/e2e` issue resolved.

## Repository state

- Repository: `Marcuss-ops/InstaeditLogin`
- Branch checked: `main`
- `main` / `origin/main` at audit time: `91e8312d95379b0ba558208f10a860e6d18dbd5c`
  (`fix(posts): classify aggregate repair no-op safely`)
- The local worktree contained unrelated uncommitted backend, frontend, test,
  fixture, migration, and utility changes. The local verification below was
  therefore intentionally limited to the already-fixed E2E package/tests and
  must not be interpreted as a clean full-worktree reproduction.

## Public GitHub issue inventory

Read-only unauthenticated GitHub API query:

```text
GET https://api.github.com/repos/Marcuss-ops/InstaeditLogin/issues?state=all&labels=flaky-or-broken%2Fe2e&per_page=100
```

Observed inventory:

- 123 issues carried the `flaky-or-broken/e2e` label.
- 123 were `open`.
- 0 were `closed`.
- No exact duplicate groups were identified by normalizing the automated title
  and grouping by the commit SHA embedded in the title. The issues represent
  separate automated reports for separate SHAs; similar titles alone are not
  evidence that an issue can be closed.

### Issue #112

- URL: <https://github.com/Marcuss-ops/InstaeditLogin/issues/112>
- State: `open`
- Title: `e2e flake OR regression detected on integration-slow @ c0ae8ff (push)`
- Created: `2026-07-31T08:58:47Z`
- Referenced run: `30618130016`
- Referenced commit: `c0ae8ff35b4009f50972bc5c1093f3763d29f740`
- Public comments observed: 0

The issue remains open and was not modified by this audit.

## CI run verification

The relevant public Actions run endpoints reported:

| Run | Commit | Job | Status | Conclusion | URL |
|---:|---|---|---|---|---|
| `30618130016` | `c0ae8ff` | `e2e` | `completed` | `failure` | <https://github.com/Marcuss-ops/InstaeditLogin/actions/runs/30618130016> |
| `30624435603` | `91e8312` | `e2e` | `completed` | `failure` | <https://github.com/Marcuss-ops/InstaeditLogin/actions/runs/30624435603> |

For run `30624435603`, the public jobs endpoint identified:

- Job `e2e`, job ID `91136297427`
- Started: `2026-07-31T10:41:54Z`
- Completed: `2026-07-31T10:42:46Z`
- Conclusion: `failure`

The unauthenticated logs endpoint returned HTTP `403`, so the failing step and
root cause of run `30624435603` could not be confirmed from public data. The
local `gh` CLI was also unauthenticated (`gh auth status` reported no logged-in
GitHub host). Consequently, this report makes no claim that the remaining CI
failure is duplicate, flaky, or resolved.

## Local verification of the targeted fixes

On the same `main` checkout, the following checks passed:

```bash
go test -tags=e2e -run '^$' ./tests/e2e/...
go test -tags=e2e -timeout 15m ./tests/e2e -run \
  'TestOAuthCallback_(NegativeChannelBinding_RefusesMismatch|HappyPath_ConnectLinkBindsExpectedChannel)|TestValidateAccount_E2E_(HappyPath_NoCanary_200|HappyPath_WithCanary_200|Step1_RefreshInvalidGrant_422|Marquee_WrongChannelAtConsent_422)' \
  -count=1
go test ./pkg/api -run 'TestHandleValidateAccount_' -count=1
```

These checks cover the targeted OAuth/validate regressions. They do not replace a
successful clean `integration-slow` GitHub Actions run.

## Required follow-up

1. Authenticate a maintainer's GitHub CLI/token with permission to read Actions
   logs and manage issues (`gh auth login`, or an approved `GH_TOKEN`).
2. Inspect run `30624435603` with:

   ```bash
   gh run view 30624435603 --log
   ```

3. Compare the failing step with issue #112 and the other open reports before
   deciding whether a duplicate can be commented/closed. Do not bulk-close by
   title or label alone.
4. Run a clean `integration-slow`/`make test-e2e` from a clean checkout of
   `main`. Only a completed successful run, with logs showing the relevant E2E
   scenarios, should support a resolved-status update.
5. After authenticated triage, update or close only the specific issue(s) with
   links to the successful run and the root-cause fix commit.

No GitHub issue or workflow was mutated during this audit.
