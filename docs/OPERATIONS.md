# Operations — InstaeditLogin production runbook (DNS + certs + monitoring + recovery)

> **Hub doc for the live `instaedit-login` Fly app + `app.instaedit.org` Vercel project.**
> Owned by the operator team. Every change to DNS, certs, or monitoring surfaces
> here first; `docs/DEPLOY.md` only points to this file for the procedural steps.

This document captures the **operational state** of the InstaeditLogin
production deploy (DNS, TLS, monitoring, recovery drills). It is referenced
from:

- `docs/DEPLOY.md` §1.5 — DNS records (quick-reference table — now includes SPF/DKIM/DMARC for Resend)
- `docs/DEPLOY.md` §2-§5 — deploy pipeline (Postgres + secrets + first deploy)
- `docs/DEPLOY.md` §8 — top-level cross-references index
- `HANDOFF-LINUX.md` §11 — local dev workflow
- `docs/OPERATIONS.md` §7 — email sender (`no-reply@instaedit.org`) deliverability runbook (Resend)

If you change DNS / certs / monitoring, update **this file** and the
relevant `docs/DEPLOY.md` cross-reference. The reverse (changing records
without updating this doc) is the failure mode `OPERATIONS.md` exists to
prevent.

---

## 1. DNS records (`instaedit.org`)

For the canonical table see `docs/DEPLOY.md` §1.5. This section covers the
**why** behind each record + the failure modes that trigger a reissue.

### 1.1 Authority + delegation

| Apex registrar | Domain controller | Notes |
|----------------|-------------------|-------|
| Cloudflare (preferred) | NS `anna.ns.cloudflare.com`, `bob.ns.cloudflare.com`, … | Proxied (orange cloud) is **forbidden** for `api.` and `app.` — disable proxy per record. |
| Namecheap (fallback) | domain basicDNS | ALIAS for apex is opt-in; we use Vercel A records instead for portability. |
| Route 53 (fallback) | alias record `instaedit.org` → Vercel A `76.76.21.21` | Use `A` (not `ALIAS`) as Vercel A already serves the redirect. |

### 1.2 Failure recovery — Fly cert issuance

**Symptoms:** `fly certs show api.instaedit.org` reports `Pending` or `Failure`.
**Root cause:** LE HTTP-01 challenge can NOT reach Fly via the CNAME
because some upstream link failed.

Triage checklist:

```bash
# 1. Confirm the CNAME resolves to Fly
dig +short api.instaedit.org CNAME
# Expected: instaedit-login.fly.dev.

# 2. Confirm Fly is reachable
dig +short instaedit-login.fly.dev A
# Expected: 2-3 Fly IPv4 addresses.

# 3. Inspect Fly cert state
flyctl certs show api.instaedit.org --app instaedit-login

# 4. Re-trigger validation (no-op if already valid)
flyctl certs check api.instaedit.org --app instaedit-login
```

**Common fixes:**

- TTL was 3600s and the previous (wrong) target was cached downstream →
  lower TTL to 60s globally, then wait one old-TTL window before retrying.
- Cloudflare proxy was turned on for `api.` → set to DNS-only (grey cloud).
- Fly app `instaedit-login` was deleted and recreated → CNAME target stale;
  Nuke `api.instaedit.org` records + add `flyctl certs add` again.
- **Storm recovery:** LE has a hard limit of 5 failed validations per
  account per hostname per hour. Wait at least 60 min between retries if
  the failure count is the limiter.

Workaround if the CNAME path is broken beyond quick repair: temporarily
pin `api.instaedit.org` to the IPs from `fly ips list` (A records, no
CNAME) — Fly will detect the A-record switch during the next renewal.
Less ideal but recovers within ~5 min vs waiting for LE rate-limit reset.

### 1.3 Failure recovery — Vercel TXT validation

**Symptoms:** Vercel → Settings → Domains shows `Invalid Configuration`
next to `app.instaedit.org` after the `_vercel` TXT was added.

Triage:

```bash
# Confirm the TXT is reachable globally
dig +short _vercel.instaedit.org TXT
# Expected: "vc-domain-verify=<token>"
```

Common causes:

- Apex resolver cached a stale CAA that excluded letsencrypt → re-add the
  `0 issue "letsencrypt.org"` CAA record (Vercel uses LE).
- The `_vercel` token was typed with whitespace → re-paste from Vercel
  dashboard (read-only after first paste).
- The domain was added to a Vercel **team**, not the project → check
  `vercel teams ls` and re-bind.

Fallback: delete the domain in Vercel, re-add, and obtain a fresh
`vc-domain-verify` token. Vercel only allows one TXT per label so
rotating the token requires deleting first.

### 1.4 Apex CNAME-flattening breaks

CNAME at apex is illegal per RFC. ALIAS / ANAME / CNAME-flattening is
registrar-specific and fragile. We deliberately use:

- Apex `A` → Vercel `76.76.21.21` (Vercel terminates and 301-redirects to `app.`)
- Apex `AAAA` (IPv6) — Vercel supports anycast: leave empty for now; add
  `2606:4700:4700::1111` style address only if validators report IPv6 missing.

If you ever need to migrate registrars (Namecheap → Cloudflare), the
existing records + apex A copy across verbatim. No ALIAS-flattening
magic to replicate.

---

## 2. TLS certificate lifecycle

Both Fly (`api.instaedit.org`) and Vercel (`app.instaedit.org`) issue
Let's Encrypt certs automatically. Renewal windows are 30 days before
expiry. Failure modes:

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| `fly certs show api.instaedit.org` → `expiration < 14d` | Slack `#ops` 7 days before expiry | `fly certs check` + DNS re-validation per §1.2 |
| Browser shows `NET::ERR_CERT_AUTHORITY_INVALID` for `app.` | Sentry capture + Vercel dashboard check | re-add domain in Vercel (regenerates cert) — typically resolves in ~60s |
| Browser shows `NET::ERR_CERT_DATE_INVALID` | Uptime monitor ping fails | Check upstream — REGRESSION-class bug, file incident |

---

## 3. Per-provider recovery drills

Cross-references to the existing recovery scripts:

| Drill | Script / doc | Cadence |
|-------|--------------|---------|
| **Postgres PITR + restore** | [`scripts/db/production-restore-drill.sh`](../scripts/db/production-restore-drill.sh) | First drill within 24h of first migration; then quarterly |
| **Postgres health check** | [`scripts/db/check-postgres-health.sh`](../scripts/db/check-postgres-health.sh) | Pre-deploy + post-deploy + on incident |
| **Tigris bucket provisioning** | [`scripts/s3/provision-tigris.sh`](../scripts/s3/provision-tigris.sh) | One-time at provisioning; re-run on key rotation; re-run on bucket-config drift |
| **Fly app always-on contract** | `docs/DEPLOY.md` §7 (Troubleshooting) | Uptime monitor alerts if /health or /ready down > 2x consecutive ticks |
| **Vercel SPA** | (manual) `curl -I https://app.instaedit.org/connections` returns 200 | On Vercel deploy + on incident |
| **Post-deploy E2E smoke** (Phase 9 sub-1-5+7) | [`scripts/ops/post_deploy_smoke.sh`](../scripts/ops/post_deploy_smoke.sh) | After every `make fly-deploy`; weekly cron once stable |
| **Workspace isolation test** (Phase 9 sub-6) | [`scripts/ops/workspace_isolation_test.sh`](../scripts/ops/workspace_isolation_test.sh) | Before opening beta to external users + on any cross-workspace query refactor |

Per-drill record-keeping paths:

- `ops/restore-drill-<UTC>.md` — Postgres drill reports
- `ops/vercel-deploys-<YYYY-MM>.log` — manual smoke captures
- Sentry issue `INFRA-FLY-CERT-*` / `INFRA-VERCEL-CERT-*` — automated captures

### 3.2 Google Drive import — `capabilities.canDownload=false` runbook

**Symptoms:** A Drive import rejects the file with HTTP 422 (or the
worker pull-path marks the upload_job `failed` with
`ErrDriveNotDownloadable` wrapped in the error).

**Root cause:** Google Drive reports `capabilities.canDownload=false`
when the file is non-downloadable. The InstaEdit import layer fails
fast at this point (Task 5/10 — see `internal/worker/authenticated_drive_source.go::Inspect`
plus `pkg/api/drive_import.go`) and surfaces a 422 to the operator
instead of letting the row burn the publish-pool quota and 403
mid-download.

The most common operational causes (in order of frequency):

1. **Google Workspace DLP rule** stamping the file as
   "download-blocked". Check the org's DLP policy
   (`admin.google.com/ac/security/rules`) — the file is in a
   "restrict download" rule category. Fix: re-apply the file to
   an exclusion rule, OR ask the operator to share a copy of
   the file under a folder NOT covered by the DLP rule.
2. **Information Rights Management (IRM)** on the file. The user
   who owns the file has IRM enabled ("Viewers can't download,
   print, or copy"; the default for some "Confidential" templates).
   Fix: file owner opens Drive → right-click → Manage access →
   toggle IRM off. If the org forbids this, share an unprotected
   copy.
3. **"Viewers and commenters can download" unchecked** in the
   file's share dialog. This is the most common cause on
   consumer Google accounts. Fix: file owner opens Drive →
   Share → "Change to anyone with the link" OR
   "Anyone at <org> with the link" + tick
   "Viewers and commenters can see the option to download".
4. **Drive shortcut pointing at a non-Drive target** (e.g. a
   `application/vnd.google-apps.shortcut` whose target is a
   third-party Box/OneDrive file that Drive can't materialize).
   Drive reports `canDownload=false` for these. Fix: the operator
   pastes the actual native file ID, NOT the shortcut ID; or
   re-imports the file natively into Drive.
5. **File owned by an external account** with a "company-only"
   share restriction that surfaces during a Brand Account grant.
   Fix: file owner re-shares with the operator's account, OR the
   operator provides their own copy of the file.

**Diagnostic flow for the on-call operator:**

```bash
# 1. Confirm the import's HTTP error body / worker error chain
#    mentions capabilities.canDownload=false (NOT a generic 403):
flyctl logs --app instaedit-login --since 15m | grep -i "canDownload\|NotDownloadable"
# If absent, this is NOT Task 5/10 — diagnose via the import endpoint's
# raw error path instead.

# 2. Check the importJobs dashboard in the SPA. The asset row
#    status will be 'failed' with `capabilities.canDownload=false`
#    in the error message. The user_id + drive_file_id on the
#    failed row tell the operator which file to inspect in Drive.

# 3. Have the file owner open the share dialog on the Drive file
#    ID and check the boxes above. Re-attempt the import.
```

**Task 5/10 acceptance bar (verified in CI):** every Drive import

that hits `capabilities.canDownload=false` rejects BEFORE any S3
upload starts, with HTTP 422 (HTTP layer) or upload_job
status='failed' + `ErrDriveNotDownloadable` wrapped in the worker
error chain (worker pull-path). Operators see the failure in
`<30s` (HTTP layer is synchronous; worker tick interval is the
floor). The spec rule "nessun fallback" is enforced — there is
no retry-the-download path that would 403 mid-stream.

---

### 3.1 Postgres PITR drill — canonical step-by-step procedure

This subsection expands the one-line row from §3 (`production-restore-drill.sh`) into the full operator-side choreography. The script itself encodes the assertions (schema fingerprint, fork latency, row counts, sslmode, same-host rejection); this section is the HUMAN-side choreography around it.

#### 3.1.1 Cadence

| Trigger | Frequency |
|---------|-----------|
| **First drill** | Within 24h of the first migration deploy (after `make fly-deploy` exits 0 + scripts/db/check-postgres-health.sh shows `9 canary tables present`). |
| **Baseline** | Quarterly (every 90 days). Track schedule in `ops/restore-drill-cadence.json` (operator-maintained). |
| **On incident** | Within 48h of any operational incident that touched the cluster (failover, manual restart, OOM, lock timeouts > 30s). The drill proves the recovery path STILL works after the incident. |
| **Pre-audit** | 7 days before any external security review (SOC2, ISO27001, etc.) — auditors expect a recent restore drill on file. |

#### 3.1.2 Pre-flight checklist

```bash
# 1. Operator auth + tooling (the drill script refuses to run without them)
flyctl version              # >= 0.10
flyctl auth whoami          # must show your org handle
command -v psql python3     # both must be on PATH
command -v openssl          # for password generation if re-provisioning

# 2. DB-name discipline assertion (CANONICAL production name)
# This MUST read "instaedit-production". If it reads "instaedit_login"
# (dev) or "instaedit_login_test" or anything else, you are pointing at the
# WRONG cluster — abort and re-pull the prod URL from your password manager.
PROD_DSN=$(cat ~/.fly-secrets-database-url-pooled.txt)
echo "$PROD_DSN"
psql "$PROD_DSN" -tA -c "SELECT current_database();"
#   expected: instaedit-production
#   TERRIBLE if: instaedit_login, instaedit_login_test, postgres, template1

# 3. Confirm migrations are at-rest before the drill. The migration runner
#    does NOT maintain a tracking table (each .sql is idempotent IF NOT
#    EXISTS — see internal/database/migrate_check.go line 17); the actual
#    readiness probe is the CanaryTables slice:
#       var CanaryTables = []string{"users","tokens","workspaces","posts",
#                                    "post_targets","webhook_deliveries"}
#    Replicate that probe here so the operator sees the same diagnostic
#    the app's /ready handler reports:
#    NOTE: must mirror internal/database/migrate_check.go::CanaryTables —
#    update BOTH together if the slice grows. A drift here silently makes
#    the query return 0 even when a new canary table is missing.
psql "$PROD_DSN" -tA -c "
  SELECT count(*)
    FROM unnest(ARRAY['users','tokens','workspaces','posts',
                      'post_targets','webhook_deliveries']) t(tbl)
   WHERE to_regclass('public.' || t.tbl) IS NULL;"
#   expected: 0. If > 0 a release_command ./migrate is mid-deploy or
#   failed partway; defer until /health reports 200 AND
#   scripts/db/check-postgres-health.sh exits 0.

# 4. Confirm the api + worker + outbox dispatcher are healthy
curl -i https://api.instaedit.org/api/v1/health
#   expected: HTTP 200 (api + worker both up)
```

#### 3.1.3 Step-by-step procedure

```bash
# ─── STEP 1: create the fork cluster ─────────────────────────────────
TS=$(date -u +%Y%m%dT%H%M%SZ)
FORK_NAME="instaedit-restore-drill-$TS"
flyctl postgres fork \
    --from instaedit-production \
    --to "$FORK_NAME" \
    --region iad
# Wait until Fly prints the POOLED URL of the new fork. It usually takes
# 30-180s — the drill script will reject a stale URL (the fork is not yet
# serving connections).

# ─── STEP 2: pocket the FORK DSN ────────────────────────────────────
# NEVER paste into shell history. `read -rs` keeps it out of `history`.
read -rs FORK_DSN
export FORK_DSN
# Verify sslmode is require (NEVER disable on prod-shaped targets).
[[ "$FORK_DSN" =~ sslmode=require ]] || { echo "FAIL: sslmode not require"; exit 1; }

# ─── STEP 3: run the drill ──────────────────────────────────────────
DATABASE_URL="$FORK_DSN" \
DATABASE_URL_PROD="$PROD_DSN" \
    ./scripts/db/production-restore-drill.sh
# Exit codes:
#   0  drill PASS. The script prints a markdown block ready to paste into
#      ops/restore-drill-$TS.md. Save it BEFORE destroying the fork.
#   1  pre-flight failure (psql/python missing, urls malformed,
#      same host, sslmode=disable on either).
#   3  drill failure (schema fingerprint mismatch, populated fork,
#      latency out of envelope). DO NOT destroy the fork yet — see §3.1.4.

# ─── STEP 4: save the report ────────────────────────────────────────
mkdir -p ops
# The script prints a markdown block on PASS. Copy it into ops/.
# On FAIL: same block with the failure mode under a banner; save BEFORE
# destroying the fork (post-mortem needs the breach/avoidance record).

# ─── STEP 5: destroy the fork ────────────────────────────────────────
# NEVER auto-destroyed — the operator must type the cluster name + --yes
# explicitly. A fat-finger or typosquatted name would hit another
# production-shaped target; the explicit confirmation prompt is the
# safety net.
flyctl postgres destroy --name "$FORK_NAME" --yes

# ─── STEP 6: append to ops/restore-drill-cadence.json ───────────────
# Track the verdict so the next quarterly cadence check is defensible
# ("quarterly is overdue but drilled less than 30d ago"). Schema:
# { "ts": "<UTC>", "verdict": "PASS"|"FAIL", "mode": "<root cause on FAIL>",
#   "operator": "<whoami>@<host>", "fork_destroyed": true|false }
```

#### 3.1.4 Common failure modes

| Symptom | Root cause | Fix |
|---------|------------|-----|
| `FAIL: both DSNs point to the same host:port` | Operator pasted the prod URL into `--fork` (or vice-versa). The drill would have hit prod. | Re-create the fork; re-run with the FORK URI in `--fork` only. The script's host-extraction match is the safety net (not a soft warning). |
| `Schema fingerprint MISMATCH` | (a) fork still spinning up (wait 60s + retry); (b) prod has a live migration in flight (deploy failed mid-migration — `scripts/db/check-postgres-health.sh` reports missing canary tables per [§3.1.2](#312-pre-flight-checklist) step 3; wait for /health=200 then retry); (c) fork was made from a stale PITR snapshot (< 1s drift same fingerprint OK; > 5s drift indicates snapshot lag). | DO NOT destroy the fork on this error. Wait 60s + re-run. If still mismatched after 5 minutes, the snapshot is the suspect — escalate to Fly support with the `prod_fp` + `fork_fp` values from the report. |
| `Fork connect latency > 5000ms` | Fork VM still spinning up OR transient Fly infrastructure issue. | Wait 60s + re-run. > 30s latency typically means the fork hit infrastructure friction; destroy + re-fork if persistent. |
| `curl https://api.instaedit.org/api/v1/health` returns 503 during pre-flight | API VM is mid-rolling-restart. NOT a drill blocker if the 5xx resolves within 30s. | If the API is permanently down, defer the drill until POST-INFRA-INCIDENT — running a drill against a degraded baseline pollutes the report. |
| `~/.fly-secrets-database-url-pooled.txt` doesn't exist or is empty | Operator hasn't provisioned the prod cluster yet (steps 1-7 of `scripts/db/provision-postgres-runbook.sh`). | Defer the drill until after step 8 of the runbook completes AND `make fly-deploy` exits 0. |
| `fly postgres fork` returns `Out of quota` or `permission denied` | The org's PG-quota is exhausted, OR the operator's role lacks `postgres:create`. | Contact org admin; do NOT create a fork through any other channel because the drill script will reject a fork from a non-Fly source (sslmode/discovery drift). |
| `psql "$PROD_DSN" -c "SELECT current_database();"` returns `instaedit_login` | Operator pasted the dev `.env`'s URL into the password manager, OR the dev `.env.production` was generated from a wrong template. | ABORT. Do NOT proceed with a dev-shaped prod URL. Re-pull from `instaedit-login/database-url/production/pooled` in your password manager; if missing, re-provision per `scripts/db/provision-postgres-runbook.sh`. |

#### 3.1.5 Output contract

After the drill completes (PASS or FAIL):

- `ops/restore-drill-<UTC>.md` contains the report block including the `next step` command (always `flyctl postgres destroy --name <fork>` for cleanup).
- A Sentry issue `INFRA-PG-RESTORE-DRILL-*` is filed MANUALLY by the operator ONLY on FAIL (the drill script is pure bash + psql + python3 and does NOT auto-capture to Sentry — the operator reviews the script's `cat` output, decides if the failure mode warrants an infrastructure issue, and files one with the verdict + failure-mode + report-file reference in the body). PASS drills don't generate Sentry noise.
- `ops/restore-drill-cadence.json` (operator-maintained local file) gains a new entry documenting the timestamp + verdict + operator + fork-destroyed flag.

#### 3.1.6 DB-name discipline (production convention)

The canonical production cluster DB name is **`instaedit-production`** — NOT `instaedit_login` (dev), NOT `instaedit_login_test` (test), NOT `postgres` (Fly default), NOT `template1`. This invariant is enforced at THREE layers:

1. **At provisioning** (`scripts/db/provision-postgres-runbook.sh` line 84): `CLUSTER_NAME="instaedit-production"`. Fly Postgres creates the default DB with the same name as the cluster, so DB name = cluster name = `instaedit-production`.
2. **At smoke check** (`scripts/db/check-postgres-health.sh`): asserts the canary tables post-migration exist + the db name contains `prod` (NOT `dev`, NOT `instaedit_login`). Run `psql "$DATABASE_URL" -tA -c "SELECT current_database();"` and confirm the output.
3. **At restore drill** (`scripts/db/production-restore-drill.sh`): the schema fingerprint is `SHA-256(enums ∪ columns ∪ indexes)` — a misconfigured dev cluster would produce a DIFFERENT fingerprint and the drill would FAIL with SCHEMA MISMATCH.

**Anti-pattern**: pasting `postgresql://instaedit:instaedit_dev_pwd@localhost:5432/instaedit_login?sslmode=disable` (from `.env`) into `.env.production`. The parser (`scripts/_parse_envfile.py`) rejects `sslmode=disable` AND the cluster-name mismatch fires on first restore drill, but the API may have ALREADY pulled real production OAuth tokens into a dev-shaped DB before either gate trips (catastrophic privacy violation). The pre-flight check in §3.1.2 step 2 exists specifically to catch this BEFORE any API traffic flows.

---

## 4. Storage (Tigris / `instaedit-prod-media`)

State (after `scripts/s3/provision-tigris.sh --apply`):

- Single-origin CORS: `https://app.instaedit.org` / PUT-GET-HEAD / Expose ETag / MaxAge 3600
- Lifecycle: AbortIncompleteMultipartUpload after 1 day (no orphan parts)
- Versioning: Enabled (audit + accidental-delete recovery)
- TLS-only policy: bucket-policy Denies `s3:*` when `aws:SecureTransport=false`
- Max object size: 200 MB enforced TWICE — bucket policy Denies `PutObject` if `s3:content-length > 209715200`, AND the application clamps the presigned URL `Content-Length` via `STORAGE_MAX_UPLOAD_BYTES = 200 * 1024 * 1024` in `internal/config/config.go`.

### 4.0 Storage recovery drills

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| Browser console: `CORS preflight failed for PUT /uploads/...` | Sentry issues spike from `app.instaedit.org` | Re-run `./scripts/s3/provision-tigris.sh --apply` (drift in CORSRules); if still failing check the Fly-side `VITE_API_BASE_URL` is `https://api.instaedit.org` (NOT `*.fly.dev` preview). |
| Browser console: `413 Request Entity Too Large` from Tigris | Media upload metric spike | Verify `pkg/api/storage.go` STORAGE_MAX_UPLOAD_BYTES = 200 MB; if a user device is bypassing the presigned clamp (e.g. direct CORS upload from presign URL), the bucket-policy DefenseInDepth statement catches it. |
| `aws s3api list-multipart-uploads` returns > 100 entries | (manual) Lifecycle rule is too lenient or unused parts piling up | Bump `AbortIncompleteMultipartUpload.DaysAfterInitiation` from 1 → 0.25 via `./scripts/s3/provision-tigris.sh --apply` (idempotent); confirm the new state with `aws s3api get-bucket-lifecycle-configuration`. |
| `aws s3api get-bucket-policy` denials in `flyctl logs` (TLS / size) | Sentry `storage.policy.deny` capture tag | If the denial is for `aws:SecureTransport=false`, the SDK misconfigured — ad-hoc curl on `:80` of fly.storage.tigris.dev from a non-prod dev machine. If for `NumericGreaterThan`, the upstream uploader sends `> 200 MB` — not an actual bug; expected behavior. |

## 4. Monitoring baselines

### 4.1 Required monitors (set up before inviting users)

- [ ] **Sentry** with `SENTRY_DSN`, `SENTRY_ENVIRONMENT=production`,
      `SENTRY_RELEASE=$(git rev-parse HEAD)`. Captured at panic + 5xx
      emission. Empty == no init (per Blocco #5.3 opt-in).
- [ ] **Uptime monitor** on `https://api.instaedit.org/api/v1/health`
      (30s cron, alert via email after 2 consecutive failures).
- [ ] **Readiness monitor** on `https://api.instaedit.org/ready`
      (Fly handles internally; operator shoulder-check on incident).
- [ ] **Postgres queue-lag alert** (cron query):
      `SELECT count(*) FROM webhook_deliveries WHERE status='queued'
       AND created_at < NOW() - interval '1 hour'` > 100 → alert.
- [ ] **Dead-letter-queue alert**:
      `SELECT count(*) FROM publish_jobs WHERE status='dlq'` > 0 → alert.
- [ ] **Refresh-token-failure alert** (Sentry capture event tag `auth.refresh.failed`).
- [ ] **Log privacy assertion**: `make verify-log-redaction` runs cleanly on the live Fly.io logs in the last 1h (catches runtime leaks that the static CI grep cannot — see §4.3). Recommended cadence: after every `make fly-deploy` + weekly cron.

### 4.2 DNS / email hygiene

- [ ] SPF record for `instaedit.org`: `v=spf1 include:_spf.resend.com ~all`. The 2026 Resend include host is `_spf.resend.com` (with `_spf.` prefix), NOT bare `resend.com`. `~all` (soft-fail) is the right choice during warm-up; flip to `-all` after month 1 of clean delivery. Full canonical record in **§7.1** below.
- [ ] DKIM: Resend dashboard publishes a CNAME; the selector host is `<selector>._domainkey.instaedit.org` (the selector is assigned by Resend per domain; look at the dashboard before pasting). Full shape + 2026 canonical CNAME target in **§7.1** below.
- [ ] DMARC: `_dmarc.instaedit.org TXT` **starts at `p=none`** for the 2-4 weeks warm-up window (not `p=reject` — the 2026 best-practice ramp for brand-new sender domains). The full progression schedule + ramp reasoning is in **§7.2**.
- [ ] CAA per RFC 8659 + this file §1.
- [ ] Gmail inbox deliverability test (using Resend `curl` API + operator's own Gmail address) — exact protocol in **§7.3**.
- [ ] Tracking verification (open + click) — magic-link emails MUST NOT carry Resend's tracking rewrite; protocol in **§7.4**.
- [ ] EMAIL_PROVIDER_KEY captured to password manager (`instaedit-login/email/EMAIL_PROVIDER_KEY`, scope = Sending Access ONLY) — and explicitly NOT pushed to `make fly-secrets` until backend wires Resend. Capture protocol in **§7.5**.

### 4.3 Log discipline (security)

Backend logs MUST NOT include:

- `access_token` / `refresh_token` (raw or encrypted preview)
- `JWT_SECRET` / `ENCRYPTION_KEYS` / `META_APP_SECRET`
- `password=...` from connection strings

Automated guard: `grep -RnE '(refresh_token|jwt_secret|encryption_key|access_token)\\s*=' internal/` returns 0 hits in CI.

---

> **Operator-side Live Log Verifier** (`./scripts/obs/verify-log-redaction.sh`, wired as `make verify-log-redaction`): the static CI grep above proves the CODE doesn't hardcode sensitive variables, but does NOT cover runtime leaks (an operator typo in `slog.Warn("...", "token", token)` would not be caught statically). To prove the *running* deploy doesn't leak, the operator MUST periodically run this script:
>
> ```bash
> make verify-log-redaction         # default: scan --since 1h
> # or explicitly:
> ./scripts/obs/verify-log-redaction.sh --apply --since 24h
> ```
>
> The script streams `flyctl logs --app instaedit-login --since <window>` into a chmod-700 tmpdir (trap-cleaned on EXIT), greps against the canonical 7-pattern list (env var names + values, Resend `re_*` tokens, AWS `AKIA*` access keys, embedded DB URI passwords, literal `password=...`, `csrf_token=<hex>` URL params, `?token=<base64url>` magic-link tokens). It pipes each `grep` hit DIRECTLY into `awk` so the FULL secret-bearing line never enters a shell var; awk truncates to the first 80 chars + appends `***redacted***` so the operator NEVER sees actual captured secrets. Exit 0 if clean / exit 1 with sanitized snippet list + remediation pointers if any pattern hit.
>
> Wire into a weekly cron on the operator laptop so a future regression gets caught without a manual prompt. Cadence: after every `make fly-deploy` + weekly cron + on any `slog.Warn`/`slog.Info` regression PR.

---

## 5. Pre-flight "go-live" gate

Tick all of these before opening the app to real users:

- [ ] Sentry captures a real test panic (then cleared)
- [ ] Uptime monitor on `/api/v1/health` alerts are wired correctly (deliberate downtime test)
- [ ] `/ready` returns 200 within 30s of Fly app boot
- [ ] Queue-lag + DLQ alerts firing on synthetic backlog (then cleared)
- [ ] No `<access_token|refresh_token|password>.*` in `flyctl logs --app instaedit-login` output (privacy check)
- [ ] SPF/DKIM/DMARC all pass `dig +short` for `instaedit.org` ✔
- [ ] Restore drill completed + signed off (see §3 + full procedure in §3.1)
- [ ] DB-name discipline assertion: `psql "$DATABASE_URL" -tA -c "SELECT current_database();"` returns `instaedit-production` (NOT `instaedit_login` dev, NOT `instaedit_login_test` test). Confirmed at provisioning ([§3.1.6](#316-db-name-discipline-production-convention) layer 1) AND at smoke check ([§3.1.6](#316-db-name-discipline-production-convention) layer 2). Full 3-layer enforcement story (provisioning + smoke check + restore drill fingerprint) — see [§3.1.6](#316-db-name-discipline-production-convention).
- [ ] Privacy policy + ToS + data-deletion page reachable (`https://app.instaedit.org/privacy`, `/tos`, `/data-deletion`)
- [ ] Support email `security@instaedit.org` (or whatever was registered) auto-responds in <60s

After ALL 9 boxes ticked the operator flips `APP_ENV=production` secret
from `production` to the auditor's confirmation line + closes the gate.

---

## 7. Email provider runbook (`no-reply@instaedit.org`)

Canonical reference for the Resend-based transactional email sender. Companion to `scripts/email/check-email-deliverability.sh` (read-only DNS verification). **NO app code commits in this section** — the backend does not yet wire Resend (see §7.5 for the deferred wiring plan).

### 7.0 State assertion

After this runbook runs:

- [ ] SPF apex TXT at `instaedit.org`: `v=spf1 include:_spf.resend.com ~all` (warm-up `~all`)
- [ ] DKIM CNAME at `<selector>._domainkey.instaedit.org` → `<selector>.dkim.resend.com.` (selector from Resend dashboard)
- [ ] DMARC TXT at `_dmarc.instaedit.org`: `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` (warm-up `p=none`)
- [ ] Resend dashboard → Domains → `instaedit.org` shows green Verified badge
- [ ] Gmail inbox test passed (Authentication-Results: dkim=pass + spf=pass + dmarc=pass on a real Gmail address; email landed in INBOX not SPAM)
- [ ] `EMAIL_PROVIDER_KEY` captured in password manager `instaedit-login/email/EMAIL_PROVIDER_KEY` (scope = Sending Access ONLY). NOT yet pushed to `make fly-secrets` because the backend does not wire Resend yet.

### 7.1 DNS records (canonical Resend values, 2026)

Operator applies these records via the registrar dashboard (Cloudflare / Namecheap / Route 53). NO provisioning script exists — registrar APIs are heterogeneous and a misclick during provisioning could overwrite the SPF apex with a junk value, breaking all outbound mail. Verify with `./scripts/email/check-email-deliverability.sh` after applying.

| Host | Type | Value | TTL | Purpose |
|------|------|-------|-----|---------|
| `instaedit.org` (apex) | `TXT` | `v=spf1 include:_spf.resend.com ~all` | 3600 | Sender Policy Framework. The include host is `_spf.resend.com` (NOT bare `resend.com` — that was the pre-2024 convention; Resend moved to a `_spf.` sub-include in 2024 for separation of envelope-return SPF). `~all` (soft-fail) is canonical during the warm-up window because Gmail still accepts mail that fails SPF soft-fail; `-all` (hard-fail) would 5xx the first validation round of legitimate mail while the sender reputation is still ramping. |
| `<selector>._domainkey.instaedit.org` | `CNAME` | `<selector>.dkim.resend.com.` | 3600 | DKIM key rotation. The `<selector>` (typically `resend1`, `resend2`) is assigned by Resend when you add the domain. **Look at Resend dashboard → Domains → `instaedit.org` → Records** before pasting — the dashboard prints the actual selector. Make the CNAME target match exactly (`<selector>.dkim.resend.com.` with trailing dot); DNS resolvers normalise trailing dot but Resend's verifier expects the explicit form. |
| `_dmarc.instaedit.org` | `TXT` | `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` | 3600 | DMARC warm-up. `p=none` (no enforcement — just collects reports). Make sure `security@instaedit.org` mailbox exists BEFORE flipping `p=quarantine` (otherwise rua/ruf reports get rejected by your own receiver — a classic ops-blind-spot). |

### 7.2 DMARC progression schedule

The 2026 best-practice for brand-new sender domains enforces a slow ramp because Gmail's DMARC alignment curve is conservative:

| Phase | Days | DMARC policy | Exit condition (verified via Google Postmaster Tools + rua reports) |
|-------|------|--------------|--------------------------------------------------------------------|
| **1. Collect** | 0–28 | `p=none` | At least 2 weeks of rua reports show >99% SPF + DKIM alignment for legitimate mail; no spoofing detected on the apex. |
| **2. Soft-enforce** | 28–42 | `p=quarantine; pct=50` | Half of failing mail moves to SPAM; Postmaster Tools "Domain reputation" tab shows ≥ Medium. |
| **3. Quarantine** | 42–70 | `p=quarantine; pct=100` | 100% of spoofed mail moves to SPAM; no reports of legitimate mail in SPAM. |
| **4. Reject (target)** | 70+ | `p=reject` | Postmaster Tools shows High domain reputation for ≥ 1 consecutive month; FBL (Feedback Loop) loop hooked up. |

**Operator workflow**: register `instaedit.org` on https://postmaster.google.com/ (TMIX requires verifying the apex via a TXT or meta-tag) BEFORE flipping Phase 2 onward — Postmaster gives the per-day IP reputation that's the actual signal. The rua emails go to `security@instaedit.org`; set up an auto-filter + Slack notifier for them.

**Edge case — strict-from-day-one**: if a sibling high-volume SaaS sender already has ≥ 90 days of Gmail reputation on a related apex (rare), `p=reject` from day 1 is acceptable. Document the reasoning in this section.

### 7.3 Gmail inbox test protocol

This is the operator's first concrete verification — runs from the operator's laptop using their own Gmail address. The test MUST pass before inviting any non-operator user.

**Step 1 — pre-flight**: run `./scripts/email/check-email-deliverability.sh` to confirm all 3 records resolve. Exit code must be 0.

**Step 2 — load the API key**: export `EMAIL_PROVIDER_KEY=<re_...>` from the password manager (`instaedit-login/email/EMAIL_PROVIDER_KEY`). NEVER paste into a shell history — use `read -s` instead.

```bash
read -rs EMAIL_PROVIDER_KEY
export EMAIL_PROVIDER_KEY
```

**Step 3 — trigger the canonical test send** (copy-paste; replace `your-test-address@gmail.com` with the operator's actual Gmail):

```bash
curl -X POST "https://api.resend.com/emails" \
  -H "Authorization: Bearer ${EMAIL_PROVIDER_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "InstaEdit <no-reply@instaedit.org>",
    "to": ["your-test-address@gmail.com"],
    "subject": "Log in to InstaEdit",
    "html": "<p>Click the link below to securely log in:</p><p><a href=\"https://app.instaedit.org/verify?token=TEST_PLACEHOLDER\">Login to InstaEdit</a></p><p>Link expires in 15 minutes.</p><p>If you did not request this, ignore this email.</p>",
    "text": "Click to log in: https://app.instaedit.org/verify?token=TEST_PLACEHOLDER (link expires in 15 minutes).",
    "track_opens": false,
    "track_links": false,
    "headers": {
      "Feedback-ID": "instaedit:magic_link",
      "List-Unsubscribe-Post": "List-Unsubscribe=One-Click"
    },
    "tags": [
      {"name": "category", "value": "magic_link_test"}
    ]
  }'
```

Expected response: HTTP 200 + JSON `{"id":"<resend-message-id>"}`. Copy the message id — you'll check it in the dashboard in step 5.

> `track_opens: false` and `track_links: false` are NON-NEGOTIABLE for transactional magic-link emails. Open-pixel is personal data (IP + UA + timestamps) — GDPR/UK-GDPR/PIPEDA-comparable regimes require explicit consent. Link rewriting can strip magic-link token integrity if a third-party proxy logs / caches the rewrite. Frontend `authedFetch` MUST work with the literal `https://app.instaedit.org/verify?token=<plain>` path — the SAME shape that the backend signs into the JWT.

**Step 4 — inspect the email in Gmail**:

1. Open `https://mail.google.com/` (operator's test address), look in INBOX.
2. Confirm the email landed in INBOX (not SPAM, not PROMOTIONS, not TRASH).
3. Open the message → kebab menu → **Show original**.
4. Inspect the `Authentication-Results:` header. MUST contain all three PASSES (any FAIL = see the table below):

```
Authentication-Results: mx.google.com;
        dkim=pass header.i=@instaedit.org header.d=instaedit.org;
        spf=pass smtp.mailfrom=instaedit.org;
        dmarc=pass header.from=instaedit.org action=none;
```

Failure-mode → DNS fix table:

| Header status | Root cause | Fix |
|---------------|------------|-----|
| `dkim=fail (signature body hash not verified)` | DKIM CNAME selector mismatch | Re-paste the DKIM CNAME from Resend dashboard (`<selector>._domainkey.instaedit.org` → `<selector>.dkim.resend.com.`). Verify the selector matches EXACTLY (dashboard prints `resend1` lowercase). |
| `dkim=neutral (no signature)` | DKIM record exists but TTL hasn't propagated to Gmail's resolver yet | Wait 60-300s (depends on TTL), re-send. |
| `spf=softfail` | SPF TXT uses bare `resend.com` instead of `_spf.resend.com`, or uses `-all` during warm-up | Re-paste SPF apex TXT with `include:_spf.resend.com` and `~all`. |
| `spf=neutral (no SPF record)` | TXT at apex missing entirely | Add `v=spf1 include:_spf.resend.com ~all` at apex. |
| `dmarc=fail (SPF or DKIM not aligned with From: domain)` | `instaedit.org` From: differs from `d=` tag in DKIM signature | Confirm Resend is signing with the `instaedit.org` apex (not a subdomain). If your From: is `no-reply@instaedit.org`, the DKIM must sign with `d=instaedit.org` for relaxed alignment — Resend does this by default for sender-domain verification. |
| `dmarc=fail (action=quarantine)` | DMARC is at `p=quarantine` AND SPF or DKIM failed AND < 50% alignment | Move back to `p=none` for 7 days, run more test volume, retry. |

**Step 5 — check Resend dashboard**: open Resend dashboard → Logs → find the message id from step 3 → confirm `email.delivered` event fired within 30s of send. If it's `email.bounced` or sit in `email.sent` without `delivered`, the issue is at the receiver (Gmail); check Gmail's response code in the raw event payload.

**Step 6 — verify tracking is OFF**: back in the email's raw source (`Show original`), confirm:

- The HTML `<a>` tag's `href` is literally `https://app.instaedit.org/verify?token=...`. If you see `href="https://track.resend.com/..."` (or any other Resend tracking host), the `track_links: false` was missing or the API version rejected it — the payload contract has been stable in Resend since 2024 so this would be an operator typo, not a Resend regression.
- The HTML body has no hidden `<img>` tracking pixel at the bottom of the body (an empty `<img src="...">` with no `alt` and `width=0 height=0`). If you see one, `track_opens: false` failed.

### 7.4 Tracking verification

Operational summary of the §7.3 step 6 protocol — what "tracking is off" actually means in 2026 Resend:

- **Open-tracking (pixel)**: a hidden `<img>` at the end of the HTML body that Resend uses to record opens (IP + UA + timestamp). For GDPR / UK-GDPR compliance you must NOT enable this for magic-link emails. Set `track_opens: false`.
- **Click-tracking (rewrite)**: Resend wraps every `<a href>` in a redirect through `track.resend.com` to record clicks. Disabling (`track_links: false`) is REQUIRED for magic-link emails because (a) the magic-link token is a security primitive — you don't want third-party proxy logs of who clicked what when, (b) some corp networks block Resend's tracking domains, which would 5xx an otherwise valid magic-link click.
- **Both options default ON in Resend**: you MUST `false` them on every transactional send. Future backend wiring (see §7.5) MUST set these defaults globally in the Send options for the magic-link + password-reset code paths, NOT per-call, so a refactor mistake doesn't silently flip them back.
- **Webhooks** (out of scope for beta): for production observability of `email.delivered` / `email.bounced` / `email.complained` events, wire a future `pkg/api/email_webhook.go` handler + sign with the HMAC `X-Resend-Signature` header. Defer to a follow-up task — the current beta does not need it because the Resend dashboard already shows all events live.

### 7.5 EMAIL_PROVIDER_KEY capture protocol

The provider key has different capture semantics than the rest of the `.env.production` secrets:

1. **Capture NOW** from Resend dashboard → API Keys → Create API Key.
2. **Scope = `Sending Access` ONLY** (= just `POST /emails`). Do NOT select `Full Access` (= includes domain + webhook management) — minimise blast radius if the key ever leaks.
3. **Save in password manager** under the entry `instaedit-login/email/EMAIL_PROVIDER_KEY`. Format: starts with `re_` (≈ 40 chars).
4. **Do NOT add to `.env.production` yet**. As of (post-commit 58742bf Resend unification), `internal/config/config.go` has no `EmailProvider*` fields; `pkg/api/magic_link.go::handleMagicLinkStart` returns the plaintext token in the response body (marked `// dev-only; production drops via Mailgun/SES`); and `pkg/api/auth_email.go::handleForgotPassword` has `// TODO(FASE 2.2): Send reset token via email` markers. The backend does NOT yet wire Resend — pushing the key into `make fly-secrets` would be a secret that has zero readers, which is worse than no secret (rotation burden without value).
5. **When the backend wires Resend** (separate future task): add `EmailProvider`, `EmailFrom`, `EmailFromName`, `EmailProviderKey` fields to `Config`; wire `internal/services/email_sender.go` (a new file) to dispatch the magic-link / password-reset emails with `track_opens: false`, `track_links: false` defaults baked in. THEN push to `.env.production` + `make fly-secrets`.

> Do NOT paste the key into shell history. `read -rs` + `export` is the safe pattern. Do NOT commit to `.env.production` until step 5 fires.

### 7.6 Recovery drills

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| Browser console: no magic-link email arrives after `POST /api/v1/auth/magic-link/start` | (Dev-mode artifact) API body returns `magic_link_token: <plain>` — backend not wired yet, expected. To capture a real email: drop Resend `curl` from §7.3 into your shell. | Defer real email sending to backend wiring task (§7.5). The current check script + DMARC ramp are the only deliverability you're responsible for today. |
| Resend dashboard shows `domain not verified` (red badge) | Resend dashboard banner | Confirm `./scripts/email/check-email-deliverability.sh` passes (exit 0) for all 3 records; re-trigger verification from Resend dashboard after a TTL window (5 minutes for Cloudflare, up to 1 hour for Namecheap) |
| Gmail inbox test email lands in SPAM (rare for `p=none` warm-up but possible) | Operator's eye on the test send | Inspect raw source for `dkim=pass` but `dmarc=quarantine` or `dmarc=reject` — indicates DMARC is at a more aggressive policy than sender reputation supports. Drop to next-earlier phase in §7.2 for 7 days before retry. |
| `curl` returns `401 Unauthorized` even with the right key format | Operator typo | Resend keys are `re_` then a random base64 url-safe string; ANY prefix other than `re_` (or any trailing whitespace / newline from copy-paste) is invalid. Print the raw length: `${#EMAIL_PROVIDER_KEY}` ≠ 40 chars usually means a stray newline. |
| `dmarc=fail (domain not aligned)` From: header has a different domain than DKIM signature | Operator regression | Update the From: in the `curl` template to use exactly `instaedit.org` parent (not a subdomain like `mail.instaedit.org`). Verify Resend is signing with the registered sender apex (`instaedit.org`), not a related domain. |
| Tracking pixel appears despite `track_opens: false` | (Operator typo) `false` got typed as `False` or `0` | Resend's API is strict-lowercase JSON. `false` (boolean literal) is the only valid value; `"False"` (string) or `0` (integer) are silently IGNORED, falling back to the default (ON). |
| `security@instaedit.org` mailbox doesn't exist | Daily digest missing in Slack | Create the mailbox FIRST (Google Workspace / Fastmail / whatever you use) before flipping DMARC to `p=quarantine` (otherwise rua RUA reports get rejected). The deposit address for the rua/ruf policy is `security@instaedit.org`, NOT `postmaster@`, NOT `abuse@` (those are GROUP addresses, not personal, which complicates auto-routing). |

## 6. Cross-references

| Concern | Reference |
|---------|-----------|
| Fly cluster provisioning + size/HA/PITR/pooler/password | `scripts/db/provision-postgres-runbook.sh` + `docs/DEPLOY.md` §2 |
| Postgres smoke check | `scripts/db/check-postgres-health.sh` |
| Postgres restore drill (script + assertions) | `scripts/db/production-restore-drill.sh` + §3.1 below (canonical step-by-step procedure, cadence, pre-flight, common failure modes, DB-name discipline) | First drill within 24h of first migration; then quarterly; on any incident touching the cluster |
| Tigris bucket provisioning | `scripts/s3/provision-tigris.sh` |
| Post-deploy E2E smoke (Phase 9 sub-1-5+7) | `scripts/ops/post_deploy_smoke.sh` |
| Workspace isolation test (Phase 9 sub-6) | `scripts/ops/workspace_isolation_test.sh` |
| Tigris storage recovery drills | `docs/OPERATIONS.md` §4.0 |
| Email sender DNS records + Gmail inbox test + tracking verification + provider-key capture | `docs/OPERATIONS.md` §7 |
| Email DNS READ-ONLY check (no registrar mutations) | `scripts/email/check-email-deliverability.sh` |
| Provider chosen: Resend (over Postmark) | commit `58742bf` (Resend unification) |
| Backend wiring of EMAIL_PROVIDER_KEY | deferred task — see `docs/OPERATIONS.md` §7.5
| Vercel project settings | `docs/DEPLOY.md` §9 |
| Frontend build-time API URL validator | `web/scripts/verify-api-base-url.ts` |
| Fly doc / API URL contract | `api/openapi.yaml` |
| Cookie / CSRF cross-subdomain semantic | `internal/auth/csrf.go` + `internal/config/config.go` Blocco #2.4 |
| Free-tier provider matrix (TikTok/X/YouTube/LinkedIn/Stripe disabled in beta) | `docs/PROVIDER_MATRIX.md` |

## §10 Worker Recovery (Task 10/10 — final pillar of the Definition of Done)

Task 10/10 wires the operator-triage workflow for all six worker failure-path scenarios. Operators reading this section find the dead-letter endpoint + the recovery metrics they need to spot a worker crash storm before it cascades.

### Dead-letter endpoint (Task 10/10)

| Endpoint | Shape | Notes |
| --- | --- | --- |
| `GET /admin/upload_jobs/dead_letter` | JSON | Up to 500 upload_jobs in `status='dead_letter'`, ordered by `completed_at DESC`. Auth: admin JWT or admin API key. 401/403 for non-admin callers; 501 if the admin store is not wired. |
| `GET /admin/upload_jobs/dead_letter.csv` | CSV | Same row shape, single-row header for spreadsheet import. 501 if the admin store is not wired. |

A row appears in this list ONLY when `MarkDeadLetter` runs, which itself fires from `internal/worker/upload_worker.go::handleProcessingError` when `job.AttemptCount >= job.MaxAttempts` (the retry budget has been exhausted). The operator decides per row: manual retry, cancel, or ignore.

### Recovery metrics (Prometheus)

| Metric | Labels | Description |
| --- | --- | --- |
| `lease_expiry_total` | `source="upload"` (today; `publish`/`ingest` come as the publish pool's reclaim lands) | Worker lease expiries reclaimed by the background reclaimer. An uptick typically means a worker crash mid-flight (heartbeat stopped); the reaper recovers the row so the next pool tick can re-claim it. |
| `resumable_recovery_total` | `reason="worker_restart"\|"chunk_lost"\|"upstream_5xx"\|"upstream_timeout"` | YouTube resumable session recoveries. `worker_restart` is the cold-start expected-rate; `chunk_lost` and `upstream_*` are the alerting signals. Rate > 0.1/min for `chunk_lost` warrants an operator scrapbook entry. |

### Explicit-protection tests (Task 10/10)

Six unit tests live in `internal/worker/task_10_10_recovery_test.go` and each one FAILS when its protection is removed (sqlmock + Prometheus testutil double-check). Coverage matrix:

1. Lease-expiry reclaim — `TestReclaimExpiredLeases_RecoversOrphanedJob` (SQL update + counter delta).
2. YouTube resumable recovery — `TestYouTubeResumableRecovery_FailsIfClearNotCalled` (SaveYouTubeSession + counter delta).
3. Concurrent-claim single-winner — `TestConcurrentClaim_OnlyOneOwner_FailsIfNoAdvisoryLock` (SKIP LOCKED + pg_advisory_xact_lock SQL primitives).
4. publish_at future gate — `TestPublishAtFuture_ClaimGateFiltersBeforePublish` (CTE predicate shape).
5. Worker-retry idempotency — `TestWorkerRetry_Idempotency_KeepsSamePayloadIdempotencyKey` (deterministic key across N attempts).
6. Retry-exhausted dead-letter — `TestRetryExhausted_MarkDeadLetterAndAdminEndpointVisible` (MarkDeadLetter + ListDeadLetterJobs query).

Each test fails in CI if the protection under test is removed — the runbook anchor here is "if you change a worker-side retry / lease / upload path, the matching test should break first".
