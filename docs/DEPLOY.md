# VPS deployment (without Fly.io or Vercel)

InstaEdit runs as one local Docker Compose stack on the VPS. Caddy is the
only public entry point:

`https://dev.instaedit.org` → Caddy → API `127.0.0.1:8080` / SPA files.

## Prerequisites

Create an `A` record for `dev.instaedit.org` pointing to the VPS public IP,
and allow only TCP ports 22, 80 and 443 in the firewall. PostgreSQL, MinIO
and the API remain bound to loopback or the Docker network.

Register these exact Google OAuth values:

```text
JavaScript origin: https://dev.instaedit.org
YouTube callback: https://dev.instaedit.org/api/v1/auth/youtube/callback
Drive callback:    https://dev.instaedit.org/api/v1/auth/google-drive/callback
```

## Application stack

Copy `.env.dev.example` to `.env.dev`, fill all required secrets, and keep
the following values aligned:

```env
FRONTEND_URL=https://dev.instaedit.org
CORS_ALLOWED_ORIGINS=https://dev.instaedit.org,https://app.instaedit.org
COOKIE_DOMAIN=.instaedit.org
S3_BUCKET=instaedit-local
YOUTUBE_REDIRECT_URI=https://dev.instaedit.org/api/v1/auth/youtube/callback
GOOGLE_DRIVE_REDIRECT_URI=https://dev.instaedit.org/api/v1/auth/google-drive/callback
```

Start or update the stack from the repository root:

```bash
docker compose -f docker-compose.yml -f docker-compose.local.yml up -d --build
docker compose ps
curl -fsS http://127.0.0.1:8080/api/v1/health
```

The Compose files bind API and PostgreSQL to `127.0.0.1`; do not change
those bindings to `0.0.0.0` on a public VPS.

## Frontend and Caddy

Build the SPA and publish it outside the repository:

```bash
cd web
npm ci
npm run build
sudo mkdir -p /srv/instaedit/web/dist
sudo rsync -a --delete dist/ /srv/instaedit/web/dist/
cd ..
```

Run Caddy with `ops/vps/Caddyfile`:

```bash
docker rm -f instaedit-caddy 2>/dev/null || true
docker run -d --name instaedit-caddy --restart unless-stopped \
  --network host \
  -v "$PWD/ops/vps/Caddyfile:/etc/caddy/Caddyfile:ro" \
  -v /srv/instaedit/web/dist:/srv/instaedit/web/dist:ro \
  -v caddy_data:/data -v caddy_config:/config caddy:2
```

Caddy obtains and renews HTTPS automatically. Validate externally with
`curl -fsS https://dev.instaedit.org/api/v1/health` after DNS propagation.

## Fly.io and Vercel

`fly.toml` and the historical Fly scripts remain in the repository for
reference during the cutover, but are not part of this runtime. Do not run
Fly deploy targets or the Vercel workflows for this VPS deployment. After
OAuth, private upload, Drive and backup tests pass, the old Fly app and its
secrets can be retired from the provider consoles.
