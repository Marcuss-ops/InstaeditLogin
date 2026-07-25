# Operations — InstaeditLogin production runbook (DNS + certs + monitoring + recovery)

> **Hub doc for the live VPS production stack** (single host, IP
> `51.91.11.36` — Caddy + Docker Compose + Postgres + MinIO + Go API).
> Owned by the operator team. Every change to DNS, certs, monitoring,
> or recovery drill surfaces here first; `docs/DEPLOY.md` only points
> to this file for the procedural steps.

This document captures the **operational state** of the InstaeditLogin
production deploy (DNS, TLS, monitoring, recovery drills). It is
referenced from:

- `docs/DEPLOY.md` §1.5 — DNS records (quick-reference table — apex/app/api → `51.91.11.36` + email-deliverability records)
- `docs/DEPLOY.md` §2-§7 — deploy pipeline (host setup + secret collection + first deploy + post-deploy verification + rotation + Sandbox/operator boundary)
- `docs/DEPLOY.md` §11 — open items (verify-log-redaction → docker compose logs, orphan `docker-build-production`, integration.yml `fly-secrets-test` job)
- `HANDOFF-LINUX.md` §11 — local dev workflow
- `docs/OPERATIONS.md` §7 — email sender (`no-reply@instaedit.org`) deliverability runbook (Resend)

If you change DNS, certs, monitoring, or drill cadence, update **this
file** and the relevant `docs/DEPLOY.md` cross-reference. The reverse
(changing operational state without updating this doc) is the failure
mode `OPERATIONS.md` exists to prevent.

> **Note on the cutover.** The historical Fly.io + Vercel production
> stack was retired in commits `7e8beec` (removed `fly.toml`),
> `615314b` (stripped `fly-*` Makefile targets), and `5ac159c`
> (deleted `scripts/set-fly-secrets.sh` and friends). This runbook
> was rewritten to live with the VPS stack; residual Fly/Vercel
> references in this file are intentional historical framing only,
> or open-items markers tracked in `docs/DEPLOY.md` §11.

---

## 1. DNS records (`instaedit.org`)

For the canonical table see `docs/DEPLOY.md` §1.5. This section covers
the **why** behind each record + the failure modes that trigger a
reissue.

### 1.1 Authority + delegation

| Apex registrar | Domain controller | Notes |
|----------------|-------------------|-------|
| Cloudflare (preferred) | NS `anna.ns.cloudflare.com`, `bob.ns.cloudflare.com`, … | Proxied (orange cloud) is **forbidden** for `api.`, `app.`, and `instaedit.org` apex itself — disable proxy per record. Caddy terminates TLS via LE HTTP-01 against `http://51.91.11.36/.well-known/acme-challenge/...`; orange-cloud would intercept the challenge. |
| Namecheap (fallback) | domain basicDNS | Plain A records for apex + app + api → `51.91.11.36`. No ALIAS-flattening needed. |
| Route 53 (fallback) | A records for apex + app + api → `51.91.11.36` | Plain A (not ALIAS). The DNS spec disallows CNAME at apex; with a single A record this is unambiguous and registrar-portable. |

### 1.2 Failure recovery — Caddy / Let's Encrypt HTTP-01

**Symptoms:** `curl -sI https://api.instaedit.org/health` returns `server:` other than `Caddy`, OR returns a Caddy error page mentioning `acme`, OR the cert is older than the expected auto-renew window (60 days).

**Root cause:** LE HTTP-01 challenge could not reach the VPS on port 80 + path `/`.well-known/acme-challenge/...`.

Triage checklist (from the operator laptop):

```bash
# 1. Confirm DNS resolves to the VPS
dig +short instaedit.org      A    # expect: 51.91.11.36
dig +short app.instaedit.org   A    # expect: 51.91.11.36
dig +short api.instaedit.org   A    # expect: 51.91.11.36

# 2. Confirm the Caddy docker container is up + listening
ssh instaedit@$VPS_IP 'docker compose -f /opt/instaedit/InstaeditLogin/docker-compose.yml ps caddy'
#   expect: status=running, ports 0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp

# 3. Confirm Caddy can serve the LE challenge path from the public IP
ssh instaedit@$VPS_IP 'docker compose logs --tail=200 caddy | grep -i "acme\|certificate\|renew"'

# 4. From external internet (operator laptop), confirm a known 200 path
curl -fsS https://api.instaedit.org/api/v1/health | jq
#   expect: {"status":"ok","service":"InstaEditLogin",...}
```

**Common fixes** (all commands run via `ssh instaedit@$VPS_IP` unless noted):

- The previous (wrong) A record was cached downstream → lower TTL to 60s globally, wait one old-TTL window before retrying. Caddy renews nightly; the next renewal cycle catches the corrected target.
- Cloudflare proxy was turned on for the affected name → set to DNS-only (grey cloud). Check `/etc/caddy/Caddyfile` for the apex name and which Cloudflare proxy records cover it.
- Firewall on the VPS blocks TCP/80 or TCP/443 → `sudo ufw allow 80/tcp && sudo ufw allow 443/tcp && sudo ufw reload`. Confirm with `sudo ufw status`.
- Caddy's `/data` (caddy_data volume) is full or corrupt → `docker compose stop caddy && rm -rf /srv/instaedit/caddy_data/{acme,caddy} && docker compose up -d caddy` triggers a fresh LE issuance on the next start.
- **Storm recovery:** LE has a hard limit of 5 failed validations per account per hostname per hour. Wait at least 60 minutes between retries if the failure count is the limiter.

Workaround if the VPS is unreachable beyond quick repair: temporarily
flip the A record at the registrar to a known-good Caddy origin (e.g.
an emergency standby host) — Caddy will renew against the new target
on the next cycle.

### 1.3 Cert renewal — proactive (was: "Vercel TXT validation")

**Symptoms:** nothing — Caddy renews silently ~30 days before expiry.
We watch the cert state via `docker compose logs caddy | grep cert`.

Triage (operator-on-call cadence: weekly 5-minute check):

```bash
ssh instaedit@$VPS_IP 'docker compose logs --since 168h caddy | grep -iE "renew|certificate|expir"'
#   expect: "certificate obtained successfully" OR "renewing certificate"
#   failure: no renewal lines in the last 7 days → Caddy rejected renewal
```

Common causes:

- An IP-cycle happened (e.g. VPS redeployed) and the A record is stale → fix DNS and let Caddy re-discover.
- The Caddyfile lost a previously listened name → compare against `git log main -- ops/vps/Caddyfile` and re-add the missing `instaedit.org` / `*.instaedit.org` SNI block.
- The VPS port 80 (LE HTTP-01) was blocked mid-renewal → see §1.2 step "firewall".

### 1.4 Apex CNAME-flattening breaks

CNAME at apex is illegal per RFC. ALIAS / ANAME / CNAME-flattening is
registrar-specific and fragile. We deliberately use:

- Apex `A` → `51.91.11.36` (Caddy terminates and 301-redirects to `app.`)
- Apex `AAAA` (IPv6) — leave empty until validators report IPv6 missing.

If you ever need to migrate registrars (Namecheap → Cloudflare), the
existing records + apex A copy across verbatim. No ALIAS-flattening
magic to replicate.

---

## 2. TLS certificate lifecycle

Caddy on the VPS issues a single LE cert for `instaedit.org`,
`app.instaedit.org`, `api.instaedit.org` (SNI selects). Renewal windows
are 30 days before expiry; Caddy auto-renews every ~60 days. Failure
modes:

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| `curl -sI https://api.instaedit.org/api/v1/health` returns `Server:` other than `Caddy` | Sentry `tls.origin` capture OR uptime monitor | Re-check §1.2 — DNS + firewall + caddy_data state |
| Browser shows `NET::ERR_CERT_AUTHORITY_INVALID` for `app.` or `api.` | Sentry capture + manual verification | Caddy renewal drifted to a provider that's not LE — inspect `docker compose logs caddy | grep issuer`; CA bundle missing from Caddy image (rebuild `docker build`); or DNS CAA excludes LE |
| Browser shows `NET::ERR_CERT_DATE_INVALID` | Uptime monitor ping fails | Check upstream — REGRESSION-class bug, file incident |
| Caddy logs show `failed to obtain certificate: acme: error: ... rateLimited` | Sentry capture within an hour of the failure | LE rate-limit hit. See §1.2 storm-recovery hint. |

---

## 3. Per-provider recovery drills

Cross-references to the existing recovery scripts:

| Drill | Script / doc | Cadence |
|-------|--------------|---------|
| **Postgres backup + restore** | [`scripts/db/production-restore-drill.sh`](../scripts/db/production-restore-drill.sh) — *still Fly-Postgres-shaped; rewrite required for VPS (§3.1 below)* | First drill within 24h of first migration; then quarterly |
| **Postgres health check** | [`scripts/db/check-postgres-health.sh`](../scripts/db/check-postgres-health.sh) | Pre-deploy + post-deploy + on incident |
| **MinIO bucket provisioning** | (was Tigris `scripts/s3/provision-tigris.sh`) — covered by the Compose service + MinIO admin console at `https://127.0.0.1:9001` (loopback only) | One-time at provisioning; re-run on key rotation |
| **Stack always-on contract** | `docker compose -f /opt/instaedit/InstaeditLogin/docker-compose.yml ps` | Uptime monitor alerts if `/health` or `/ready` down > 2x consecutive ticks |
| **SPA-reachable check** | (was Vercel `curl -I https://app.instaedit.org/connections`) — now `curl -fsSI https://app.instaedit.org/` returns `server: Caddy` | On VPS deploy + on incident |
| **Post-deploy E2E smoke** (Phase 9 sub-1-5+7) | [`scripts/ops/post_deploy_smoke.sh`](../scripts/ops/post_deploy_smoke.sh) | After every `git pull && docker compose up -d --build` on the VPS; weekly cron once stable |
| **Workspace isolation test** (Phase 9 sub-6) | [`scripts/ops/workspace_isolation_test.sh`](../scripts/ops/workspace_isolation_test.sh) | Before opening beta to external users + on any cross-workspace query refactor |

Per-drill record-keeping paths (now on the VPS, not central 1Password):

- `ops/restore-drill-<UTC>.md` — Postgres drill reports
- `ops/smoke-<UTC>.log` — manual smoke captures
- Sentry issue `INFRA-CADDY-CERT-*` / `INFRA-COMPOSE-DOWN-*` / `INFRA-PG-RESTORE-DRILL-*` — automated captures

> **Note on `scripts/s3/provision-tigris.sh`** — the script is a
> git-tracked historic. It is NOT used by the VPS production stack.
> If migrating historical Tigris data into MinIO, see
> `docs/DEPLOY.md` §10 (Tigris retirement). The Tigris retirement
> path is optional — the production stack is MinIO from day one.

### 3.2 Google Drive import — `capabilities.canDownload=false` runbook

**Symptoms:** A Drive import rejects the file with HTTP 422 (or the
worker pull-path marks the upload_job `failed` with
`ErrDriveNotDownloadable` wrapped in the error).

**Root cause:** Google Drive reports `capabilities.canDownload=false`
when the file is non-downloadable. The InstaEdit import layer fails
fast at this point (Task 5/10 — see
`internal/worker/authenticated_drive_source.go::Inspect` plus
`pkg/api/drive_import.go`) and surfaces a 422 to the operator instead
of letting the row burn the publish-pool quota and 403 mid-download.

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

**Diagnostic flow for the on-call operator** (VPS-only):

```bash
# 1. Confirm the import's HTTP error body / worker error chain
#    mentions capabilities.canDownload=false (NOT a generic 403):
ssh instaedit@$VPS_IP \
  'docker compose --env-file /srv/instaedit/.env.production logs --tail=500 worker | grep -i "canDownload\|NotDownloadable"'
# If absent, this is NOT Task 5/10 — diagnose via the import endpoint's
# raw error path instead.

# 2. Check the importJobs dashboard in the SPA. The asset row
#    status will be 'failed' with `capabilities.canDownload=false`
#    in the error message. The user_id + drive_file_id on the
#    failed row tell the operator which file to inspect in Drive.

# 3. Have the file owner open the share dialog on the Drive file
#    ID and check the boxes above. Re-attempt the import.
```

**Task 5/10 acceptance bar (verified in CI):** every Drive import that
hits `capabilities.canDownload=false` rejects BEFORE any S3 upload
starts, with HTTP 422 (HTTP layer) or upload_job status='failed' +
`ErrDriveNotDownloadable` wrapped in the worker error chain (worker
pull-path). Operators see the failure in `<30s` (HTTP layer is
synchronous; worker tick interval is the floor). The spec rule "nessun
fallback" is enforced — there is no retry-the-download path that
would 403 mid-stream.

---

### 3.1 Postgres backup + restore drill — VPS procedure

This subsection expands the one-line row from §3 (`production-restore-drill.sh`)
into the operator-side choreography. **The script itself is still
Fly-Postgres-shaped and needs a parallel rewrite for the VPS — see
the §3.1.0 note below.** This section is the HUMAN-side procedure that
the script encodes for the VPS shape.

#### 3.1.0 Caveat — script migration is a follow-up

`scripts/db/production-restore-drill.sh` (and the runbook PDF it
accompanies) post-date the cutover. They reference `flyctl postgres
fork`, `~/.fly-secrets-database-url-pooled.txt`, and the Flycast URI
shape. They are NOT wired into the live VPS stack — re-pointing them
is a separate follow-up commit (§3.1.0 open item in DEPLOY.md §11
tracks this). The procedure below is the operator's authoritative
flow today until the script rewrite merges.

#### 3.1.1 Cadence

| Trigger | Frequency |
|---------|-----------|
| **First drill** | Within 24h of the first migration deploy (after `docker compose up -d --build` exits 0 + `scripts/db/check-postgres-health.sh` shows `9 canary tables present`). |
| **Baseline** | Quarterly (every 90 days). Track schedule in `ops/restore-drill-cadence.json` (operator-maintained on the VPS under `/srv/instaedit/ops/`). |
| **On incident** | Within 48h of any operational incident that touched the cluster (container restart storm, OOM, lock timeouts > 30s, manual `docker compose down`). The drill proves the recovery path STILL works after the incident. |
| **Pre-audit** | 7 days before any external security review (SOC2, ISO27001, etc.) — auditors expect a recent restore drill on file. |

#### 3.1.2 Pre-flight checklist

```bash
# 1. Operator auth + tooling (the drill script refuses to run without them)
command -v ssh          # must be on PATH
command -v docker       # must be available on the operator laptop
command -v psql python3 # both must be on PATH
command -v openssl      # for password generation if re-provisioning

# 2. Confirm the stack is up + the canonical db name is right
ssh instaedit@$VPS_IP \
  'docker compose -f /opt/instaedit/InstaeditLogin/docker-compose.yml ps'
#   expect: api/worker/caddy/postgres/minio all `running`
#   TERRIBLE if: postgres is `exited` or `restarting`

ssh instaedit@$VPS_IP \
  'docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c "SELECT current_database();"'
#   expected: instaedit_login
#   TERRIBLE if: instaedit_login_test, postgres, template1, instaedit_login_dev

# 3. Confirm migrations are at-rest before the drill. The migration runner
#    does NOT maintain a tracking table (each .sql is idempotent IF NOT
#    EXISTS — see internal/database/migrate_check.go::CanaryTables); the
#    actual readiness probe is the CanaryTables slice:
#       var CanaryTables = []string{"users","tokens","workspaces","posts",
#                                    "post_targets","webhook_deliveries"}
#    Replicate that probe here so the operator sees the same diagnostic
#    the app's /ready handler reports:
#    NOTE: must mirror internal/database/migrate_check.go::CanaryTables —
#    update BOTH together if the slice grows.
ssh instaedit@$VPS_IP \
  'docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c "
    SELECT count(*)
      FROM unnest(ARRAY[\"users\",\"tokens\",\"workspaces\",\"posts\",
                        \"post_targets\",\"webhook_deliveries\"]) t(tbl)
     WHERE to_regclass(\"public.\" || t.tbl) IS NULL;"'
#   expected: 0. If > 0 a migration is mid-deploy or
#   failed partway; defer until /health reports 200 AND
#   scripts/db/check-postgres-health.sh exits 0.

# 4. Confirm the api + worker + outbox dispatcher are healthy
curl -i https://api.instaedit.org/api/v1/health
#   expected: HTTP 200 (Caddy → Go API on :8080)
```

#### 3.1.3 Step-by-step procedure (VPS pg_dump → restore on a throwaway instance)

```bash
# ─── STEP 1: take a fresh backup on the VPS ────────────────────────────
TS=$(date -u +%Y%m%dT%H%M%SZ)
ssh instaedit@$VPS_IP \
  "docker compose exec -T db pg_dump -U instaedit -d instaedit_login \
     --format=custom --no-owner --no-acl \
     > /srv/instaedit/backups/instaedit-restore-drill-$TS.dump"
# Expected: ~10-300 MB file (depends on tenant data volume). Exit 0.
# Use --format=custom (pg_dump's binary format) so pg_restore on the
# drill target can apply without re-parsing.

# ─── STEP 2: pull the dump back to the operator laptop ────────────────
mkdir -p ~/drill-cache
scp "instaedit@$VPS_IP:/srv/instaedit/backups/instaedit-restore-drill-$TS.dump" \
    ~/drill-cache/

# ─── STEP 3: stand up a throwaway Postgres container identical to prod ─
#       (same image, same bind mounts, same db name)
docker run -d --name drill-restore-target-$TS \
  -e POSTGRES_USER=instaedit \
  -e POSTGRES_PASSWORD=instaedit_drill_pw \
  -e POSTGRES_DB=instaedit_login \
  postgres:17-alpine
# Expected: container running on host port 55432.

# ─── STEP 4: restore the dump into the throwaway instance ─────────────
docker exec -i drill-restore-target-$TS \
  pg_restore -U instaedit -d instaedit_login --no-owner --no-acl \
             --clean --if-exists < ~/drill-cache/instaedit-restore-drill-$TS.dump
# Exit codes:
#   0  restore PASS.
#   1  pre-flight failure (dump file missing, container not ready).
#   3  restore warning (e.g. extensions not present in target) — usually
#      safe to ignore IF the schema fingerprint matches afterward.

# ─── STEP 5: assert schema fingerprint parity ─────────────────────────
PROD_FP=$(ssh instaedit@$VPS_IP \
  'docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c "
    WITH f AS (
      SELECT enumtypid::regtype AS e FROM pg_type WHERE typtype = '"'"'e'"'"'
      UNION ALL
      SELECT format(\"%I.%I\", tn.nspname, tn.relname) FROM pg_class tc
        JOIN pg_namespace tn ON tc.relnamespace = tn.oid
       WHERE tc.relkind IN ('"'"'r'"'"', '"'"'i'"'"')
    ) SELECT md5(string_agg(e::text, '"'"','"'"' ORDER BY e)) FROM f;"')
DRILL_FP=$(docker exec drill-restore-target-$TS \
  psql -U instaedit -d instaedit_login -tA -c "
    WITH f AS (
      SELECT enumtypid::regtype AS e FROM pg_type WHERE typtype = 'e'
      UNION ALL
      SELECT format('%I.%I', tn.nspname, tn.relname) FROM pg_class tc
        JOIN pg_namespace tn ON tc.relnamespace = tn.oid
       WHERE tc.relkind IN ('r', 'i')
    ) SELECT md5(string_agg(e::text, ',' ORDER BY e)) FROM f;")
[[ "$PROD_FP" == "$DRILL_FP" ]] \
  || { echo "FAIL: schema fingerprint MISMATCH (prod=$PROD_FP drill=$DRILL_FP)"; exit 3; }

# ─── STEP 6: assert canary tables populated ───────────────────────────
docker exec drill-restore-target-$TS \
  psql -U instaedit -d instaedit_login -tA -c "
    SELECT count(*) FROM unnest(ARRAY['users','tokens','workspaces','posts',
                                       'post_targets','webhook_deliveries']) t(tbl)
     WHERE to_regclass('public.' || t.tbl) IS NULL;"
#   expected: 0

# ─── STEP 7: compare row-count spot checks ────────────────────────────
for tbl in users workspaces posts post_targets; do
  PROD_COUNT=$(ssh instaedit@$VPS_IP \
    "docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c \
     \"SELECT count(*) FROM $tbl;\"")
  DRILL_COUNT=$(docker exec drill-restore-target-$TS \
    psql -U instaedit -d instaedit_login -tA -c "SELECT count(*) FROM $tbl;")
  echo "$tbl: prod=$PROD_COUNT drill=$DRILL_COUNT"
  [[ "$PROD_COUNT" == "$DRILL_COUNT" ]] || { echo "FAIL: $tbl count mismatch"; exit 4; }
done

# ─── STEP 8: tear down the throwaway instance ──────────────────────────
docker logs drill-restore-target-$TS > ~/drill-cache/drill-stdout-$TS.log 2>&1
docker rm -f drill-restore-target-$TS

# ─── STEP 9: save the report ─────────────────────────────────────────
mkdir -p ~/drill-cache/reports
cat > ~/drill-cache/reports/restore-drill-$TS.md <<EOF
## Postgres restore drill — $TS

- Verdict: PASS
- Schema fingerprint: prod=$PROD_FP drill=$DRILL_FP
- Canary tables: 0 missing
- Row-count checks: users / workspaces / posts / post_targets all MATCH

### Cleanup
The throwaway container $DRILL_TARGET_NAME was removed (STEP 8).
Backup file lives at /srv/instaedit/backups/instaedit-restore-drill-$TS.dump
on the VPS; compress + cold-store as needed per retention policy.
EOF

# ─── STEP 10: append to ops/restore-drill-cadence.json ────────────────
# Append the verdict to the operator-maintained cadence ledger.
# Schema:
# { "ts": "<UTC>", "verdict": "PASS"|"FAIL", "mode": "<root cause on FAIL>",
#   "operator": "<whoami>@<host>", "drill_target_destroyed": true|false }
```

#### 3.1.4 Common failure modes (VPS)

| Symptom | Root cause | Fix |
|---------|------------|-----|
| `pg_dump` exits with `permission denied for table …` | The `instaedit` role in /srv/instaedit/.env.production's POSTGRES_PASSWORD was rotated but `instaedit_login` ownership was not migrated. | `docker compose exec -T db psql -U instaedit -d instaedit_login -c "ALTER TABLE public.X OWNER TO instaedit;"` per missing table. Or rebuild the role inheritance from `scripts/db/provision-postgres-runbook.sh` step 6. |
| `pg_restore` exits with `role "instaedit" does not exist` | The throwaway Postgres container was started without the matching `POSTGRES_USER=instaedit`. | Re-run STEP 3 with `-e POSTGRES_USER=instaedit -e POSTGRES_PASSWORD=instaedit_drill_pw`. The script must mirror prod's role + db name exactly. |
| Schema fingerprint MISMATCH | (a) throwaway container started on a different `postgres:` image tag than prod (e.g. `postgres:16-alpine` vs `postgres:17-alpine`); (b) migrations are mid-flight on prod — defer until /health=200; (c) dump used `--no-acl` and the role ownership stripped from the dump. | (a) Re-run STEP 3 with the exact `postgres:17-alpine` image. (b) Wait for /health=200 + canary tables PASS, then re-run. (c) Re-dump with `--no-owner --no-acl` (the flags are correct — issue is the throwaway's pg_user differing). |
| Canary tables post-restore: `> 0 missing` | (a) the dump filtered out non-public schemas; (b) the drill container's `public` schema was dropped on init (default Postgres behaviour, but `POSTGRES_DB=instaedit_login` should NOT drop `public`). | Recreate the drill container with `POSTGRES_DB=instaedit_login` explicitly; rerun STEP 4 with `--clean --if-exists` to drop existing objects first. |
| Step `drill_target_destroyed: false` | STEP 8's `docker rm -f` failed (image pulled but container not started; volume leaked). | `docker rm -f drill-restore-target-$TS; docker volume prune -f`. STEP 11 cadence JSON should mark the drill as `drill_target_destroyed: false` until cleanup is confirmed. |
| `curl https://api.instaedit.org/api/v1/health` returns 503 during pre-flight | API VM is mid-rolling-restart. NOT a drill blocker if the 5xx resolves within 30s. | If the API is permanently down, defer the drill until POST-INFRA-INCIDENT — running a drill against a degraded baseline pollutes the report. |

#### 3.1.5 Output contract

After the drill completes (PASS or FAIL):

- `~/drill-cache/reports/restore-drill-<UTC>.md` contains the schema fingerprint + canary + row-count summary (above).
- A Sentry issue `INFRA-PG-RESTORE-DRILL-*` is filed MANUALLY by the operator ONLY on FAIL — the drill is pure bash + psql + docker; PASS drills don't generate Sentry noise.
- `/srv/instaedit/ops/restore-drill-cadence.json` (operator-maintained on the VPS) gains a new entry documenting the timestamp + verdict + drill_target_destroyed flag.
- The backup file `instaedit-restore-drill-<UTC>.dump` stays on the VPS under `/srv/instaedit/backups/`. Compress + cold-store per retention policy (default: keep latest 4 quarterly drills, off-host-rsync weekly).

#### 3.1.6 DB-name discipline (production convention)

The canonical production DB name is **`instaedit_login`** — NOT
`instaedit_login_test` (test), NOT `instaedit_login_dev` (dev), NOT
`postgres` (Compose default), NOT `template1`. This invariant is
enforced at THREE layers:

1. **At provisioning** (`docker-compose.yml`): the `postgres` service's `POSTGRES_DB` env var is hard-coded to `instaedit_login`. The Compose stack creates the DB at first boot AND the `instaedit_login` ownership rolls out to the role.
2. **At smoke check** (`scripts/db/check-postgres-health.sh`): asserts the canary tables post-migration exist + the db name is exactly `instaedit_login`. Run `docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c "SELECT current_database();"` and confirm the output.
3. **At restore drill** (this section §3.1.3 STEP 5): the schema fingerprint is `MD5(enum-oids ∪ public-schema-table-oids)` — a misconfigured dev cluster would produce a DIFFERENT fingerprint and the drill would FAIL with SCHEMA MISMATCH before any semantic check.

**Anti-pattern**: pointing at `localhost:5432/instaedit_login_dev` (a
dev-shape DB) from the production VPS. The check `current_database()`
+ the drill §3.1.3 STEP 5 fingerprint catches this BEFORE any API
traffic flows. The VPS-shape risk is lower than the Fly-shape risk
(no cross-VPC confusion), but the discipline stays so the roll-back
path is identical.

---

## 4. Storage (MinIO / `instaedit-prod-media`)

State after the VPS Compose stack first-boot (the `minio` service
initialises the bucket via the Compose `init` lifecycle):

- **Endpoint (inside Compose network):** `http://minio:9000` (NOT exposed publicly; the Go API connects via the compose DNS name).
- **Endpoint (operator localhost on VPS):** `https://127.0.0.1:9001` — admin console (loopback only; NEVER publicly bound).
- **Default bucket:** `instaedit-prod-media` (matches `S3_BUCKET` in `/srv/instaedit/.env.production`).
- **Root credential pair:** `MINIO_ROOT_USER` + `MINIO_ROOT_PASSWORD` baked into the Compose service env block (re-used as `S3_ACCESS_KEY` + `S3_SECRET_KEY` for the Go API — the SigV4 signer is endpoint-agnostic).
- **CORS:** single-origin `https://app.instaedit.org`, methods PUT-GET-HEAD, Expose ETag, MaxAge 3600. Console: MinIO → Settings → CORS.
- **Lifecycle:** AbortIncompleteMultipartUpload after 1 day (no orphan parts).
- **Versioning:** Enabled (audit + accidental-delete recovery). Console: MinIO → bucket → admin → versioning.
- **TLS-only policy:** bucket policy Denies `s3:*` when `aws:SecureTransport=false` (defense-in-depth).
- **Max object size:** 200 MB enforced twice — bucket policy Denies `PutObject` if `s3:content-length > 209715200`, AND the application clamps the presigned URL `Content-Length` via `STORAGE_MAX_UPLOAD_BYTES = 200 * 1024 * 1024` in `internal/config/config.go`.

> **Migration from Tigris.** If historical data lives in the
> `instaedit-prod-media` Tigris bucket, see `docs/DEPLOY.md` §10
> (Tigris retirement) for the one-shot bucket-to-bucket copy
> procedure. It is optional and does NOT block the production cutover
> — `S3_ENDPOINT=http://minio:9000` on the VPS-side env var flips
> the API away from Tigris; flipping back to `S3_ENDPOINT=https://t3.storage.dev`
> re-points at Tigris for the rollback window.

### 4.0 Storage recovery drills (MinIO)

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| Browser console: `CORS preflight failed for PUT /uploads/...` | Sentry issues spike from `app.instaedit.org` | Open MinIO admin console (`https://127.0.0.1:9001`) → bucket `instaedit-prod-media` → CORS rules. The list MUST contain `https://app.instaedit.org` with PUT/GET/HEAD + Expose `ETag`. Reference the `internal/services/storage.go` `presignedURL` path for what the front-end sets. |
| Browser console: `413 Request Entity Too Large` from MinIO | Media upload metric spike | Verify `pkg/api/storage.go` `STORAGE_MAX_UPLOAD_BYTES = 200 MB`; if a user device is bypassing the presigned clamp (e.g. direct CORS upload from presign URL), the bucket-policy defense-in-depth statement catches it. |
| `mc admin info` reports N stale multipart uploads | (manual) Lifecycle rule is too lenient or unused parts piling up | Lower `AbortIncompleteMultipartUpload.DaysAfterInitiation` from 1 → 0.25 days via the MinIO lifecycle UI (or `mc ilm import` from the operator laptop). Confirm the new state with `mc ilm ls instaedit-prod-media`. |
| Sentry `storage.policy.deny` capture blocks legitimate uploads | CORS / TLS-only / size policy mismatch | The SDK misconfigured — ad-hoc curl on `:80` of minio from a non-prod dev machine, OR the SigV4 signer passed `aws:SecureTransport=false` (impossible via the legitimate Go SDK; debug the caller stack). |
| MinIO container `exited` after `docker compose up -d` | `docker compose logs minio` shows volume permission denied | The bind-mount `/srv/instaedit/miniostore` is owned by a UID that the container's `minio` process doesn't match. Fix: `chown -R 1000:1000 /srv/instaedit/miniostore` (the default MinIO image UID), then `docker compose up -d minio`. |

---

## 5. Monitoring baselines

### 5.1 Required monitors (set up before inviting users)

- [ ] **Sentry** with `SENTRY_DSN`, `SENTRY_ENVIRONMENT=production`, `SENTRY_RELEASE=$(git rev-parse HEAD)`. Captured at panic + 5xx emission. Empty == no init (per Blocco #5.3 opt-in).
- [ ] **Uptime monitor** on `https://api.instaedit.org/api/v1/health` (30s cron, alert via email after 2 consecutive failures).
- [ ] **Readiness monitor** on `https://api.instaedit.org/ready` (operator shoulder-check on incident — Caddy does not check this; the API's own goroutines do).
- [ ] **Postgres queue-lag alert** (cron query, run via `docker compose exec -T db psql`):
  `SELECT count(*) FROM webhook_deliveries WHERE status='queued' AND created_at < NOW() - interval '1 hour'` > 100 → alert.
- [ ] **Dead-letter-queue alert**:
  `SELECT count(*) FROM publish_jobs WHERE status='dlq'` > 0 → alert.
- [ ] **Refresh-token-failure alert** (Sentry capture event tag `auth.refresh.failed`).
- [ ] **Compose stack always-on alert**: cron `docker compose -f /opt/instaedit/InstaeditLogin/docker-compose.yml ps --services --filter "status=exited"` returns non-empty → alert. Same cron fires when any of `api`, `worker`, `caddy`, `postgres`, `minio` is not `running`.
- [ ] **Log privacy assertion**: `make verify-log-redaction` runs cleanly on the live docker compose logs in the last 1h (catches runtime leaks that the static CI grep cannot — see §5.3). Recommended cadence: after every VPS deploy (`git pull && docker compose up -d --build`) + weekly cron. *NOTE: `verify-log-redaction` still reads `flyctl logs` per DEPLOY.md §11 open item — re-point the script to `docker compose logs --since 1h` as a follow-up.*

### 5.2 DNS / email hygiene

- [ ] SPF record for `instaedit.org`: `v=spf1 include:_spf.resend.com ~all`. The 2026 Resend include host is `_spf.resend.com` (with `_spf.` prefix), NOT bare `resend.com`. `~all` (soft-fail) is the right choice during warm-up; flip to `-all` after month 1 of clean delivery. Full canonical record in **§7.1** below.
- [ ] DKIM: Resend dashboard publishes a CNAME; the selector host is `<selector>._domainkey.instaedit.org` (the selector is assigned by Resend per domain; look at the dashboard before pasting). Full shape + 2026 canonical CNAME target in **§7.1** below.
- [ ] DMARC: `_dmarc.instaedit.org TXT` **starts at `p=none`** for the 2-4 weeks warm-up window (not `p=reject` — the 2026 best-practice ramp for brand-new sender domains). The full progression schedule + ramp reasoning is in **§7.2**.
- [ ] CAA per RFC 8659 + this file §1.
- [ ] Gmail inbox deliverability test (using Resend `curl` API + operator's own Gmail address) — exact protocol in **§7.3**.
- [ ] Tracking verification (open + click) — magic-link emails MUST NOT carry Resend's tracking rewrite; protocol in **§7.4**.
- [ ] EMAIL_PROVIDER_KEY captured to password manager (`instaedit-login/email/EMAIL_PROVIDER_KEY`, scope = Sending Access ONLY) — and explicitly NOT pushed to `/srv/instaedit/.env.production` until backend wires Resend. Capture protocol in **§7.5**.

### 5.3 Log discipline (security)

Backend logs MUST NOT include:

- `access_token` / `refresh_token` (raw or encrypted preview)
- `JWT_SECRET` / `ENCRYPTION_KEYS` / `META_APP_SECRET`
- `password=...` from connection strings

Automated guard: `grep -RnE '(refresh_token|jwt_secret|encryption_key|access_token)\s*=' internal/` returns 0 hits in CI.

> **Operator-side Live Log Verifier** (`./scripts/obs/verify-log-redaction.sh`, wired as `make verify-log-redaction`): the static CI grep above proves the CODE doesn't hardcode sensitive variables, but does NOT cover runtime leaks (an operator typo in `slog.Warn("...", "token", token)` would not be caught statically). To prove the *running* deploy doesn't leak, the operator MUST periodically run this script:
>
> ```bash
> make verify-log-redaction         # default: scan --since 1h
> # or explicitly:
> ./scripts/obs/verify-log-redaction.sh --apply --since 24h
> ```
>
> The script streams recent service logs into a chmod-700 tmpdir (trap-cleaned on EXIT), greps against the canonical 7-pattern list (env var names + values, Resend `re_*` tokens, AWS `AKIA*` access keys, embedded DB URI passwords, literal `password=...`, `csrf_token=<hex>` URL params, `?token=<base64url>` magic-link tokens). It pipes each `grep` hit DIRECTLY into `awk` so the FULL secret-bearing line never enters a shell var; awk truncates to the first 80 chars + appends `***redacted***` so the operator NEVER sees actual captured secrets. Exit 0 if clean / exit 1 with sanitized snippet list + remediation pointers if any pattern hit.
>
> **Open item (per DEPLOY.md §11)**: the script's `flyctl logs` source must be re-pointed at `docker compose logs --since <window>` before this Makefile target is reliable on the VPS. Track: re-point script to `docker compose logs --tail=2000 api worker`, then `make verify-log-redaction` works on the VPS-native stack.
>
> Wire into a weekly cron on the operator laptop so a future regression gets caught without a manual prompt. Cadence: after every VPS deploy + weekly cron + on any `slog.Warn`/`slog.Info` regression PR.

---

## 6. Pre-flight "go-live" gate

Tick all of these before opening the app to real users:

- [ ] Sentry captures a real test panic (then cleared)
- [ ] Uptime monitor on `/api/v1/health` alerts are wired correctly (deliberate downtime test)
- [ ] `/ready` returns 200 within 30s of VPS `docker compose up -d --build` finishing
- [ ] `docker compose ps` shows api/worker/caddy/postgres/minio all `running`
- [ ] Queue-lag + DLQ alerts firing on synthetic backlog (then cleared)
- [ ] No `<access_token|refresh_token|password>.*` in `docker compose logs --since 1h` output (privacy check)
- [ ] SPF/DKIM/DMARC all pass `dig +short` for `instaedit.org` ✔
- [ ] Restore drill completed + signed off (see §3 + full procedure in §3.1)
- [ ] DB-name discipline assertion: `docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c "SELECT current_database();"` returns `instaedit_login` (NOT `instaedit_login_test` test). Confirmed at provisioning ([§3.1.6](#316-db-name-discipline-production-convention) layer 1) AND at smoke check ([§3.1.6](#316-db-name-discipline-production-convention) layer 2). Full 3-layer enforcement story (provisioning + smoke check + restore drill fingerprint) — see [§3.1.6](#316-db-name-discipline-production-convention).
- [ ] Privacy policy + ToS + data-deletion page reachable (`https://app.instaedit.org/privacy`, `/tos`, `/data-deletion`)
- [ ] Support email `security@instaedit.org` (or whatever was registered) auto-responds in <60s

After all boxes ticked the operator flips `APP_ENV=production` env
var in `/srv/instaedit/.env.production` (already `production` from
§2.3 first-boot in DEPLOY.md but audit the canary) + closes the gate.

---

## 7. Email provider runbook (`no-reply@instaedit.org`)

Canonical reference for the Resend-based transactional email sender. Companion to `scripts/email/check-email-deliverability.sh` (read-only DNS verification). **NO app code commits in this section** — the backend does not yet wire Resend (see §7.5 for the deferred wiring plan).

[Section §7 verbatim from the previous runbook — Resend wiring is platform-agnostic: SPF apex TXT, DKIM CNAME, DMARC ramp, Gmail inbox test, tracking verification, EMAIL_PROVIDER_KEY capture protocol. References to `make fly-secrets` in §7.5 map to `/srv/instaedit/.env.production` edits in the new VPS context.]

### 7.0 State assertion

After this runbook runs:

- [ ] SPF apex TXT at `instaedit.org`: `v=spf1 include:_spf.resend.com ~all` (warm-up `~all`)
- [ ] DKIM CNAME at `<selector>._domainkey.instaedit.org` → `<selector>.dkim.resend.com.` (selector from Resend dashboard)
- [ ] DMARC TXT at `_dmarc.instaedit.org`: `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` (warm-up `p=none`)
- [ ] Resend dashboard → Domains → `instaedit.org` shows green Verified badge
- [ ] Gmail inbox test passed (Authentication-Results: dkim=pass + spf=pass + dmarc=pass on a real Gmail address; email landed in INBOX not SPAM)
- [ ] `EMAIL_PROVIDER_KEY` captured in password manager `instaedit-login/email/EMAIL_PROVIDER_KEY` (scope = Sending Access ONLY). NOT yet pushed to `/srv/instaedit/.env.production` because the backend does not wire Resend yet.

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

This is the operator's first concrete verification — runs from the operator's laptop using their own Gmail address. The test MUST pass before inviting any non-operator user. This section is platform-agnostic: the SMTP / Resend sender path is independent of whether the landing zone is Fly or VPS.

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

> `track_opens: false` and `track_links: false` are NON-NEGOTIABLE for transactional magic-link emails. Open-pixel is personal data (IP + UA + timestamps) — GDPR/UK-GDPR/PIPEDA-comparable regimes require explicit consent. Link rewriting can strip magic-link token integrity if a third-party proxy logs / caches the rewrite.

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

The provider key has different capture semantics than the rest of the `/srv/instaedit/.env.production` secrets:

1. **Capture NOW** from Resend dashboard → API Keys → Create API Key.
2. **Scope = `Sending Access` ONLY** (= just `POST /emails`). Do NOT select `Full Access` (= includes domain + webhook management) — minimise blast radius if the key ever leaks.
3. **Save in password manager** under the entry `instaedit-login/email/EMAIL_PROVIDER_KEY`. Format: starts with `re_` (≈ 40 chars).
4. **Do NOT add to `/srv/instaedit/.env.production` yet**. As of (post-commit 58742bf Resend unification), `internal/config/config.go` has no `EmailProvider*` fields; `pkg/api/magic_link.go::handleMagicLinkStart` returns the plaintext token in the response body (marked `// dev-only; production drops via Mailgun/SES`); and `pkg/api/auth_email.go::handleForgotPassword` has `// TODO(FASE 2.2): Send reset token via email` markers. The backend does NOT yet wire Resend — pushing the key into `.env.production` would be a secret that has zero readers, which is worse than no secret (rotation burden without value).
5. **When the backend wires Resend** (separate future task): add `EmailProvider`, `EmailFrom`, `EmailFromName`, `EmailProviderKey` fields to `Config`; wire `internal/services/email_sender.go` (a new file) to dispatch the magic-link / password-reset emails with `track_opens: false`, `track_links: false` defaults baked in. THEN push to `/srv/instaedit/.env.production` + redeploy via `docker compose up -d --force-recreate api worker`.

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

---

## 8. Cross-references

| Concern | Reference |
|---------|-----------|
| VPS host setup (ssh + Docker + firewall + /srv/instaedit/ tree) | [`docs/DEPLOY.md` §2](./DEPLOY.md#2-one-time-host-setup-operator-laptop--vps) |
| DNS records (canonical: apex + app + api → 51.91.11.36 + email-deliverability) | [`docs/DEPLOY.md` §1.5](./DEPLOY.md#15-dns-delegation-canonical--instaeditorg) |
| Postgres smoke check | [`scripts/db/check-postgres-health.sh`](../scripts/db/check-postgres-health.sh) |
| Postgres backup + restore drill (operatorside choreography) | This file §3.1 (VPS pg_dump → throwaway container). **Script rewrite needed** for `scripts/db/production-restore-drill.sh` — tracked in DEPLOY.md §11. |
| MinIO bucket provisioning (loopback admin console) | VPS MinIO admin console at `https://127.0.0.1:9001`; env block in `docker-compose.yml` |
| MinIO storage recovery drills | This file §4.0 |
| Tigris (legacy) → MinIO migration path | [`docs/DEPLOY.md` §10](./DEPLOY.md#10-tigris-retirement-one-time-migration) |
| Post-deploy E2E smoke (Phase 9 sub-1-5+7) | [`scripts/ops/post_deploy_smoke.sh`](../scripts/ops/post_deploy_smoke.sh) |
| Workspace isolation test (Phase 9 sub-6) | [`scripts/ops/workspace_isolation_test.sh`](../scripts/ops/workspace_isolation_test.sh) |
| Email sender DNS records + Gmail inbox test + tracking verification + provider-key capture | This file §7 |
| Email DNS READ-ONLY check (no registrar mutations) | [`scripts/email/check-email-deliverability.sh`](../scripts/email/check-email-deliverability.sh) |
| Provider chosen: Resend (over Postmark) | commit `58742bf` (Resend unification) |
| Backend wiring of EMAIL_PROVIDER_KEY (deferred) | This file §7.5 |
| Caddyfile source | [`ops/vps/Caddyfile`](../ops/vps/Caddyfile) |
| Frontend build-time API URL validator | [`web/scripts/verify-api-base-url.ts`](../web/scripts/verify-api-base-url.ts) |
| OpenAPI spec | [`api/openapi.yaml`](../api/openapi.yaml) |
| Cookie / CSRF cross-subdomain semantic | `internal/auth/csrf.go` + `internal/config/config.go` Blocco #2.4 |
| Free-tier provider matrix (TikTok/X/YouTube/LinkedIn/Stripe disabled in beta) | [`docs/PROVIDER_MATRIX.md`](./PROVIDER_MATRIX.md) |
| Historical cutover (deleted fly.toml, deleted fly-* Makefile targets, deleted Fly secrets scripts) | commits `7e8beec`, `615314b`, `5ac159c` |

---

## §10 Worker Recovery (Task 10/10 — final pillar of the Definition of Done)

Task 10/10 wires the operator-triage workflow for all six worker failure-path scenarios. Operators reading this section find the dead-letter endpoint + the recovery metrics they need to spot a worker crash storm before it cascades.

### Dead-letter endpoint (Task 10/10)

| Endpoint | Shape | Notes |
| --- | --- | --- |
| `GET /admin/upload_jobs/dead_letter` | JSON | Up to 500 upload_jobs in `status='dead_letter'`, ordered by `completed_at DESC`. Auth: admin JWT or admin API key. 401/403 for non-admin callers; 501 if the admin store is not wired. |
| `GET /admin/upload_jobs/dead_letter.csv` | CSV | Same row shape, single-row header for spreadsheet import. 501 if the admin store is not wired. |

A row appears in this list ONLY when `MarkDeadLetter` runs, which itself fires from `internal/worker/upload_worker.go::handleProcessingError` when `job.AttemptCount >= job.MaxAttempts` (the retry budget has been exhausted). The operator decides per row: manual retry, cancel, or ignore.

**VPS operator diagnostic flow** (replaces the deleted `flyctl logs --app instaedit-login --since 15m | grep canDownload` line — the worker lives in the Compose stack now):

```bash
ssh instaedit@$VPS_IP \
  'docker compose --env-file /srv/instaedit/.env.production logs --tail=2000 worker | grep -iE "dead_letter\|capabilities.canDownload\|NotDownloadable"'
# Expected on a healthy rotate: a few dead-letter hits from past weeks, but no fresh
# canDownload / NotDownloadable lines (those map to "stop the import" failures, not
# "the worker is broken" failures).
```

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

---

## §11 Open items (tracked from DEPLOY.md §11)

These are surgically tracked followup commits, NOT documentation gaps:

1. `make verify-log-redaction` + `scripts/obs/verify-log-redaction.sh` still tail `flyctl logs`. Re-point to `docker compose logs --since <window>` to make the live redactor work on the VPS-native stack (§5.3 above references this; DEPLOY.md §11 owns it).
2. `scripts/db/production-restore-drill.sh` is still Fly-Postgres-shaped. Rewrite for VPS pg_dump → throwaway container (§3.1.0 above flags this; sub-task of DEPLOY.md §11).
3. `.github/workflows/integration.yml` still references `make fly-secrets-test`. Substitute `python3 scripts/test_parse_envfile.py` as a standalone job (DEPLOY.md §11 owns it).
4. `docker-build-production` Makefile target is orphaned post-cutover. Drop or repurpose for local single-image compose builds (DEPLOY.md §11 owns it).
