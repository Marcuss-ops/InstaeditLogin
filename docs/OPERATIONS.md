# Operations — InstaeditLogin production runbook (DNS + certs + monitoring + recovery)

> **Hub doc for the live `instaedit-login` Fly app + `app.instaedit.org` Vercel project.**
> Owned by the operator team. Every change to DNS, certs, or monitoring surfaces
> here first; `docs/DEPLOY.md` only points to this file for the procedural steps.

This document captures the **operational state** of the InstaeditLogin
production deploy (DNS, TLS, monitoring, recovery drills). It is referenced
from:

- `docs/DEPLOY.md` §1.5 — DNS records (quick-reference table)
- `docs/DEPLOY.md` §2-§5 — deploy pipeline (Postgres + secrets + first deploy)
- `docs/DEPLOY.md` §8 — top-level cross-references index
- `HANDOFF-LINUX.md` §11 — local dev workflow

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

Per-drill record-keeping paths:

- `ops/restore-drill-<UTC>.md` — Postgres drill reports
- `ops/vercel-deploys-<YYYY-MM>.log` — manual smoke captures
- Sentry issue `INFRA-FLY-CERT-*` / `INFRA-VERCEL-CERT-*` — automated captures

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

### 4.2 DNS / email hygiene

- [ ] SPF record for `instaedit.org`: `v=spf1 include:resend.com -all` (matching `EMAIL_FROM=no-reply@instaedit.org`).
- [ ] DKIM: Resend dashboard publishes a CNAME record; add to `instaedit.org`.
- [ ] DMARC: `_dmarc.instaedit.org TXT` `v=DMARC1; p=reject; rua=mailto:security@instaedit.org` (this file §1).
- [ ] CAA per RFC 8659 + this file §1.

### 4.3 Log discipline (security)

Backend logs MUST NOT include:

- `access_token` / `refresh_token` (raw or encrypted preview)
- `JWT_SECRET` / `ENCRYPTION_KEYS` / `META_APP_SECRET`
- `password=...` from connection strings

Automated guard: `grep -RnE '(refresh_token|jwt_secret|encryption_key|access_token)\\s*=' internal/` returns 0 hits in CI.

---

## 5. Pre-flight "go-live" gate

Tick all of these before opening the app to real users:

- [ ] Sentry captures a real test panic (then cleared)
- [ ] Uptime monitor on `/api/v1/health` alerts are wired correctly (deliberate downtime test)
- [ ] `/ready` returns 200 within 30s of Fly app boot
- [ ] Queue-lag + DLQ alerts firing on synthetic backlog (then cleared)
- [ ] No `<access_token|refresh_token|password>.*` in `flyctl logs --app instaedit-login` output (privacy check)
- [ ] SPF/DKIM/DMARC all pass `dig +short` for `instaedit.org` ✔
- [ ] Restore drill completed + signed off (see §3)
- [ ] Privacy policy + ToS + data-deletion page reachable (`https://app.instaedit.org/privacy`, `/tos`, `/data-deletion`)
- [ ] Support email `security@instaedit.org` (or whatever was registered) auto-responds in <60s

After ALL 9 boxes ticked the operator flips `APP_ENV=production` secret
from `production` to the auditor's confirmation line + closes the gate.

---

## 6. Cross-references

| Concern | Reference |
|---------|-----------|
| Fly cluster provisioning + size/HA/PITR/pooler/password | `scripts/db/provision-postgres-runbook.sh` + `docs/DEPLOY.md` §2 |
| Postgres smoke check | `scripts/db/check-postgres-health.sh` |
| Postgres restore drill | `scripts/db/production-restore-drill.sh` |
| Tigris bucket provisioning | `scripts/s3/provision-tigris.sh` |
| Tigris storage recovery drills | `docs/OPERATIONS.md` §4.0 |
| Vercel project settings | `docs/DEPLOY.md` §9 |
| Frontend build-time API URL validator | `web/scripts/verify-api-base-url.ts` |
| Fly doc / API URL contract | `api/openapi.yaml` |
| Cookie / CSRF cross-subdomain semantic | `internal/auth/csrf.go` + `internal/config/config.go` Blocco #2.4 |
| Free-tier provider matrix (TikTok/X/YouTube/LinkedIn/Stripe disabled in beta) | `docs/PROVIDER_MATRIX.md` |
