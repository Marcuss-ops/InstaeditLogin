# Deploy — VPS production stack for `instaedit-login`

Canonical reference for first deploy and ongoing operations to the VPS
production target. **This is the single source of truth for production
deployment as of 2026-07-25.** The historical Fly.io / Vercel
configurations have been removed from the live path; their files
(`fly.toml`, `scripts/set-fly-secrets.sh`, `scripts/verify-fly-secrets.sh`,
the `web/` Vercel preview workflow, etc.) were deleted in earlier
cutover commits. The remaining references in the repo are archaeological
only.

The doc is structured around the 10 deploy surface areas. End-to-end
deploy execution lives in **§10**; the prior sections are build blocks
that the execution block assumes.

## Topology

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
        │   └─────────────────────────────────────────────┘     │
        └───────────────────────────────────────────────────────┘
```

Caddy is the only process listening on the public network (TCP/80 + /443).
Postgres and MinIO are bound to the Compose network (postgres on
`db:5432`, minio on `minio:9000`) with port mapping to `127.0.0.1` only.
The Go API listens on `127.0.0.1:8080` inside the Compose network; the
Caddy `api.instaedit.org` block is the sole public reverse-proxy toward
it. Pre-cutover evidence is preserved in `docs/VPS-DEPLOY-STATUS.md`
and `docs/DEPLOY-AUDIT.md`.

---

## 1. VPS

Single-host architecture. The live system runs at `51.91.11.36` (a
Hetzner Cloud VPS). Re-pointing to a new VPS is a registrar A-record
update + a fresh `docker compose up`; no managed-cluster migration is
involved.

### 1.1 Recommended spec

| Resource         | Spec                                                |
|------------------|-----------------------------------------------------|
| Region           | eu-west-1 or closest EU zone (GDPR proximity)       |
| Image            | Ubuntu 24.04 LTS (or Debian 12)                     |
| Plan             | 4 vCPU / 8 GB RAM / 80 GB NVMe SSD                  |
| Inbound firewall | TCP/22 (SSH) + TCP/80 + TCP/443 only                |
| Outbound         | unrestricted (LE ACME + provider OAuth round-trips) |
| DNS path         | A record + Cloudflare proxy OFF (DNS-only / "grey") |

### 1.2 First-time host setup (operator laptop → VPS)

```bash
# 1. Provision the VPS at your host of choice (Hetzner, OVH,
#    DigitalOcean, Scaleway). Record the public IPv4 as $VPS_IP.

# 2. SSH in and harden:
ssh root@$VPS_IP
adduser instaedit
mkdir -p /home/instaedit/.ssh
# Paste the operator laptop's pubkey into /home/instaedit/.ssh/authorized_keys
chmod 700 /home/instaedit/.ssh
chmod 600 /home/instaedit/.ssh/authorized_keys
chown -R instaedit:instaedit /home/instaedit/.ssh
# /etc/ssh/sshd_config:
#   PermitRootLogin no
#   PasswordAuthentication no
# Then systemctl reload ssh.

# 3. Install Docker Engine + Compose plugin (Ubuntu 24.04):
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

# 4. Stateful directories (bind-mounted into the Compose stack):
mkdir -p /srv/instaedit/{pgdata,miniostore,caddy_data,caddy_config,web/dist,backups,ops}
chown -R instaedit:instaedit /srv/instaedit
```

### 1.3 Project layout on the VPS

```bash
cd /opt/instaedit/InstaeditLogin                 # clone or rsync from laptop
sudo install -m 0644 ops/vps/Caddyfile /srv/instaedit/Caddyfile
sudo install -m 0644 docker-compose.yml /srv/instaedit/docker-compose.yml

# Secrets on disk (mode 0600, owned by instaedit). NEVER commit it.
sudo cp .env.production.example /srv/instaedit/.env.production
sudo chmod 600 /srv/instaedit/.env.production
sudo chown instaedit:instaedit /srv/instaedit/.env.production
```

---

## 2. Docker Compose

### 2.1 Services

The canonical production stack has six services. Five run continuously;
one (`migrate`) is one-shot and runs before the `api` and `worker`
services start.

| Service    | Image / target                  | Public? | Notes |
|------------|----------------------------------|---------|-------|
| `caddy`    | `caddy:2`                        | TCP/80 + TCP/443 | TLS termination + apex/app/api SNI routing |
| `postgres` | `postgres:17-alpine`             | loopback to host only | DB `instaedit_login`, bind-mount `/srv/instaedit/pgdata` |
| `minio`    | `minio/minio` + `minio/mc` init  | loopback admin port `127.0.0.1:9001` | S3-compatible media store, bucket `instaedit-prod-media` |
| `api`      | Dockerfile `[api]` target        | reached via Caddy only (loopback `:8080`) | Go HTTP server |
| `worker`   | Dockerfile `[worker]` target     | internal only | 5 background goroutines per `cmd/worker/main.go` |
| `migrate`  | Dockerfile `[migrate]` target    | one-shot | runs `cmd/migrate/main.go`, exits 0 on success |

### 2.2 Compose files

| File | Purpose |
|------|---------|
| `docker-compose.yml`     | Baseline topology for both dev and prod |
| `docker-compose.local.yml` | Local-dev overrides (bind mounts, dev secrets) |
| `docker-compose.production.yml` (optional) | Per-VPS overrides (paths, ports, signed Caddyfile mirror) |

Production runs the baseline with `--env-file /srv/instaedit/.env.production`:

```bash
ssh instaedit@$VPS_IP
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production -f docker-compose.yml up -d --build
docker compose --env-file /srv/instaedit/.env.production run --rm migrate
```

### 2.3 Network binding (loopback-first safety)

Postgres and MinIO are NOT exposed on a public interface. The Compose
file constrains port publishing to `127.0.0.1`:

```yaml
postgres:
  ports: ["127.0.0.1:5432:5432"]
minio:
  ports: ["127.0.0.1:9000-9001:9000-9001"]
```

**Do not change these bindings to `0.0.0.0` on a public VPS** — both
services have no auth at the network layer (postgres is password-only
inside the Compose env block; minio's admin port has root creds baked
in). Caddy is the only public-facing process and rejects `/internal/*`
at the proxy layer (see §3).

The Go API inside Compose connects via the compose-internal DNS alias
`db:5432` (NOT host `127.0.0.1:5432`). The MinIO endpoint is `minio:9000`.
`S3_ENDPOINT=http://minio:9000` lives in `/srv/instaedit/.env.production`.

---

## 3. Caddy

### 3.1 Caddyfile source

`ops/vps/Caddyfile` is the canonical source. It is bind-mounted into
the running Caddy container at `/etc/caddy/Caddyfile`. Caddy reads the
file at process start; editing the source does NOT auto-reload — see
§3.4.

The current shape (paraphrased; see `ops/vps/Caddyfile` in the repo):

```caddyfile
api.instaedit.org {
    encode gzip
    handle /internal/* { abort }
    reverse_proxy 127.0.0.1:8080
}

app.instaedit.org {
    encode gzip
    handle /internal/* { abort }
    handle /api/* { reverse_proxy 127.0.0.1:8080 }
    handle /instaedit-local/* {
        reverse_proxy 127.0.0.1:19000 { header_up Host {host} }
    }
    handle {
        root * /srv/instaedit/web/dist
        try_files {path} /index.html
        file_server
    }
}
```

Two hosts today (production will add a third — `instaedit.org` apex for
the 301 → `app.instaedit.org` redirect). The `handle /internal/* { abort }`
block is present on every public host: probes must see nothing on the
internalVelox / diagnostics surface.

### 3.2 TLS auto-renew (Let's Encrypt via HTTP-01)

Caddy obtains and renews a Let's Encrypt cert for every host it
listens on. The HTTP-01 challenge goes to
`http://51.91.11.36/.well-known/acme-challenge/...` from the LE CA
vantage points, so verify **`dig +short instaedit.org A` returns
`51.91.11.36`** before the first `docker compose up`. No manual cert
step is required (`flyctl certs add` is intentionally gone).

LE has rate limits on prod issuance (5/h per host); the staging env is
free. Confirm renewal cadence weekly (`docker compose logs caddy | grep -i certificate`).

### 3.3 Sandbox-only HTTP probe (`HandlerCompatChecker` — Stage 2/2 sandbox probe)

The `cmd/sandbox-probe` binary runs as a one-shot HTTP probe against
`https://api.instaedit.org/api/v1/metrics` and asserts the Caddy route
responds with our handler contract. Useful locally for blue/green
confirmation before flipping DNS.

### 3.4 Edit / reload / validate / restart

```bash
# Edit the source-of-truth:
$EDITOR ops/vps/Caddyfile

# Validate BEFORE reload (dry-run, catches syntax errors):
ssh instaedit@$VPS_IP \
  'cd /opt/instaedit/InstaeditLogin && \
   docker compose exec -T caddy caddy validate --config /etc/caddy/Caddyfile'

# Reload (lightweight, no conn-drop):
ssh instaedit@$VPS_IP \
  'cd /opt/instaedit/InstaeditLogin && \
   docker compose exec -T caddy caddy reload --config /etc/caddy/Caddyfile'

# Restart (fallback when reload refuses — ~5-15s conn churn):
ssh instaedit@$VPS_IP \
  'cd /opt/instaedit/InstaeditLogin && docker compose restart caddy'
```

Editing `/etc/caddy/Caddyfile` directly inside the container WILL be
wiped on the next `docker compose restart caddy`; the bind-mount is the
canonical path. (`caddy_data` volume retains cert + OCSP staples — do
not delete without intent.)

---

## 4. PostgreSQL locale

### 4.1 Image and DB-name discipline

Image: `postgres:17-alpine`. Canonical DB name: **`instaedit_login`**
(NOT `_test`, NOT `_dev`, NOT `postgres`, NOT `template1`). This
invariant is enforced at three layers:

1. **`docker-compose.yml`** — the `postgres` service has `POSTGRES_DB:
   instaedit_login` hard-coded; the Compose stack creates the DB at
   first boot.
2. **`scripts/db/check-postgres-health.sh`** — asserts canary tables
   post-migration + the db name is exactly `instaedit_login`.
3. **`scripts/db/production-restore-drill.sh`** (or the manual
   equivalent in `docs/OPERATIONS.md §3.1.3` STEP 5) — the schema
   fingerprint catches a misconfigured dev cluster before any API
   traffic flows.

### 4.2 Env block (in `/srv/instaedit/.env.production`)

```env
POSTGRES_USER=instaedit
POSTGRES_PASSWORD=<rotatable>
POSTGRES_DB=instaedit_login
DATABASE_URL=postgres://instaedit:<pw>@db:5432/instaedit_login?sslmode=disable
```

The `DATABASE_URL` host is the Compose network DNS alias **`db`** (NOT
`127.0.0.1` — `127.0.0.1` would only work from containers sharing the
Compose network AND would bind on the host loopback instead of the
service alias).

### 4.3 Bind-mount + stateful directory

Host path: `/srv/instaedit/pgdata` → container path
`/var/lib/postgresql/data`. Mode `0700`, UID matched to the postgres
image's default UID (`999`).

```bash
mkdir -p /srv/instaedit/pgdata
chown -R 999:999 /srv/instaedit/pgdata
chmod 700 /srv/instaedit/pgdata
```

The first `docker compose up` initialises the cluster at
`/var/lib/postgresql/data` via the standard `postgres` image entrypoint.

### 4.4 Connecting for SQL audits

```bash
# From the VPS (NEVER from the public network — pg is loopback-bound):
ssh instaedit@$VPS_IP \
  'docker compose exec -T postgres psql -U instaedit -d instaedit_login -tA \
     -c "SELECT current_database();"'
# expect: instaedit_login

# Canary-tables probe (matches scripts/db/check-postgres-health.sh):
ssh instaedit@$VPS_IP \
  'docker compose exec -T postgres psql -U instaedit -d instaedit_login -tA -c "
    SELECT count(*)
      FROM unnest(ARRAY[\"users\",\"tokens\",\"workspaces\",\"posts\",
                        \"post_targets\",\"webhook_deliveries\"]) t(tbl)
     WHERE to_regclass(\"public.\" || t.tbl) IS NULL;"'
# expect: 0
```

The canary-table slice MUST mirror
`internal/database/migrate_check.go::CanaryTables`; update both
together when the slice grows.

### 4.5 Migrations

The Go server applies all `internal/database/migrations/*.sql` at boot
via `db.Migrate` (`go:embed` in the binary). The migration runner does
NOT maintain a tracking table — each `.sql` is idempotent (`CREATE
TABLE IF NOT EXISTS`, `ADD COLUMN IF NOT EXISTS`, etc.). To apply
migrations without starting the HTTP server:

```bash
ssh instaedit@$VPS_IP
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production run --rm migrate
```

Expected: `9 canary tables present` (post-migration), then exit 0.

---

## 5. MinIO

### 5.1 Image and credentials

Image: `minio/minio`. The Compose `minio` service has a static credential
pair baked into the env block (`MINIO_ROOT_USER` + `MINIO_ROOT_PASSWORD`).
The same pair is reused by the Go API as `S3_ACCESS_KEY` +
`S3_SECRET_KEY` — the SigV4 signer (`internal/services/storage.go`) is
endpoint-agnostic, so a local MinIO talks to it the same way Tigris
used to.

Generate the pair once at first-boot:

```bash
S3_ACCESS_KEY=$(openssl rand -base64 24)
S3_SECRET_KEY=$(openssl rand -base64 24)
# Store both in the password manager AND in
#   /srv/instaedit/.env.production (rows 5+6) — rotate the pair together.
```

### 5.2 Bind-mount and stateful directory

Host path: `/srv/instaedit/miniostore` → container data path. The
MinIO image default UID is `1000`:

```bash
mkdir -p /srv/instaedit/miniostore
chown -R 1000:1000 /srv/instaedit/miniostore
```

### 5.3 Admin console (loopback only)

The MinIO admin console is at **`https://127.0.0.1:9001`** — bind-mount
exposes it on the host loopback ONLY, never on the public interface.
The compose network has a separate `mc` init container that provisions
the bucket on first boot using the same root creds.

### 5.4 Default bucket + policy

| Setting                  | Value                                  |
|--------------------------|----------------------------------------|
| Default bucket           | `instaedit-prod-media`                 |
| CORS origin (single)     | `https://app.instaedit.org`            |
| CORS methods             | PUT, GET, HEAD                          |
| CORS expose headers      | `ETag`                                  |
| CORS max-age             | 3600 seconds                            |
| Lifecycle                | `AbortIncompleteMultipartUpload` after 1 day |
| Versioning               | ON                                      |
| TLS bucket policy        | Deny `s3:*` when `aws:SecureTransport=false` |
| Max object size          | 200 MB (bucket policy denies larger; API clamps at `STORAGE_MAX_UPLOAD_BYTES = 200 * 1024 * 1024`) |

Presign URL generation lives in `internal/services/storage.go::presignedURL`.
The front-end sets `Content-Length` to the EXACT user-byte count
before PUT — do not bypass the pre-signed envelope.

---

## 6. backup

### 6.1 Postgres — pg_dump cadence

Backup target: `/srv/instaedit/backups/` on the VPS. Format:
`pg_dump -Fc` (binary). Rotation: keep the latest 4 quarterly drills
on-host; off-host rsync weekly.

```bash
# Step 1 — take a fresh backup on the VPS
TS=$(date -u +%Y%m%dT%H%M%SZ)
ssh instaedit@$VPS_IP \
  "docker compose exec -T postgres pg_dump -U instaedit -d instaedit_login \
     --format=custom --no-owner --no-acl \
     > /srv/instaedit/backups/instaedit-restore-drill-\$TS.dump"
# Expected: ~10-300 MB, exit 0.

# Step 2 — pull the dump back to the operator laptop
scp "instaedit@\$VPS_IP:/srv/instaedit/backups/instaedit-restore-drill-\$TS.dump" \
    ~/drill-cache/

# Step 3 — restore into a throwaway Postgres container identical to prod
docker run -d --name drill-restore-target-\$TS \
  -e POSTGRES_USER=instaedit \
  -e POSTGRES_PASSWORD=instaedit_drill_pw \
  -e POSTGRES_DB=instaedit_login \
  postgres:17-alpine
docker exec -i drill-restore-target-\$TS \
  pg_restore -U instaedit -d instaedit_login --no-owner --no-acl \
             --clean --if-exists < ~/drill-cache/instaedit-restore-drill-\$TS.dump

# Step 4 — schema fingerprint parity (matches STEP 5 of the canonical drill)
# (see docs/OPERATIONS.md §3.1.3 STEP 5 for the MD5 query).

# Step 5 — tear down the throwaway
docker rm -f drill-restore-target-\$TS

# Step 6 — append to ops/restore-drill-cadence.json on the VPS
```

### 6.2 Trigger cadences

| Trigger | Cadence |
|---------|---------|
| First drill on a fresh VPS | within 24 h of first `docker compose up` |
| Baseline | quarterly (every 90 days) |
| On incident | within 48 h of any operational incident (container restart storm, OOM, manual `docker compose down`) |
| Pre-audit | 7 days before any external security review |

### 6.3 Storage backup — Tigris → MinIO (one-time)

If the historical Tigris bucket `instaedit-prod-media` is still in use,
mirror it into MinIO before retiring the Tigris contract:

```bash
# 1. Inventory Tigris (capture the listing before sync):
AWS_ACCESS_KEY_ID=\$TIGRIS_KEY AWS_SECRET_ACCESS_KEY=\$TIGRIS_SECRET \
  aws --endpoint https://t3.storage.dev s3 ls s3://instaedit-prod-media/ --recursive \
  | tee /tmp/objects.txt

# 2. Mirror:
AWS_ACCESS_KEY_ID=\$TIGRIS_KEY AWS_SECRET_ACCESS_KEY=\$TIGRIS_SECRET \
  aws --endpoint https://t3.storage.dev s3 sync \
    s3://instaedit-prod-media/ /tmp/media-mirror/
rsync -a /tmp/media-mirror/ instaedit@\$VPS_IP:/srv/instaedit/miniostore/instaedit-prod-media/

# 3. Re-point the backend (# see /srv/instaedit/.env.production):
#    S3_ENDPOINT=http://minio:9000
#    S3_BUCKET=instaedit-prod-media
#    AWS_REGION=us-east-1   # MinIO ignores but the SigV4 signer wants it

# 4. Restart and re-run §4 probes:
ssh instaedit@\$VPS_IP \
  'cd /opt/instaedit/InstaeditLogin && \
   docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker'

# 5. Once parity is confirmed, retire Tigris (delete bucket, key, account).
```

`S3_ENDPOINT=https://t3.storage.dev` is the documented toggle back to
Tigris for the rollback window. After Tigris is gone, drop the env
var entirely (defaults to MinIO).

### 6.4 Retention policy (on-host + off-host)

- On-host: `/srv/instaedit/backups/instaedit-restore-drill-<UTC>.dump`
  — keep latest 4 quarterly drills, plus the latest weekly snapshot.
- Off-host: weekly rsync to operator-controlled destination (e.g.
  Backblaze B2, AWS Glacier, your laptop, …). Compress with `gzip -6`
  before off-host shipping.
- Media (MinIO bucket): versioning already covers accidental delete.
  Lifecycle policy handles `AbortIncompleteMultipartUpload` after 1 day.

---

## 7. DNS

After the cutover, **all three names resolve to the same VPS IP**:
`51.91.11.36`. Caddy distinguishes them by SNI: apex + `app` serve the
SPA, `api` reverse-proxies to the Go API. The apex → `app.` 301 redirect
lives in `ops/vps/Caddyfile` as a `redir` block (NOT in DNS — apex
cannot use CNAME per RFC).

### 7.1 Canonical records (VPS-side, all A record)

| Host | Type | Value | TTL | Purpose |
|------|------|-------|-----|---------|
| `instaedit.org` (apex) | `A` | `51.91.11.36` | 60 | VPS — Caddy serves 301 → `app.instaedit.org` |
| `app.instaedit.org` | `A` | `51.91.11.36` | 60 | VPS — Caddy serves the SPA + reverse-proxies `/api/*` and `/instaedit-local/*` |
| `api.instaedit.org` | `A` | `51.91.11.36` | 300 | VPS — single SNI host, reverse-proxy to Go API on `127.0.0.1:8080` |
| `instaedit.org` (apex) | `CAA` | `0 issue "letsencrypt.org"` | 3600 | Restrict cert issuance to Let's Encrypt |
| `instaedit.org` (apex) | `CAA` | `0 iodef "mailto:security@instaedit.org"` | 3600 | Incident reporting for unauthorized issuance attempts |

### 7.2 Email deliverability (Resend)

These records are mandatory for `no-reply@instaedit.org` to be a
sender domain. Set them in the registrar dashboard BEFORE inviting
users. Full protocol: [docs/OPERATIONS.md §7](./OPERATIONS.md).

| Host | Type | Value | TTL |
|------|------|-------|-----|
| `instaedit.org` (apex) | `TXT` | `v=spf1 include:_spf.resend.com ~all` | 3600 |
| `<selector>._domainkey.instaedit.org` | `CNAME` | `<selector>.dkim.resend.com.` | 3600 |
| `_dmarc.instaedit.org` | `TXT` | `v=DMARC1; p=none; rua=mailto:security@instaedit.org; ruf=mailto:security@instaedit.org; pct=100` | 3600 |

DMARC starts at **`p=none`** for the 2-4 weeks warm-up window. Ramp
schedule (`p=quarantine` → `p=reject`) is in
[docs/OPERATIONS.md §7.2](./OPERATIONS.md#72-dmarc-progression-schedule).

### 7.3 Cloudflare pitfalls

**Set `instaedit.org`, `app.instaedit.org`, `api.instaedit.org` to
DNS-only ("grey cloud").** The orange-cloud proxy terminates TLS
itself and intercepts Let's Encrypt HTTP-01 challenges — the VPS
will fail to renew its cert after 60 days. Plus:

- DNSSEC at the registrar: Cloudflare is one-click; Namecheap requires
  opt-in via DS records. Required for the CAA records to be honored
  by resolvers.
- TTL rationale: 60 s on the frontend lets near-instant switchover in
  CDN-failure events; 300 s on the backend balances conn-churn vs
  regional rerouting. With a single VPS, regional rerouting is moot
  until a second region is added.

### 7.4 Historical / dead records (REMOVE THESE)

If the registrar zone file still contains any of the following, they
are noise from the prior deployment and should be **removed** as part
of the cutover. None resolve to anything useful post-cutover:

- `_vercel` TXT (or any Vercel-style TXT)
- `cname.vercel-dns.com.` CNAME
- `instaedit-login.fly.dev.` CNAME
- Anything pointing at the historical Fly app hostname

### 7.5 Verification

```bash
dig +short instaedit.org       A    # expect: 51.91.11.36
dig +short app.instaedit.org    A    # expect: 51.91.11.36
dig +short api.instaedit.org    A    # expect: 51.91.11.36

curl -sI https://api.instaedit.org/api/v1/health | grep -i '^server:'
# expect: server: Caddy

echo | openssl s_client -servername api.instaedit.org \
  -connect api.instaedit.org:443 2>/dev/null \
  | openssl x509 -noout -issuer -subject -dates
# expect: issuer: O = Let's Encrypt, CN = R10 or R11
```

Any name resolving to a different IP, or `Server:` carrying anything
other than `Caddy`, is an escalation.

---

## 8. OAuth callback

### 8.1 Canonical callback URLs (production)

Every OAuth round-trip on `instaedit-login` lands on one of these
public hosts. Each redirect URI is registered on the relevant
provider console AND baked into the relevant env var.

| Provider | Canonical callback URL (production) | Source-of-truth |
|----------|------------------------------------|-----------------|
| Instagram | `https://api.instaedit.org/api/v1/auth/instagram/callback` | `INSTAGRAM_REDIRECT_URI` + Meta console |
| Facebook | `https://api.instaedit.org/api/v1/auth/facebook/callback` | `FACEBOOK_REDIRECT_URI` + Meta console |
| Threads | `https://api.instaedit.org/api/v1/auth/threads/callback` | `THREADS_REDIRECT_URI` + Meta console |
| X (Twitter) | `https://api.instaedit.org/api/v1/auth/twitter/callback` | `X_REDIRECT_URI` + X Developer Portal |
| TikTok | `https://api.instaedit.org/api/v1/auth/tiktok/callback` | `TIKTOK_REDIRECT_URI` + TikTok Developer Portal |
| YouTube | `https://api.instaedit.org/api/v1/auth/youtube/callback` | `YOUTUBE_REDIRECT_URI` + Google Cloud Console |
| LinkedIn | `https://api.instaedit.org/api/v1/auth/linkedin/callback` | `LINKEDIN_REDIRECT_URI` + LinkedIn Developer Portal |
| Google Drive | `https://api.instaedit.org/api/v1/auth/google-drive/callback` | `GOOGLE_DRIVE_REDIRECT_URI` + Google Cloud Console |

For the `dev.instaedit.org` dev workflow, substitute the host in each
URL. Local-only path: `http://localhost:8080/api/v1/auth/instagram/callback`
(Meta dev/test mode).

### 8.2 Provider App Review requirements

Each provider has a non-trivial App Review path before production
scopes are granted. Capture the App Review-Required scope in the dev
ticket; production cannot ship without it.

- **Meta** (Instagram/Facebook/Threads): App must be in **Live mode**,
  roles configured (Admin/Developer/Tester), and a **Verified Business**
  (or App Verification) submission for `instagram_business_basic` /
  `pages_show_list` / `pages_manage_posts` etc. Status badge appears at
  https://developers.facebook.com → My Apps → App Review.
- **TikTok**: Login Kit + Content Posting API; scopes `user.info.basic`
  + `video.publish`. App Review at https://developers.tiktok.com →
  My Apps → [App] → Submit for Review. See
  [docs/TIKTOK-APP-REVIEW.md](./TIKTOK-APP-REVIEW.md) for the canonical
  review timeline + verification scripts (`scripts/verify-tiktok-app-review-config.sh`).
- **X (Twitter)**: App Review for `tweet.read` + `tweet.write` +
  `users.read` + `offline.access`. https://developer.twitter.com →
  Developer Portal → Project → App → Permissions. Capture Client
  Secret at creation — show-once.
- **YouTube**: OAuth consent screen verification + Data API v3
  `youtube.upload` scope. https://console.cloud.google.com → APIs &
  Services → Credentials → OAuth 2.0 Client IDs.
- **LinkedIn**: Product approval for `r_liteprofile` + `r_emailaddress`
  (+ `w_member_social` if posting). https://www.linkedin.com/developers
  → My Apps → [App] → Products.
- **Google Drive**: OAuth consent screen + `drive.readonly` or
  `drive.file` (depending on workflow). Often combined with YouTube
  Cloud Console credentials under the same OAuth client.

### 8.3 Callback registration (canonical pattern)

Every provider console accepts BOTH dev and prod URLs in their
"Valid OAuth Redirect URIs" / "Callback URIs" list. Register:

1. `https://dev.instaedit.org/api/v1/auth/<provider>/callback` (dev)
2. `https://api.instaedit.org/api/v1/auth/<provider>/callback` (prod)

Plus (Meta only) `http://localhost:8080/api/v1/auth/instagram/callback`
for Tester-only local-flow verification.

The Go API fails-fast at startup if `*_CLIENT_ID` is set without
`*_CLIENT_SECRET` (or vice versa) — see
`internal/config/config.go` (`Blocco #2.X`) for the per-provider
validator list.

---

## 9. creazione utenti

### 9.1 Three intake paths

There are three ways to bring a user into the system, in decreasing
order of operator involvement:

1. **Admin CLI (`cmd/create-user/main.go`)** — operator-only. Idempotent,
   creates the user record + workspace + API keys directly via the
   `internal/database` layer. Used by the very first admin onboard.
2. **Self-registration via `POST /api/v1/auth/register`** — end-user
   sign-up. Email + password. Returns a JWT in the response body
   (NOT yet via email magic-link on prod — see §9.4).
3. **OAuth-driven create-on-first-login** — user clicks "Login with Meta"
   / X / TikTok / etc. on `app.instaedit.org`. If no local user exists
   for the OAuth subject, the OAuth handler creates a new user +
   workspace + accounts (linked platform) record. The JWT from this
   path is also returned in the response body (dev) — production should
   move to Set-Cookie (cross-subdomain) once `internal/services/email_sender.go`
   lands (see Resend wiring below).

### 9.2 The admin CLI (`cmd/create-user/main.go`)

Built + run from the operator laptop or a one-off VPS shell:

```bash
# Build the CLI
go build -o /tmp/create-user ./cmd/create-user/main.go

# Run it — reads creds from the .env file passed via --env-file flag
# (writes the audit row + the new user's workspace_id + api_key to stdout)
/tmp/create-user --env-file /srv/instaedit/.env.production \
  --email <admin@instaedit.org> --role admin --workspace-label "Admin"
# Expected output: created user_id=:N workspace_id=:M api_key=instaedit_...
# The api_key is the ALL-POWERFUL-scoped admin key — capture in password
# manager immediately, never in chat or commits.
```

The CLI:
- Connects to the Compose `postgres` service via the URL passed in.
- Inserts `users` row + `workspaces` row + `api_keys` row + `audit_log` row
  in one transaction.
- Asserts `email` is unique across the `users` table (re-run safe).
- Asserts `role` is in the allowed admin role set (`admin` or
  `superadmin`); anything else exits 1.

### 9.3 Self-register + login flow (dev + staging)

```bash
# Register (no email confirmation yet — backend does not yet wire Resend):
curl -X POST https://api.instaedit.org/api/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email":"<user>","password":"<strong>","workspace_label":"Default"}'
# → {"user_id":N,"workspace_id":M,"jwt":"<plaintext JWT>","magic_link_token":"<dev-only>"}
# In dev the body carries the JWT (and a magic-link token for parity with
# the email path). Production rotates to Set-Cookie + cross-subdomain CSRF
# once email_sender.go is wired.

# Login:
curl -X POST https://api.instaedit.org/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"<user>","password":"<strong>"}'
```

### 9.4 Magic-link + password reset (Resend wiring — DEFERRED)

The backend does NOT yet wire Resend:

- `pkg/api/magic_link.go::handleMagicLinkStart` returns the plaintext
  token in the response body (`// dev-only; production drops via Mailgun/SES`)
- `pkg/api/auth_email.go::handleForgotPassword` has a `// TODO(FASE 2.2):
  Send reset token via email` marker.
- `internal/config/config.go` has no `EmailProvider*` fields as of
  post-`58742bf` (Resend unification).

`EMAIL_PROVIDER_KEY` lives ONLY in the password manager
(`instaedit-login/email/EMAIL_PROVIDER_KEY`, scope = `Sending Access` ONLY)
until `internal/services/email_sender.go` lands. Do NOT push it to
`/srv/instaedit/.env.production` yet.

### 9.5 JWT lifetime + secret rotation

JWTs are HS256, signed with `JWT_SECRET` (≥ 32 bytes per
`internal/config/config.go::jwtSecretMinBytes=32`). TTL is 168 hours (7
days) — see the `Router configured jwt_ttl_hours=168` startup log line
in `cmd/api/main.go`.

Rotating `JWT_SECRET` invalidates ALL in-flight sessions — every user
gets a one-shot 401 and re-login prompt. For zero-downtime rotation,
add a JWT key ring (out of scope for the beta). The current rotation
recipe is in §6 backup's "Provider / Mail / S3 rotation" pattern;
see [docs/OPERATIONS.md §6](./OPERATIONS.md) for the full recipe.

---

## 10. deploy e rollback

### 10.1 Canonical deploy block (paste-ready, fresh VPS)

```bash
# ── §1 VPS — one-time host setup (skip on re-deploys) ────────────
ssh root@$VPS_IP
adduser instaedit && usermod -aG docker instaedit
mkdir -p /srv/instaedit/{pgdata,miniostore,caddy_data,caddy_config,web/dist,backups,ops}
chown -R instaedit:instaedit /srv/instaedit
# (Install docker-ce + plugin as in §1.2 step 3.)
exit

# ── §1.3 — install compose + Caddyfile on the VPS ───────────────
scp ops/vps/Caddyfile instaedit@$VPS_IP:/srv/instaedit/Caddyfile
scp docker-compose.yml instaedit@$VPS_IP:/srv/instaedit/docker-compose.yml
ssh instaedit@$VPS_IP 'chmod 0644 /srv/instaedit/Caddyfile /srv/instaedit/docker-compose.yml'

# ── ENV — write .env.production on the VPS ──────────────────────
# Local: fill the 26 secrets (see §8 cross-ref) + DATABASE_URL.
scp .env.production instaedit@$VPS_IP:/srv/instaedit/.env.production
ssh instaedit@$VPS_IP 'chmod 600 /srv/instaedit/.env.production'

# ── §2.2 — first boot ──────────────────────────────────────────
ssh instaedit@$VPS_IP
cd /opt/instaedit/InstaeditLogin
docker compose --env-file /srv/instaedit/.env.production -f docker-compose.yml up -d --build
docker compose --env-file /srv/instaedit/.env.production run --rm migrate

# ── §10.4 — gates A/B/C ────────────────────────────────────────
curl -fsS https://api.instaedit.org/api/v1/health | jq   # Gate A (healthz)
curl -fsS https://api.instaedit.org/ready | jq           # Gate B (readiness)
docker compose ps                                        # Gate C (containers)
```

### 10.2 Re-deploy (image update only)

```bash
ssh instaedit@$VPS_IP
cd /opt/instaedit/InstaeditLogin
git pull --ff-only                                            # stage new image
docker compose --env-file /srv/instaedit/.env.production up -d --build
docker compose --env-file /srv/instaedit/.env.production run --rm migrate
docker compose --env-file /srv/instaedit/.env.production up -d --force-recreate api worker
# Then re-run §10.4 gates from the operator laptop.
```

### 10.3 Rollback playbook

| Surface                 | Forward action                       | Rollback action                          |
|-------------------------|--------------------------------------|------------------------------------------|
| Code regression         | `git pull --ff-only` + `docker compose up -d --build` | `git revert <SHA>; git pull --ff-only; docker compose up -d --build` |
| Migration introduces bug | (the migration runner is forwards-only) | `pg_dump --schema-only` to capture pre-deployed schema; restore from `/srv/instaedit/backups/instaedit-restore-drill-<UTC>.dump` to a throwaway and re-apply the *old* schema; flip DNS to the throwaway if you can't roll back the live DB. (Best practice: do NOT roll forward migrations on prod without a tested backup.) |
| `S3_ENDPOINT` flip (storage cutover) | `S3_ENDPOINT=http://minio:9000`     | `S3_ENDPOINT=https://t3.storage.dev` (re-point to the prior hosting) |
| `ACTIVE_ENCRYPTION_KEY_ID` regression | bump id to the new key                | bump id BACK to the old key + restart; existing tokens still decrypt under both |
| Caddy config error      | `caddy validate` BEFORE reload       | `git restore ops/vps/Caddyfile` + reload |
| Single container crash  | `docker compose up -d --force-recreate <service>` | less than ~30 s of downtime |

### 10.4 Gates A/B/C (live verification)

```bash
# Gate A — healthz (HTTP api process responding):
curl -fsS https://api.instaedit.org/api/v1/health | jq
#   → {"platforms":["instagram","facebook","threads"],"service":"InstaEditLogin","status":"ok","version":"..."}

# Gate B — readiness (DB + migrations + worker goroutines):
curl -fsS https://api.instaedit.org/ready | jq
#   → warm:   {"status":"ok","db":"ok","migrations":"ok","workers_ready":true}
#   → cold-start: 503 + workers_pending list (drive_batch_crawler, metrics, publish, …)

# Gate C — VPS container state:
ssh instaedit@$VPS_IP \
  'cd /opt/instaedit/InstaeditLogin && \
   docker compose --env-file /srv/instaedit/.env.production ps'
#   → api          running   127.0.0.1:8080->8080/tcp
#   → worker       running
#   → postgres     running   127.0.0.1:5432->5432/tcp
#   → minio        running   127.0.0.1:9000-9001->9000-9001/tcp
#   → caddy        running   0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp
```

### 10.5 Post-deploy smoke + workspace isolation

```bash
# Comprehensive end-to-end smoke (Phase 9 sub-phases 1-5 + 7):
./scripts/ops/post_deploy_smoke.sh                              # read-only
APPLY_PUBLISH=1 ./scripts/ops/post_deploy_smoke.sh              # also creates a real draft post

# Workspace isolation test (Phase 9 sub-phase 6 — verifies user A
# cannot reach user B's data across /accounts + /posts/workspace/{wid}):
./scripts/ops/workspace_isolation_test.sh --dry-run                              # preview
DATABASE_URL=postgres://instaedit:<pw>@127.0.0.1:5432/instaedit_login?sslmode=disable \
  ./scripts/ops/workspace_isolation_test.sh                                       # apply + cleanup
```

Pass criteria: PASS > 0 AND FAIL = 0; WARNs are advisory.

### 10.6 Failure-mode quick reference (VPS-specific)

- `docker compose up` fails on the postgres volume →
  `chown -R 999:999 /srv/instaedit/pgdata`.
- Caddy cannot obtain the LE cert →
  confirm `dig +short instaedit.org` returns `51.91.11.36` BEFORE the
  first `up`; Cloudflare must be DNS-only.
- `/api/v1/health` 502 from Caddy →
  `docker compose logs api` for Go startup; restart with
  `docker compose up -d --force-recreate api`.
- Worker health-check timeout →
  worker binds `WORKER_HEALTH_PORT` (9090 default per
  `cmd/worker/health_listener.go`); 0 disables the listener.
- MinIO unavailable →
  `docker compose logs minio`; admin port `127.0.0.1:9001` only;
  the Go SigV4 signer retries 3× on transient errors.
- Image build failure → `docker build --target api .` (or the relevant
  target) on the VPS to isolate.

### 10.7 Rotation recipes

- `JWT_SECRET` rotation: edit on VPS, then
  `docker compose up -d --force-recreate api worker` (in-flight JWTs
  invalidate, all users get 401, brief re-login window).
- `ENCRYPTION_KEYS` rotation (zero-downtime): add the new id to the
  CSV, bump `ACTIVE_ENCRYPTION_KEY_ID` on the next deploy, watch
  `instaedit_vault_cipher_id` metric converge, then drop the old id.
- Provider / Mail / S3 rotation: editor change on the VPS, then
  `docker compose up -d --force-recreate <service>`.
- MinIO credential rotation: regenerate the pair via the admin
  console, update rows 5 + 6 of `.env.production` AND the
  `MINIO_ROOT_*` env block in the Compose override; restart
  `minio` then `api` + `worker` in that order.

### 10.8 Open items (tracked, not blockers)

- `verify-log-redaction` currently reads from a non-VPS log source;
  scoped to a follow-up re-pointing at `docker compose logs`.
- Sandbox-only handlers (e.g. `cmd/sandbox-probe`) are documented in
  `cmd/<probe>/main.go` and wired by `pkg/api/<probe>.go`. Re-confirm
  their handler contract on each deploy if you depend on them.
- The Fly-era gate probes (`docs/DEPLOY-AUDIT.md`) are now historical
  and should be archived separately.

---

## Cross-references

- **Live probe log:** [docs/VPS-DEPLOY-STATUS.md](./VPS-DEPLOY-STATUS.md)
  (DNS resolution, HTTP identity probes, /ready envelope, ready/warm
  log table).
- **DNS runbook + cert renewal + DMARC ramp + Gmail inbox test:**
  [docs/OPERATIONS.md §1 + §7](./OPERATIONS.md).
- **OAuth provider configuration for each platform (App Review,
  scopes, callback URIs):** [docs/OAUTH-PRODUCTION.md](./OAUTH-PRODUCTION.md)
  + [docs/TIKTOK-APP-REVIEW.md](./TIKTOK-APP-REVIEW.md).
- **Capability / provider matrix:** [docs/PROVIDER_MATRIX.md](./PROVIDER_MATRIX.md).
- **API surface / endpoints:** [docs/ENDPOINTS.md](./ENDPOINTS.md).
- **OpenAPI spec:** [docs/OPENAPI.md](./OPENAPI.md) + [api/openapi.yaml](../api/openapi.yaml).
- **Architecture overview:** [docs/ARCHITECTURE.md](./ARCHITECTURE.md).
- **Local dev / Linux onboarding (the earlier `HANDOFF-LINUX.md`):**
  [HANDOFF-LINUX.md](../HANDOFF-LINUX.md).
- **Provision-Postgres runbook:** [scripts/db/provision-postgres-runbook.sh](../scripts/db/provision-postgres-runbook.sh).
- **Live-log redaction verifier:** [scripts/obs/verify-log-redaction.sh](../scripts/obs/verify-log-redaction.sh).
