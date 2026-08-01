# Operations — Recovery drills + storage + worker recovery

Part of the [Operations runbook](OPERATIONS.md) documentation set. This file
holds the **recovery** operational state: per-provider recovery drills
(Postgres backup+restore, Google Drive import), the MinIO storage contract,
the worker-recovery runbook (Task 10/10), and the tracked open items.

Related documents:

- [Deploy edge (DNS + TLS/Caddy)](operations-deploy.md)
- [Monitoring baselines + go-live gate](operations-monitoring.md)
- [Email provider runbook (Resend)](operations-email.md)

---

## 3. Per-provider recovery drills

Cross-references to the existing recovery scripts:

| Drill | Script / doc | Cadence |
|-------|--------------|---------|
| **Postgres backup + restore** | [`scripts/db/production-restore-drill.sh`](../scripts/db/production-restore-drill.sh) — *the current VPS pg_dump → throwaway procedure is documented below; the legacy managed-Postgres helper remains separate historical material* | First drill within 24h of first migration; then quarterly |
| **Postgres health check** | [`scripts/db/check-postgres-health.sh`](../scripts/db/check-postgres-health.sh) | Pre-deploy + post-deploy + on incident |
| **MinIO bucket provisioning** | Compose `minio-init` service; inspect with the MinIO console through a loopback-only tunnel when required | First deployment; repeat after an intentional credential rotation |
| **Stack always-on contract** | `docker compose -f /opt/instaedit/InstaeditLogin/docker-compose.yml ps` | Uptime monitor alerts if `/health` or `/ready` down > 2x consecutive ticks |
| **SPA-reachable check** | `curl -fsSI https://app.instaedit.org/` returns the expected Vercel response (HTTP 200 or configured redirect) | After a Vercel deploy + on incident |
| **Post-deploy E2E smoke** (Phase 9 sub-1-5+7) | [`scripts/ops/post_deploy_smoke.sh`](../scripts/ops/post_deploy_smoke.sh) | After every `git pull && docker compose up -d --build` on the VPS; weekly cron once stable |
| **Workspace isolation test** (Phase 9 sub-6) | [`scripts/ops/workspace_isolation_test.sh`](../scripts/ops/workspace_isolation_test.sh) | Before opening beta to external users + on any cross-workspace query refactor |

Per-drill record-keeping paths (now on the VPS, not central 1Password):

- `ops/restore-drill-<UTC>.md` — Postgres drill reports
- `ops/smoke-<UTC>.log` — manual smoke captures
- Sentry issue `INFRA-CADDY-CERT-*` / `INFRA-COMPOSE-DOWN-*` / `INFRA-PG-RESTORE-DRILL-*` — automated captures

> **Storage contract:** MinIO is the only production object store. Follow
> `docs/DEPLOY.md` §6 for bucket initialization, object backups, and recovery;
> no external storage cutover is part of the supported deploy path.

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
  'docker compose --env-file /opt/instaedit/secrets/.env.production logs --tail=500 worker | grep -i "canDownload\|NotDownloadable"'
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

The automated `internal/crypto/restore_isolation_test.go` verifies the
cryptographic backup invariant: encrypted token data is unreadable without
its historical keyring, and keyring data alone cannot restore a missing row.
It intentionally does not exercise PostgreSQL dump/restore I/O. The
`production-restore-drill.sh` procedure below is the operational end-to-end
PostgreSQL restore verification and must be run separately.

This subsection expands the one-line row from §3 (`production-restore-drill.sh`)
into the operator-side choreography. **The script itself still needs a parallel rewrite for the VPS pg_dump → throwaway flow — see the §3.1.0 note below.** This section is the HUMAN-side procedure the operator follows until the script rewrite lands.

#### 3.1.0 Caveat — script migration is a follow-up

`scripts/db/production-restore-drill.sh` (and the runbook PDF it
accompanies) post-date the cutover. They document the legacy managed-Postgres tooling pipeline (cluster
fork, secrets-pool file name, internal-cluster URI shape). They are
NOT wired into the live VPS stack — re-pointing them is a separate
follow-up commit (§3.1.0 open item in the current deployment runbook tracks this).
The procedure below is the operator's authoritative flow today until
the script rewrite merges.

#### 3.1.1 Cadence

| Trigger | Frequency |
|---------|-----------|
| **First drill** | Within 24h of the first migration deploy (after `docker compose up -d --build` exits 0 + `scripts/db/check-postgres-health.sh` shows `9 canary tables present`). |
| **Baseline** | Quarterly (every 90 days). Track schedule in `ops/restore-drill-cadence.json` (operator-maintained on the VPS under `/opt/instaedit/ops/`). |
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
#   expect: api/worker/db/minio all `running`; Caddy is checked separately with systemd
#   TERRIBLE if: db is `exited` or `restarting`

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
  "mkdir -p /opt/instaedit/backups && docker compose exec -T db pg_dump -U instaedit -d instaedit_login \
     --format=custom --no-owner --no-acl \
     > /opt/instaedit/backups/instaedit-restore-drill-$TS.dump"
# Expected: ~10-300 MB file (depends on tenant data volume). Exit 0.
# Use --format=custom (pg_dump's binary format) so pg_restore on the
# drill target can apply without re-parsing.

# ─── STEP 2: pull the dump back to the operator laptop ────────────────
mkdir -p ~/drill-cache
scp "instaedit@$VPS_IP:/opt/instaedit/backups/instaedit-restore-drill-$TS.dump" \
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
Backup file lives at /opt/instaedit/backups/instaedit-restore-drill-$TS.dump
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
| `pg_dump` exits with `permission denied for table …` | The `instaedit` role in /opt/instaedit/secrets/.env.production's POSTGRES_PASSWORD was rotated but `instaedit_login` ownership was not migrated. | `docker compose exec -T db psql -U instaedit -d instaedit_login -c "ALTER TABLE public.X OWNER TO instaedit;"` per missing table. Use the current Docker Compose PostgreSQL role procedure in `docs/DEPLOY.md` §8; the former Fly provisioning runbook is archived at `docs/archive/legacy-fly/provision-postgres-runbook.sh` and must not be executed. |
| `pg_restore` exits with `role "instaedit" does not exist` | The throwaway Postgres container was started without the matching `POSTGRES_USER=instaedit`. | Re-run STEP 3 with `-e POSTGRES_USER=instaedit -e POSTGRES_PASSWORD=instaedit_drill_pw`. The script must mirror prod's role + db name exactly. |
| Schema fingerprint MISMATCH | (a) throwaway container started on a different `postgres:` image tag than prod (e.g. `postgres:16-alpine` vs `postgres:17-alpine`); (b) migrations are mid-flight on prod — defer until /health=200; (c) dump used `--no-acl` and the role ownership stripped from the dump. | (a) Re-run STEP 3 with the exact `postgres:17-alpine` image. (b) Wait for /health=200 + canary tables PASS, then re-run. (c) Re-dump with `--no-owner --no-acl` (the flags are correct — issue is the throwaway's pg_user differing). |
| Canary tables post-restore: `> 0 missing` | (a) the dump filtered out non-public schemas; (b) the drill container's `public` schema was dropped on init (default Postgres behaviour, but `POSTGRES_DB=instaedit_login` should NOT drop `public`). | Recreate the drill container with `POSTGRES_DB=instaedit_login` explicitly; rerun STEP 4 with `--clean --if-exists` to drop existing objects first. |
| Step `drill_target_destroyed: false` | STEP 8's `docker rm -f` failed (image pulled but container not started; volume leaked). | `docker rm -f drill-restore-target-$TS; docker volume prune -f`. STEP 11 cadence JSON should mark the drill as `drill_target_destroyed: false` until cleanup is confirmed. |
| `curl https://api.instaedit.org/api/v1/health` returns 503 during pre-flight | API VM is mid-rolling-restart. NOT a drill blocker if the 5xx resolves within 30s. | If the API is permanently down, defer the drill until POST-INFRA-INCIDENT — running a drill against a degraded baseline pollutes the report. |

#### 3.1.5 Output contract

After the drill completes (PASS or FAIL):

- `~/drill-cache/reports/restore-drill-<UTC>.md` contains the schema fingerprint + canary + row-count summary (above).
- A Sentry issue `INFRA-PG-RESTORE-DRILL-*` is filed MANUALLY by the operator ONLY on FAIL — the drill is pure bash + psql + docker; PASS drills don't generate Sentry noise.
- `/opt/instaedit/ops/restore-drill-cadence.json` (operator-maintained on the VPS) gains a new entry documenting the timestamp + verdict + drill_target_destroyed flag.
- The backup file `instaedit-restore-drill-<UTC>.dump` stays on the VPS under `/opt/instaedit/backups/`. Compress + cold-store per retention policy (default: keep latest 4 quarterly drills, off-host-rsync weekly).

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
traffic flows. The single-host VPS shape keeps the recovery
fingerprint clean (no cross-cluster / managed-Postgres confusion),
and the discipline stays so the roll-back path remains identical
to drift recovery.

---

## 4. Storage (MinIO / `instaedit-prod-media`)

State after the VPS Compose stack first-boot (the `minio` service
initialises the bucket via the Compose `init` lifecycle):

- **Endpoint (inside Compose network):** `http://minio:9000` (NOT exposed publicly; the Go API connects via the compose DNS name).
- **Endpoint (operator localhost on VPS):** `https://127.0.0.1:9001` — admin console (loopback only; NEVER publicly bound).
- **Default bucket:** `instaedit-prod-media` (matches `S3_BUCKET` in `/opt/instaedit/secrets/.env.production`).
- **Root credential pair:** `MINIO_ROOT_USER` + `MINIO_ROOT_PASSWORD` baked into the Compose service env block (re-used as `S3_ACCESS_KEY` + `S3_SECRET_KEY` for the Go API — the SigV4 signer is endpoint-agnostic).
- **CORS:** single-origin `https://app.instaedit.org`, methods PUT-GET-HEAD, Expose ETag, MaxAge 3600. Console: MinIO → Settings → CORS.
- **Lifecycle:** AbortIncompleteMultipartUpload after 1 day (no orphan parts).
- **Versioning:** Enabled (audit + accidental-delete recovery). Console: MinIO → bucket → admin → versioning.
- **TLS-only policy:** bucket policy Denies `s3:*` when `aws:SecureTransport=false` (defense-in-depth).
- **Max object size:** 200 MB enforced twice — bucket policy Denies `PutObject` if `s3:content-length > 209715200`, AND the application clamps the presigned URL `Content-Length` via `STORAGE_MAX_UPLOAD_BYTES = 200 * 1024 * 1024` in `internal/config/config.go`.

> **Storage contract:** MinIO is the only production object store. Follow
> `docs/DEPLOY.md` §6 for the supported endpoint, bucket, backup, and recovery
> procedures. Storage rollback is handled through the verified MinIO backup,
> not by switching providers.

### 4.0 Storage recovery drills (MinIO)

| Symptom | Fire alarm | Runbook |
|---------|------------|---------|
| Browser console: `CORS preflight failed for PUT /uploads/...` | Sentry issues spike from `app.instaedit.org` | Open MinIO admin console (`https://127.0.0.1:9001`) → bucket `instaedit-prod-media` → CORS rules. The list MUST contain `https://app.instaedit.org` with PUT/GET/HEAD + Expose `ETag`. Reference the `internal/services/storage.go` `presignedURL` path for what the front-end sets. |
| Browser console: `413 Request Entity Too Large` from MinIO | Media upload metric spike | Verify `pkg/api/storage.go` `STORAGE_MAX_UPLOAD_BYTES = 200 MB`; if a user device is bypassing the presigned clamp (e.g. direct CORS upload from presign URL), the bucket-policy defense-in-depth statement catches it. |
| `mc admin info` reports N stale multipart uploads | (manual) Lifecycle rule is too lenient or unused parts piling up | Lower `AbortIncompleteMultipartUpload.DaysAfterInitiation` from 1 → 0.25 days via the MinIO lifecycle UI (or `mc ilm import` from the operator laptop). Confirm the new state with `mc ilm ls instaedit-prod-media`. |
| Sentry `storage.policy.deny` capture blocks legitimate uploads | CORS / TLS-only / size policy mismatch | The SDK misconfigured — ad-hoc curl on `:80` of minio from a non-prod dev machine, OR the SigV4 signer passed `aws:SecureTransport=false` (impossible via the legitimate Go SDK; debug the caller stack). |
| MinIO container `exited` after `docker compose up -d` | `docker compose logs minio` shows volume permission denied | The bind-mount the MinIO named volume is owned by a UID that the container's `minio` process doesn't match. Inspect the actual volume with `docker volume inspect` and repair ownership only during an approved maintenance window; then run `docker compose up -d minio`. |

---

## §10 Worker Recovery (Task 10/10 — final pillar of the Definition of Done)

Task 10/10 wires the operator-triage workflow for all six worker failure-path scenarios. Operators reading this section find the dead-letter endpoint + the recovery metrics they need to spot a worker crash storm before it cascades.

### Dead-letter endpoint (Task 10/10)

| Endpoint | Shape | Notes |
| --- | --- | --- |
| `GET /admin/upload_jobs/dead_letter` | JSON | Up to 500 upload_jobs in `status='dead_letter'`, ordered by `completed_at DESC`. Auth: admin JWT or admin API key. 401/403 for non-admin callers; 501 if the admin store is not wired. |
| `GET /admin/upload_jobs/dead_letter.csv` | CSV | Same row shape, single-row header for spreadsheet import. 501 if the admin store is not wired. |

A row appears in this list ONLY when `MarkDeadLetter` runs, which itself fires from `internal/worker/upload_worker.go::handleProcessingError` when `job.AttemptCount >= job.MaxAttempts` (the retry budget has been exhausted). The operator decides per row: manual retry, cancel, or ignore.

**VPS operator diagnostic flow** (live `docker compose logs --since 15m worker | grep canDownload` on the VPS — the worker runs in the Compose stack):

```bash
ssh instaedit@$VPS_IP \
  'docker compose --env-file /opt/instaedit/secrets/.env.production logs --tail=2000 worker | grep -iE "dead_letter\|capabilities.canDownload\|NotDownloadable"'
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

## §11 Open items

These are surgically tracked followup commits, NOT documentation gaps:

1. `make verify-log-redaction` + `scripts/obs/verify-log-redaction.sh` must source from `docker compose logs --since <window> api worker` to make the live redactor work on the VPS-native stack (§5.3 of the [monitoring doc](operations-monitoring.md#53-log-discipline-security) references this; the current deployment runbook owns it).
2. `scripts/db/production-restore-drill.sh` still follows the pre-cutover managed-Postgres pattern. Rewrite for VPS pg_dump → throwaway container (§3.1.0 above flags this; sub-task of the current deployment runbook).
3. **(RESOLVED at this commit.)** Legacy `.github/workflows/integration.yml` USED to host the stale secrets-parser step; the underlying make-target and .py parsers were dropped at commit `1ab88ef`. The legacy workflow file itself is now retired (deleted alongside this scrub) and coverage lives in `integration-fast.yml` (gate) + `integration-slow.yml` (e2e, alert-only).
4. `docker-build-production` Makefile target was orphaned post-cutover. **Dropped at commit `4382ae8`** (target removed from `.PHONY` and recipe block); residue cleanup, if any, tracked in the current deployment runbook.
