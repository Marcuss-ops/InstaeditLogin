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
This runbook anchors the VPS-native stack end-to-end; legacy
hosting-platform references (managed-Postgres forks, hidden-service URI
shapes, platform-specific CI steps) should be removed in favor of the
corresponding VPS procedure.

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
