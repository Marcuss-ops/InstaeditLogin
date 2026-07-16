# Local Development Stack

Single-origin HTTPS dev environment for InstaEditLogin. Everything
runs locally on a Linux VM. The browser on your laptop/desktop
connects through an SSH tunnel — no Fly, Vercel, Cloudflare, Supabase,
or external object storage.

```
Browser (your machine)
    ↓  https://localhost:8443
SSH tunnel
    ↓
Caddy on the VM (port :8443)
    ├── /api/*  →  Go API  (docker:8080)
    └── *       →  Vite    (tmux:5173)

Docker on the VM
    ├── db             Postgres 15
    ├── minio          S3-compatible storage
    ├── minio-init     creates the bucket before `api` starts
    ├── migrate        one-shot, blocks `api` on success
    ├── api            cmd/api (HTTP + per-deploy manual workers)
    └── worker         cmd/worker (5 background goroutines)
```

## Why this shape

* **Single origin** (frontend + API both at `https://localhost:8443`)
  lets the browser keep cookies `SameSite=Lax` instead of `None`,
  sidesteps CSRF cross-site headaches, and avoids `Secure`-only
  cookie warnings that appear on `http://` in modern browsers.

* **No external services** means the dev loop is the same on every
  laptop, regardless of who's online or what SaaS rate-limits we're
  at. Migration is also a single `docker compose down` away.

* **HTTPS via mkcert** (not Caddy-on-the-fly provisioning) means the
  browser trusts the `localhost:8443` origin without manual cert
  approval. mkcert installs the local CA in your OS trust store
  during `mkcert -install`.

* **Worker as a separate process** mirrors the production topology
  (Fly machine `api` + Fly machine `worker`). Starting with the
  same separation in dev means there's no "but it worked on my
  laptop" rewrite when the deploy story comes around.

## Prerequisites

| Where | What |
|---|---|
| VM  | Linux, Docker (compose v2), tmux, Node.js 20+ (only for the Vite dev server in tmux), openssl, sudo |
| PC  | Windows / macOS / Linux with `mkcert`-eligible OS trust store |

## Step 0 — rotate anything that has been in chat

Before doing anything else, rotate every secret that has appeared
unredacted in chat history, screenshots, or commit messages of
**any** branch (even ephemeral ones). For this stack specifically:

* TikTok sandbox `client_secret`  → regenerate via the TikTok
  Developer Portal, set the new value in `.env.dev`, never paste
  the new secret anywhere except `.env.dev`.
* `JWT_SECRET`, `ENCRYPTION_KEY`, `ADMIN_INVITE_TOKEN` → regenerate
  via `openssl rand -hex 32` (see Step 3).

Secrets live ONLY in `.env.dev`. They never appear in this file,
in the repo, in the Caddyfile, in the docker-compose local overlay,
or in any commit. If you see one anywhere else, that's a bug —
open a PR removing it.

## Step 1 — clone / pull the repo on the VM

```bash
mkdir -p ~/instaedit-certs
git clone <repo-url> ~/Projects/instaedit
cd ~/Projects/instaedit
```

## Step 2 — generate locally-trusted TLS certs

On **your PC** (the one that opens the browser):

```powershell
# Windows (PowerShell)
winget install FiloSottile.mkcert
mkcert -install
mkdir C:\instaedit-certs ; cd C:\instaedit-certs
mkcert localhost 127.0.0.1 ::1
# produces  localhost.pem  +  localhost-key.pem
```

```bash
# macOS
brew install mkcert
mkcert -install
mkdir -p ~/instaedit-certs && cd ~/instaedit-certs
mkcert localhost 127.0.0.1 ::1
```

Copy the two files to the VM:

```bash
scp ~/instaedit-certs/localhost.pem     <vm-user>@<vm-ip>:/home/<vm-user>/instaedit-certs/
scp ~/instaedit-certs/localhost-key.pem <vm-user>@<vm-ip>:/home/<vm-user>/instaedit-certs/
```

Caddy reads these at startup. **If you replace them later, you
MUST restart the Caddy container** — Chrome will keep showing the
old cert warning otherwise.

## Step 3 — `.env.dev` on the VM

```bash
cd ~/Projects/instaedit
test -f .env.dev || cp .env.dev.example .env.dev

# Generate three fresh secrets (Linux + macOS):
NEW_JWT=$(openssl rand -hex 32)
NEW_ENC=$(openssl rand -base64 32)
NEW_ADMIN=$(openssl rand -hex 32)

sed -i "s|^JWT_SECRET=.*|JWT_SECRET=${NEW_JWT}|"           .env.dev
sed -i "s|^ENCRYPTION_KEY=.*|ENCRYPTION_KEY=${NEW_ENC}|"    .env.dev
sed -i "s|^ADMIN_INVITE_TOKEN=.*|ADMIN_INVITE_TOKEN=${NEW_ADMIN}|" .env.dev
unset NEW_JWT NEW_ENC NEW_ADMIN
```

Then hand-edit `.env.dev`:

| Key                       | Value (the literal you put)                               |
| ---                       | ---                                                       |
| `DATABASE_URL`            | `postgresql://instaedit:dev_password@db:5432/instaedit_login?sslmode=disable` |
| `S3_ENDPOINT`             | `http://minio:9000`                                       |
| `S3_BUCKET`               | `instaedit-dev`                                           |
| `S3_ACCESS_KEY`           | (matches `MINIO_ROOT_PASSWORD` in the docker overlay)     |
| `S3_SECRET_KEY`           | (same string as above)                                    |
| `S3_REGION`               | `us-east-1`                                               |
| `FRONTEND_URL`            | `https://localhost:8443`                                  |
| `CORS_ALLOWED_ORIGINS`    | `https://localhost:8443`                                  |
| `COOKIE_DOMAIN`           | *(empty — host-only cookies are correct when FE+API share origin)* |
| `TIKTOK_REDIRECT_URI`     | `https://localhost:8443/api/v1/auth/tiktok/callback`      |
| `TIKTOK_CLIENT_ID`        | your TikTok Developer Portal sandbox `Client key`        |
| `TIKTOK_CLIENT_SECRET`    | your TikTok Developer Portal sandbox `Client secret`      |

> **Never** commit `.env.dev`. The repo `.gitignore` already
> excludes it. If the values appear in a PR diff, the diff is wrong.

## Step 4 — bring up the Docker stack

```bash
cd ~/Projects/instaedit
docker compose \
  -f docker-compose.yml \
  -f docker-compose.local.yml \
  up -d --build
```

This starts PostgreSQL, MinIO, the bucket-init helper, the API
container, AND the worker container (5 background goroutines:
`publish`, `reconcile`, `outbox`, `webhook`, `sessions_cleanup`).

The Compose overlay (`docker-compose.local.yml`) is committed; the
base `docker-compose.yml` is committed; the `Dockerfile` is
committed. **No host port mapping on MinIO** — `api` reaches MinIO
via Compose service DNS (`http://minio:9000`).

## Step 5 — start Vite in a tmux session

The frontend dev server is NOT in the Compose stack. Developers
keep it in tmux so HMR (file-watch + WebSocket reload) survives
across restarts.

```bash
cat > web/.env.local <<'EOF'
VITE_API_BASE_URL=https://localhost:8443
EOF

tmux new-session -d -s vite -c "$(pwd)/web"
tmux send-keys -t vite 'npm ci && npm run dev -- --host 127.0.0.1 --port 5173' Enter
tmux attach -t vite    # Ctrl-B D to detach
```

## Step 6 — run Caddy

The `ops/local/Caddyfile` is committed. Place it where Docker can
mount it, and bind-mount the cert directory from Step 2:

```bash
cp ~/Projects/instaedit/ops/local/Caddyfile ~/Caddyfile

docker rm -f instaedit-caddy 2>/dev/null
docker run -d \
  --name instaedit-caddy \
  --restart unless-stopped \
  --network host \
  -v ~/Caddyfile:/etc/caddy/Caddyfile:ro \
  -v ~/instaedit-certs:/certs:ro \
  caddy:2
```

Check from the VM:

```bash
curl -k -i https://localhost:8443/api/v1/health
# → should be HTTP/2 200
```

> `auto_https off` is set in the Caddyfile because we explicitly
> want HTTPS only on `:8443`. If you also want a plain HTTP listener
> on `:80` to redirect to `:8443`, remove that line — Caddy will
> bind `:80` for the redirect.

## Step 7 — register the first user

The `/api/v1/auth/register` endpoint is gated by `X-Admin-Token`.

```bash
ADMIN_TOKEN=$(grep '^ADMIN_INVITE_TOKEN=' ~/Projects/instaedit/.env.dev | cut -d= -f2-)

curl -i -k -X POST https://localhost:8443/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -H "X-Admin-Token: $ADMIN_TOKEN" \
  -d '{"email":"dev@example.com","password":"changeMeDevPassword2025","name":"Developer"}'
```

Response should be `200` with `{email,user_id,workspace_id,...}`.
The endpoint also creates the Personal workspace for that user.

## Step 8 — open the browser via SSH tunnel

On **your PC**:

```powershell
ssh -N -L 8443:127.0.0.1:8443 <vm-user>@<vm-ip>
```

Open `https://localhost:8443/login` on the PC's browser. The
browser thinks `localhost:8443` is local; the actual traffic goes
through the SSH tunnel and lands on Caddy on the VM, which
reverse-proxies `/api/*` to the Go API and everything else to
Vite. Cookies stay host-only because both are on the same origin.

## Step 9 — TikTok OAuth (the one external dependency)

TikTok is the only thing that still talks to an external API.

1. In the TikTok Developer Portal, register the redirect URI:
   `https://localhost:8443/api/v1/auth/tiktok/callback` (exact,
   character-for-character match).
2. Add your two sandbox target users (max 2) so the OAuth consent
   screen has someone to authorize.
3. Set `TIKTOK_CLIENT_ID` and `TIKTOK_CLIENT_SECRET` in
   `~/Projects/instaedit/.env.dev` (Step 3 above).
4. Restart the API so it picks up the new env:

   ```bash
   docker compose -f docker-compose.yml -f docker-compose.local.yml restart api worker
   ```

5. In the SPA, navigate to `/app/linking` and click "Connect
   TikTok". You will be redirected to TikTok's consent screen;
   authorize as one of the target users; TikTok redirects back to
   `/api/v1/auth/tiktok/callback?code=...&state=...`; the backend
   exchanges the code for tokens; the dashboard shows the
   connection under "Linked Accounts".

> TikTok DOES NOT officially accept `localhost` as a redirect
> host. Plan B: register a tunnel URL (ngrok / cloudflared /
> devtunnel) as the redirect URI and adjust
> `TIKTOK_REDIRECT_URI` in `.env.dev` accordingly. The rest of
> this stack's design still applies.

## Operational notes

* **Cert rotation:** every cert swap requires
  `docker restart instaedit-caddy`. Chrome will continue showing
  the previous cert until Caddy reloads.
* **MinIO password lock-step:** `S3_SECRET_KEY` (in `.env.dev`)
  and `MINIO_ROOT_PASSWORD` (in `docker-compose.local.yml`) MUST
  stay identical. If you rotate one, rotate the other and bounce
  both `minio` and `api` containers
  (`docker compose ... restart minio api`).
* **Per-day bring-up / tear-down:** `docker compose ... down -v`
  to wipe volumes (clean DB) | `up -d` to bring everything back.
* **Logs:**

  ```bash
  tmux attach -t vite                       # Ctrl-B D to detach
  docker logs -f instaedit-api
  docker logs -f instaedit-worker
  docker logs -f instaedit-caddy
  docker logs -f instaedit-minio-init
  ```

## What does NOT belong in this dev stack

* Cloudflare tunnels (we run the stack on the VM directly via SSH
  tunnel; no need for a public hostname in dev)
* External Postgres (the dev `db` container is the source of truth)
* External S3 (MinIO replaces it locally)
* `VITE_DEMO_MODE=true` (the demo mode is for static-UI previews,
  not for testing the real OAuth flows described above)

## File summary

| File                            | Committed? | Purpose                                       |
| ---                             | ---        | ---                                           |
| `docker-compose.local.yml`      | ✓          | MinIO + minio-init overlay                    |
| `ops/local/Caddyfile`           | ✓          | Repo-friendly Caddyfile (generic mount paths) |
| `docs/LOCAL-DEVELOPMENT.md`     | ✓          | This document                                 |
| `.env.dev`                      | ✗          | All secrets; gitignored                       |
| `~/.Caddyfile`                  | ✗          | Copied from `ops/local/Caddyfile`             |
| `~/instaedit-certs/*.pem`       | ✗          | mkcert output for `localhost:8443`            |
| `web/.env.local`                | ✗          | `VITE_API_BASE_URL` for the dev Vite          |
