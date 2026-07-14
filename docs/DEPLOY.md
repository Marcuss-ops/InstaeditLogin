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

Quote minimally here for the deploy path; the **full DNS runbook (cert renewal, failure recovery, monitoring) lives in [docs/OPERATIONS.md §1](./OPERATIONS.md#1-dns-records-instaeditorg)**.

| Host | Type | Value | TTL | Purpose |
|------|------|-------|-----|---------|
| `instaedit.org` (apex) | `A` | `76.76.21.21` | 60 | Vercel Anycast — 301 redirects to `app.instaedit.org`. Apex cannot use CNAME (DNS spec); use Vercel's A record + dashboard-level redirect (canonical over ALIAS-flattening for portability across registrars). |
| `app.instaedit.org` | `CNAME` | `cname.vercel-dns.com.` | 60 | Vercel edge route to the SPA. |
| `api.instaedit.org` | `CNAME` | `instaedit-login.fly.dev.` | 300 | Fly.io ingress for the backend. **Never** hardcode A records from `fly ips list` — Fly re-IPs during migrations and the CNAME keeps failover transparent. |
| `_vercel.instaedit.org` | `TXT` | `vc-domain-verify=<token-from-Vercel>` | 300 | Vercel domain-ownership challenge. Token is surfaced in Vercel → Project → Settings → Domains; paste as-is. |
| `instaedit.org` (apex) | `CAA` | `0 issue "letsencrypt.org"` | 3600 | Restrict cert issuance to Let's Encrypt (both Fly and Vercel use LE). |
| `instaedit.org` (apex) | `CAA` | `0 iodef "mailto:security@instaedit.org"` | 3600 | Incident reporting for unauthorized issuance attempts. |
| `_dmarc.instaedit.org` | `TXT` | `v=DMARC1; p=reject; rua=mailto:security@instaedit.org` | 3600 | Anti-spoofing for `no-reply@instaedit.org`. Tighten to `p=quarantine` first if some legit mail relays reject `p=reject`. |

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

The following **15 secrets** must be set on `instaedit-login`. Where to
get each:

| # | Secret | Where to get it |
|---|--------|-----------------|
| 1 | `DATABASE_URL` | **Pooled URL** from step 2 above (PgBouncer on Fly port 6432 — saves 1 round trip per worker start under burst load). Direct URL stays on the operator's machine only; migrations go direct via release_command. |
| 2 | `JWT_SECRET` | `openssl rand -hex 32` — **separate from dev** |
| 3 | `ENCRYPTION_KEYS` | CSV string: `id:base64key,id:base64key,…` where each `id` is a **uint32** (e.g. `1`, `2`) and each `key` is the base64 of a 32-byte AES-256-GCM key. See "ENCRYPTION_KEYS format" below for the canonical `openssl` one-liner |
| 4 | `ACTIVE_ENCRYPTION_KEY_ID` | The uint32 id of the key in `ENCRYPTION_KEYS` used for **new** encryption. Must be present in the parsed `ENCRYPTION_KEYS` map (validated by `internal/config/config.go`) |
| 5 | `S3_ACCESS_KEY` | Tigris dashboard → "Access Keys" — captured as part of step 5 above (the same keys feed the `./scripts/s3/provision-tigris.sh` dry-run / apply run). The bucket name is `instaedit-prod-media` (per step 5/a). NEVER regenerate keys without rotating BOTH Fly secrets + the Tigris dashboard key — a half-rotated setup will silently fail presigned uploads. |
| 6 | `S3_SECRET_KEY` | Tigris dashboard → "Access Keys" — see row 5 above. After Tigris revokes an old key, run `./scripts/s3/provision-tigris.sh --apply` again with the new creds (the script is idempotent — a regeneration does not require re-creating the bucket). |
| 7 | `EMAIL_PROVIDER_KEY` | Resend dashboard → "API Keys" (starts with `re_`) |
| 8 | `META_APP_ID` | Meta Developer Console → your app → Settings → Basic |
| 9 | `META_APP_SECRET` | Meta Developer Console → Settings → Basic → "Show" |
| 10 | `FRONTEND_URL` | `https://app.instaedit.org` |
| 11 | `CORS_ALLOWED_ORIGINS` | `https://instaedit.org,https://app.instaedit.org` (comma-separated, **no spaces**) |
| 12 | `COOKIE_DOMAIN` | `.instaedit.org` (leading dot — needed for the SPA to read the csrf_token across subdomains; see `internal/config/config.go` Blocco #2.4) |
| 13 | `INSTAGRAM_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/instagram/callback` |
| 14 | `FACEBOOK_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/facebook/callback` |
| 15 | `THREADS_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/threads/callback` |

**Do NOT include** (disabled providers, beta scope): `TIKTOK_*`, `X_*`,
`X_CLIENT_*`, `YOUTUBE_*`, `LINKEDIN_*`, `STRIPE_*`. The set script
refuses to push if any of these prefixes appear in the .env file.

**Where to store the .env.production file**:

```bash
# 1. Copy the dev template
cp .env.example .env.production

# 2. Fill in the 15 values above. Use your secret manager (1Password,
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

## 4. First deploy (the canonical pipeline)

```bash
# 0. Auth (one-time per machine)
flyctl auth login

# 1. Preview the secrets push (no secrets leave your machine)
make fly-secrets-dry-run
#    → prints a redacted table of all 15 keys + lengths
#    → exits 0 if validation passes

# 2. Stage the secrets on Fly (NO restart triggered)
make fly-secrets
#    → pipes the .env to `flyctl secrets set --app X --stage -` via stdin
#    → Fly banks the secrets; they attach to instances on the next
#      `fly deploy`

# 3. Verify clean state
make fly-secrets-verify
#    → asserts no <redacted>, no disabled-provider keys, all 15 keys present
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

## 7. Troubleshooting

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

## 8. Cross-references

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

## 9. Frontend deploy (Vercel)

The Vite SPA (`web/`) deploys to Vercel; the Go backend deploys to Fly
(§2–§7). The two are decoupled — the frontend is a static bundle that
hits the backend over HTTPS. This section is the canonical reference
for the first Vercel setup + subsequent preview/production deploys.

### 9.1 Pre-flight

- Vercel account (https://vercel.com/signup) — sign up with GitHub for
  the auto-deploy integration.
- The `InstaeditLogin` repo connected to Vercel via the GitHub app.
- (Optional) `vercel` CLI for env-var management from the terminal:
  `npm i -g vercel`.

### 9.2 Project settings (Vercel dashboard)

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

### 9.3 SPA rewrites (history push for React Router)

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

### 9.4 Environment variables

Set these in **Settings → Environment Variables**. For each var, pick
the scope (Production / Preview / Development). For beta, only
Production matters.

| Variable | Value | Scope | Notes |
|----------|-------|-------|-------|
| `VITE_API_BASE_URL` | `https://api.instaedit.org` | Production | The Fly-deployed backend. Preview deployments can override this to a Fly preview URL or stay on production — see §9.7. |

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

### 9.5 Build-time validation

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

### 9.6 First deploy

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

### 9.7 Preview deployments (per PR)

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

### 9.8 Troubleshooting

#### Build fails: "VITE_API_BASE_URL validation failed in production context"
The `verifyApiBaseUrlPlugin` rejected the env. Common causes:
- Forgot to set the env var in the Vercel dashboard (§9.4).
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
