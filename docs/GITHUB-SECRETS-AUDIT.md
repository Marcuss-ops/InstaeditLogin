# docs/GITHUB-SECRETS-AUDIT.md

## Purpose

Audit the GitHub repository's **Secrets and Variables → Actions**
post-Fly cutover. Identify which Fly-coupled secrets/vars can be safely
removed via the GitHub UI or `gh secret delete`, enumerate orchestrating
files that still contain Fly references (follow-up commits pending), and
re-confirm that no current `.github/workflows/*` actually dereferences
the secret names listed below.

**Date**: 2026-07-25 (post-VPS cutover; main HEAD is past the
documentation cleanup chain — `cmd/oauth-scope-canary` slimmed, Makefile
Fly targets dropped in commit `646146d`, `Dockerfile` Fly stage dropped,
`docs/DEPLOY.md` rewritten as VPS-only.)

**Status**: Cutover code changes already on `main`. GitHub UI side pending
operator action. Tigris bucket (separate code path) is **explicitly out of
scope** of this audit per the cutover plan (handled in §6 of TOMORROW.md).

---

## 1. GitHub Secrets SAFE TO REMOVE (Fly platform tokens)

These were used exclusively by the `flyctl` deploy pipeline that was
removed across the cleanup chain (Makefile, Dockerfile, legacy integration.yml (retired at this commit)
`Verify Fly secrets parser` step, oauth-canary's `verifySecretCoherence`
logic). They are **not referenced by any current `.github/workflows/*`**.

| Secret/Var            | Why it can go                                                            | Workflow ref grep |
| --------------------- | ------------------------------------------------------------------------ | ----------------- |
| `FLY_API_TOKEN`       | Fly deploy path is gone; no remaining `flyctl deploy` in CI              | 0 matches         |
| `FLY_ACCESS_TOKEN`    | Same                                                                    | 0 matches         |
| `FLY_APP_NAME`        | Same (was `instaedit-login`); the only consumer was `flyctl apps ...`    | 0 matches         |

The above three are **Secrets**. There are also two **GitHub repository
variables** that were used by `cmd/oauth-scope-canary`'s now-removed
`verifySecretCoherence` / `required-fly-secrets.txt` /
`disabled-fly-secrets-prefixes.txt` parsing logic. Delete via
`Settings → Variables`, **not** `Secrets`:

| Variable                                | Why it can go                                              | Workflow ref grep |
| --------------------------------------- | ---------------------------------------------------------- | ----------------- |
| `INSTAEDIT_REQUIRED_SECRETS_PATH`       | Was `docs/archive/legacy-fly/required-fly-secrets.txt`; legacy logic removed | 0 matches         |
| `INSTAEDIT_DISABLED_SECRETS_PATH`       | Was `scripts/disabled-fly-secrets-prefixes.txt`; same      | 0 matches         |

Confirmed via ripgrep across `.github/workflows/`:

```bash
grep -rnE 'secrets\.FLY_|FLY_API_TOKEN|FLY_ACCESS_TOKEN|FLY_APP_NAME' \
  .github/workflows/
# (no output — 0 matches)
```

If the user previously also staged `INSTAEDIT_REQUIRED_SECRETS_PATH` /
`INSTAEDIT_DISABLED_SECRETS_PATH` as **GitHub repository variables**
(pointing to the archived Fly fixtures under
`docs/archive/legacy-fly/` and the legacy disabled-prefix list respectively),
they can be removed too — no current deploy or runtime reads them. The
compatibility parser regression in `integration-fast.yml` is the only
remaining automated reader.

---

## 2. Removal command (operator-side, runs from a laptop)

```bash
# Detect repo slug from local git remote. Handles https URLs with or
# without `.git` suffix, plus git@github.com:owner/repo.git SSH form.
# If you can't derive the slug (e.g., GitHub Enterprise or non-Git
# remote), skip §2 and use the GUI fallback in §6.
SLUG=$(git remote get-url origin \
  | sed -E 's#.*github\.com[:/]([^/]+/[^/]+?)(\.git)?$#\1#')

# Step A — confirm they exist BEFORE deletion (read-only).
echo "── Existing FLY secrets + INSTAEDIT_PATH variables in repo $SLUG ──"
gh secret list   --repo "$SLUG" | grep -E 'FLY_'       || echo "  secrets:   (none)"
gh variable list --repo "$SLUG" | grep -E '^INSTAEDIT_' || echo "  variables: (none)"

# Step B — delete each Fly secret.
for S in FLY_API_TOKEN FLY_ACCESS_TOKEN FLY_APP_NAME; do
  gh secret delete "$S" --repo "$SLUG"
done

# Step B′ — delete each Fly-coupled repository variable. The 2 vars
# formerly pointed to the archived Fly fixture and the legacy disabled
# prefix list. Both are runtime-dead; the compatibility regression is
# the only remaining automated reader, so we surface "already gone"
# instead of a silent green (keeps idempotent re-runs debuggable and
# surfaces auth / network blips as named output).
for V in INSTAEDIT_REQUIRED_SECRETS_PATH INSTAEDIT_DISABLED_SECRETS_PATH; do
  gh variable delete "$V" --repo "$SLUG" \
    || echo "  (already gone, or auth-flaky: $V)"
done

# Step C — re-verify AFTER deletion.
echo "── After: FLY secrets AND INSTAEDIT_PATH variables should be EMPTY ──"
gh secret list   --repo "$SLUG" | grep -E 'FLY_'       || echo "  ✓ secrets:   clean"
gh variable list --repo "$SLUG" | grep -E '^INSTAEDIT_' || echo "  ✓ variables: clean"
```

If `git remote get-url origin` is non-GitHub (e.g., SSH-only local alias),
do the same operations via the GitHub web UI:
**Settings → Secrets and variables → Actions → delete FLY\_\***.

---

## 3. Fly references that STILL exist in the repo (follow-up commits pending)

The audit grep found these files still contain Fly-coupled content. None
are **critical blockers** for the secrets removal — they are
documentation, comments, or one-shot orchestrators that can be cleaned
in separate commits after §1/§2.

| File                                                         | Refs                                                              | Status / recommendation                                                                       |
| ------------------------------------------------------------ | ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `TOMORROW.md`                                                | 6 `flyctl` references (lines 97, 98, 99, 101, 108, 744, 760)       | Working doc with historical Fly cutover notes. Either `git rm` or annotate as ARCHIVED.        |
| `internal/config/config.go`                                  | 2 doc-comments (lines 134, 464) saying "rotate via flyctl secrets import" | In-code documentation that points to a tool no longer in use. Update to "edit VPS .env + restart container". |
| `docs/archive/legacy-fly/destroy-fly-app.sh`                                 | 22 `flyctl` invocations                                           | Historical archive only; non-executable after the VPS cutover. No current deployment invokes it.                       |
| `docs/archive/legacy-fly/provision-postgres-runbook.sh`      | Historical Fly Postgres provisioning runbook                     | Archived and non-operational after the VPS cutover. Current provisioning is Docker Compose PostgreSQL on the VPS; no runbook, CI, deploy, or runtime invokes this file. |
| `scripts/db/production-restore-drill.sh`                     | 3 `flyctl postgres destroy` references                            | Disaster-recovery drill for Fly Postgres. Rewrite for local Postgres.                          |
| `docs/archive/legacy-fly/clean-gh-fly-secrets.sh`          | Historical helper for the three FLY secrets                           | Archived and non-operational after the VPS cutover. No current runbook, CI, deploy, or runtime invokes it; use the manual GitHub UI/`gh` cleanup instructions above only after independent operator confirmation. |
| `docs/archive/legacy-fly/provision-tigris.sh`                | Historical Tigris bucket provisioning helper                    | Archived and non-operational; MinIO + Docker Compose on the VPS is the canonical object-store path. No current deployment invokes it. |
| `docs/archive/legacy-fly/PASTE-BACK-FLY-DESTROY.md`           | Historical Fly destroy paste-back checklist                     | Archived and non-operational; the canonical operational path is the VPS Docker Compose stack. No current deployment, CI, or runtime invokes it. |
| `scripts/_parse_envfile.py`                                  | Reads the archived Fly fixture as a compatibility fallback | Historical parser retained only for the CI regression; no deploy/runtime caller. |
| `scripts/test_parse_envfile.py`                              | Reads the archived fixture in CI                               | Compatibility regression test; not an operational secret push.                |
| `docs/archive/legacy-fly/required-fly-secrets.txt`            | Archived Fly-secrets contract spec                             | Historical, frozen fixture; no current deployment or runtime use.              |
| `scripts/disabled-fly-secrets-prefixes.txt`                  | Fly disabled-provider prefix list                                | Reject-list contract. `git rm`.                                                                 |
| `scripts/ops/post_deploy_smoke.sh`                           | 3 `flyctl logs --app instaedit-login` references (lines 98, 302, 334) | Smoke test references flyctl. **Rewrite** to use `docker compose logs api worker` (same pattern as `scripts/obs/verify-log-redaction.sh`). |
| `docs/ENDPOINTS.md` line 105                                 | "Production (Fly): var is a secret; set via `flyctl secrets set`" | Documents a Fly-specific rotation path. Update to "Production (VPS): secret managed via `/etc/instaedit/api.env`; rotation = edit + `docker compose restart api`". |
| `docs/DEPLOY-AUDIT.md` line 141                              | `flyctl status` mentioned in "active investigation"               | Historical audit-doc. Annotate as closed (cutover complete).                                  |
| `.github/workflows/deploy.yml` (lines 37, 43)                | "Required GitHub repo secrets" metadata block listing FLY\_\*      | After §1/§2 deletion, the metadata block is wrong-by-default. Remove the block, or delete the workflow if post-cutover deploy is VPS-only. |

> **Snapshot note**: line numbers above are from the audit on 2026-07-25.
> After other commits land, refs may drift — re-run ripgrep against each
> file (e.g. `grep -nE 'flyctl' TOMORROW.md`) to confirm current counts.

The required Fly-secrets contract is now archived at
`docs/archive/legacy-fly/required-fly-secrets.txt`. The parser and its test
remain only as a compatibility regression until the legacy Fly contract is
fully retired; neither is an operational deployment path. The disabled
prefix list and other Fly helpers remain separate follow-up cleanup items.

---

## 4. Cross-confirmation — no workflow dereferences FLY\_\* after the cutover

Final grep, full repo, on-line source:
```bash
grep -rnE 'secrets\.FLY_|FLY_API_TOKEN|FLY_ACCESS_TOKEN|FLY_APP_NAME|\${{ vars\.FLY_' \
  .github/workflows/
# (no output — 0 matches; the only remaining refs are documentation
#  headers in deploy.yml, not actual `secrets.*` dereferences)
```

A more cautious check that traces **all** Fly-coupled variables including
the env-file pointers:
```bash
grep -rnE 'INSTAEDIT_REQUIRED_SECRETS_PATH|INSTAEDIT_DISABLED_SECRETS_PATH|required-fly-secrets\.txt|disabled-fly-secrets-prefixes\.txt' \
  .github/workflows/
# expected: 0 matches after cleanup chain (was 1 in legacy integration.yml pre-cutover; file retired at this commit)
```

---

## 5. KEEP these secrets/vars (NOT Fly-coupled)

The following are application-layer OAuth / API credentials and per-service
rotation paths; do NOT delete from Settings:

- `DRIVE_OAUTH_CANARY_TOKEN` (used by `cmd/oauth-scope-canary` /
  `.github/workflows/oauth-canary.yml`'s Google scope drift check; not Fly)
- Application OAuth credentials — any `*_CLIENT_ID` /
  `*_CLIENT_SECRET` / `*_CLIENT_KEY` (e.g. `TIKTOK_CLIENT_KEY`,
  `YOUTUBE_CLIENT_ID`, `LINKEDIN_CLIENT_SECRET`, etc.). These are app-layer
  secrets, not platform tokens.
- `S3_ACCESS_KEY` / `S3_SECRET_KEY` / **`S3_BUCKET` / `S3_ENDPOINT`** —
  used by the VPS MinIO and any CI-side integration tests; not Fly.
- **Security-critical app secrets**: `JWT_SECRET`, `ENCRYPTION_KEY` /
  `ENCRYPTION_KEYS`. Rotation is via ops-direct edit + `docker compose
  restart` — these are **not** Fly platform tokens; do NOT route them via
  `gh secret delete`.
- **`SENTRY_DSN`** (used by `internal/bootstrap/sentry_wiring*` for
  observability; rotation is via the Sentry dashboard, not GitHub).

Verify via:
```bash
# Secrets surface (after §2 removed the Fly tokens, anything left is
# a Keep-secret; do NOT touch):
gh secret list   --repo "$SLUG" | grep -v -E '^(FLY_|FLY)'

# Variables surface (after §2 removed the 2 oauth-canary INSTAEDIT_*
# variables, anything left is a Keep-variable; do NOT touch):
gh variable list --repo "$SLUG" | grep -v -E '^INSTAEDIT_(REQUIRED|DISABLED)_SECRETS_PATH$'
```

---

## 6. Post-cleanup checklist (operator's terminal)

First-time prep (per laptop):
```bash
gh auth login   # if not already authed; required for §2
```

After completing §1 + §2, mark these off:

- [ ] `gh secret list --repo "$SLUG" | grep '^FLY_'` returns empty
- [ ] Open a PR with the §3 file remediations: 4 `git rm` + 2 rewrite
      (`scripts/ops/post_deploy_smoke.sh` and `docs/ENDPOINTS.md` line 105)
- [ ] Run `scripts/verify-log-redaction.sh` once on the VPS to confirm
      the redaction discipline post-cutover (uses `docker compose logs`,
      not `flyctl logs`, per the parallel rewrite)
- [ ] `scripts/ops/verify-tiktok-oauth-e2e.sh <workspace_id>` from the
      operator's machine: confirm TikTok OAuth E2E green → close out the
      PENDING probe-log row in `docs/VPS-DEPLOY-STATUS.md`
- [ ] Verify `https://api.instaedit.org/api/v1/health` returns `200` and
      `server: Caddy` (not Fly)

When all 6 are green, the Fly cutover is fully complete from the GitHub
secrets perspective. The §3 file remediations are follow-ups, not blocking.
