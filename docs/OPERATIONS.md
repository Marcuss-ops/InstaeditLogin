# Operations — InstaeditLogin production runbook (DNS + certs + monitoring + recovery)

> **Hub doc for the live VPS production stack** (single host, IP
> `51.91.11.36` — Caddy + Docker Compose + Postgres + MinIO + Go API).
> Owned by the operator team. Every change to DNS, certs, monitoring,
> or recovery drill surfaces here first; `docs/DEPLOY.md` only points
> to this file for the procedural steps.

This document is the **index** for the operations documentation set.
The detailed procedures live in the linked documents below; this page
keeps the anchor map (so code comments and scripts referencing
`docs/OPERATIONS.md §N` keep resolving) and the cross-reference table.

**VPS canonical refs:** the live stack is `docker-compose.yml` (topology) +
`ops/vps/Caddyfile` (edge routing) + `docs/DEPLOY.md` (full cutover runbook).
This runbook anchors the VPS-native stack end-to-end. Vercel is the only
supported frontend host; the VPS Compose stack is the only supported backend,
PostgreSQL, and MinIO runtime. Fly, Railway, Render, Tigris, and other
alternative backend-hosting or object-storage paths are retired and are not
operational alternatives. Historical references, when retained, belong under
`docs/archive/` and must not be used as procedures.

## Documentation map

| Document | Contents |
| --- | --- |
| [operations-deploy.md](operations-deploy.md) | **Deploy edge**: §1 DNS records (`instaedit.org`), §2 TLS certificate lifecycle + Caddy reload |
| [operations-monitoring.md](operations-monitoring.md) | **Monitoring**: §5 monitoring baselines (Sentry, uptime, queue-lag, log privacy), §6 pre-flight "go-live" gate |
| [operations-runbook.md](operations-runbook.md) | **Recovery**: §3 per-provider recovery drills (Postgres restore, Drive import), §4 MinIO storage, §10 worker recovery (Task 10/10), §11 open items |
| [operations-email.md](operations-email.md) | **Email**: §7 Resend provider runbook (`no-reply@instaedit.org` — DNS records, DMARC ramp, Gmail inbox test, tracking, key capture) |

## Section anchors (linked from code comments and scripts)

The sections below moved to dedicated documents; the anchors remain
here so references in code comments and scripts (e.g.
"docs/OPERATIONS.md §7", "§3.2", "§5.3") keep resolving.

### §1 DNS records (`instaedit.org`) — [operations-deploy.md](operations-deploy.md#1-dns-records-instaeditorg)

Authority + delegation (§1.1), Caddy/LE HTTP-01 failure recovery (§1.2),
proactive cert renewal (§1.3), apex CNAME-flattening (§1.4).

### §2 TLS certificate lifecycle — [operations-deploy.md](operations-deploy.md#2-tls-certificate-lifecycle)

Failure-mode table + §2.1 Reload Caddy (host-managed systemd service;
edit `ops/vps/Caddyfile`, validate, reload).

### §3 Per-provider recovery drills — [operations-runbook.md](operations-runbook.md#3-per-provider-recovery-drills)

Drill table (Postgres, MinIO, smoke, workspace isolation) + per-drill
record-keeping paths.

#### §3.2 Google Drive import — `capabilities.canDownload=false` runbook — [operations-runbook.md](operations-runbook.md#32-google-drive-import--capabilitiescandownloadfalse-runbook)

Symptoms, 5 root causes (DLP, IRM, share-dialog, shortcut, external
owner), on-call diagnostic flow, Task 5/10 acceptance bar.

#### §3.1 Postgres backup + restore drill — VPS procedure — [operations-runbook.md](operations-runbook.md#31-postgres-backup--restore-drill--vps-procedure)

§3.1.0 script-migration caveat, §3.1.1 cadence, §3.1.2 pre-flight
checklist, §3.1.3 step-by-step (STEP 1–10), §3.1.4 failure modes,
§3.1.5 output contract, §3.1.6 DB-name discipline.

### §4 Storage (MinIO / `instaedit-prod-media`) — [operations-runbook.md](operations-runbook.md#4-storage-minio--instaedit-prod-media)

Endpoint/credentials/CORS/lifecycle/versioning/TLS-only/max-size state +
§4.0 storage recovery drills.

### §5 Monitoring baselines — [operations-monitoring.md](operations-monitoring.md#5-monitoring-baselines)

§5.1 required monitors, §5.2 DNS/email hygiene, §5.3 log discipline
(security) + the operator-side live log verifier.

### §6 Pre-flight "go-live" gate — [operations-monitoring.md](operations-monitoring.md#6-pre-flight-go-live-gate)

Checklist to tick before opening the app to real users.

### §7 Email provider runbook (`no-reply@instaedit.org`) — [operations-email.md](operations-email.md#7-email-provider-runbook-no-replyinstaeditorg)

Resend wiring: §7.0 state assertion, §7.1 canonical DNS records,
§7.2 DMARC progression schedule, §7.3 Gmail inbox test protocol,
§7.4 tracking verification, §7.5 `EMAIL_PROVIDER_KEY` capture protocol,
§7.6 recovery drills.

### §10 Worker Recovery (Task 10/10) — [operations-runbook.md](operations-runbook.md#10-worker-recovery-task-1010--final-pillar-of-the-definition-of-done)

Dead-letter endpoint, recovery metrics, explicit-protection tests.

### §11 Open items — [operations-runbook.md](operations-runbook.md#11-open-items)

Tracked followup commits (log-redaction VPS sourcing, restore-drill
script rewrite, resolved workflow retirement, dropped Makefile target).

### §12 YouTube read-side quota + "YouTube non risponde temporaneamente"

The SPA shows **"YouTube non risponde temporaneamente. Riprova tra
poco."** when `GET /api/v1/groups/{group_id}/youtube/videos` answers
**502**. The handler (`pkg/api/youtube_group_videos_helpers.go` →
`writeGroupVideosOK`) returns 502 only when **every** per-account YouTube
fetch in the group failed. The response body carries the per-account
`warnings[]` plus a `summary` with `failed_accounts` /
`invalid_token_accounts`, and the 502 path emits a diagnostic log line:

```
level=WARN msg="group youtube videos: every account failed (502)" group_id=… total_accounts=… invalid_token_accounts=[…] warnings=[…]
```

(available in the current checkout; rebuild the `api` container to make
it live). **Read the warnings before declaring an outage** — the toast
is a generic "all fetches failed" signal and is frequently NOT a YouTube
outage.

#### §12.1 Root-cause decision tree

| Warning pattern in `warnings[]` / log | Cause | Action |
|---|---|---|
| `status 403 … quotaExceeded` | **Read-side quota exhausted** (see §12.2) | Verify quota in Google Cloud Console; requests succeed again after the midnight-Pacific reset (07:00 UTC during PDT, 08:00 UTC during PST) |
| `vault: decrypt refresh token: … cipher: message authentication failed` | **Vault `ENCRYPTION_KEY` mismatch** — tokens were encrypted with a different key than the running API | Restore the key that encrypted the stored tokens (`ENCRYPTION_KEY` or multi-key `ENCRYPTION_KEYS`, see 2026-08-02 incident below) |
| `invalid_grant` / `status 401` / `token expired` | OAuth token revoked/expired → account flagged `reauth_required` | Complete a fresh OAuth dance for the account |
| `status 429` / timeout / transport error | Transient upstream failure | Retry; escalate if persistent |

#### §12.2 YouTube Data API read-side quota verification

* **Budget**: 10,000 units/day shared read pool **per Google Cloud
  project** (`videos.list` = 1 unit; `search.list` = 100 units/call).
  Uploads draw from the separate 2026 "Video Uploads" bucket — see
  [oauth-google-limits.md](oauth-google-limits.md) — NOT from this pool.
* **Reset**: every day at **midnight Pacific Time** (America/Los_Angeles
  — the 07:00/08:00 UTC instants below are just the UTC conversion of
  that instant, **NOT** a UTC-midnight reset). This is the same
  boundary the app's quota gate uses (`YouTubeQuotaDay` in
  `internal/repository/youtube_quota_repo.go` — midnight LA, never
  `time.Now().UTC().Truncate(24h)`), so the scheduler's per-bucket day
  always matches Google's. Quota does not roll over.
* **Legacy comments in applied migrations**: the SQL comments in the
  already-applied migrations `059_youtube_quota_daily.sql` and
  `124_youtube_quota_buckets.sql` still say "UTC date". Those files are
  **immutable history**: the migration runner stores a SHA-256 checksum
  of the whole file (comments included) in `schema_migrations` and
  hard-fails on any modification, so the comments MUST NOT be edited.
  The authoritative statement is this doc + `YouTubeQuotaDay` in
  `internal/repository/youtube_quota_repo.go`: the `youtube_quota_daily`
  `date` column is keyed by the Pacific calendar date.
* **Verify (Google Cloud Console)**:
  1. https://console.cloud.google.com → select the project → **APIs &
     Services** → **YouTube Data API v3** → **Quotas**.
  2. Check today's usage against the 10,000-unit daily limit. When
     exhausted, every read call answers `403 quotaExceeded` until the
     next reset.
  3. To raise the cap, request a quota increase (Step 6 of
     [oauth-google-setup.md](oauth-google-setup.md)).
* **On-call shortcut**: `docker logs instaedit-api --since 2h | grep -E
  'every account failed|quotaExceeded'` shows whether the last episode
  was quota vs token vs vault.

> **Incident log — 2026-08-02**: the toast appeared on `/app/groups/1`
> with `vault: decrypt refresh token: cipher: message authentication
> failed` on every account. Root cause: the `api` container was rebuilt
> (image `cert-4669`, manual `docker run`) with the `.env` (root)
> `ENCRYPTION_KEY` while the stored YouTube tokens were encrypted with
> the `.env.dev` key. NOT quota, NOT a YouTube outage. Fix: run the API
> with the key that encrypted the tokens (single `ENCRYPTION_KEY` or
> multi-key `ENCRYPTION_KEYS=1:<old>,2:<new>` +
> `ACTIVE_ENCRYPTION_KEY_ID`). Note: `ENCRYPTION_KEY_HISTORY` is not
> read by any Go code — the only supported rotation mechanism is
> `ENCRYPTION_KEYS`.

### §13 Boot resilience: api/worker ensure-up + health monitoring

> **Incident log — 2026-08-18**: `app.instaedit.org` users could not log
> in — the SPA showed `No 'Access-Control-Allow-Origin' header` on the
> login preflight. NOT a CORS bug: `api.instaedit.org` answered **502**
> on every route because **no API listened on `127.0.0.1:8080`**
> (Caddy proxies `api.instaedit.org` → `127.0.0.1:8080`; the 502 body
> carries no CORS headers, which the browser reports as a CORS error).
> Root cause chain: the operator ran `sudo reboot` at 06:01 UTC; after
> boot, `db`/`minio` (running at shutdown) restarted automatically via
> `restart: unless-stopped`, but **`api`/`worker` were in a stopped
> state at shutdown** (they had been stopped earlier without a sudo
> record) and `unless-stopped` does NOT resurrect previously-stopped
> containers across a daemon restart. Production was down for hours
> with no alert. This host reboots most mornings (~06:00-07:30 UTC,
> manual `sudo reboot`) — every reboot is a recurrence opportunity.

**Canonical production invocation on this VPS** (the ONLY one to use):

```bash
cd /home/pierone/Projects/company/InstaeditLogin
INSTAEDIT_ENV_FILE=.env.dev docker compose \
  --env-file .env.dev \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d api worker
```

- `.env.dev` is the canonical operational env file on this host (the
  only complete one: DB, S3, MinIO, OAuth, JWT — verified 2026-08-18;
  `/opt/instaedit/secrets/.env.production` does NOT exist here).
- **NEVER run `make dev` (or the local overlay
  `docker-compose.local.yml`) on this VPS**: it uses the SAME compose
  project (`instaeditlogin`) and rebinds the API to `:8081`, silently
  taking production down (Caddy still proxies `:8080`).

**Ensure-up unit** (`ops/systemd/instaedit-compose.service`, enabled):

```bash
sudo systemctl status instaedit-compose.service   # active (exited) = ok
sudo systemctl start instaedit-compose.service    # re-run manually
```

Runs `docker compose up -d api worker` (production overlay) at every
boot: starts the containers if stopped and **recreates them if their
config drifted** (e.g. after a stray local-overlay run), restoring the
canonical `127.0.0.1:8080` binding. Idempotent no-op on a healthy
stack. Reinstall after edits:

```bash
sudo install -m 0644 ops/systemd/instaedit-compose.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now instaedit-compose.service
```

**Health monitoring** (root cron, every 5 min):

```
*/5 * * * * /home/pierone/Projects/company/InstaeditLogin/scripts/ops/instaedit-health-check.sh
```

`scripts/ops/instaedit-health-check.sh` curls
`https://api.instaedit.org/api/v1/health`; on failure it appends a
**diagnostic line** to `/var/log/instaedit-health.log` and exits 1
(visible to cron):

```
2026-08-18T07:46:52Z DOWN http=502 time=14ms url=https://api.instaedit.org/api/v1/health hint=Caddy upstream down — no listener on 127.0.0.1:8080 (docker ps; systemctl status instaedit-compose.service)
```

`http=` distinguishes the failure classes at a glance: `502` (Caddy
upstream down — the 2026-08-18 incident), `5xx` (API error),
`4xx` (route/auth), `000` (Caddy/DNS/network — host down). `time=` is
the probe duration in ms (a slow-but-200 response is a leading
indicator). **Log-only by design** (operator preference 2026-08-18):
no external notification channel; the file's mtime is the tripwire,
and the `hint=` column is the on-call starting point. The log is
rotated daily by logrotate (`/etc/logrotate.d/instaedit-health`, source
`ops/logrotate/instaedit-health` — 14 compressed copies kept,
`maxsize 1M`). Success is silent (exit 0). Check it manually:

```bash
sudo /home/pierone/Projects/company/InstaeditLogin/scripts/ops/instaedit-health-check.sh; echo $?
sudo tail -5 /var/log/instaedit-health.log   # empty file = no outages

# simulate a failure without touching production (override the URL):
# INSTAEDIT_HEALTH_URL=https://httpstat.us/502 sudo \
#   /home/pierone/Projects/company/InstaeditLogin/scripts/ops/instaedit-health-check.sh
```

**Post-recovery verification** (run after any manual restart):

```bash
curl -fsS https://api.instaedit.org/api/v1/health
curl -fsS https://api.instaedit.org/ready
```

### §14 Log retention (automatic cleanup)

Every log source on this VPS is bounded so nothing can grow the disk
unboundedly (applied 2026-08-18):

| Source | Cap | Enforcement |
|--------|-----|-------------|
| Container logs (api, worker, db, minio, one-shots) | **20 MB × 5 files** = 100 MB/container | `logging:` block in `docker-compose.yml` (`x-logging` anchor) — applies on container (re)creation: `docker compose up -d` |
| Non-compose containers (velox, courserpierone, any future one) | 20 MB × 5 files | `/etc/docker/daemon.json` (source `ops/docker/daemon.json`) — global default, takes effect at next **docker daemon restart** (this host reboots most mornings, so it lands automatically; until then recreated containers use the compose-level config) |
| systemd journal | **500 MB** hard cap, 14 days, compressed, 2 GB free kept | `/etc/systemd/journald.conf.d/zz-retention.conf` (source `ops/systemd/journald-retention.conf`) — active after `systemctl restart systemd-journald`; freed **3.1 GB** on first application |
| Health-check log | 14 compressed copies, `maxsize 1M` | logrotate `/etc/logrotate.d/instaedit-health` (see §13) |

Operational commands:

```bash
# free journal space immediately if it ever hits the cap
sudo journalctl --vacuum-size=500M

# check current usage (must stay ≤ 500M)
journalctl --disk-usage

# current container log ceiling (docker rotates at 20 MB, keeps 5 files)
sudo du -sh /var/lib/docker/containers/*/*-json.log | sort -rh | head

# re-apply container logging config after editing the compose anchor
INSTAEDIT_ENV_FILE=.env.dev docker compose --env-file .env.dev \
  -f docker-compose.yml -f docker-compose.production.yml up -d
```

Note: rotating a container's log does NOT reset it — the running
container keeps writing to the current file until it hits 20 MB, then
rotates. After the docker daemon restarts (next boot), the
`daemon.json` defaults also govern any container that lacks an
explicit compose-level `logging:` block.

---

## 8. Cross-references

| Concern | Reference |
|---------|-----------|
| VPS host setup (SSH + Docker + firewall + `/opt/instaedit/` tree) | [`docs/DEPLOY.md` §2](./DEPLOY.md#2-vps-provisioning) |
| DNS records (canonical: apex + app + api) | [`docs/DEPLOY.md` §1](./DEPLOY.md#1-production-topology-and-dns) |
| Postgres smoke check | [`scripts/db/check-postgres-health.sh`](../scripts/db/check-postgres-health.sh) |
| Postgres backup + restore drill (operatorside choreography) | [operations-runbook.md §3.1](operations-runbook.md#31-postgres-backup--restore-drill--vps-procedure) (VPS pg_dump → throwaway container). **Script rewrite needed** for `scripts/db/production-restore-drill.sh` — tracked in [operations-runbook.md §11 Open items](operations-runbook.md#11-open-items). |
| MinIO bucket provisioning (loopback admin console) | VPS MinIO admin console at `https://127.0.0.1:9001`; env block in `docker-compose.yml` |
| MinIO storage recovery drills | [operations-runbook.md §4.0](operations-runbook.md#40-storage-recovery-drills-minio) |
| Post-deploy E2E smoke (Phase 9 sub-1-5+7) | [`scripts/ops/post_deploy_smoke.sh`](../scripts/ops/post_deploy_smoke.sh) |
| Workspace isolation test (Phase 9 sub-6) | [`scripts/ops/workspace_isolation_test.sh`](../scripts/ops/workspace_isolation_test.sh) |
| Email sender DNS records + Gmail inbox test + tracking verification + provider-key capture | [operations-email.md §7](operations-email.md#7-email-provider-runbook-no-replyinstaeditorg) |
| Email DNS READ-ONLY check (no registrar mutations) | [`scripts/email/check-email-deliverability.sh`](../scripts/email/check-email-deliverability.sh) |
| Provider chosen: Resend (over Postmark) | commit `58742bf` (Resend unification) |
| Backend wiring of EMAIL_PROVIDER_KEY (deferred) | [operations-email.md §7.5](operations-email.md#75-email_provider_key-capture-protocol) |
| Caddyfile source | [`ops/vps/Caddyfile`](../ops/vps/Caddyfile) |
| Frontend build-time API URL validator | [`web/scripts/verify-api-base-url.ts`](../web/scripts/verify-api-base-url.ts) |
| OpenAPI spec | [`api/openapi.yaml`](../api/openapi.yaml) |
| Cookie / CSRF cross-subdomain semantic | `internal/auth/csrf.go` + `internal/config/config.go` Blocco #2.4 |
| Free-tier provider matrix (TikTok/X/YouTube/LinkedIn/Stripe disabled in beta) | [`docs/PROVIDER_MATRIX.md`](./PROVIDER_MATRIX.md) |
| Platform cutover origin (deleted hosted-platform config, dropped hosted-platform Makefile targets, deleted hosted-platform secrets scripts) | commits `7e8beec`, `615314b`, `5ac159c` |
| YouTube read-side quota verification + "YouTube non risponde temporaneamente" (502) troubleshooting | §12 in this file |
| Boot resilience: ensure-up unit + health cron + production invocation discipline (incident 2026-08-18) | §13 in this file + [`ops/systemd/instaedit-compose.service`](../ops/systemd/instaedit-compose.service) + [`scripts/ops/instaedit-health-check.sh`](../scripts/ops/instaedit-health-check.sh) |
| Log retention: container + daemon + journald caps | §14 in this file + [`ops/docker/daemon.json`](../ops/docker/daemon.json) + [`ops/systemd/journald-retention.conf`](../ops/systemd/journald-retention.conf) + `x-logging` anchor in `docker-compose.yml` |
