# Operations — Monitoring baselines + go-live gate

Part of the [Operations runbook](OPERATIONS.md) documentation set. This file
holds the **monitoring + pre-flight** operational state: required monitors,
DNS/email hygiene checks, log-discipline contract, and the "go-live" gate
the operator ticks before opening the app to real users.

Related documents:

- [Deploy edge (DNS + TLS/Caddy)](operations-deploy.md)
- [Recovery drills + storage + worker recovery](operations-runbook.md)
- [Email provider runbook (Resend)](operations-email.md)

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
- [ ] **Compose stack always-on alert**: cron `docker compose -f /opt/instaedit/InstaeditLogin/docker-compose.yml ps --services --filter "status=exited"` returns non-empty → alert. Same cron fires when any of `api`, `worker`, `db`, or `minio` is not `running`; Caddy is checked separately with systemd.
- [ ] **Log privacy assertion**: `make verify-log-redaction` runs cleanly on the live Docker Compose logs in the last 1h (catches runtime leaks that the static CI grep cannot — see §5.3). Recommended cadence: after every VPS deploy (`git pull && docker compose up -d --build`) + weekly cron.

### 5.2 DNS / email hygiene

- [ ] SPF record for `instaedit.org`: `v=spf1 include:_spf.resend.com ~all`. The 2026 Resend include host is `_spf.resend.com` (with `_spf.` prefix), NOT bare `resend.com`. `~all` (soft-fail) is the right choice during warm-up; flip to `-all` after month 1 of clean delivery. Full canonical record in **§7.1** of the [email runbook](operations-email.md#71-dns-records-canonical-resend-values-2026).
- [ ] DKIM: Resend dashboard publishes a CNAME; the selector host is `<selector>._domainkey.instaedit.org` (the selector is assigned by Resend per domain; look at the dashboard before pasting). Full shape + 2026 canonical CNAME target in **§7.1** of the [email runbook](operations-email.md#71-dns-records-canonical-resend-values-2026).
- [ ] DMARC: `_dmarc.instaedit.org TXT` **starts at `p=none`** for the 2-4 weeks warm-up window (not `p=reject` — the 2026 best-practice ramp for brand-new sender domains). The full progression schedule + ramp reasoning is in **§7.2** of the [email runbook](operations-email.md#72-dmarc-progression-schedule).
- [ ] CAA per RFC 8659 + this file §1 (see the [deploy-edge doc](operations-deploy.md#1-dns-records-instaeditorg)).
- [ ] Gmail inbox deliverability test (using Resend `curl` API + operator's own Gmail address) — exact protocol in **§7.3** of the [email runbook](operations-email.md#73-gmail-inbox-test-protocol).
- [ ] Tracking verification (open + click) — magic-link emails MUST NOT carry Resend's tracking rewrite; protocol in **§7.4** of the [email runbook](operations-email.md#74-tracking-verification).
- [ ] EMAIL_PROVIDER_KEY captured to password manager (`instaedit-login/email/EMAIL_PROVIDER_KEY`, scope = Sending Access ONLY) — and explicitly NOT pushed to `/opt/instaedit/secrets/.env.production` until backend wires Resend. Capture protocol in **§7.5** of the [email runbook](operations-email.md#75-email_provider_key-capture-protocol).

### 5.3 Log discipline (security)

**Frontend dependency audit note (2026-08):** the web app pins
`react-router-dom` to `7.18.2` and uses only Vite client-side
`BrowserRouter`/`MemoryRouter`; it does not enable React Server Components,
SSR, or framework mode. `npm audit` may still report the upstream RSC-only
advisory `GHSA-qwww-vcr4-c8h2` for the v7 dependency range. Do not force an
incompatible `react-router@8` override: `react-router-dom` v8 is not published
as a compatible package. Re-evaluate this exception when a compatible DOM
release containing the upstream fix is available.

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
> The script streams recent service logs into a chmod-700 tmpdir (trap-cleaned on EXIT), greps against the canonical 10-pattern list (token assignments, Resend `re_*` keys, AWS `AKIA*` access keys, embedded DB URI passwords, literal `password=...`, `csrf_token=<hex>` URL params, `?token=<base64url>` magic-link tokens, Google `ya29.*` access tokens, Google `1//*` refresh tokens, and `Bearer` credentials). It counts matches without placing matching log lines in shell variables or output. Exit 0 if clean / exit 1 with pattern names and counts only, plus remediation pointers if any pattern hits.
>
> The verifier reads `docker compose logs --since <window> api worker` on the VPS and withholds all matching log content from its output. Wire it into a weekly cron on the operator laptop so a future regression gets caught without a manual prompt. Cadence: after every VPS deploy + weekly cron + on any `slog.Warn`/`slog.Info` regression PR.

---

## 6. Pre-flight "go-live" gate

Tick all of these before opening the app to real users:

- [ ] Sentry captures a real test panic (then cleared)
- [ ] Uptime monitor on `/api/v1/health` alerts are wired correctly (deliberate downtime test)
- [ ] `/ready` returns 200 within 30s of VPS `docker compose up -d --build` finishing
- [ ] `docker compose ps` shows api/worker/postgres/minio healthy and `systemctl is-active caddy` succeeds
- [ ] Queue-lag + DLQ alerts firing on synthetic backlog (then cleared)
- [ ] No `<access_token|refresh_token|password>.*` in `docker compose logs --since 1h` output (privacy check)
- [ ] SPF/DKIM/DMARC all pass `dig +short` for `instaedit.org` ✔
- [ ] Restore drill completed + signed off (see [§3 recovery drills](operations-runbook.md#3-per-provider-recovery-drills) + full procedure in [§3.1](operations-runbook.md#31-postgres-backup--restore-drill--vps-procedure))
- [ ] DB-name discipline assertion: `docker compose exec -T db psql -U instaedit -d instaedit_login -tA -c "SELECT current_database();"` returns `instaedit_login` (NOT `instaedit_login_test` test). Confirmed at provisioning ([§3.1.6](operations-runbook.md#316-db-name-discipline-production-convention) layer 1) AND at smoke check ([§3.1.6](operations-runbook.md#316-db-name-discipline-production-convention) layer 2). Full 3-layer enforcement story (provisioning + smoke check + restore drill fingerprint) — see [§3.1.6](operations-runbook.md#316-db-name-discipline-production-convention).
- [ ] Privacy policy + ToS + data-deletion page reachable (`https://app.instaedit.org/privacy`, `/tos`, `/data-deletion`)
- [ ] Support email `security@instaedit.org` (or whatever was registered) auto-responds in <60s

After all boxes ticked the operator flips `APP_ENV=production` env
var in `/opt/instaedit/secrets/.env.production` (already `production` from
§2.3 first-boot in DEPLOY.md but audit the canary) + closes the gate.
