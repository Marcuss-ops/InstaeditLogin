# Deploy — Docker Compose + MinIO + VPS + Vercel

This is the canonical deployment runbook for InstaEditLogin.

The supported production shape is:

- **VPS:** one hardened Linux host running Docker Compose and host-managed
  Caddy;
- **Docker Compose:** PostgreSQL, the one-shot migration job, the HTTP API,
  background workers, and MinIO;
- **MinIO:** the only production S3-compatible object store;
- **Vercel:** the React/Vite frontend only.

The backend and its stateful services are not deployed by Vercel. Caddy is the
only public entry point for the backend; PostgreSQL and MinIO are private to
the VPS and Compose network.

> **Source of truth:** keep this runbook aligned with `docker-compose.yml`,
> `docker-compose.production.yml`, `Dockerfile`, the production secret-manager
> record, `ops/vps/Caddyfile`, root `vercel.json`, and
> `.github/workflows/deploy.yml`.
>
> **Supported-provider boundary:** production uses only Vercel for the
> frontend and the VPS Compose stack for backend hosting, PostgreSQL, and MinIO
> object storage. Fly, Railway, Render, Tigris, and other alternative backend
> hosting or object-storage paths are retired and are not deployment options.
> Historical material, when retained, belongs under `docs/archive/` and is not
> an operational procedure.

## 1. Production topology and DNS

```text
                         Internet
                             │
                 ┌───────────┴───────────┐
                 │                       │
       app.instaedit.org          api.instaedit.org
                 │                       │
              Vercel                 VPS :80/:443
           React/Vite SPA                │
                                host-managed Caddy
                                        │
                              127.0.0.1:8080
                                        │
                          Docker Compose application stack
                                        │
        ┌──────────────┬───────────────┼──────────────┬──────────────┐
        │              │               │              │              │
     db/Postgres     migrate          api          worker         MinIO
     private         one-shot       HTTP only    background      private
                                                                    │
                                                               named volume
```

### 1.1 Service responsibilities

| Component | Runtime | Responsibility | Publicly exposed? |
| --- | --- | --- | --- |
| `caddy` | VPS host service | TLS termination and reverse proxy to `127.0.0.1:8080` | Yes, ports 80/443 |
| `db` | Compose | PostgreSQL persistence | No |
| `migrate` | Compose one-shot | Apply idempotent database migrations, then exit | No |
| `api` | Compose target `api` | HTTP API and readiness endpoints | Only through Caddy |
| `worker` | Compose target `worker` | Publishing, reconciliation, outbox, webhook, metrics, and cleanup workers | No |
| `minio` | Compose | S3-compatible media and artifact storage | No |
| Vercel | Managed frontend host | Build and serve `web/` | Yes, `app.instaedit.org` |

The canonical process split is also available locally through `make dev`.
The single-process compatibility wrapper is not part of this deployment path.

### 1.2 DNS records

Use the actual VPS address in place of `$VPS_IP`.

| Name | Record | Target | Purpose |
| --- | --- | --- | --- |
| `api.instaedit.org` | A | `$VPS_IP` | Backend Caddy endpoint |
| `app.instaedit.org` | CNAME or Vercel-managed record | Vercel project target | Frontend SPA |
| `instaedit.org` | Vercel-managed apex record | Vercel project | Redirect to `app.instaedit.org` |
| `www.instaedit.org` | Vercel-managed record | Vercel project | Redirect to `app.instaedit.org` |

The exact Vercel DNS values are supplied by the Vercel project dashboard. Do
not invent a provider target in this document: verify the record shown by the
project before changing DNS.

For the backend, verify that `api.instaedit.org` resolves to the VPS before
starting Caddy. If DNS is managed through a proxy service, use DNS-only mode
for the backend record so Caddy can complete the HTTP-01 certificate
challenge directly.

### 1.3 Firewall contract

Allow only:

- TCP 22 from the operator network, preferably restricted by source IP;
- TCP 80 and TCP 443 from the Internet.

Do not publish PostgreSQL, MinIO API, MinIO console, or the internal worker
health listener on a public interface. The production Compose overlay removes
the database host binding, and the canonical Compose file keeps the API on
loopback.

### 1.4 Public endpoint contract

The backend endpoints used by checks and monitoring are:

- `GET https://api.instaedit.org/api/v1/health`;
- `GET https://api.instaedit.org/ready`.

The frontend uses `VITE_API_BASE_URL=https://api.instaedit.org` and never talks
directly to PostgreSQL, MinIO, or a private Compose hostname.

### 1.5 DNS and TLS verification

Run these checks from a machine outside the VPS:

```bash
dig +short api.instaedit.org A
curl -fsS https://api.instaedit.org/api/v1/health
curl -fsS https://api.instaedit.org/ready
curl -fsSI https://app.instaedit.org/
```

Expected results:

- the API A record returns `$VPS_IP`;
- `/api/v1/health` returns HTTP 200 and a healthy service response;
- `/ready` returns HTTP 200 only when database and migration checks pass;
- the frontend returns HTTP 200 or its configured redirect response.

## 2. VPS provisioning

The examples assume Ubuntu 24.04 or Debian 12 and an operator account named
`instaedit`. Adapt usernames and paths only when the same values are used
consistently in the rest of the runbook.

### 2.1 Harden the host

```bash
# Run as root during initial provisioning.
adduser instaedit
usermod -aG sudo instaedit
install -d -m 700 -o instaedit -g instaedit /home/instaedit/.ssh
# Install the operator's public key in authorized_keys, then:
chmod 600 /home/instaedit/.ssh/authorized_keys

# Disable password/root SSH login after confirming key access.
# Edit /etc/ssh/sshd_config:
#   PermitRootLogin no
#   PasswordAuthentication no
systemctl reload ssh
```

Install Docker Engine and the Compose plugin using the distribution's
supported Docker instructions. Confirm the operator can run Docker without
`sudo`:

```bash
docker version
docker compose version
```

Configure the host firewall for TCP 22/80/443 only. Do not use a host firewall
rule as a substitute for Compose's private network and loopback bindings.

### 2.2 Create stateful directories

```bash
sudo install -d -m 750 -o instaedit -g instaedit \
  /opt/instaedit \
  /opt/instaedit/secrets \
  /opt/instaedit/backups \
  /opt/instaedit/ops
```

The repository may live at `/opt/instaedit/InstaeditLogin`; the commands below
use that path. Keep secrets outside the Git checkout:

```bash
sudo install -d -m 750 -o instaedit -g instaedit \
  /opt/instaedit/InstaeditLogin
```

Docker named volumes persist the PostgreSQL and MinIO data. Never run
`docker compose down -v` against the production project unless the explicit
purpose is destructive data removal and an independently verified backup
exists.

### 2.3 Install the repository

```bash
sudo -u instaedit git clone <repository-url> \
  /opt/instaedit/InstaeditLogin
cd /opt/instaedit/InstaeditLogin
sudo -u instaedit git checkout main
```

For subsequent releases, update only through fast-forward pulls:

```bash
cd /opt/instaedit/InstaeditLogin
git fetch origin main
git pull --ff-only origin main
```

Do not edit deployment files directly on the VPS. Change the repository,
review it, push to `main`, then pull the reviewed commit on the host.

## 3. Production secrets and environment

Create the production environment file with mode `0600` and ownership limited
to the deployment operator. The atomic no-clobber step is safe to repeat and
safe under concurrent invocations: it creates the file only when absent and
never truncates existing secrets.

```bash
sudo install -d -m 750 -o instaedit -g instaedit /opt/instaedit/secrets
sudo sh -c '
  set -eu
  target=/opt/instaedit/secrets/.env.production
  expected_owner=$(id -u instaedit):$(id -g instaedit)
  if test -e "$target" || test -L "$target"; then
    test -f "$target"
    test ! -L "$target"
    test "$(stat -c %a "$target")" = 600
    test "$(stat -c %u:%g "$target")" = "$expected_owner"
  else
    tmp=$(mktemp "$target.tmp.XXXXXX")
    trap '\''rm -f -- "$tmp"'\'' EXIT HUP INT TERM
    chmod 0600 "$tmp"
    chown "$expected_owner" "$tmp"
    if ! ln -T "$tmp" "$target" 2>/dev/null; then
      if test -e "$target" || test -L "$target"; then
        test -f "$target"
        test ! -L "$target"
        test "$(stat -c %a "$target")" = 600
        test "$(stat -c %u:%g "$target")" = "$expected_owner"
      else
        echo "cannot create $target" >&2
        exit 1
      fi
    fi
  fi
'
sudoedit /opt/instaedit/secrets/.env.production
# Populate it from the approved secret manager and the variable groups below;
# do not copy a local or untracked example file to production.
```

The file must contain real values for all required application settings. At a
minimum, populate and review these groups against the application config
validator before deployment:

- `POSTGRES_USER`, `POSTGRES_PASSWORD`, and `POSTGRES_DB`;
- `JWT_SECRET` and the encryption key/keyring settings;
- `S3_ENDPOINT=http://minio:9000`, `S3_BUCKET`, `S3_ACCESS_KEY`,
  `S3_SECRET_KEY`, and `S3_REGION`;
- `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD`;
- `FRONTEND_URL`, `INSTAEDITOR_URL` (required in production; `EDITOR_URL` is
  no longer read — a missing value fails fast), `CORS_ALLOWED_ORIGINS`, and
  cookie settings;
- enabled OAuth provider credentials and their registered redirect URIs;
- the explicitly enabled internal integration settings, if used.

The MinIO root credentials and API S3 credentials must be coordinated. Never
print the environment file, pass it as a command-line argument, or commit it.
A secret rotation is incomplete until every service that consumes the value has
been recreated and the relevant health checks pass.

### 3.1 Compose interpolation check

From the repository root, validate the fully interpolated configuration without
starting services. The command must receive the production environment file:

```bash
cd /opt/instaedit/InstaeditLogin
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production
export INSTAEDIT_YOUTUBE_ENV_FILE=/opt/instaedit/secrets/.env.youtube.local

docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  config --quiet
```

If this fails, fix the missing or malformed environment variable before any
`up` command. Do not bypass required-value checks with placeholder secrets.

## 4. Caddy on the VPS

Caddy is host-managed; it is not a Compose service. The production Caddyfile
must reverse-proxy `api.instaedit.org` to `127.0.0.1:8080` and abort public
requests to `/internal/*`.

Use the tracked `ops/vps/Caddyfile` as the production source to review.
It contains a single `api.instaedit.org` block: API on `127.0.0.1:8080`,
`/internal/*` aborted. The legacy `dev.instaedit.org` compatibility host
(InstaEditor deployment, formerly Dark Editor, MinIO proxy, SPA redirects) was removed on 2026-08-07 — the
frontend is served exclusively by Vercel. Install the file on the VPS,
validate it, and reload Caddy without editing the live file by hand:

```bash
cd /opt/instaedit/InstaeditLogin
sudo install -m 0644 ops/vps/Caddyfile /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
sudo systemctl is-active --quiet caddy
```

The host must already have TCP 80 and 443 open and DNS must resolve the API
hostname to the VPS. Caddy obtains and renews certificates automatically.
Check renewal and routing when bringing up a new host:

```bash
sudo journalctl -u caddy --since '1 hour ago' --no-pager
curl -fsSI https://api.instaedit.org/api/v1/health
```

The supported VPS installation is the systemd-managed Caddy service. Do not
maintain a second container-managed Caddy configuration or a second source of
truth.

## 5. Docker Compose deployment

> **⚠️ Live migration exception (not the canonical production shape).**
> The canonical runbook deploys with `docker-compose.production.yml` and
> `/opt/instaedit/secrets/.env.production`. **The production VPS that currently
> serves `api.instaedit.org` does not use that shape.** The live stack
> (`pierone@51.91.11.36`, repo `~/Projects/company/InstaeditLogin`) runs:
>
> ```text
> compose files : docker-compose.yml + docker-compose.local.yml
> env file     : INSTAEDIT_ENV_FILE=.env.dev  (holds MINIO_ROOT_* and the
>                API_HOST_PORT override)
> api binding  : 127.0.0.1:${API_HOST_PORT:-8080}:8080  → must stay 8080
> Caddy target : api.instaedit.org → 127.0.0.1:8080
> ```
>
> The effective release command on that host is:
>
> ```bash
> cd ~/Projects/company/InstaeditLogin
> git pull --ff-only origin main
> INSTAEDIT_ENV_FILE=.env.dev docker compose \
>   --env-file .env.dev \
>   -f docker-compose.yml -f docker-compose.local.yml \
>   config --quiet
> INSTAEDIT_ENV_FILE=.env.dev docker compose \
>   --env-file .env.dev \
>   -f docker-compose.yml -f docker-compose.local.yml \
>   up -d --build
> ```
>
> This live exception must be converged to the canonical production shape;
> do not copy its `.env.dev` or local Compose settings into new production
> hosts. **Port-ownership pitfall (incident 2026-08-05, legacy binary retired
> 2026-08-18):** Caddy only ever talks to `127.0.0.1:8080`. If `API_HOST_PORT`
> drifts from `8080` (it was `8082`), the rebuilt container publishes
> elsewhere while Caddy keeps proxying to the old port, so **new API routes
> return 404**. The historical orphaned single-process dev binary
> (`/usr/local/bin/instaeditlogin-dev`, launched by the deprecated
> `instaeditlogin.service` systemd unit) was removed on 2026-08-18; the
> Compose ensure-up unit `instaedit-compose.service` now restores the
> canonical `127.0.0.1:8080` binding at every boot. Before deploying, verify
> the port owner:
>
> ```bash
> ss -ltnp | grep ':8080'                   # must be the Compose api container
> systemctl status instaedit-compose.service # active (exited) = ensure-up OK
> ```

### 5.1 First deployment

Run the migration-gated stack from the repository root:

```bash
cd /opt/instaedit/InstaeditLogin
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production

# Validate first; this does not start containers.
docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  config --quiet

# Build the canonical api, worker, and migrate images and start the stack.
# Compose waits for a healthy database and lets migrate complete before
# allowing api and worker to start.
docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d --build
```

The expected startup behavior is:

1. PostgreSQL becomes healthy;
2. `migrate` applies pending migrations and exits successfully;
3. `api` and `worker` are released after migration success;
4. MinIO becomes healthy and `minio-init` ensures the application bucket exists;
5. `api` waits for `minio-init` before serving storage-backed requests; `worker`
   has no `minio-init` dependency and starts after the database/migration gate;
6. Caddy routes public API traffic to the loopback API listener.

Inspect the state without exposing environment values:

```bash
docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  ps

docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  logs --tail=200 migrate api worker minio minio-init
```

Do not run the deprecated single-process service or expose the Compose server
profile on the production host.

### 5.2 Routine release deployment

```bash
cd /opt/instaedit/InstaeditLogin
git pull --ff-only origin main
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production

docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  config --quiet

docker compose \
  --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml \
  -f docker-compose.production.yml \
  up -d --build
```

A release that changes migrations must have a fresh database backup before the
`up` command. A release that changes Caddy must validate and reload Caddy
separately after the repository update.

### 5.3 Service operations

```bash
# Recreate only the API after an API-only change.
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  up -d --build --force-recreate api

# Recreate workers after worker/config changes.
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  up -d --build --force-recreate worker

# Follow service logs without printing the environment file.
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  logs -f --tail=200 api worker
```

Use `docker compose stop` or a targeted service restart for maintenance. Avoid
`down -v`; named volume deletion destroys application data.

## 6. MinIO and media storage

MinIO is the sole production object store. The API reaches it through the
Compose DNS name `minio:9000`; browsers never receive a private Compose
hostname. The application bucket is created idempotently by `minio-init`.

### 6.1 Storage invariants

- `S3_ENDPOINT` points to `http://minio:9000` from the API/worker containers.
- `S3_BUCKET` is the production bucket named in the secret file.
- `S3_ACCESS_KEY`/`S3_SECRET_KEY` are valid MinIO credentials.
- `MINIO_ROOT_USER`/`MINIO_ROOT_PASSWORD` are not logged or exposed through
  the frontend.
- MinIO has persistent storage and is not recreated with `down -v`.
- MinIO API and console ports are not published to the Internet.
- MinIO is private: `S3_ENDPOINT=http://minio:9000` is only reachable
  inside the Compose network; presigned URLs are used by server-side
  flows (drive import, upload worker, media resolution). Browser uploads
  that need a PUBLIC presigned URL are intentionally out of the canonical
  path — to enable them, publish MinIO on the VPS loopback
  (`127.0.0.1:19000:9000`), set `S3_ENDPOINT=https://api.instaedit.org`
  (path-style) and uncomment the media handle in `ops/vps/Caddyfile`.

Verify the service and its health without publishing a port:

```bash
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  ps minio minio-init

docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  logs --tail=200 minio minio-init
```

If operators need the MinIO console for maintenance, bind it temporarily to
loopback only through an explicitly reviewed Compose override or an SSH tunnel.
Never open ports 9000 or 9001 in the VPS firewall.

### 6.2 MinIO backup

A database dump does not include object data. Back up both layers according to
the retention policy. The repeatable default is an authenticated object-level
copy from the MinIO service to an operator-controlled directory on the VPS.
Before copying, confirm that the production `minio` service is running, the
bucket name is present in the production env file, and the destination is an
empty private directory:

```bash
cd /opt/instaedit/InstaeditLogin
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production
TS=$(date -u +%Y%m%dT%H%M%SZ)
DEST=/opt/instaedit/backups/minio-$TS
mkdir -p "$DEST"


# minio-init already contains the MinIO client and the production env_file.
# Override its one-shot entrypoint; this does not start or recreate the stack.
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  run --rm --no-deps \
  -v "$DEST:/backup" \
  --entrypoint /bin/sh minio-init -c '
    set -eu
    test -n "${S3_BUCKET:-}"
    test -z "$(find /backup -mindepth 1 -print -quit)"
    mc alias set local http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
    mc ready local
    mc mirror --overwrite local/"$S3_BUCKET" /backup
    mc ls --recursive --summarize local/"$S3_BUCKET"
  '

chmod -R go-rwx "$DEST"
find "$DEST" -type f -print0 | sort -z | xargs -0 -r sha256sum > "$DEST.sha256"
chmod 600 "$DEST.sha256"
```

Store the backup and checksum outside the VPS as well. Record the timestamp,
bucket name, object count, byte total, and destination. Test restoration into
an isolated MinIO instance before declaring the drill successful; never mirror
a production bucket into a public bucket.

For a volume-level snapshot, identify the actual named volume first because
Compose project names affect it:

```bash
docker volume ls | grep -i minio
docker volume inspect <project>_instaedit_minio_data
```

Do not copy a live volume by deleting or stopping the production stack without
an approved maintenance window. Prefer the object-level copy above or a
storage-native snapshot supplied by the VPS operator.

## 7. Vercel frontend deployment

Vercel hosts only the `web/` Vite application. The repository-root
`vercel.json` is the single tracked Vercel configuration:

- install command: `npm --prefix web ci`;
- build command: `npm --prefix web run build`;
- output directory: `web/dist`;
- SPA fallback: rewrite to `/index.html`;
- apex and `www` redirects: redirect to `app.instaedit.org`.

### 7.1 Vercel project configuration

Set the Vercel project's Root Directory to the repository root (leave it
empty/default in the dashboard). The root `vercel.json` points commands and
output at `web/`. Configure the production frontend variable:

```text
VITE_API_BASE_URL=https://api.instaedit.org
```

Only values intended for a browser bundle belong in `VITE_*` variables. Never
put database credentials, signing keys, OAuth client secrets, MinIO
credentials, or internal integration tokens in Vercel frontend variables.

### 7.2 Deploy through CI

The supported deploy workflow is `.github/workflows/deploy.yml`:

1. push the reviewed commit to `main`;
2. `integration-fast` must complete successfully;
3. the deploy workflow checks out the tested commit;
4. Vercel runs `npm --prefix web ci`, `npm --prefix web run build`, and publishes `web/dist` to the production alias.

Required GitHub/Vercel project credentials are configured as repository
secrets, not committed to this repository. A manual workflow dispatch is an
exception path and must be recorded by the operator.

Validate the published frontend after a deploy:

```bash
curl -fsSI https://app.instaedit.org/
curl -fsS https://app.instaedit.org/ | head -c 500
```

## 8. PostgreSQL backup and restore drill

The production database is the Compose `db` service using PostgreSQL. Take a
custom-format dump before migration-bearing releases and retain encrypted,
off-host copies according to the operator retention policy.

### 8.1 Create a dump

```bash
cd /opt/instaedit/InstaeditLogin
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production
TS=$(date -u +%Y%m%dT%H%M%SZ)
BACKUP=/opt/instaedit/backups/instaedit-$TS.dump

mkdir -p /opt/instaedit/backups

docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  exec -T db sh -c \
  'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner --no-acl' \
  > "$BACKUP"

chmod 600 "$BACKUP"
ls -lh "$BACKUP"
```

Confirm the dump is non-empty and copy it to independent storage. Do not place
production dumps in Git or in a publicly accessible object bucket.

### 8.2 Restore drill

Run a restore drill on an isolated host or isolated Docker network, never over
the live database:

```bash
TS=$(date -u +%Y%m%dT%H%M%SZ)
DUMP=/path/to/instaedit-$TS.dump

# Start an isolated PostgreSQL instance with the same major version as prod.
docker run -d --name instaedit-restore-$TS \
  -e POSTGRES_USER=instaedit \
  -e POSTGRES_PASSWORD='use-a-temporary-value' \
  -e POSTGRES_DB=instaedit_login \
  postgres:17-alpine

# Wait for readiness, then restore.
until docker exec instaedit-restore-$TS \
  pg_isready -U instaedit -d instaedit_login >/dev/null 2>&1; do sleep 2; done

docker exec -i instaedit-restore-$TS \
  pg_restore -U instaedit -d instaedit_login --no-owner --no-acl \
  --clean --if-exists < "$DUMP"

# Verify the database is usable and the canary tables exist.
docker exec instaedit-restore-$TS \
  psql -U instaedit -d instaedit_login -c '\dt'

# Always remove the isolated instance after the drill.
docker rm -f instaedit-restore-$TS
```

Record the dump timestamp, schema/migration result, canary-table result,
representative row-count checks, and cleanup result in the operator's restore
drill report. The restore drill is not complete until the temporary instance
has been removed.

## 9. Deploy verification and monitoring

Run the following gates after every backend deployment:

```bash
cd /opt/instaedit/InstaeditLogin
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production

# Compose state and recent startup logs.
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml ps

docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  logs --tail=200 migrate api worker minio minio-init

# Public API gates.
curl -fsS https://api.instaedit.org/api/v1/health
curl -fsS https://api.instaedit.org/ready

# Frontend gate.
curl -fsSI https://app.instaedit.org/
```

Then run the read-only operational checks from the repository where their
required credentials and test-user policy are configured:

```bash
make verify-entrypoint-topology
make verify-log-redaction
make ops-smoke
```

The smoke checks must not be treated as a substitute for reviewing Compose
state, migration completion, Caddy routing, and the frontend API base URL.

### 9.1 Common failures

| Symptom | First checks | Safe action |
| --- | --- | --- |
| `api` does not start | `docker compose logs migrate api`, `docker compose ps` | Fix migration/config failure; do not bypass the migration dependency |
| `/ready` is not 200 | `db` health, migration logs, database canary tables | Resolve schema/config issue, then recreate the affected service |
| API is unreachable | DNS, firewall, `systemctl status caddy`, Caddy validation | Fix DNS/firewall/Caddy before changing application containers |
| Uploads fail | MinIO health/logs, S3 env names, bucket initialization | Correct MinIO credentials/endpoint and recreate `api`/`worker` |
| Frontend calls the wrong host | Vercel production `VITE_API_BASE_URL`, published asset | Correct the Vercel variable and redeploy the frontend |
| New API routes return 404 after a deploy | `ss -ltnp \| grep :8080` (who owns the port), `docker port api`, `API_HOST_PORT` in `.env.dev`, `systemctl status instaedit-compose.service` | Recreate `api` with `API_HOST_PORT=8080` (see §5 note); the legacy `instaeditlogin.service` dev binary was retired 2026-08-18 |
| Worker is idle | worker logs, database connectivity, pending job state | Recreate `worker` only after confirming API and migrations are healthy |

Do not solve a deployment failure by publishing private service ports or by
copying secrets into logs, shell history, or issue trackers.

## 10. Rollback and secret rotation

### 10.1 Application rollback

Database migrations are forward-only unless a reviewed reverse migration exists.
Before a migration-bearing release, take the database and MinIO backups in §8
and §6.2.

For a code-only rollback, revert the reviewed commit on `main`, then deploy the
new `main` commit normally:

```bash
# On a development/operator checkout, after review:
git revert <commit-sha>
git push origin main

# On the VPS:
cd /opt/instaedit/InstaeditLogin
git pull --ff-only origin main
export INSTAEDIT_ENV_FILE=/opt/instaedit/secrets/.env.production
docker compose --env-file "$INSTAEDIT_ENV_FILE" \
  -f docker-compose.yml -f docker-compose.production.yml \
  up -d --build
```

Do not use `git reset --hard` on the shared production branch as a rollback
mechanism. If a schema change is incompatible, stop rollout, preserve the
backup, and follow the incident/recovery decision with the operator team.

### 10.2 Rotate runtime secrets

1. Generate the replacement value in the approved secret manager.
2. Update `/opt/instaedit/secrets/.env.production` with mode `0600`.
3. Validate Compose interpolation without printing the rendered config.
4. Recreate every consumer (`api`, `worker`, and any relevant one-shot job).
5. Run `/api/v1/health`, `/ready`, upload, OAuth, and worker smoke checks.
6. Revoke the old credential only after all consumers are healthy.

For MinIO credentials, rotate the MinIO credential and the matching API S3
credential as one change. For JWT/encryption keys, follow the key-rotation and
legacy-decryption compatibility rules in the application security runbooks;
do not delete old decryption material until the documented compatibility
window has ended.

## 11. Release checklist

### Before deployment

- [ ] The change is reviewed and pushed to `main`.
- [ ] CI (`integration-fast`) is green.
- [ ] The VPS checkout can fast-forward to the intended commit.
- [ ] `/opt/instaedit/secrets/.env.production` exists, is `0600`, and has no
      placeholder values.
- [ ] `docker compose ... config --quiet` passes.
- [ ] A fresh PostgreSQL dump exists for migration-bearing changes.
- [ ] MinIO object backup/snapshot policy is satisfied for storage changes.
- [ ] DNS still points the API hostname to the intended VPS.

### Deployment

- [ ] `git pull --ff-only origin main` succeeds.
- [ ] Compose starts `db`, `migrate`, `api`, `worker`, `minio`, and
      `minio-init` in the expected order.
- [ ] `migrate` exits successfully.
- [ ] Caddy configuration validates and reloads if changed.
- [ ] No private service port is exposed publicly.

### After deployment

- [ ] `docker compose ps` shows the long-running services healthy.
- [ ] `/api/v1/health` returns 200.
- [ ] `/ready` returns 200.
- [ ] `app.instaedit.org` serves the new frontend.
- [ ] The frontend uses `https://api.instaedit.org` as its API base.
- [ ] API, worker, migration, and MinIO logs contain no unexpected errors or
      credentials.
- [ ] Post-deploy smoke and log-redaction checks pass.
- [ ] The deployment commit, operator, timestamp, and verdict are recorded.

## Cross-references

- `docker-compose.yml` — canonical service graph and migration dependency.
- `docker-compose.production.yml` — production hardening overlay.
- `Dockerfile` — `api`, `worker`, and `migrate` image targets.
- the production secret-manager record — required environment variable surface;
  keep the real file outside the Git checkout.
- `ops/vps/Caddyfile` — tracked production VPS reverse-proxy source.
- `vercel.json` — root-level frontend build, rewrite, and domain configuration.
- `.github/workflows/deploy.yml` — CI-gated Vercel production deployment.
- `docs/OPERATIONS.md` — ongoing DNS, TLS, monitoring, and recovery procedures.
- `scripts/verify-entrypoint-topology.sh` — regression check for canonical
  entrypoint usage.
