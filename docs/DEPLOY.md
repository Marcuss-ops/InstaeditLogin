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
| DNS for `instaedit.org` | registrar (or Cloudflare) — delegate A/CNAME per Fly's TLS setup guide |

---

## 2. One-time app setup

```bash
# 1. Create the app (no machines yet — release_command runs migrations
#    before any api/worker VM rolls out, per Blocco #4.1 contract).
flyctl apps create instaedit-login

# 2. Create the managed Postgres (Fly's "upstash-like" managed PG).
flyctl postgres create --name instaedit-pg --region iad
# Output: Connection string of the form
#   postgres://<user>:<pass>@instaedit-pg.flycast:5432/instaedit?sslmode=disable
# That's your DATABASE_URL.

# 3. Attach the Postgres to the app (writes DATABASE_URL as a
#    *secret* on the app — but the set-fly-secrets.sh script will
#    re-set it from your .env.production, so this is a fallback).
flyctl postgres attach instaedit-pg --app instaedit-login

# 4. Tigris bucket: sign up at https://tigrisdata.com, create a
#    bucket named e.g. "instaedit-prod-uploads", copy the Access
#    Key + Secret Key from the dashboard. These are S3_ACCESS_KEY
#    + S3_SECRET_KEY.
```

> The `*_REDIRECT_URI` values in step 3 below MUST also be registered
> in the Meta Developer Console (Facebook Login for Business → Settings
> → Valid OAuth Redirect URIs). Without that, the OAuth round-trip
> fails with "Invalid Redirect URI".

---

## 3. Secret collection

The following **14 secrets** must be set on `instaedit-login`. Where to
get each:

| # | Secret | Where to get it |
|---|--------|-----------------|
| 1 | `DATABASE_URL` | Step 2's `flyctl postgres create` output (or Neon/Supabase dashboard) |
| 2 | `JWT_SECRET` | `openssl rand -hex 32` — **separate from dev** |
| 3 | `ENCRYPTION_KEYS` | CSV string: `id:base64key,id:base64key,…` where each `id` is a **uint32** (e.g. `1`, `2`) and each `key` is the base64 of a 32-byte AES-256-GCM key. See "ENCRYPTION_KEYS format" below for the canonical `openssl` one-liner |
| 4 | `ACTIVE_ENCRYPTION_KEY_ID` | The uint32 id of the key in `ENCRYPTION_KEYS` used for **new** encryption. Must be present in the parsed `ENCRYPTION_KEYS` map (validated by `internal/config/config.go`) |
| 5 | `S3_ACCESS_KEY` | Tigris dashboard → "Access Keys" |
| 6 | `S3_SECRET_KEY` | Tigris dashboard → "Access Keys" |
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

# 2. Fill in the 14 values above. Use your secret manager (1Password,
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
#    → prints a redacted table of all 14 keys + lengths
#    → exits 0 if validation passes

# 2. Stage the secrets on Fly (NO restart triggered)
make fly-secrets
#    → pipes the .env to `flyctl secrets import --app X --stage`
#    → Fly banks the secrets; they attach to instances on the next
#      `fly deploy`

# 3. Verify clean state
make fly-secrets-verify
#    → asserts no <redacted>, no disabled-provider keys, all 14 keys present
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
You forgot to set one of the 14. See §3 for the full list + where to
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
| OpenAPI spec | `api/openapi.yaml` |
