# Deploy — Fly.io production deploy for `instaedit-login`

Canonical reference for first deploy + ongoing secret rotation to the
Fly.io production target. Mirrors `HANDOFF-LINUX.md` for local dev, but
for the live `instaedit-login` app on Fly.

> The production deploy is **secrets-first, code-second**:
> 1. Stage secrets on Fly (no restart).
> 2. Verify the secrets are clean.
> 3. Deploy the code (attaches the staged secrets to the new image).
>
> This ordering matters: `flyctl secrets set` without `--stage` triggers
> an immediate rolling restart on the *existing* image, which is the
> wrong ordering for a coordinated rollout. The scripts in
> `scripts/` use `--stage` for exactly this reason.

---

## 1. Pre-flight

Tools + accounts required:

| Tool / Account | Where to get it |
|----------------|-----------------|
| `flyctl` | `brew install flyctl` or `curl -L https://fly.io/install.sh \| sh` |
| `jq` | `brew install jq` (optional — for smoke tests) |
| Fly.io account | https://fly.io/app/sign-up |
| Meta Developer app | https://developers.facebook.com (Settings → Basic → App ID + App Secret) |
| Tigris account | https://tigrisdata.com (S3-compatible storage; Access Keys in dashboard) |
| Resend account | https://resend.com (or your SMTP — magic-link mail) |
| Managed Postgres | Fly Postgres (`flyctl postgres create`) or Neon/Supabase |
| DNS for `instaedit.org` | registrar (or Cloudflare) — see **§1.5 below** for the canonical records + see **[docs/OPERATIONS.md §1](./OPERATIONS.md#1-dns-records-instaeditorg)** for the full DNS runbook (CAA, DNSSEC, apex redirect) |

---

## 1.5 DNS delegation (canonical) — `instaedit.org`

Records table below is now authoritative for ALL 7 records (Vercel + Fly + email deliverability). The **full DNS runbook (cert renewal, failure recovery, DMARC progression, Gmail inbox test) lives in [docs/OPERATIONS.md §1 + §7](./OPERATIONS.md#1-dns-records-instaeditorg)** — the table here is the quick-reference; the runbook is the playbook.

| Host | Type | Value | TTL | Purpose |
|------|------|-------|-----|---------|
| `instaedit.org` (apex) | `A` | `76.76.21.21` | 60 | Vercel Anycast — 301 redirects to `app.instaedit.org`. Apex cannot use CNAME (DNS spec); use Vercel's A record + dashboard-level redirect (canonical over ALIAS-flattening for portability across registrars). |
| `app.instaedit.org` | `CNAME` | `cname.vercel-dns.com.` | 60 | Vercel edge route to the SPA. |
| `api.instaedit.org` | `CNAME` | `instaedit-login.fly.dev.` | 300 | Fly.io ingress for the backend. **Never** hardcode A records from `fly ips list` — Fly re-IPs during migrations and the CNAME keeps failover transparent. |
| `_vercel.instaedit.org` | `TXT` | `vc-domain-verify=<token-from-Vercel>` | 300 | Vercel domain-ownership challenge. Token is surfaced in Vercel → Project → Settings → Domains; paste as-is. |
| `instaedit.org` (apex) | `CAA` | `0 issue "letsencrypt.org"` | 3600 | Restrict cert issuance to Let's Encrypt (both Fly and Vercel use LE). |
| `instaedit.org` (apex) | `CAA` | `0 iodef "mailto:security@instaedit.org"` | 3600 | Incident reporting for unauthorized issuance attempts. |
| `instaedit.org` (apex) | `TXT` | `v=spf1 include:_spf.resend.com ~all` | 3600 | SPF for Resend (sender domain `no-reply@instaedit.org`). Use `~all` (soft-fail) during the 2-4 weeks warm-up; flip to `-all` (hard-fail) after first month clean. **Note:** include host is `_spf.resend.com` (with `_spf.` prefix), not bare `resend.com` — this is the 2026 Resend canonical. |
| `<selector>._domainkey.instaedit.org` | `CNAME` | `<selector>.dkim.resend.com.` | 3600 | DKIM rotation. **The `<selector>` is assigned by Resend when you add the domain** — look at the Resend dashboard → Domains → `instaedit.org` → Records BEFORE pasting. Typical values: `resend1`, `resend2`. The format `<selector>.dkim.resend.com.` is canonical in 2026; do NOT switch to a TXT-based DKIM record (some providers have migrated — Resend has NOT). |
| `_dmarc.instaedit.org` | `TXT` | `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` | 3600 | **DMARC starts at `p=none`** for the 2-4 weeks warm-up window — Gmail requires a soft enforcement ramp for brand-new sender domains. Ramp schedule + reasoning: see [docs/OPERATIONS.md §7.2](./OPERATIONS.md#72-dmarc-progression-schedule). The rua/ruf reports go to `security@instaedit.org` — make sure that mailbox exists before flipping `p=quarantine` (otherwise reports get rejected by your own receiver). |

Plus:
- **DNSSEC** at the registrar (Cloudflare: one-click; Namecheap: opt-in via DS records). Required for the CAA records to be honored by resolvers.
- **Cloudflare users:** set `api.` and `app.` to **DNS-only** ("grey cloud"). The orange-cloud proxy returns fly/vercel's certs before LE validation can complete — HTTP-01 challenges will fail and cert renewal will silently break after 60 days.
- **TTL rationale:** 60s on the frontend lets near-instant switchover in CDN failure events; 300s on the backend balances low-API-conn-churn vs cheap regional rerouting.

> **Kicking it off** (after Fly app exists):
> ```bash
> flyctl certs add api.instaedit.org --app instaedit-login
> ```
> Fly will HTTP-01 validate against `instaedit-login.fly.dev` via the CNAME. Watch the log for `Cert issued` (typically 30-90s once DNS propagates). For Vercel: add `app.instaedit.org` in Project → Settings → Domains, paste the `_vercel` TXT value, wait for "Valid Configuration".

---

## 2. One-time app setup

```bash
# 1. Create the app (no machines yet — release_command runs migrations
#    before any api/worker VM rolls out, per Blocco #4.1 contract).
flyctl apps create instaedit-login

# 2. Provision the production database. The canonical walkthrough is
#    ./scripts/db/provision-postgres-runbook.sh — print it once at the
#    start of the session and step through it. Locked-in parameters
#    (deviating from these means documenting why in a comment next to
#    the runbook and re-committing):
#
#       a) Cluster name       = instaedit-production         (per spec;
#         do NOT use instaedit-pg, instaedit-prod, or any non-spec name)
#       b) Region              = iad                          (matches fly.toml
#         primary_region so api/worker + pg share latency budget)
#       c) VM                  = shared-cpu-1x / 1gb RAM     (cost-balanced
#         for beta; upgradeable via dashboard without recreate)
#       d) HA replicas         = 1                            (one standby
#         for failover; ZERO = no auto-failover)
#       e) PITR retention      = 14 days via dashboard         (Fly default
#         is 7; bumping covers a 2-week incident window)
#       f) Pooler              = built-in PgBouncer (port 6432)
#         (app talks to the pooler; migrations go direct to bypass
#         PgBouncer's DDL-incompatible txn model)
#       g) Password            = openssl rand -base64 48       (384-bit;
#         ONE password, NEVER reused from dev/staging, saved ONLY
#         in the password manager — never in .env.example / git)
#
#    The command emits TWO connection strings — save BOTH in the
#    password manager under separate keys:
#
#       DIRECT (admin / migrations):
#         postgres://<user>:<pw>@instaedit-production.flycast:5432/<db>?sslmode=require
#       POOLED  (app api + worker):
#         postgres://<user>:<pw>@instaedit-production-bouncer.flycast:6432/<db>?sslmode=require&pgbouncer=true
#
#    The POOLED URL is what `make fly-secrets` will push into
#    `DATABASE_URL` on the Fly app. The DIRECT URL stays in the
#    operator's toolbelt only (runbook manual for migrations if
#    release_command ever needs to connect with statement_timeout
#    disabled).

# 3. Smoke check the new cluster from your laptop BEFORE pushing secrets
#    to Fly. This catches sslmode drift + connection issues BEFORE the
#    first deploy attempts to apply migrations:
#
#       DATABASE_URL=<POOL-URL-FROM-PASSWORDMANAGER> \
#           ./scripts/db/check-postgres-health.sh
#    # Expected: "✓ sslmode=require" + "✓ server_version=16.x"
#    #           + "✓ 0 of 9 canary tables present (pre-migration)"
#
#    After `make fly-deploy` succeeds, the same script run again will
#    show "✓ 9 of 9 canary tables present (post-migration)".

# 4. Schedule the FIRST restore drill (mandatory — 24h after first
#    migration). Fly supports PITR out-of-the-box via `fly postgres
#    fork`. The drill script lives at ./scripts/db/production-restore-drill.sh:
#
#       FLY_TS="$(date -u +%Y%m%dT%H%M%SZ)"
#       fly postgres fork \
#           --from instaedit-production \
#           --to "instaedit-restore-drill-$FLY_TS" \
#           --region iad
#       # Wait for fork-ready (~30-180s). Fly prints the new fork's POOLED URI.
#       DATABASE_URL_PROD=<PROD-POOL-URL> \
#       DATABASE_URL=<FORK-POOL-URL> \#    ./scripts/db/production-restore-drill.sh
#    # Expected: schema sha256 MATCH + row counts MATCH + verdict PASS.
#    # The script prints a copy-pasteable `fly postgres destroy ...` command
#    # for cleanup; do NOT auto-destroy (operator must type --yes).

# 5. Tigris bucket: sign up at https://tigrisdata.com, generate Access
#    Key + Secret Key from the dashboard; capture BOTH in the password
#    manager under `instaedit-login/s3/<key>` BEFORE running the
#    provisioning script (the script reads them from env, never CLI args).
#
#    The full canonical bucket-setup runbook lives at
#    ./scripts/s3/provision-tigris.sh — print it once and step through it.
#    Final bucket state (the runbook is idempotent + dry-run-by-default):
#
#       a) Name                = instaedit-prod-media   (matches fly.toml
#         S3_BUCKET = "instaedit-prod-media"; do NOT use instaedit-media
#         or any non-canonical name — the backend / asset_repo invariants
#         assume the exact name once GIN_MODE=release)
#       b) Endpoint            = fly.storage.tigris.dev (public, lives in
#         fly.toml [env] S3_ENDPOINT; not a secret)
#       c) CORS                = single-origin https://app.instaedit.org,
#         methods PUT/GET/HEAD, Expose ETag, MaxAgeSeconds=3600
#         (the application / CSRF contract REQUIRES no other origins; adding
#         the Vercel preview URL would leak the prod bucket to PRs)
#       d) Lifecycle           = AbortIncompleteMultipartUpload after 1 day
#         (no orphan parts from cancelled uploads; no need for a separate
#         bucket-cleanup cron)
#       e) Versioning          = Enabled    (production media + audit
#         trail; protects against accidental overwrite / delete)
#       f) TLS-only            = bucket policy Denies s3:* when
#         aws:SecureTransport=false (defense-in-depth — the SDK already
#         uses HTTPS only)
#       g) Max object size     = 200 MB enforced TWICE: (1) Bucket policy
#         Denies PutObject if s3:content-length > 209715200;
#         (2) backend presigned URL issuance (pkg/api/storage.go) clamps
#         Content-Length to STORAGE_MAX_UPLOAD_BYTES = 200 * 1024 * 1024.
#
#    Run from your laptop AFTER postgres smoke check (§2 step 3) succeeds:
#
#       cd InstaeditLogin
#       AWS_ACCESS_KEY_ID=<tigris-access-key> \
#       AWS_SECRET_ACCESS_KEY=<tigris-secret-key> \
#           ./scripts/s3/provision-tigris.sh          # dry-run; prints intent
#       # Expected: "DRY-RUN COMPLETE — no mutations."
#       #            + 6 steps listed, each prefixed with "→ would"
#
#       # Verify the dry-run output looks sane, THEN:
#       AWS_ACCESS_KEY_ID=<tigris-access-key> \
#       AWS_SECRET_ACCESS_KEY=<tigris-secret-key> \
#           ./scripts/s3/provision-tigris.sh --apply  # commits state
#       # Expected: "✓ PROVISIONING COMPLETE" + 6 ✓ GREEN steps + smoke PASS.
#
#    The script also runs a write+head+delete round-trip under the
#    `ops-smoke-test-<UTC>.txt` key. Anything else = FAIL. Capture the
#    S3_ACCESS_KEY + S3_SECRET_KEY values in the password manager
#    BEFORE running this — they never appear in script output.
```

> The `*_REDIRECT_URI` values in step 3 below MUST also be registered
> in the Meta Developer Console (Facebook Login for Business → Settings
> → Valid OAuth Redirect URIs). Without that, the OAuth round-trip
> fails with "Invalid Redirect URI".

---

## 3. Secret collection

The following **21 secrets** must be set on `instaedit-login`. Where to
get each:

| # | Secret | Where to get it |
|---|--------|-----------------|
| 1 | `DATABASE_URL` | **Pooled URL** from step 2 above (PgBouncer on Fly port 6432 — saves 1 round trip per worker start under burst load). Direct URL stays on the operator's machine only; migrations go direct via release_command. |
| 2 | `JWT_SECRET` | `openssl rand -hex 32` — **separate from dev** |
| 3 | `ENCRYPTION_KEYS` | CSV string: `id:base64key,id:base64key,…` where each `id` is a **uint32** (e.g. `1`, `2`) and each `key` is the base64 of a 32-byte AES-256-GCM key. See "ENCRYPTION_KEYS format" below for the canonical `openssl` one-liner |
| 4 | `ACTIVE_ENCRYPTION_KEY_ID` | The uint32 id of the key in `ENCRYPTION_KEYS` used for **new** encryption. Must be present in the parsed `ENCRYPTION_KEYS` map (validated by `internal/config/config.go`) |
| 5 | `S3_ACCESS_KEY` | Tigris dashboard → "Access Keys" — captured as part of step 5 above (the same keys feed the `./scripts/s3/provision-tigris.sh` dry-run / apply run). The bucket name is `instaedit-prod-media` (per step 5/a). NEVER regenerate keys without rotating BOTH Fly secrets + the Tigris dashboard key — a half-rotated setup will silently fail presigned uploads. |
| 6 | `S3_SECRET_KEY` | Tigris dashboard → "Access Keys" — see row 5 above. After Tigris revokes an old key, run `./scripts/s3/provision-tigris.sh --apply` again with the new creds (the script is idempotent — a regeneration does not require re-creating the bucket). |
| 7 | `EMAIL_PROVIDER_KEY` | Resend dashboard → "API Keys" (starts with `re_`). **Capture now, push to Fly LATER.** As of (post-commit 58742bf Resend unification), the backend does NOT yet wire this key — `internal/config/config.go` has no `EmailProvider*` fields and `pkg/api/magic_link.go::handleMagicLinkStart` returns the magic-link token in the response body (dev fallback). The provider key is needed RIGHT NOW ONLY for the Gmail inbox test in [`docs/OPERATIONS.md` §7.3](./OPERATIONS.md#73-gmail-inbox-test-protocol) and the future backend wiring (separate task). Capture NOW into password manager `instaedit-login/email/EMAIL_PROVIDER_KEY` (resend's dashboard → Create API Key, scope = `Sending Access` ONLY not `Full Access`); do NOT yet add to `.env.production` / `make fly-secrets` until the backend wires Resend. Tracking defaults (open + click) MUST be `false` for transactional magic-link emails — see [`docs/OPERATIONS.md` §7.4](./OPERATIONS.md#74-tracking-verification). |
| 8 | `META_APP_ID` | Meta Developer Console → your app → Settings → Basic |
| 9 | `META_APP_SECRET` | Meta Developer Console → Settings → Basic → "Show" |
| 10 | `FRONTEND_URL` | `https://app.instaedit.org` |
| 11 | `CORS_ALLOWED_ORIGINS` | `https://instaedit.org,https://app.instaedit.org` (comma-separated, **no spaces**) |
| 12 | `COOKIE_DOMAIN` | `.instaedit.org` (leading dot — needed for the SPA to read the csrf_token across subdomains; see `internal/config/config.go` Blocco #2.4) |
| 13 | `INSTAGRAM_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/instagram/callback` |
| 14 | `FACEBOOK_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/facebook/callback` |
| 15 | `THREADS_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/threads/callback` |
| 16 | `X_CLIENT_ID` | X Developer Portal → created app → "Keys and tokens" → "OAuth 2.0 Client ID" (post-App Review for scopes `tweet.read` / `tweet.write` / `users.read` / `offline.access`) |
| 17 | `X_CLIENT_SECRET` | X Developer Portal → created app → "Keys and tokens" → "OAuth 2.0 Client Secret" (show-once; never committed — capture immediately on display) |
| 18 | `X_REDIRECT_URI` | Register `https://api.instaedit.org/api/v1/auth/twitter/callback` in X Developer Portal → Apps → "User authentication settings" → "Callback URIs". Also lives in `fly.toml` `[env]` as a public, non-sensitive value. |
| 19 | `TIKTOK_CLIENT_ID` | TikTok Developer Portal → created app → "App ID" (Client Key, post-App Review for scopes `user.info.basic` + `video.publish`). The Client Key is the alpha-numeric string issued by TikTok when registering a Web/App platform. |
| 20 | `TIKTOK_CLIENT_SECRET` | TikTok Developer Portal → created app → "App secret" (visible ONLY right after creation; if you reload the dashboard later it stops showing — capture immediately). |
| 21 | `TIKTOK_REDIRECT_URI` | Register `https://api.instaedit.org/api/v1/auth/tiktok/callback` in TikTok Developer Portal → created app → "Login Kit" → "Redirect URI" (also surfaced under "App settings" → "Authentication" → "Callback URL"). Also lives in `fly.toml` `[env]` as a public, non-sensitive value. |

**Do NOT include** (disabled providers, beta scope): `YOUTUBE_*`,
`LINKEDIN_*`, `STRIPE_*`. The set script
refuses to push if any of these prefixes appear in the .env file.

**Where to store the .env.production file**:

```bash
# 1. Copy the dev template
cp .env.example .env.production

# 2. Fill in the 21 values above. Use your secret manager (1Password,
#    Bitwarden, …) — never paste real secrets into chat / git / issues.

# 3. Verify the file is gitignored (it should be — `.env` is in
#    .gitignore at the repo root).
ls -la .env.production   # confirm the file exists locally
git check-ignore .env.production || echo "WARN: .env.production is NOT gitignored"
```

> **`ENCRYPTION_KEYS` format (per `internal/crypto/encrypt.go`)**:
> The config loader parses this with `strconv.ParseUint(idStr, 10, 32)`,
> so each id MUST be a uint32 digit string. Each entry is
> `id:base64key` separated by commas (no spaces). The base64 payload
> is the 32-byte AES-256-GCM key. Single-quote the value in the .env
> file to prevent bash from interpreting the `:` or `,`:
>
> ```bash
> # Canonical one-liner: generate one key + print the .env line
> KEY_B64=$(openssl rand -base64 32)
> echo "ENCRYPTION_KEYS='1:$KEY_B64'"
> echo "ACTIVE_ENCRYPTION_KEY_ID=1"
> ```
>
> Example for the .env file:
> ```env
> ENCRYPTION_KEYS='1:Abc123Base64KeyHere,2:Def456AnotherBase64Key'
> ACTIVE_ENCRYPTION_KEY_ID=1
> ```
>
> The bootstrap (`internal/crypto/encrypt.go`) uses the active key for
> **new** encryption operations and the full map (id → base64) for
> **decryption** — so existing tokens encrypted with an older key are
> still readable after rotation, as long as the old entry stays in the
> CSV. See §6 for the zero-downtime rotation runbook.

---

### 3.0 Operator reference manifest (2026-07-14)

**Per-secret status table**: confirms what has already been captured + the shape
constraints the captured value should match + where in the password manager
the captured value lives. **Values are NEVER printed in this manifest** — only
the status + shape (length / charset / regex) + capture location.

| # | Secret | Source (resolved) | Shape (length / charset / regex) | Password manager entry | Captured? | Action ref |
|---|--------|-------------------|----------------------------------|------------------------|-----------|------------|
| 1 | `DATABASE_URL` | Pooled URL from `fly postgres create` (§2 step 2). PgBouncer on port 6432; sslmode=require baked in via flycast | Format spec: see DEPLOY.md §2 step 2 for the canonical DIRECT/POOLED structure (≈ 70–110 chars; Flycast URI; pooler port 6432; sslmode=require; the password URI-component is the only randomized segment). The ACTUAL DATABASE_URL is never printed in this manifest — only the password-manager entry column tells the operator where the captured value lives | `instaedit-login/database-url/production/pooled` ( + `/direct` separately) | ○ PENDING | DEPLOY.md §2 step 2 |
| 2 | `JWT_SECRET` | `openssl rand -hex 32 \| head -c 64` (sha12=`2df3c07a1d40`) | 64 lowercase hex chars / 32 bytes binary (RFC 7518 HS256 minimum per `internal/config/config.go::jwtSecretMinBytes=32`) | `instaedit-login/jwt-secret/production` | ✓ CAPTURED | already in PM |
| 3 | `ENCRYPTION_KEYS` | `openssl rand -base64 32 \| tr -d '\n'` for id=1 (sha12=`94e5775e101d`) | CSV `id:base64,id:base64,...`; each base64 decodes to exactly 32 bytes (AES-256 GCM slot per `internal/crypto/encrypt.go::aesKeyBytes=32`); each id is uint32 in [0, 4294967295] | `instaedit-login/encryption-key-1/production` (one entry per slot) | ✓ CAPTURED (1 slot) | already in PM |
| 4 | `ACTIVE_ENCRYPTION_KEY_ID` | literal `1` (uint32, no randomness; MUST be present in the parsed `ENCRYPTION_KEYS` map) | digit string in [0, 4294967295]; MUST equal one of the ids in the `ENCRYPTION_KEYS` CSV | `instaedit-login/active-encryption-key-id/production` | ✓ CAPTURED (literal `1`) | already in PM |
| 5 | `S3_ACCESS_KEY` | Tigris dashboard → Access Keys → Generate new (paired with row 6) | non-empty (length ≈ 32–40 chars for Tigris tokens) | `instaedit-login/s3-access-key/production` | ○ PENDING | DEPLOY.md §2 step 5 + `scripts/s3/provision-tigris.sh` |
| 6 | `S3_SECRET_KEY` | Tigris dashboard → Access Keys (paired with row 5; rotate the pair ONLY together) | non-empty (length ≈ 32–40 chars) | `instaedit-login/s3-secret-key/production` | ○ PENDING | DEPLOY.md §2 step 5 |
| 7 | `EMAIL_PROVIDER_KEY` | Resend dashboard → API Keys → Create. **CRITICAL**: scope = `Sending Access` ONLY (NOT Full Access) — minimises blast radius if the key leaks | prefix is `re_`; total length ≈ 40 chars | `instaedit-login/email-provider-key/production` | ○ PENDING (NOT yet pushed to Fly because backend does not yet wire Resend — see OPERATIONS.md §7.5) | OPERATIONS.md §7.5 (deferred backend wiring) |
| 8 | `META_APP_ID` | Meta Developer Console → your prod-app → Settings → Basic (App ID) | numeric string (typically 15 digits) | `instaedit-login/meta-app-id/production` | ○ PENDING | DEPLOY.md §6 followup (Meta prod-app review) |
| 9 | `META_APP_SECRET` | Meta Developer Console → Settings → Basic → “Show” | ≥ 32 chars (per `internal/config/config.go::secretMinChars=32`) | `instaedit-login/meta-app-secret/production` | ○ PENDING | DEPLOY.md §6 followup |
| 10 | `FRONTEND_URL` | Canonical per commit `716c709` + DNS §1.5 | exactly `https://app.instaedit.org` (HTTPS required; no trailing slash; no localhost) | N/A (public, lives in `fly.toml` `[env]`) | ✓ STABLE | no action |
| 11 | `CORS_ALLOWED_ORIGINS` | Canonical per commit `716c709` + DNS §1.5 (apex redirect) | exactly `https://instaedit.org,https://app.instaedit.org` (2 comma-separated entries; no spaces) | N/A (public) | ✓ STABLE | no action |
| 12 | `COOKIE_DOMAIN` | Canonical per commit `716c709` + `internal/config/config.go` Blocco #2.4 (cross-subdomain CSRF) | exactly `.instaedit.org` (leading dot — required for cross-subdomain match) | N/A (public) | ✓ STABLE | no action |
| 13 | `INSTAGRAM_REDIRECT_URI` | Canonical per `fly.toml` `[env]`; exact registration in Meta Dev Console | exactly `https://api.instaedit.org/api/v1/auth/instagram/callback` | N/A (public; pinned by Meta console) | ✓ STABLE | no action |
| 14 | `FACEBOOK_REDIRECT_URI` | Canonical per `fly.toml` `[env]` | exactly `https://api.instaedit.org/api/v1/auth/facebook/callback` | N/A (public) | ✓ STABLE | no action |
| 15 | `THREADS_REDIRECT_URI` | Canonical per `fly.toml` `[env]` | exactly `https://api.instaedit.org/api/v1/auth/threads/callback` | N/A (public) | ✓ STABLE | no action |
| 16 | `X_CLIENT_ID` | Sourced from X Developer Portal (post-App Review) | exactly an OAuth 2.0 Client ID (≈ 22-char alphanumeric) | `instaedit-login/x-client-id/production` | ○ PENDING | requires App Review for scopes `tweet.read` / `tweet.write` / `users.read` / `offline.access`; capture **together** with `X_CLIENT_SECRET` in a single password-manager pull |
| 17 | `X_CLIENT_SECRET` | Sourced from X Developer Portal (post-App Review) | exactly an OAuth 2.0 Client Secret (≈ 40-50 chars) | `instaedit-login/x-client-secret/production` | ○ PENDING | captured together with `X_CLIENT_ID` (both surfaced in the same dashboard modal) |
| 18 | `X_REDIRECT_URI` | Canonical per `fly.toml` `[env]` | exactly `https://api.instaedit.org/api/v1/auth/twitter/callback` | N/A (public; pinned by X Developer Portal) | ✓ STABLE | no action |
| 19 | `TIKTOK_CLIENT_ID` | Sourced from TikTok Developer Portal (post-App Review) | exactly a TikTok Client Key (≈ 32 alphanumeric chars) | `instaedit-login/tiktok-client-id/production` | ○ PENDING | requires App Review for scopes `user.info.basic` + `video.publish`; capture **together** with `TIKTOK_CLIENT_SECRET` in a single dashboard pull |
| 20 | `TIKTOK_CLIENT_SECRET` | Sourced from TikTok Developer Portal (post-App Review) | exactly a TikTok Client Secret (≈ 32-50 chars) | `instaedit-login/tiktok-client-secret/production` | ○ PENDING | captured together with `TIKTOK_CLIENT_ID` (both visible only IMMEDIATELY after app creation — capture before page refresh) |
| 21 | `TIKTOK_REDIRECT_URI` | Canonical per `fly.toml` `[env]` | exactly `https://api.instaedit.org/api/v1/auth/tiktok/callback` | N/A (public; pinned by TikTok Developer Portal) | ✓ STABLE | no action |

**Aggregate status (2026-07-14)**: 3 CAPTURED (JWT_SECRET + ENCRYPTION_KEYS[id=1] + ACTIVE_ENCRYPTION_KEY_ID) • 7 STABLE (4 public env + 5 redirect URIs) • 11 PENDING — requires operator-side actions against external services (Fly Postgres / Tigris dashboard / Resend dashboard / Meta Dev Console + **X Developer Portal App Review** for scopes `tweet.read`/`tweet.write`/`users.read`/`offline.access` + **TikTok Developer Portal App Review** for scopes `user.info.basic` + `video.publish`).

**Privacy contract**: the actual secret values are NEVER printed in this manifest or in any commit output. The shape column gives the operator enough metadata to confirm locally (a) the captured value satisfies the input contract (e.g. JWT_SECRET is exactly 64 hex chars), (b) the captured value is correctly stored (the password-manager-entry column matches where the operator saved it). If you ever need to actually verify a value, paste it into your terminal locally WITHOUT piping it to the chat agent.

**Pipeline self-test (pure local, no flyctl needed)**:
```text
# Equivalent to `make fly-secrets-dry-run` minus the bash wrapper's flyctl pre-flight.
# Verified 2026-07-14 on the synthetic shape-valid fixture: exit code 0, 21 keys validated.
# The bash wrapper (make fly-secrets-dry-run) and the parser-direct (this snippet) share
# the SAME _parse_envfile.py contract; the regression suite scripts/test_parse_envfile.py
# pins the contract with 21 invariant tests.
umask 077
python3 scripts/_parse_envfile.py .env.production dry-run instaedit-login scripts \
  2>&1 >/dev/null
# Expected: exit 0 + redacted preview `KEY = first3***last3 (len=N)` per key on stderr.
# Synthetic fixture leak-audit (2026-07-14): none of the 7 known fixture strings appeared
# in stderr; 21 `len=N` preview entries emitted; stdout was 0 bytes.
```

**Operator-sequence prerequisite (sandbox-blocked steps)**:
The push + verify + deploy chain (`make fly-secrets` + `make fly-secrets-verify` + `make fly-deploy`) requires an authenticated `flyctl` session. The bash wrapper pre-flight `[[ -x ./scripts/set-fly-secrets.sh ]]` + `command -v flyctl` + `flyctl auth whoami` gates the actual `flyctl secrets set --stage -` push; the dry-run validates the .env shape upstream of any flyctl call. Pipeline parity is guaranteed by `scripts/test_parse_envfile.py` (15 regression tests pin the contract). To execute the push: complete steps 4–5 of the operator next-steps block (see the `Suggest followups` of the prior turn).

## 4. First deploy (the canonical pipeline)

```bash
# 0. Auth (one-time per machine)
flyctl auth login

# 1. Preview the secrets push (no secrets leave your machine)
make fly-secrets-dry-run
#    → prints a redacted table of all 21 keys + lengths
#    → exits 0 if validation passes

# 2. Stage the secrets on Fly (NO restart triggered)
make fly-secrets
#    → pipes the .env to `flyctl secrets set --app X --stage -` via stdin
#    → Fly banks the secrets; they attach to instances on the next
#      `fly deploy`

# 3. Verify clean state
make fly-secrets-verify
#    → asserts no <redacted>, no disabled-provider keys, all 21 keys present
#    → exits 0 if all checks pass

# 4. Sanity-check fly.toml
make fly-verify
#    → pure-shell parse of fly.toml (app name, processes, health checks)

# 5. Deploy the code (attaches the staged secrets to the new image)
make fly-deploy
#    → runs release_command = "./migrate" first, then rolls api + worker
```

If any step fails, fix the input and re-run from that step. The pipeline
is idempotent — re-running `fly-secrets` overwrites, `fly-deploy`
re-builds and re-rolls.

---

## 5. Post-deploy smoke test

**APPLY ALL of these on operator laptop AFTER `make fly-deploy` exits 0:**

### 5.0 Lightweight shell probes (read-only)

```bash
# 1. Health endpoint
curl -sS https://api.instaedit.org/api/v1/health | jq
#    → {"status":"ok","service":"InstaEdit","version":"...","platforms":["instagram","facebook","threads"]}

# 2. OAuth round-trip (302 → Facebook)
curl -sI https://api.instaedit.org/api/v1/auth/instagram/login
#    → HTTP/1.1 302 Found
#    → Location: https://www.facebook.com/v18.0/dialog/oauth?...

# 3. Cross-subdomain CSRF cookie contract
curl -sI -H "Origin: https://app.instaedit.org" \
  https://api.instaedit.org/api/v1/auth/me | grep -i 'set-cookie'
#    → must include: csrf_token=...; Domain=instaedit.org; Secure; SameSite=None
#      (NO Domain= on session / refresh cookies — they stay host-only.
#       See Blocco #2.4 in internal/config/config.go.)

# 4. Tail logs
flyctl logs --app instaedit-login
```

### 5.1 Comprehensive end-to-end smoke (`scripts/ops/post_deploy_smoke.sh`)

The script above is a quick check (4 probes). The **THOROUGH pre-launch verification** lives in `scripts/ops/post_deploy_smoke.sh` and covers Phase 9 sub-phases 1, 2, 3, 4, 5 + 7:

```bash
# Default mode (read-only — probes only, no prod-state-creation):
./scripts/ops/post_deploy_smoke.sh

# Verbose mode (also creates a real draft post + polls state 30s):
APPLY_PUBLISH=1 ./scripts/ops/post_deploy_smoke.sh

# Against a staging deploy:
BASE_URL=https://staging.instaedit.org ./scripts/ops/post_deploy_smoke.sh
```

Pass criteria: ALL PASS count > 0 AND FAIL count = 0; WARNs are advisory (e.g., magic-link dev-fallback path detected). The script **is adaptive** on Phase 9.1: if the backend reply includes the dev-fallback `magic_link_token` field, the script consumes the token via `/verify` and exercises the cookie/CSRF contract end-to-end. If NOT (production email-wired path), the cookie/CSRF + /accounts + /media sub-phases are SKIPPED with a `DEFERRED` warning (until backend Resend wiring lands — see `docs/OPERATIONS.md` §7.5).

### 5.2 Workspace isolation test (`scripts/ops/workspace_isolation_test.sh`)

Phase 9 sub-phase 6 — verifies user A cannot access user B's data across /accounts + /posts/workspace/{wid} + cross-workspace POST /posts. The script creates 2 fresh users via the **email/password register flow** (not magic-link — the test must NOT depend on Resend email delivery), runs 4 isolation assertions, then **hard-deletes its own test data** via `psql $DATABASE_URL` CASCADE on users matching the random suffix.

```bash
# Preview only (no mutations):
./scripts/ops/workspace_isolation_test.sh --dry-run

# Apply (creates 2 users + 2 workspaces + runs assertions + hard-deletes on EXIT):
DATABASE_URL=postgres://<POOL-URL-FROM-PM> \
  ./scripts/ops/workspace_isolation_test.sh

# Hard cleanup is ALWAYS attempted on EXIT (trap) — even on FAIL.
# If cleanup fails (network, DB unreachable), the script prints the exact
# psql commands to run by hand to remove the test users.
```

Pass criteria: 4/4 PASS; each FAIL exits 1 after cleanup. Cleanup SQL uses the random suffix so even if the trap fails, the operator can run a manual `psql ... WHERE email LIKE 'isol-%-%<SUFFIX>'`.

> Both scripts integrate with the established pattern: idempotent, dry-run-by-default, `set -euo pipefail`, `bash -n` clean, exit codes (0/1/2/3), `mktemp` + `trap` cleanup. See `scripts/db/check-postgres-health.sh` + `scripts/email/check-email-deliverability.sh` + `scripts/s3/provision-tigris.sh` for the canonical pattern.

---

## 6. Rotation

### `JWT_SECRET`

```bash
# Generate the new value
NEW_JWT=$(openssl rand -hex 32)
# Edit .env.production: JWT_SECRET=$NEW_JWT
make fly-secrets                  # stages the new secret
make fly-deploy                   # rolls out (in-flight JWTs are now invalid; users get 401 → re-login)
```

> JWT rotation invalidates ALL in-flight sessions. Plan for a brief
> re-login window. For zero-downtime, you'd need a JWT key ring (not
> in scope for the beta).

### `ENCRYPTION_KEYS` (zero-downtime rotation)

The bootstrap (`internal/crypto/encrypt.go`) uses the active key for
**new** encryption and the full key map for **decryption**. So you can
add a new key alongside the old, roll the deploy, then drop the old
key once all tokens have been re-encrypted.

```bash
# 1. Read the current ENCRYPTION_KEYS + ACTIVE_ENCRYPTION_KEY_ID
grep -E '^(ENCRYPTION_KEYS|ACTIVE_ENCRYPTION_KEY_ID)' .env.production

# 2. Append a new key (e.g. id=2) to the CSV. Generate the key:
NEW_B64=$(openssl rand -base64 32)
#    Then edit .env.production:
#      was: ENCRYPTION_KEYS='1:<OLD>'
#            ACTIVE_ENCRYPTION_KEY_ID=1
#      now: ENCRYPTION_KEYS='1:<OLD>,2:<NEW_B64>'
#            ACTIVE_ENCRYPTION_KEY_ID=1   # CRITICAL: keep on the OLD key
#    Why: ACTIVE_ENCRYPTION_KEY_ID=2 here would mean in-flight writes
#    between deploy 1 and deploy 2 use the new key for ENCRYPTION,
#    but existing tokens still in flight decrypt with the old key on
#    re-read. Setting it to 1 keeps the new key in the map (decrypt)
#    while the old key still owns new writes. The cutover is step 4.

# 3. Push + deploy (no downtime — both keys are accepted on decrypt)
make fly-secrets
make fly-deploy
#    → existing tokens still decrypt with id=1; new writes use id=1.

# 4. Cut over: bump the active id to the new key
#      now: ACTIVE_ENCRYPTION_KEY_ID=2
make fly-secrets
make fly-deploy
#    → existing tokens still decrypt with id=1; new writes use id=2.

# 5. After all tokens have been re-written (watch the metric
#    `instaedit_vault_cipher_id` — it should converge to 2), drop
#    the old key:
#      now: ENCRYPTION_KEYS='2:<NEW_B64>'
make fly-secrets
make fly-deploy
```

### Provider / Mail / S3 rotation

These are usually set-and-forget (rotate the credential in the provider
console, then push the new value to Fly):

```bash
# Edit .env.production
make fly-secrets
make fly-deploy
```

---

## 7. Phase 7: Deploy Backend (operator laptop flow)

The target is `instaedit-login` on Fly.io. Code deploys should only occur *after* secrets are successfully staged (§3).

### 7.1 One-time setup

Ensure you have authenticated your local terminal with Fly.io:

```bash
flyctl auth login
```

### 7.2 Pre-deploy checks

Verify your environment is shaped correctly for the Fly infrastructure:

```bash
make fly-verify
```

*Passes if `fly.toml` matches expected bounds (`min_machines_running=1`, valid process groups `api` and `worker`, `release_command = "./migrate"`).*

### 7.3 Deploy command

```bash
make fly-deploy
```

### 7.4 What `fly-deploy` does

1. **Builds the unified image** via the `[production]` target in `Dockerfile` (api + worker + migrate bundled into a single image so a single `fly deploy` ships all three binaries).
2. **Runs `release_command`** (`./migrate`) in an ephemeral machine to apply pending database schema updates. If migrations fail, Fly ABORTS the rollout and the existing api/worker VMs keep running on their previous image (no half-deployed state).
3. **Rolls the existing instances** (api and worker process groups) with the new image, binding any staged secrets via `fly secrets import --stage` (committed in `make fly-secrets` from §4).

### 7.5 Secret-rotated redeploys

If deploying specifically to lock in a secret rotation (e.g. a new `JWT_SECRET`), remember that all active in-flight worker and HTTP instances will immediately adopt the new value upon restart. For keys like JWT, this drops all current sessions, requiring user re-authentication. For `ENCRYPTION_KEYS` see §6 — the zero-downtime path requires keeping the old key in the CSV during the cutover window.

### 7.6 Live tailing + log privacy

To tail logs during a rollout:

```bash
flyctl logs --app instaedit-login
```

*Privacy contract:* Fly logs must **never** show any of the **21 staged secrets** enumerated in §3 secret collection. That is: `DATABASE_URL` (the password embedded in the URI is just as risky as a separate column), `JWT_SECRET`, `ENCRYPTION_KEYS`, `ACTIVE_ENCRYPTION_KEY_ID`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `EMAIL_PROVIDER_KEY` (the `re_*` Resend token), `META_APP_ID`, `META_APP_SECRET`, plus first-party credentials: `access_token`, `refresh_token`, user passwords, the `csrf_token` value, and any magic-link `?token=` query parameter.

Any such leak is an immediate incident requiring credential revocation. The `fly.toml` contract also relies on the app binary's own `*http.Request` log filter (see `pkg/api/handlers.go` and `internal/services/sessions_service.go`) — the Fly platform strips injected ENV vars from logs by default; we're defending in depth. The canonical secret-name list is pinned in `scripts/_parse_envfile.py` + `scripts/test_parse_envfile.py` (21 regression cases) so any future secret addition automatically inherits the privacy contract.

### 7.7 Common failure modes

- **`release_command` fails:** Usually means the `DATABASE_URL` is pointing to the wrong host (e.g. localhost / Direct URL not Pooled URL) or `sslmode=require` is missing. Run `make fly-secrets-verify` to confirm the secret staged correctly, then re-run `/ready` on a prior-machine if accessible.
- **Worker group failing tcp_checks:** The worker binds `WORKER_HEALTH_PORT` (9090 by default; 0 disables the listener per `cmd/worker/health_listener.go`). Ensure this port matches `fly.toml` service definitions for the worker process group.
- **Health-check timeout:** App takes longer than `grace_period` to boot (default 10s in `fly.toml`). Bump `grace_period` if migrations took longer than ~8s on first start.
- **Image build failure:** Dockerfile stage issues (Go version mismatch; missing build-arg). Retry with `flyctl deploy --config fly.toml --build-only` to isolate image-build problems from rollout problems.

---

## 8. Phase 8: Post-deploy Verification

Perform these 3 gates on the live domain to confirm the rollout succeeded. All probes are read-only — they do NOT depend on a populated session.

### Gate A — Healthz (HTTP api process responding)

```bash
curl -sS https://api.instaedit.org/api/v1/health | jq
```

**Expected envelope** (exact keys per `pkg/api/handlers.go::handleHealth`):

```json
{
  "platforms": ["instagram", "facebook", "threads"],
  "service":   "InstaEditLogin",
  "status":    "ok",
  "version":   "2.0.0"
}
```

*What this proves:* The HTTP listener binds successfully on port 8080, the Go handlers package is reachable, and the provider capabilities block initialized (no provider-secret-missing panic on startup). It does NOT prove wire-level correctness — Phase 9.4 covers that.

### Gate B — Readiness (DB + migrations + worker goroutines)

```bash
curl -sS https://api.instaedit.org/ready | jq
```

**Expected envelope** (exact keys per `pkg/api/ready.go::readinessResponse`, in priority order):

```json
{
  "status":         "ok",
  "db":             "ok",
  "migrations":     "ok",
  "workers_ready":  true
}
```

> **CAVEAT (schema-inferred):** The healthy-state envelope (`workers_ready: true`) above is inferred from `pkg/api/ready.go::readinessResponse` + `pkg/api/worker_status.go::startedFields`. Live evidence on **2026-07-14 ONLY observed the FAILURE shape** (HTTP 503 + `workers_pending: [...]`). The healthy state has **no live evidence yet** — once `make fly-deploy` succeeds, the fresh probe should match the JSON above; if it returns something different, treat it as a regression and re-read `pkg/api/worker_status.go::startedFields` against the new response shape.

If failing, `"status"` reads `"not_ready"`, AND you may see `"workers_pending": ["metrics", "outbox", "publish", "reconcile", "webhook"]` (the 5 startup-race losers that haven't yet flipped their `atomic.Bool` to true per `pkg/api/worker_status.go`), OR a non-`"ok"` value on `db` / `migrations`.

*What this proves:* Postgres connection pool is active, all 9 canary tables (schema head from the migrations in `internal/database/migrations/`) exist, and all 5 background goroutines reached their first executable line without deadlocking.

### Gate C — Fly machine state

```bash
flyctl status --app instaedit-login
```

*What this proves:* At least 2 machines are running in `started` state — 1 with `processes = ["api"]` and 1 with `processes = ["worker"]` — per the `min_machines_running = 1` per-process-group contract in `fly.toml`. Any per-process-group count less than 1 means Fly auto-stop kicked in (shouldn't happen with `min_machines_running=1` unless the app is being regionally migrated).

### 8.1 Current status (2026-07-14) — INVESTIGATION REQUIRED before trusting any probe

**CRITICAL WARNING:** As of today, the live `api.instaedit.org` is **almost certainly NOT** the latest `a74f575` deploy, and is **likely NOT** a Fly-deployed binary at all. Operators MUST investigate before assuming a `fly-deploy` will seamlessly replace what is responding today.

Live probes via `curl -i` from a sandboxed host on 2026-07-14 returned:

| Probe | Expected | Actual | Interpretation |
|-------|----------|--------|----------------|
| `GET /api/v1/health` | 200 | **200** + `{"platforms":["threads"],"service":"InstaEditLogin","status":"ok","version":"2.0.0"}` | API is alive, but `platforms=["threads"]` (NOT 3 platforms — our latest has IG/FB/Threads arrays of providers successfully configured) |
| `GET /ready` | 200 | **503** + `{"status":"not_ready","db":"ok","migrations":"ok","workers_pending":["metrics","outbox","publish","reconcile","webhook"]}` | All 5 worker loops NOT yet flipped their `workers_ready` atomic.Bool |
| `GET /api/v1/accounts` | 401 | **404** (NOT 401) | Our commit `033ab78`'s `handleListAccounts` route is NOT mounted on the live build — suggests a stale or partially-deployed codebase |
| `POST /api/v1/auth/magic-link/start` | 200 | **404** | Our existing magic-link-start route is not mounted either |
| `Server:` header + `Fly-Region` header + other Fly proxy signals | Fly-specific | **`Server: Caddy`** AND NO `Fly-Region` AND NO `fly-request-id` | This COMBINED signature is strong cross-evidence for a non-Fly origin. Fly's edge proxy always emits `fly-request-id` + `fly-region`; their absence (alongside a non-Fly `Server:`) is very unlikely to be coincidence. A Caddy `Server:` alone could be a custom entrypoint layer; the COMBINATION (Caddy + missing Fly proxy signals) is the diagnostic. |

**Concrete hypotheses to disambiguate (test in this order):**

1. **DNS re-pointed away from Fly**: run `dig +short api.instaedit.org CNAME` on the operator laptop — expected canonical is `instaedit-login.fly.dev.` per §1.5. If it resolves to a different host (e.g. a Caddy-box A record, a Vercel proxy, a personal-server IP), the DNS CNAME has been re-pointed by a prior session and `make fly-deploy` will not claim this endpoint.
2. **Stale Fly deploy not yet reached this endpoint**: if the CNAME IS `instaedit-login.fly.dev.`, run `flyctl status --app instaedit-login` to confirm the app exists; if `Image` tag doesn't match the SHA just pushed, it's a prior-rollout artifact (older Go binary lacking our `handleListAccounts` from commit `033ab78`).
3. **Other developer's isolated deploy**: if the live response shows code paths that don't exist anywhere in our `main` (e.g. platform count from earlier wiring), it's a separate instance addressing the same CNAME during testing.

Do NOT assume a `fly-deploy` will seamlessly replace what is responding today. The live verify (Gate A + Gate B + Gate C in this section) MUST be re-run after each `make fly-deploy` to confirm the new image is the one serving.

**Operator action sequence before declaring a fresh deploy successful:**

1. Run `dig +short api.instaedit.org CNAME` — confirm it still resolves to `instaedit-login.fly.dev.` per §1.5. If it points elsewhere (e.g. Caddy's box), the CNAME has been re-pointed; update.
2. Confirm `flyctl status --app instaedit-login` lists at least one healthy machine whose `Image` tag matches the SHA just pushed.
3. Re-run all 3 gates (A + B + C) — Gate B in particular must return `workers_ready: true` BEFORE declaring the rollout successful.

### 8.2 Deeper probes (operator laptop)

Beyond the 3 gates, run the canonical post-deploy E2E runbooks (NO code commit needed; these invoke existing scripts):

```bash
# Comprehensive Phase 9 sub-1-5+7 smoke (read-only by default)
make ops-smoke

# Workspace isolation (Phase 9 sub-6) — creates 2 users + asserts cross-tenant boundaries
make ops-isolation-dry-run     # preview the plan + cleanup SQL without mutating
DATABASE_URL=postgres://...@instaedit-production-bouncer.flycast:6432/instaedit-production?sslmode=require \
  make ops-isolation           # apply: 2 users + 4 assertions + psql CASCADE on EXIT
```

Both scripts are idempotent + bash -n clean (per commit `a74f575` review). Pass criteria: ALL PASS count > 0 AND FAIL count = 0; WARNs are advisory.

---

## 9. Phase 9: Sandbox vs Operator Boundary

There is a hard boundary between what the Codex agent sandbox can verify locally and what strictly requires the operator's laptop with authenticated Fly.io access.

### 9.1 Local Sandbox (CAN verify)

- HTTP probes against `https://api.instaedit.org` (the sandbox has outbound internet egress — see today's live probes in §8.1).
- `make fly-verify` — pure-shell parse of `fly.toml` (app name, processes, health checks, env surface counts).
- `make lint-check` (gofmt + go vet + oxlint) — confirms Go code is lint-clean regardless of deploy state.
- `make fly-secrets-test` — runs the .env parser's 15-case regression suite in `scripts/test_parse_envfile.py`.
- Local file inspection (grep, awk, jq on git-tracked files).
- Static code review against the just-committed source tree on `main`.

### 9.2 Operator Laptop (REQUIRES Fly auth + raw secrets)

- `flyctl` CLI invocation (Fly OAuth session).
- Real `.env.production` file with actual secret values.
- `make fly-secrets` — the actual `flyctl secrets import --stage -` push.
- `make fly-deploy` — the actual `flyctl deploy` push.
- `flyctl logs --app instaedit-login` — live log tailing during rollout.
- `flyctl status --app instaedit-login` — Gate C of §8.

### 9.3 Canonical Deploy Execution Block (paste-ready)

> **Safety property reminder:** `make fly-deploy` runs `release_command = "./migrate"` BEFORE any api/worker VM rollouts. If migrations fail, Fly aborts the rollout and the existing api/worker VMs keep running on their previous image (`pkg/api/ready.go` will keep reporting `status: "ok"` because the existing VMs are still healthy). An exit-0 from `make fly-deploy` is therefore the ONLY acceptable deployment success signal — partial deploys cannot occur.

For a fresh environment, paste this EXACT block into the operator's authenticated terminal session:

```bash
# ----- phase 7: deploy backend (this document, §7) -----

# 0. One-time per machine
flyctl auth login

# 1. Verify the .env.production file is clean (no leftover placeholder values, no disabled provider keys)
make fly-secrets-test        # local: 15 regression cases pass
make fly-secrets-dry-run     # local: parser-direct redacted preview

# 2. Push the 21 secrets to Fly (--stage = no premature restart)
make fly-secrets             # operator: flyctl secrets import --stage

# 3. Verify staged secrets are clean on Fly
make fly-secrets-verify      # operator: flyctl secrets list + assertions

# 4. Sanity-check fly.toml pre-deploy
make fly-verify              # local: pure-shell parse

# 5. Ship it
make fly-deploy              # operator: flyctl deploy --config fly.toml
                             #   -> release_command ./migrate (rolling abort on failure)
                             #   -> rollout api group   (http_checks /api/v1/health)
                             #   -> rollout worker group (tcp_checks :9090)

# ----- phase 8: post-deploy verification (§8) -----

# Gate A — Healthz (HTTP api + provider init)
curl -sS https://api.instaedit.org/api/v1/health | jq

# Gate B — Readiness (DB + 9 canary tables + 5 worker goroutines)
curl -sS https://api.instaedit.org/ready | jq

# Gate C — Fly machine state (>= 1 api + 1 worker per min_machines_running)
flyctl status --app instaedit-login

# ----- phase 9: deeper E2E (§9 + Phase 9.4-7) -----

# Read-only smoke (Phase 9 sub-1-5+7)
make ops-smoke

# Workspace isolation drill (Phase 9 sub-6) — REQUIRES DATABASE_URL locally
make ops-isolation-dry-run   # preview only
# single-line to avoid `\` continuation + inline-comment shell-parse ambiguity:
DATABASE_URL=postgres://...@instaedit-production-bouncer.flycast:6432/instaedit-production?sslmode=require make ops-isolation   # apply + CASCADE cleanup on EXIT
```

**Reconciliation notes:**
- Satisfies `docs/OPERATIONS.md §5` go-live gate (the 9-box checklist).
- The deploy sequence aligns with `internal/bootstrap/app.go::RunMigrationThenServer` — migrations run before any HTTP listener binds.
- `make ops-smoke` / `make ops-isolation` are wired via `Makefile` targets added in commit `a74f575`.

---

## 10. Troubleshooting

### `❌ flyctl not installed`
Install: https://fly.io/docs/hands-on/install-flyctl/

### `❌ Not authenticated with Fly.io`
Run `flyctl auth login` (opens a browser OAuth flow).

### `❌ <redacted> placeholder found in .env.production`
You left a literal `<redacted>` string in your env file. Replace it
with the real value (e.g., from 1Password). The script refuses to push
a placeholder.

### `❌ disabled-provider secret detected in .env.production (pattern: ^STRIPE_*)`
Beta scope excludes Stripe / TikTok / X / YouTube / LinkedIn. Remove
the line from `.env.production` (you can leave it commented for
context) and re-run.

### `❌ missing required keys: META_APP_SECRET`
You forgot to set one of the 15. See §3 for the full list + where to
get each.

### `App is not deployed` (during `fly secrets import`)
The app must exist before you can stage secrets. Run
`flyctl apps create instaedit-login` first (see §2).

### Secrets staged but instances don't see them
You ran `fly-secrets` but skipped `fly-deploy`. `--stage` banks the
secrets on Fly; they attach to instances only on the next deploy.
Run `make fly-deploy`.

### `release_command` fails on first deploy
The release_command runs `./migrate` against `DATABASE_URL`. If it
fails, check:
- `DATABASE_URL` is set as a secret (run `make fly-secrets-verify`).
- The Postgres is reachable from Fly's network (Fly Postgres is on
  Flycast, so this should be automatic if you used `flyctl postgres
  attach`).
- The migrations in `internal/database/migrations/` are valid SQL
  (the file count should match the latest `db/migrations/` mirror).

### `min_machines_running = 1` and you see two healthy instances
That's normal during a rolling deploy. The new VM comes up healthy
on `/api/v1/health` before the old VM is torn down.

---

## 11. Cross-references

| Concern | Reference |
|---------|-----------|
| fly.toml secrets policy | `fly.toml` (header comment block) |
| Config env validation | `internal/config/config.go` (Blocco #2.4) |
| CSRF cookie Domain semantics | `internal/auth/csrf.go` + Blocco #2.4 |
| API health endpoint | `pkg/api/handlers.go` (`/api/v1/health`) |
| Process groups (api / worker) | `Makefile` (`fly-help`, `fly-verify`) + `Dockerfile` (Blocco #4.1) |
| Migrations | `internal/database/migrations/` (apply via `release_command`) |
| Local dev handoff | `HANDOFF-LINUX.md` |
| Operational runbook (DNS, certs, monitoring, recovery) | **[`docs/OPERATIONS.md`](./OPERATIONS.md)** |
| OpenAPI spec | `api/openapi.yaml` |

---

## 12. Frontend deploy (Vercel)

The Vite SPA (`web/`) deploys to Vercel; the Go backend deploys to Fly
(§2–§7). The two are decoupled — the frontend is a static bundle that
hits the backend over HTTPS. This section is the canonical reference
for the first Vercel setup + subsequent preview/production deploys.

### 12.1 Pre-flight

- Vercel account (https://vercel.com/signup) — sign up with GitHub for
  the auto-deploy integration.
- The `InstaeditLogin` repo connected to Vercel via the GitHub app.
- (Optional) `vercel` CLI for env-var management from the terminal:
  `npm i -g vercel`.

### 12.2 Project settings (Vercel dashboard)

Set these in the project's **Settings → General** page. They are
file-equivalent in `web/vercel.json` (so re-importing the project
preserves them) but the dashboard wins for the canonical values:

| Setting | Value | Source of truth |
|---------|-------|-----------------|
| **Root Directory** | `web` | Vercel project setting (NOT in vercel.json) |
| **Framework Preset** | Vite | `web/vercel.json` (`"framework": "vite"`) |
| **Install Command** | `npm ci` | `web/vercel.json` (`"installCommand"`) |
| **Build Command** | `npm run build` | `web/vercel.json` (`"buildCommand"`) |
| **Output Directory** | `dist` | `web/vercel.json` (`"outputDirectory"`) |
| **Node.js Version** | 22.12 | `web/vercel.json` (`"engines.node"`) + the Vercel runtime selector |

> **Node version precedence**: Vercel resolves the runtime as
> `vercel.json` `engines.node` → `package.json` `engines.node` →
> project setting → default. The `engines.node` in `web/package.json`
> is `>=20.19.0` (loose, so local dev works on any modern Node) —
> `web/vercel.json` pins the Vercel production runtime to 22.12. They
> do NOT need to match: local dev = minimum, Vercel = exact.

### 12.3 SPA rewrites (history push for React Router)

React Router uses the browser history API (e.g. `/connections`,
`/compose`, `/posts`). Vercel must serve `index.html` for ALL
non-asset routes so the client-side router can take over.

`web/vercel.json` already configures this:

```json
"rewrites": [
  { "source": "/(.*)", "destination": "/index.html" }
]
```

This rewrites every request that doesn't match a static asset in
`dist/` to `/index.html`. Vite emits assets at well-known paths
(`/assets/index-*.js`, `/assets/index-*.css`, `/favicon.ico`, etc.)
which Vercel serves before the rewrite rule fires, so the fallback
is safe.

> The rewrites block is technically redundant with
> `"framework": "vite"` (Vercel auto-configures the SPA fallback for
> Vite projects). We keep it explicit for readability — a future
> maintainer who deletes the `framework` field won't silently break
> client-side routing.

If a future route legitimately needs a different file (e.g.
`/robots.txt`, `/sitemap.xml`), add an explicit `routes` entry BEFORE
the catch-all rewrite — the first match wins.

### 12.4 Environment variables

Set these in **Settings → Environment Variables**. For each var, pick
the scope (Production / Preview / Development). For beta, only
Production matters.

| Variable | Value | Scope | Notes |
|----------|-------|-------|-------|
| `VITE_API_BASE_URL` | `https://api.instaedit.org` | Production | The Fly-deployed backend. Preview deployments can override this to a Fly preview URL or stay on production — see §12.7. |

CLI equivalent (after `vercel login`):

```bash
cd web
vercel env add VITE_API_BASE_URL production
# paste: https://api.instaedit.org
```

> **Do NOT** put the production URL in `web/.env.example` or
> `web/.env.production` — Vercel env vars override file-based ones
> at build time, and committing a `.env.production` to the repo would
> leak the URL to anyone with repo read access.

### 12.5 Build-time validation

`web/vite.config.ts` ships with a `verifyApiBaseUrlPlugin` that
inspects `VITE_API_BASE_URL` at build start. The plugin:

- **Production** build (`vite build` with `VERCEL_ENV=production`):
  FAILS the build if the URL is missing, non-https, or pointing to
  `localhost`. This catches the classic "Vercel stale deploy" bug
  class before it ships to users.
- **Preview** build (PR previews): WARNS but does not fail — the
  operator may legitimately point a preview at a Fly staging URL.
- **Local** build: silent on success, warns on the dev defaults.

See `web/scripts/verify-api-base-url.ts` for the validation rules.

### 12.6 First deploy

```bash
# 1. Push to main (Vercel auto-detects the push via the GitHub app)
git push origin main

# 2. Watch the deploy in the Vercel dashboard
#    → "Building…" → "Deploying…" → "Ready" (or "Error" with logs)

# 3. Smoke test the production URL
#    (If you haven't set up the custom domain yet, use the Vercel-assigned
#     default URL from the dashboard — same rewrite contract applies.)
curl -sSI https://app.instaedit.org | head -5
#    Expected: HTTP/2 200 + a Vercel header
curl -sS https://app.instaedit.org | grep -o '<title>[^<]*</title>'
#    Expected: <title>InstaEdit — ...</title>

# 4. Smoke test the SPA route (history push)
curl -sSI https://app.instaedit.org/connections | head -3
#    Expected: HTTP/2 200 (NOT 404 — the rewrite rule kicks in)
```

### 12.7 Preview deployments (per PR)

Vercel auto-creates a preview deployment for every PR. The preview
URL looks like `https://instaedit-login-git-<branch>-<team>.vercel.app`.
The preview can either:

- Use the **same** `VITE_API_BASE_URL` as production (simplest, hits
  the real Fly backend — use with caution on user-facing features).
- Use a **per-PR** override (Settings → Environment Variables → add
  `VITE_API_BASE_URL` scoped to "Preview" with a different value,
  e.g. a Fly staging URL).

For the beta, leave the Preview scope empty so previews hit the
production Fly backend (single source of truth, simplest to debug).

### 12.8 Troubleshooting

#### Build fails: "VITE_API_BASE_URL validation failed in production context"
The `verifyApiBaseUrlPlugin` rejected the env. Common causes:
- Forgot to set the env var in the Vercel dashboard (§12.4).
- Set the env var on the wrong scope (Preview only, not Production).
- The value is `http://...` instead of `https://...` (Vite treats
  `http://api.instaedit.org` as an error in production because
  mixed-content + CORS issues).
- The value is `http://localhost:8080` (the local-dev default
  leaked into the production env).

Fix: set the correct value in **Settings → Environment Variables**,
then redeploy (the dashboard has a "Redeploy" button on the failed
deploy that re-runs with the new env).

#### SPA route returns 404 on hard refresh
The `vercel.json` rewrites block is missing or wrong. Verify:
```bash
cat web/vercel.json | jq '.rewrites'
# Expected: [{ "source": "/(.*)", "destination": "/index.html" }]
```

#### "Build failed: Could not resolve …"
Usually a missing dev dep or a typescript error. Check:
- `web/package.json` has all required deps
- `cd web && npm ci` succeeds locally
- `cd web && npm run build` succeeds locally

#### "Deploy succeeded but the page shows the Vercel default"
The Output Directory is wrong. Vercel is serving an empty `dist/`.
Verify `web/vercel.json` has `"outputDirectory": "dist"` AND the
Vite build actually emitted files to `dist/` (check the build log).
