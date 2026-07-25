# Deploy — VPS production stack for `instaedit-login`

Canonical reference for first deploy and ongoing secret rotation to the
VPS production target. **This is the single source of truth for production
deployment as of 2026-07-25.** The historical Fly.io / Vercel
configurations are removed from the live path; their files (`fly.toml`,
`scripts/set-fly-secrets.sh`, `scripts/verify-fly-secrets.sh`, the `web/`
Vercel preview workflow, etc.) were deleted in commits `7e8beec`,
`615314b`, and `5ac159c`. The remaining references in the repo are
archaeological only.

## Topology (one diagram, locking in the cutover)

```text
        ┌───────────────────────────────────────────────────────┐
        │                      Public DNS                       │
        │   apex  →  A  51.91.11.36   (VPS)                     │
        │   app.  →  A  51.91.11.36   (VPS)                     │
        │   api.  →  A  51.91.11.36   (VPS)                     │
        │   email-deliverability (SPF/DKIM/DMARC) → Resend      │
        └───────────────────────────┬───────────────────────────┘
                                    │ TCP/80 + TCP/443 (LE via HTTP-01)
                                    ▼
        ┌───────────────────────────────────────────────────────┐
        │ VPS — single host, IP 51.91.11.36                    │
        │                                                       │
        │   ┌─────────────┐    Let's Encrypt (auto-renew)      │
        │   │   Caddy     │◄──────── TLS termination (SNI)      │
        │   │   :80/:443  │                                     │
        │   └──┬───────┬──┘                                     │
        │      │       │                                        │
        │      │ SNI = apex / app. → SPA (static)               │
        │      │         serve /srv/instaedit/web/dist/         │
        │      │                                                │
        │      │ SNI = api. → reverse_proxy 127.0.0.1:8080      │
        │      │                                                │
        │      ▼                                                │
        │   ┌─────────────────────────────────────────────┐     │
        │   │ docker compose (one daemon, one project)    │     │
        │   │                                              │     │
        │   │   api      :8080   cmd/api                   │     │
        │   │   worker   —       cmd/worker (5 goroutines) │     │
        │   │   migrate  —       one-shot pre-deploy      │     │
        │   │   caddy    :443    tls + reverse-proxy      │     │
        │   │   minio    :9000   S3-compatible media store │     │
        │   │   postgres :5432   postgres:17-alpine        │     │
        │   │   (all on the compose network; pg + minio    │     │
        │   │    bound to 127.0.0.1 for external safety)   │     │
        │   └─────────────────────────────────────────────┘     │
        └───────────────────────────────────────────────────────┘
```

API and worker share one image (Dockerfile `[production]` target builds
the unified bundle the way Fly used to). Migration runs as a one-shot
container before any `api` / `worker` container starts. Postgres and
MinIO are NOT exposed to the public network — only Caddy listens on
80/443.

Pre-cutover evidence is preserved in `docs/VPS-DEPLOY-STATUS.md` and
`docs/DEPLOY-AUDIT.md`.

---

## 1. Pre-flight

Tools + accounts required on the operator laptop (or CI runner):

| Tool / Account | Where to get it |
|----------------|-----------------|
| `ssh` + `ssh-keygen` | OS-bundled (Linux/macOS); Windows: OpenSSH or WSL |
| `docker` + `docker compose` | Docker Desktop or `docker-ce` from docker.com |
| `jq` | `brew install jq` / `apt install jq` (optional — pretty-prints smoke probes) |
| `dig` | `brew install bind` / `apt install dnsutils` (DNS verification) |
| VPS shell account either `root` or a sudo-capable user; SSH pubkey authorized | Provisioned once (see §3) |
| Meta Developer app (Facebook Login for Business) | https://developers.facebook.com (Settings → Basic → App ID + App Secret). Needed even on VPS-only; the OAuth round-trip still runs through Meta. |
| Resend account | https://resend.com (for `no-reply@instaedit.org` magic links) |
| Object storage | **MinIO** runs inside the Compose stack (see §4). No external S3 / Tigris account required for the canonical VPS deploy. If migrating from the historical Tigris setup, see §10 (Tigris retirement). |
| DNS for `instaedit.org` | registrar (or Cloudflare) — see **§1.5 below** for the canonical records. The full DNS runbook (cert renewal, DMARC progression, Gmail inbox test) lives in [docs/OPERATIONS.md §1 + §7](./OPERATIONS.md#1-dns-records-instaeditorg). |

---

## 1.5 DNS delegation (canonical) — `instaedit.org`

After the cutover, **all three names resolve to the same VPS IP**
(`51.91.11.36`). Caddy distinguishes them by SNI: apex + `app` serve the
SPA, `api` reverse-proxies to the Go API. The PHP-style `apex → app`
redirect (previously declared in `web/vercel.json`) is now served by a
Caddy `redir` block; reproduce the exact behaviour with:

```caddyfile
@apex host instaedit.org
redir @apex https://app.instaedit.org{uri} permanent
```

(That block lives in `ops/vps/Caddyfile`; commit `8271639` in the legacy
audit pinned the equivalent redirect at the Vercel edge layer.)

| Host | Type | Value | TTL | Purpose |
|------|------|-------|-----|---------|
| `instaedit.org` (apex) | `A` | `51.91.11.36` | 60 | VPS — Caddy serves 301 → `app.instaedit.org` (apex cannot use CNAME per DNS spec). Single A record; Caddy terminates TLS. |
| `app.instaedit.org` | `A` | `51.91.11.36` | 60 | VPS — Caddy serves the SPA (`/srv/instaedit/web/dist` + SPA fallback `/* → /index.html`). |
| `api.instaedit.org` | `A` | `51.91.11.36` | 300 | VPS — Caddy reverse-proxies `/api/*` to `127.0.0.1:8080` (Go API inside the Compose stack). |
| `instaedit.org` (apex) | `CAA` | `0 issue "letsencrypt.org"` | 3600 | Restrict cert issuance to Let's Encrypt (Caddy uses LE via HTTP-01). |
| `instaedit.org` (apex) | `CAA` | `0 iodef "mailto:security@instaedit.org"` | 3600 | Incident reporting for unauthorized issuance attempts. |
| `instaedit.org` (apex) | `TXT` | `v=spf1 include:_spf.resend.com ~all` | 3600 | SPF for Resend (sender domain `no-reply@instaedit.org`). Use `~all` (soft-fail) during the 2-4 weeks warm-up; flip to `-all` after first month clean. **Note:** include host is `_spf.resend.com` (with `_spf.` prefix), not bare `resend.com` — this is the 2026 Resend canonical. |
| `<selector>._domainkey.instaedit.org` | `CNAME` | `<selector>.dkim.resend.com.` | 3600 | DKIM rotation. **The `<selector>` is assigned by Resend when you add the domain** — look at the Resend dashboard → Domains → `instaedit.org` → Records BEFORE pasting. Typical values: `resend1`, `resend2`. The format `<selector>.dkim.resend.com.` is the 2026 canonical; do NOT switch to TXT-based DKIM (Resend has not). |
| `_dmarc.instaedit.org` | `TXT` | `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` | 3600 | **DMARC starts at `p=none`** for the 2-4 weeks warm-up window — Gmail requires a soft enforcement ramp for brand-new sender domains. Ramp schedule + reasoning: see [docs/OPERATIONS.md §7.2](./OPERATIONS.md#72-dmarc-progression-schedule). The rua/ruf reports go to `security@instaedit.org` — make sure that mailbox exists before flipping `p=quarantine`. |

Plus:
- **DNSSEC** at the registrar (Cloudflare: one-click; Namecheap: opt-in via DS records). Required for the CAA records to be honored by resolvers.
- **Cloudflare users:** set `instaedit.org`, `app.instaedit.org`, `api.instaedit.org` to **DNS-only** ("grey cloud"). The orange-cloud proxy terminates TLS itself and intercepts LE HTTP-01 challenges — the VPS will fail to renew its cert after 60 days.
- **TTL rationale:** 60s on the frontend lets near-instant switchover in CDN-failure events; 300s on the backend balances low-API-conn-churn vs cheap regional rerouting. With a single VPS, regional rerouting is moot until a second region is added.
- **No `_vercel` TXT, no `cname.vercel-dns.com.`, no `instaedit-login.fly.dev.`:**
  those records are historical and should be **removed** at the registrar
  as part of the cutover. If they linger, neither resolves, but they are
  noise in the zone file and confuse future audits.

> **Cert issuance:** Caddy auto-provisions and auto-renews a Let's Encrypt
> certificate for all three names during the first start. The HTTP-01
> challenge goes to `http://51.91.11.36/.well-known/acme-challenge/...`
> from the LE CA vantage points (verify `dig +short instaedit.org` returns
> `51.91.11.36` BEFORE the first `docker compose up`). No manual cert
> step is required — `flyctl certs add` is intentionally NOT a step
> anymore.

Re-run procedure (operator laptop):

```bash
dig +short instaedit.org       A    # expect: 51.91.11.36
dig +short app.instaedit.org    A    # expect: 51.91.11.36
dig +short api.instaedit.org    A    # expect: 51.91.11.36

curl -sI https://api.instaedit.org/health | grep -i '^server:'
# expect: server: Caddy

curl -sI https://api.instaedit.org/ready | head -1
# expect: HTTP/2 200 (cold start) or 503 with workers_pending (warming)
```

Failure mode to escalate on: any of the three names resolving to a
different IP, or `Server:` header carrying anything other than `Caddy`.

---

## 2. One-time host setup (operator laptop → VPS)

These steps run once per VPS host. They do NOT change between
production deploys.

### 2.1 Initial server

```bash
# 1. Provision a Linux VPS at your host of choice (Hetzner, OVH,
#    DigitalOcean, Scaleway, …). Recommended specs for the beta:
#
#       Region             : eu-west-1 or your closest EU zone
#       Image              : Ubuntu 24.04 LTS (or Debian 12)
#       Plan               : 4 vCPU / 8 GB RAM / 80 GB NVMe SSD
#       Firewall (default) : allow TCP 22 (SSH) + TCP 80 + TCP 443 only
#
# 2. Take note of the public IPv4 — call it VPS_IP. The live system
#    runs at 51.91.11.36 (recorded in docs/VPS-DEPLOY-STATUS.md).
#    Re-pointing to a new VPS only requires an A-record update at
#    the registrar + re-running Caddy's first-boot ACME flow.

# 3. SSH in and harden:
ssh root@$VPS_IP
adduser instaedit
mkdir -p /home/instaedit/.ssh
# Paste the operator laptop's pubkey into /home/instaedit/.ssh/authorized_keys
chmod 700 /home/instaedit/.ssh
chmod 600 /home/instaedit/.ssh/authorized_keys
chown -R instaedit:instaedit /home/instaedit/.ssh
# Disallow root SSH + password auth in /etc/ssh/sshd_config:
#   PermitRootLogin no
#   PasswordAuthentication no
# Then systemctl reload ssh.

# 4. Install Docker Engine + Compose plugin (Ubuntu 24.04):
apt update && apt install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) \
  signed-by=/etc/apt/keyrings/docker.gpg] \
  https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
  > /etc/apt/sources.list.d/docker.list
apt update && apt install -y docker-ce docker-ce-cli containerd.io \
  docker-buildx-plugin docker-compose-plugin
usermod -aG docker instaedit
# Log out + back in for the docker group to take effect.

# 5. Create the stateful directories (bind-mounted into the Compose stack):
mkdir -p /srv/instaedit/{pgdata,miniostore,caddy_data,caddy_config,web/dist}
chown -R instaedit:instaedit /srv/instaedit
```

### 2.2 Project layout on the VPS

```bash
# Clone the repo (or rsync from the laptop) into the standard location:
#   /opt/instaedit/InstaeditLogin
#
# In production this is maintained out-of-band (git pull on a deploy
# hook), but the layout below is canonical.

cd /opt/instaedit/InstaeditLogin

# Copy Caddyfile and Compose overrides into /srv/instaedit:
sudo install -m 0644 ops/vps/Caddyfile /srv/instaedit/Caddyfile
sudo install -m 0644 docker-compose.yml /srv/instaedit/docker-compose.yml
# (Add docker-compose.production.yml on top if you split staging vs prod.)

sudo cp .env.production.example /srv/instaedit/.env.production 2>/dev/null || true
chmod 600 /srv/instaedit/.env.production
chown instaedit:instaedit /srv/instaedit/.env.production
```

### 2.3 First boot of the stack

```bash
# From the VPS, as `instaedit`:
cd /opt/instaedit/InstaeditLogin
docker compose \
  --env-file /srv/instaedit/.env.production \
  -f docker-compose.yml \
  up -d --build
# What this does:
#   - Builds the unified Dockerfile [production] image (api + worker +
#     migrate in one image, same one Fly was shipping).
#   - Starts postgres, minio, caddy, api, worker.
#   - Caddy obtains the LE cert on first start for the three names in
#     §1.5 (HTTP-01 against the public IP).
#
# Run the migrate container once explicitly:
docker compose --env-file /srv/instaedit/.env.production \
  run --rm migrate
# Expected: 9 canary tables present (post-migration).

# Verify from the operator laptop:
curl -fsS https://api.instaedit.org/api/v1/health | jq
# Expected: {"status":"ok","service":"InstaEditLogin","version":"2.0.0", ... }

curl -fsS https://api.instaedit.org/ready | jq
# Expected (warm): { "status":"ok", "db":"ok",
#                    "migrations":"ok", "workers_ready":true }
# Expected (cold):  503 + workers_pending list. See §5 Gate B.
```

---

## 3. Secret collection

The **26 secrets** the application needs at runtime. The shape of the
catalog is preserved with the previous Fly-era doc so existing
`docs/OPERATIONS.md` cross-references continue to make sense. **Where
each lives now:**

- The **public, stable** values (`FRONTEND_URL`, `CORS_ALLOWED_ORIGINS`,
  `COOKIE_DOMAIN`, the 7 `*_REDIRECT_URI`s) are baked into
  `/srv/instaedit/Caddyfile` and the Compose service env blocks. They
  are not secrets in the leakable sense; they appear here so the
  complete env surface is auditable in one place.
- The **rotatable** values (`JWT_SECRET`, `ENCRYPTION_KEYS`,
  `ACTIVE_ENCRYPTION_KEY_ID`, the OAuth `*_CLIENT_ID/SECRET`, etc.)
  live in `/srv/instaedit/.env.production` (mode `0600`, owned by
  `instaedit`). The Compose stack reads them via
  `--env-file /srv/instaedit/.env.production`.

| # | Secret | Where to get it (VPS-only) |
|---|--------|------------------------------|
| 1 | `DATABASE_URL` | Connection string to the Compose `postgres` service: `postgres://instaedit:<pw>@db:5432/instaedit_login?sslmode=disable`. The host is the compose network DNS name `db`, NOT `127.0.0.1` — `127.0.0.1` would only work from inside a container that shares the network with the postgres bind. Bind pg to `127.0.0.1:5432` on the host; the Compose service connects on the compose-internal alias `db:5432`. Password is one of the values in the secrets block. |
| 2 | `JWT_SECRET` | `openssl rand -hex 32` — **separate from dev** |
| 3 | `ENCRYPTION_KEYS` | CSV `id:base64key,id:base64key,…` where each `id` is `uint32` and each `key` is the base64 of a 32-byte AES-256-GCM key. Format recipe below the table. |
| 4 | `ACTIVE_ENCRYPTION_KEY_ID` | The `uint32` id of the key used for **new** encryption. Must be present in the parsed `ENCRYPTION_KEYS` map. |
| 5 | `S3_ACCESS_KEY` | Static credential pair baked into the Compose `minio` service env block (rows 5 + 6); also used by the Go API as `MINIO_ROOT_USER`. Generate once via `openssl rand -base64 24`. |
| 6 | `S3_SECRET_KEY` | Paired with row 5. Used as `MINIO_ROOT_PASSWORD` for the MinIO container AND as `S3_SECRET_KEY` for the Go API (the backend SigV4 signer is endpoint-agnostic, so a local MinIO talks to it just like Tigris). |
| 7 | `META_APP_ID` | Meta Developer Console → your prod-app → Settings → Basic |
| 8 | `META_APP_SECRET` | Meta Developer Console → Settings → Basic → "Show" |
| 9 | `FRONTEND_URL` | `https://app.instaedit.org` (public, lives in `/srv/instaedit/Caddyfile`) |
| 10 | `CORS_ALLOWED_ORIGINS` | `https://instaedit.org,https://app.instaedit.org` (comma-separated, no spaces) |
| 11 | `COOKIE_DOMAIN` | `.instaedit.org` (leading dot for cross-subdomain CSRF; see `internal/config/config.go` Blocco #2.4) |
| 12 | `INSTAGRAM_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/instagram/callback` |
| 13 | `FACEBOOK_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/facebook/callback` |
| 14 | `THREADS_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/threads/callback` |
| 15 | `X_CLIENT_ID` | X Developer Portal → created app → "Keys and tokens" → "OAuth 2.0 Client ID" (post-App Review for scopes `tweet.read` / `tweet.write` / `users.read` / `offline.access`) |
| 16 | `X_CLIENT_SECRET` | X Developer Portal → created app → "Keys and tokens" → "OAuth 2.0 Client Secret" (show-once — capture immediately) |
| 17 | `X_REDIRECT_URI` | Register `https://api.instaedit.org/api/v1/auth/twitter/callback` in X Developer Portal → Apps → "User authentication settings" → "Callback URIs". Public value in `/srv/instaedit/.env.production`. |
| 18 | `TIKTOK_CLIENT_ID` | TikTok Developer Portal → created app → "App ID" (Client Key, post-App Review for scopes `user.info.basic` + `video.publish`) |
| 19 | `TIKTOK_CLIENT_SECRET` | TikTok Developer Portal → created app → "App secret" (visible ONLY right after creation; capture immediately) |
| 20 | `TIKTOK_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/tiktok/callback` |
| 21 | `YOUTUBE_CLIENT_ID` | Google Cloud Console → "APIs & Services" → "Credentials" → OAuth client ID (suffix `.apps.googleusercontent.com`) |
| 22 | `YOUTUBE_CLIENT_SECRET` | Same flow as `YOUTUBE_CLIENT_ID`; capture immediately on display |
| 23 | `YOUTUBE_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/youtube/callback` |
| 24 | `LINKEDIN_CLIENT_ID` | LinkedIn Developer Portal → My Apps → "Auth" tab → "OAuth 2.0 settings" → "Client ID" |
| 25 | `LINKEDIN_CLIENT_SECRET` | LinkedIn Developer Portal → "Auth" tab → "Client Secret" |
| 26 | `LINKEDIN_REDIRECT_URI` | `https://api.instaedit.org/api/v1/auth/linkedin/callback` |

> **Note on `EMAIL_PROVIDER_KEY`**: capture it in your password manager
> (`instaedit-login/email/EMAIL_PROVIDER_KEY`, Resend scope = `Sending
> Access` ONLY) for the Gmail inbox test in [docs/OPERATIONS.md §7.3](./OPERATIONS.md#73-gmail-inbox-test-protocol).
> Do **not** add it to `/srv/instaedit/.env.production` until the
> backend wires Resend (see [docs/OPERATIONS.md §7.5](./OPERATIONS.md#75-email_provider_key-capture-protocol)).

**Do NOT include** (disabled providers, beta scope): `STRIPE_*`. The
deploy pipeline refuses to start any container that references these
prefixes.

> **`ENCRYPTION_KEYS` format (per `internal/crypto/encrypt.go`)**:
> The config loader parses this with `strconv.ParseUint(idStr, 10, 32)`,
> so each id MUST be a `uint32` digit string. Each entry is
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
> Example for the `.env.production` file:
> ```env
> ENCRYPTION_KEYS='1:Abc123Base64KeyHere,2:Def456AnotherBase64Key'
> ACTIVE_ENCRYPTION_KEY_ID=1
> ```

### 3.0 Operator reference manifest (2026-07-25)

**Per-secret status table**: confirms what has already been captured +
the shape constraints the captured value should match + where in the
password manager the captured value lives. **Values are NEVER printed in
this manifest** — only the status + shape (length / charset / regex) +
capture location.

| # | Secret | Source (resolved) | Shape | Password manager entry | Captured? | Action ref |
|---|--------|-------------------|-------|------------------------|-----------|------------|
| 1 | `DATABASE_URL` | Local URL from /srv/instaedit/.env.production (Compose `db` service; sslmode=disable on the docker network) | Format: `postgres://<user>:<pw>@db:5432/<db>?sslmode=disable` (≈ 60–90 chars; the password URI-component is the only randomized segment) | `instaedit-login/database-url/production` | ○ PENDING | DEPLOY.md §2 + §3 row 1 |
| 2 | `JWT_SECRET` | `openssl rand -hex 32` (sha12=`2df3c07a1d40`) | 64 lowercase hex chars / 32 bytes binary (RFC 7518 HS256 minimum per `internal/config/config.go::jwtSecretMinBytes=32`) | `instaedit-login/jwt-secret/production` | ✓ CAPTURED | already in PM |
| 3 | `ENCRYPTION_KEYS` | `openssl rand -base64 32` for id=1 (sha12=`94e5775e101d`) | CSV `id:base64,…`; each base64 decodes to exactly 32 bytes (AES-256-GCM slot per `internal/crypto/encrypt.go::aesKeyBytes=32`) | `instaedit-login/encryption-key-1/production` (one entry per slot) | ✓ CAPTURED (1 slot) | already in PM |
| 4 | `ACTIVE_ENCRYPTION_KEY_ID` | literal `1` (uint32, MUST be present in `ENCRYPTION_KEYS` map) | digit string in [0, 4294967295] | `instaedit-login/active-encryption-key-id/production` | ✓ CAPTURED | already in PM |
| 5 | `S3_ACCESS_KEY` | Local MinIO root credential (paired with row 6) | non-empty (≈ 30–40 chars for `openssl rand -base64 24`) | `instaedit-login/s3-access-key/production` | ○ PENDING | DEPLOY.md §4 launch step |
| 6 | `S3_SECRET_KEY` | Local MinIO root credential (paired with row 5; rotate the pair ONLY together) | non-empty (≈ 30–40 chars) | `instaedit-login/s3-secret-key/production` | ○ PENDING | DEPLOY.md §4 launch step |
| 7 | `META_APP_ID` | Meta Developer Console → prod-app → Settings → Basic (numeric) | numeric string (typically 15 digits) | `instaedit-login/meta-app-id/production` | ○ PENDING | Meta prod-app review |
| 8 | `META_APP_SECRET` | Meta Developer Console → Settings → Basic → "Show" | ≥ 32 chars (per `internal/config/config.go::secretMinChars=32`) | `instaedit-login/meta-app-secret/production` | ○ PENDING | Meta prod-app review |
| 9 | `FRONTEND_URL` | Canonical per commit `716c709` + Caddyfile | exactly `https://app.instaedit.org` (HTTPS required; no trailing slash; no localhost) | N/A (public, in `/srv/instaedit/Caddyfile`) | ✓ STABLE | no action |
| 10 | `CORS_ALLOWED_ORIGINS` | Canonical per commit `716c709` + apex redirect | exactly `https://instaedit.org,https://app.instaedit.org` | N/A (public) | ✓ STABLE | no action |
| 11 | `COOKIE_DOMAIN` | Canonical per `internal/config/config.go` Blocco #2.4 | exactly `.instaedit.org` (leading dot) | N/A (public) | ✓ STABLE | no action |
| 12 | `INSTAGRAM_REDIRECT_URI` | Canonical per Caddyfile + Meta console registration | exactly `https://api.instaedit.org/api/v1/auth/instagram/callback` | N/A (public; pinned by Meta console) | ✓ STABLE | no action |
| 13 | `FACEBOOK_REDIRECT_URI` | Canonical per Caddyfile | exactly `https://api.instaedit.org/api/v1/auth/facebook/callback` | N/A (public) | ✓ STABLE | no action |
| 14 | `THREADS_REDIRECT_URI` | Canonical per Caddyfile | exactly `https://api.instaedit.org/api/v1/auth/threads/callback` | N/A (public) | ✓ STABLE | no action |
| 15 | `X_CLIENT_ID` | X Developer Portal (post-App Review) | exact OAuth Client ID (≈ 22-char alphanumeric) | `instaedit-login/x-client-id/production` | ○ PENDING | requires App Review for scopes `tweet.read` / `tweet.write` / `users.read` / `offline.access` |
| 16 | `X_CLIENT_SECRET` | X Developer Portal (post-App Review) | exact OAuth Client Secret (≈ 40–50 chars) | `instaedit-login/x-client-secret/production` | ○ PENDING | captured together with row 15 |
| 17 | `X_REDIRECT_URI` | Canonical per Caddyfile | exactly `https://api.instaedit.org/api/v1/auth/twitter/callback` | N/A (public; pinned by X Developer Portal) | ✓ STABLE | no action |
| 18 | `TIKTOK_CLIENT_ID` | TikTok Developer Portal (post-App Review) | exact TikTok Client Key (≈ 32 alphanumeric chars) | `instaedit-login/tiktok-client-id/production` | ○ PENDING | requires App Review for `user.info.basic` + `video.publish` |
| 19 | `TIKTOK_CLIENT_SECRET` | TikTok Developer Portal (post-App Review) | exact TikTok Client Secret (≈ 32–50 chars; visible ONLY right after creation — capture immediately) | `instaedit-login/tiktok-client-secret/production` | ○ PENDING | captured together with row 18 |
| 20 | `TIKTOK_REDIRECT_URI` | Canonical per Caddyfile | exactly `https://api.instaedit.org/api/v1/auth/tiktok/callback` | N/A (public; pinned by TikTok Developer Portal) | ✓ STABLE | no action |
| 21 | `YOUTUBE_CLIENT_ID` | Google Cloud Console (post-OAuth consent screen verification) | exactly `<random>.apps.googleusercontent.com` (≈ 72 chars) | `instaedit-login/youtube-client-id/production` | ○ PENDING | requires OAuth consent screen verification + Data API v3 `youtube.upload` scope |
| 22 | `YOUTUBE_CLIENT_SECRET` | Google Cloud Console (post-OAuth verification) | exactly the Client Secret shown in "OAuth client created" dialog (≈ 24–35 chars) | `instaedit-login/youtube-client-secret/production` | ○ PENDING | captured together with row 21 |
| 23 | `YOUTUBE_REDIRECT_URI` | Canonical per Caddyfile | exactly `https://api.instaedit.org/api/v1/auth/youtube/callback` | N/A (public; pinned by Google Cloud Console) | ✓ STABLE | no action |
| 24 | `LINKEDIN_CLIENT_ID` | LinkedIn Developer Portal → My Apps → "Auth" → "OAuth 2.0 settings" | alphanumeric string (≈ 14–18 chars) | `instaedit-login/linkedin-client-id/production` | ○ PENDING | requires LinkedIn product approval for `r_liteprofile` + `r_emailaddress` |
| 25 | `LINKEDIN_CLIENT_SECRET` | LinkedIn Developer Portal → "Auth" → "Client Secret" | alphanumeric string (≈ 32–40 chars) | `instaedit-login/linkedin-client-secret/production` | ○ PENDING | captured together with row 24 |
| 26 | `LINKEDIN_REDIRECT_URI` | Canonical per Caddyfile | exactly `https://api.instaedit.org/api/v1/auth/linkedin/callback` | N/A (public; pinned by LinkedIn Developer Portal) | ✓ STABLE | no action |

**Aggregate status (2026-07-25)**: 3 CAPTURED • 10 STABLE • 13 PENDING —
requires operator-side actions against external services (Meta Dev
Console + X Developer Portal App Review + TikTok Developer Portal App
Review + YouTube Data API v3 OAuth Verification + LinkedIn Developer
Portal product approval).

**Privacy contract**: the actual secret values are NEVER printed in this
manifest or in any commit output. The shape column gives the operator
enough metadata to confirm locally (a) the captured value satisfies the
input contract, (b) the captured value is correctly stored. If you ever
need to actually verify a value, paste it into your terminal locally
WITHOUT piping it to the chat agent.

---

## 4. Post-deploy smoke test (VPS-only)

**APPLY ALL of these AFTER `docker compose up` exits 0.**

### 4.0 Lightweight shell probes (read-only)

```bash
# 1. Health endpoint
curl -fsS https://api.instaedit.org/api/v1/health | jq
#   → {"status":"ok","service":"InstaEditLogin","version":"...","platforms":[...]}

# 2. OAuth round-trip (302 → Facebook)
curl -sI https://api.instaedit.org/api/v1/auth/instagram/login
#   → HTTP/2 302 Found
#   → Location: https://www.facebook.com/v18.0/dialog/oauth?...

# 3. Cross-subdomain CSRF cookie contract
curl -sI -H "Origin: https://app.instaedit.org" \
  https://api.instaedit.org/api/v1/auth/me | grep -i 'set-cookie'
#   → must include: csrf_token=...; Domain=instaedit.org; Secure; SameSite=None
#     (NO Domain= on session / refresh cookies — they stay host-only.
#      See Blocco #2.4 in internal/config/config.go.)

# 4. Caddy cert + identity
echo | openssl s_client -servername api.instaedit.org \
  -connect api.instaedit.org:443 2>/dev/null \
  | openssl x509 -noout -issuer -subject -dates
#   → issuer: O = Let's Encrypt, CN = R10 / R11 (depends on issuance date)
#   → subject: CN = api.instaedit.org (or *.instaedit.org if wildcard rotated in)

# 5. Require Bunny-CDN-style readiness to the LE staging threshold
#    (LE has rate limits on prod issuance; the staging env is free).
#    This block is intentionally NOT auto-run in CI.
```

### 4.1 Comprehensive end-to-end smoke (`scripts/ops/post_deploy_smoke.sh`)

`scripts/ops/post_deploy_smoke.sh` covers Phase 9 sub-phases 1, 2, 3, 4,
5 + 7 against `https://api.instaedit.org`. Run from the operator
laptop:

```bash
# Default mode (read-only — probes only, no prod-state-creation):
./scripts/ops/post_deploy_smoke.sh

# Verbose mode (also creates a real draft post + polls state 30s):
APPLY_PUBLISH=1 ./scripts/ops/post_deploy_smoke.sh

# Against staging (when ran on a VPS that serves staging.instaedit.org):
BASE_URL=https://staging.instaedit.org ./scripts/ops/post_deploy_smoke.sh
```

Pass criteria: PASS count > 0 AND FAIL count = 0; WARNs are advisory.

### 4.2 Workspace isolation test (`scripts/ops/workspace_isolation_test.sh`)

Phase 9 sub-phase 6 — verifies user A cannot access user B's data across
`/accounts` + `/posts/workspace/{wid}` + cross-workspace `POST /posts`.
The script creates 2 fresh users via the email/password register flow
(not magic-link — the test must NOT depend on Resend email delivery),
runs 4 isolation assertions, then hard-deletes its own test data via
`psql $DATABASE_URL` CASCADE on users matching the random suffix.

```bash
# Preview only (no mutations):
./scripts/ops/workspace_isolation_test.sh --dry-run

# Apply (creates 2 users + 2 workspaces + runs assertions + hard-deletes on EXIT):
DATABASE_URL=postgres://instaedit:<pw>@127.0.0.1:5432/instaedit_login?sslmode=disable \
  ./scripts/ops/workspace_isolation_test.sh
```

Pass criteria: 4/4 PASS; each FAIL exits 1 after cleanup. Cleanup SQL
uses the random suffix so even if the trap fails, the operator can run
a manual `psql ... WHERE email LIKE 'isol-%-%<SUFFIX>'`.

---

## 5. Phase 5: Post-deploy Verification

Performed on the live domain from operator laptop. All probes are
read-only — they do NOT depend on a populated session.

### Gate A — Healthz (HTTP api process responding)

```bash
curl -fsS https://api.instaedit.org/api/v1/health | jq
```

**Expected envelope** (per `pkg/api/handlers.go::handleHealth`):

```json
{
  "platforms": ["instagram", "facebook", "threads"],
  "service":   "InstaEditLogin",
  "status":    "ok",
  "version":   "2.0.0"
}
```

*What this proves:* the Go API listener bound successfully on `:8080`,
the handlers package is reachable, and the provider capabilities block
initialized (no provider-secret-missing panic on startup). It does NOT
prove wire-level correctness — sub-phase 9.4 covers that.

### Gate B — Readiness (DB + migrations + worker goroutines)

```bash
curl -fsS https://api.instaedit.org/ready | jq
```

**Expected envelope (warm)**:

```json
{
  "status":         "ok",
  "db":             "ok",
  "migrations":     "ok",
  "workers_ready":  true
}
```

**Live evidence**: `docs/VPS-DEPLOY-STATUS.md` §3 captures the probe
of 2026-07-25 — `db: ok`, `migrations: ok`, `workers_pending`
non-empty (cold-start signature). The warm-state has no live capture
yet; re-run after Compose stabilises and append a row to §6.

If failing, `"status"` reads `"unavailable"` and `workers_pending` names
the goroutines that have not finished their first iteration
(`drive_batch_crawler`, `metrics`, `publish`, …, per
`pkg/api/worker_status.go::startedFields`).

### Gate C — VPS container state

```bash
# On the VPS, as `instaedit`:
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production ps
#   → api          running   127.0.0.1:8080->8080/tcp
#   → worker       running
#   → postgres     running   127.0.0.1:5432->5432/tcp
#   → minio        running   127.0.0.1:9000-9001->9000-9001/tcp
#   → caddy        running   0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp
```

Also confirm image freshness:

```bash
docker compose --env-file /srv/instaedit/.env.production images
# The image tag should match the SHA just pushed.
```

### 5.1 Current status (2026-07-25) — CUTOVER VERIFIED

Live probes from a sandboxed host on 2026-07-25 confirmed:

| Probe | Result | Interpretation |
|-------|--------|----------------|
| `dig +short api.instaedit.org A` | `51.91.11.36` | API on VPS single-host A record (not Vercel anycast, not Fly) |
| `curl -sI https://api.instaedit.org/health` | `server: Caddy` (404) | Caddy is the only entry; **no `fly-request-id`, no `fly-region`** |
| `curl -sI https://api.instaedit.org/ready` | `server: Caddy` (405 on HEAD; use GET) | Caddy routes `api.` SNI → API reverse-proxy |
| `curl -sS https://api.instaedit.org/ready` (GET) | 503 + `db: ok` + `migrations: ok` + `workers_pending` non-empty | Postgres connected, all migrations applied, worker goroutines warming (cold-start) |

The Fly stack is gone. The cutover is operative. See
`docs/VPS-DEPLOY-STATUS.md` §6 for the probe log table.

---

## 6. Rotation

### `JWT_SECRET`

```bash
# On the VPS:
ssh instaedit@$VPS_IP
NEW_JWT=$(openssl rand -hex 32)
# Edit /srv/instaedit/.env.production: JWT_SECRET=$NEW_JWT
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker
# In-flight JWTs are now invalid; users get 401 → re-login.
```

> JWT rotation invalidates ALL in-flight sessions. Plan for a brief
> re-login window. For zero-downtime, you'd need a JWT key ring (not
> in scope for the beta).

### `ENCRYPTION_KEYS` (zero-downtime rotation)

The bootstrap (`internal/crypto/encrypt.go`) uses the active key for
**new** encryption and the full key map for **decryption**. Add a new
key alongside the old, roll the deploy, then drop the old key once
all tokens have been re-encrypted.

```bash
# 1. Read the current ENCRYPTION_KEYS + ACTIVE_ENCRYPTION_KEY_ID
ssh instaedit@$VPS_IP 'grep -E "^(ENCRYPTION_KEYS|ACTIVE_ENCRYPTION_KEY_ID)" /srv/instaedit/.env.production'

# 2. Append a new key (e.g. id=2) to the CSV
NEW_B64=$(openssl rand -base64 32)
# Edit /srv/instaedit/.env.production on the VPS:
#   was: ENCRYPTION_KEYS='1:<OLD>'
#         ACTIVE_ENCRYPTION_KEY_ID=1
#   now: ENCRYPTION_KEYS='1:<OLD>,2:<NEW_B64>'
#         ACTIVE_ENCRYPTION_KEY_ID=1   # keep on the OLD key

# 3. Restart api + worker (no downtime — both keys accepted on decrypt)
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker
#   → existing tokens still decrypt with id=1; new writes use id=1.

# 4. Cut over: bump the active id to the new key
#   now: ACTIVE_ENCRYPTION_KEY_ID=2
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker
#   → existing tokens still decrypt with id=1; new writes use id=2.

# 5. After all tokens have been re-written (watch the metric
#   `instaedit_vault_cipher_id` — should converge to 2), drop the old key:
#   now: ENCRYPTION_KEYS='2:<NEW_B64>'
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker
```

### Provider / Mail / S3 rotation

Editor change on the VPS, then restart the affected service:
`docker compose up -d --force-recreate api worker`.

For the MinIO bucket: rotate via the MinIO admin console
(`https://<VPS_IP>:9001` over the host network; the admin port is NOT
exposed publicly) — generate a fresh access key + secret, then update
rows 5 + 6 of `/srv/instaedit/.env.production` AND the `MINIO_ROOT_*`
env block in the Compose override. Restart `minio` then `api` +
`worker` in that order.

---

## 7. Sandbox vs Operator Boundary (VPS-only)

There is a hard boundary between what the Codex agent sandbox can
verify locally and what strictly requires operator-side VPS access.

### 7.1 Local Sandbox (CAN verify)

- HTTP probes against `https://api.instaedit.org` (the sandbox has
  outbound internet egress — see §5.1 / `docs/VPS-DEPLOY-STATUS.md`).
- `make lint-check` (gofmt + go vet) — confirms Go code is lint-clean
  regardless of deploy state.
- Local file inspection (grep, awk, jq on git-tracked files).
- Static code review against `git log main --oneline`.
- `make backend-test` + `make test-integration` (the latter requires
  Docker on the operator/runner).

### 7.2 Operator VPS (REQUIRES ssh to the VPS)

- `ssh instaedit@$VPS_IP` for any change to `/srv/instaedit/`.
- `docker compose ps` / `docker compose logs` against the running stack
  (live tail: `docker compose logs -f worker`).
- `psql` against `postgres` on `127.0.0.1:5432` for SQL audits
  (NEVER from the public network — pg is bound to loopback).
- For MinIO: `mc` CLI over the loopback admin port `9001`.

### 7.3 Canonical Deploy Execution Block (paste-ready)

For a fresh VPS (or after the host is wiped), the operator laptop
runs:

```bash
# ----- §2: one-time host setup (skip on re-deploys) -----
ssh root@$VPS_IP
# ... follow §2 steps 3-5 ...

# ----- §3: secrets on disk -----
# Local: write /srv/instaedit/.env.production with the 26 vars from §3.
# (Use your secret manager — never paste real secrets into chat.)
scp .env.production instaedit@$VPS_IP:/srv/instaedit/.env.production
ssh instaedit@$VPS_IP 'chmod 600 /srv/instaedit/.env.production'

# ----- §2.3 / §4: first boot -----
ssh instaedit@$VPS_IP
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production up -d --build
docker compose --env-file /srv/instaedit/.env.production run --rm migrate

# ----- §4 / §5: post-deploy verification -----
curl -fsS https://api.instaedit.org/api/v1/health | jq   # Gate A
curl -fsS https://api.instaedit.org/ready | jq           # Gate B
docker compose ps                                        # Gate C

./scripts/ops/post_deploy_smoke.sh                       # §4.1
./scripts/ops/workspace_isolation_test.sh --dry-run      # §4.2 preview
DATABASE_URL=postgres://... ./scripts/ops/workspace_isolation_test.sh   # §4.2 apply
```

For a re-deploy (image update only):

```bash
ssh instaedit@$VPS_IP
cd /opt/instaedit/InstaeditLogin
git pull --ff-only
docker compose --env-file /srv/instaedit/.env.production up -d --build
docker compose --env-file /srv/instaedit/.env.production run --rm migrate
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker
# Then re-run Gate A + Gate B from the operator laptop.
```

---

## 8. Failure modes (VPS-specific)

- **`docker compose up` fails on the postgres volume:**
  check `/srv/instaedit/pgdata` permissions; `chown -R
  instaedit:instaedit /srv/instaedit/pgdata` if needed.
- **Caddy cannot obtain the LE cert:**
  confirm `dig +short instaedit.org` returns `51.91.11.36` BEFORE the
  first `up`. If you recently changed the A record, wait until the
  TTL (-old TTL window) has elapsed globally. Cloudflare users must
  set the three names to **DNS-only** ("grey cloud").
- **`/api/v1/health` 502 from Caddy:** the api container is down or
  not yet listening. `docker compose logs api` for the Go startup
  output; fix the env or the image, restart with
  `docker compose up -d --force-recreate api`.
- **Worker group health-check timeout:** worker binds `WORKER_HEALTH_PORT`
  (9090 by default; 0 disables the listener per
  `cmd/worker/health_listener.go`). The Compose healthcheck in
  `docker-compose.yml` references this — keep the default unless you
  have a reason to override.
- **MinIO unavailable:** `docker compose logs minio`; the admin port
  is `127.0.0.1:9001`, not public. The Go SigV4 signer
  (`internal/services/storage.go`) retries 3× on transient errors.
- **Image build failure:** `docker build --target production .` on the
  VPS to isolate. Source-level issues are caught by
  `make lint-check` BEFORE the build; if the build fails on the VPS,
  it's almost certainly a network or change-in-base-image issue.

---

## 9. Cross-references

- **Live probe log:** [docs/VPS-DEPLOY-STATUS.md](./VPS-DEPLOY-STATUS.md)
  (DNS resolution, HTTP identity probes, /ready envelope, ready/warm
  log table).
- **DNS runbook + cert renewal + DMARC escalation + Gmail inbox test:**
  [docs/OPERATIONS.md §1 + §7](./OPERATIONS.md).
- **OAuth provider configuration for each platform:** [docs/OAUTH-PRODUCTION.md](./OAUTH-PRODUCTION.md).
- **Capability / provider matrix:** [docs/PROVIDER_MATRIX.md](./PROVIDER_MATRIX.md).
- **API surface / endpoints:** [docs/ENDPOINTS.md](./ENDPOINTS.md).
- **OpenAPI spec:** [docs/OPENAPI.md](./OPENAPI.md) + [api/openapi.yaml](../api/openapi.yaml).
- **Architecture overview:** [docs/ARCHITECTURE.md](./ARCHITECTURE.md).

---

## 10. Tigris retirement (one-time migration)

If the historical Tigris bucket (`instaedit-prod-media`) is still in
use, the cutover to MinIO is a bucket-to-bucket copy. This is a
self-contained project that does not block the production cutover.

```bash
# 1. Take inventory of the Tigris bucket:
AWS_ACCESS_KEY_ID=$TIGRIS_KEY AWS_SECRET_ACCESS_KEY=$TIGRIS_SECRET \
  aws --endpoint https://t3.storage.dev s3 ls s3://instaedit-prod-media/ --recursive | tee /tmp/objects.txt

# 2. Provision the MinIO bucket with the same name + CORS + lifecycle:
#    (Use the MinIO admin console at http://127.0.0.1:9001 on the VPS.)

# 3. Mirror the data:
AWS_ACCESS_KEY_ID=$TIGRIS_KEY AWS_SECRET_ACCESS_KEY=$TIGRIS_SECRET \
  aws --endpoint https://t3.storage.dev s3 sync \
    s3://instaedit-prod-media/ /tmp/media-mirror/
# Then rsync /tmp/media-mirror/ into the MinIO volume on the VPS:
rsync -a /tmp/media-mirror/ instaedit@$VPS_IP:/srv/instaedit/miniostore/instaedit-prod-media/

# 4. Re-point the backend's S3_ENDPOINT (in /srv/instaedit/.env.production):
#      S3_ENDPOINT=http://minio:9000
#      S3_BUCKET=instaedit-prod-media
#      AWS_REGION=us-east-1   # MinIO ignores but the SigV4 signer wants it
#      (the rest of rows 5–6 stays the same)

# 5. Restart api + worker and re-run §4 probes:
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker

# 6. Once §4 probes confirm parity, retire the Tigris account:
#    tigrisdata.com → delete bucket → delete access key → close account.
```

The Tigris → MinIO migration is NOT a pre-requisite for the production
cutover; both can run in parallel, with the Go API's `S3_ENDPOINT`
env var toggling which one is current. Roll back to Tigris by flipping
the env var back to `https://t3.storage.dev` if needed.

---

## 11. Open items

- `verify-log-redaction` Makefile target currently reads `flyctl logs`;
  follow-up commit to re-point its log source at
  `docker compose logs`. The target itself stays; only the script changes.
- `docker-build-production` Makefile target now orphans (only `fly-deploy`
  consumed it); follow-up commit to either rename / repurpose it (compose
  builds) or remove.
- `.github/workflows/integration.yml` still references `make fly-secrets-test`;
  follow-up commit to wire `python3 scripts/test_parse_envfile.py` as a
  standalone job (the test script itself is unchanged).
- Caddy + Compose + MinIO + Postgres compose file
  ([ops/vps/Caddyfile](../ops/vps/Caddyfile), `docker-compose.yml`,
  `docker-compose.local.yml`) are kept as-is — they remain correct for
  the VPS target even after the Fly-era docs are stripped.
- The Fly gate probe (`docs/DEPLOY-AUDIT.md`) is now historical; a
  Redis-style audit at this checkpoint is still useful and should be
  archived separately after this cutover ships.
