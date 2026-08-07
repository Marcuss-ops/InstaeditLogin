# InstaEditLogin — Binary Topology

This document describes the supported process split for InstaEditLogin. The
backend runs on a VPS with Docker Compose; Vercel hosts the frontend only.
PostgreSQL and MinIO remain private to the VPS and Compose network, while
Caddy on the host is the public backend entry point.

The canonical runtime uses three single-purpose binaries. They share the
wiring layer in `internal/bootstrap.Wire`, but each process starts only the
components it owns:

| Binary | Source | Responsibility |
| --- | --- | --- |
| `cmd/migrate` | [`cmd/migrate/main.go`](../cmd/migrate/main.go) | One-shot database migration job; exits successfully only after migrations complete. |
| `cmd/api` | [`cmd/api/main.go`](../cmd/api/main.go) | HTTP API and readiness endpoints; no background worker runtime. |
| `cmd/worker` | [`cmd/worker/main.go`](../cmd/worker/main.go) | Background publishing, reconciliation, outbox, webhook, metrics, cleanup, and related worker loops; no public HTTP listener. |

There is no single-process compatibility wrapper. All local, recovery, and
production workflows use the same split entrypoints so migrations, HTTP, and
background workers have independent lifecycle and scaling boundaries.

## Canonical production topology

```text
                         Internet
                             |
              +--------------+--------------+
              |                             |
       app.instaedit.org             api.instaedit.org
              |                             |
           Vercel                    VPS :80/:443
       React/Vite frontend                |
                                  host-managed Caddy
                                          |
                                  127.0.0.1:8080
                                          |
                              Docker Compose application
                                          |
       +------------+-----------+---------+-----------+
       |            |           |                     |
   PostgreSQL    migrate      api                  worker
    private      one-shot   HTTP only          background loops
                                          |
                                         MinIO
                                  private S3-compatible store
```

Deployment responsibilities are intentionally separated:

1. `migrate` waits for a healthy PostgreSQL service, applies migrations, and
   exits. Compose releases `api` and `worker` only after it succeeds.
2. `api` serves HTTP through Caddy. Its container binding remains private to
   the VPS host.
3. `worker` processes asynchronous jobs independently from the API process.
4. MinIO stores media and artifacts through the internal Compose network; its
   API and console are not public endpoints.
5. Vercel builds and serves `web/`; it does not run backend binaries or access
   PostgreSQL/MinIO directly.

The authoritative deployment procedure is [`docs/DEPLOY.md`](DEPLOY.md).

## Local development topology

The default local workflow uses the same split as production:

```text
PostgreSQL + MinIO
        |
     migrate  -- completes successfully
       /  \
     api  worker
```

Run the complete local stack with:

```bash
make dev
```

The default Compose graph starts `db`, `minio`, `minio-init`, `migrate`, `api`,
and `worker`. `minio-init` creates the application bucket idempotently before
storage-backed API requests are served. The individual processes can also be
run against a configured environment after the migration step:

```bash
make run-migrate
make run-api
make run-worker
```

The split topology is the only supported local and recovery workflow:

```bash
make run-migrate
make run-api
make run-worker
```

## Dockerfile targets

[`Dockerfile`](../Dockerfile) uses one Go builder stage and separate final
stages:

| Target | Binary | Use |
| --- | --- | --- |
| `api` | `/app/api` | Canonical HTTP service. |
| `worker` | `/app/worker` | Canonical background-worker service. |
| `migrate` | `/app/migrate` | Canonical one-shot migration service. |

Build the canonical images with:

```bash
docker build --target migrate -t instaedit-migrate .
docker build --target api     -t instaedit-api .
docker build --target worker  -t instaedit-worker .
```

The complete service graph and migration dependency are defined in
[`docker-compose.yml`](../docker-compose.yml) and hardened for the VPS by
[`docker-compose.production.yml`](../docker-compose.production.yml).

## Startup and shutdown contracts

All three canonical binaries call `bootstrap.Wire` first. `Wire` loads and
validates configuration, connects to PostgreSQL, builds repositories and
providers, and prepares the API handler. It does not apply migrations or start
long-running workers by itself. The supported bootstrap boundary is this
`Wire` entrypoint; the former `Core`/`WireCore`/`WireAPI`/`WireWorkers` split
was removed after repository-wide call-site verification and is not part of
the supported internal API.

### `cmd/migrate`

```text
DB_POOL_ROLE=maintenance
bootstrap.Wire
  -> database.Migrate(app.DB)
  -> installation-identity verification
  -> exit 0 on success, exit 1 on failure
```

### `cmd/api`

```text
DB_POOL_ROLE=api
bootstrap.Wire
  -> HTTP listener on PORT (default 8080)
  -> graceful HTTP and metrics shutdown on SIGTERM
```

### `cmd/worker`

```text
bootstrap.Wire
  -> WORKER_HEALTH_PORT listener (disabled by default in local dev)
  -> worker registry metrics setup
  -> app.RunWorkers(ctx)
  -> context cancellation and graceful worker drain on SIGTERM
```

The worker health listener exposes `/health` and `/ready` when
`WORKER_HEALTH_PORT` is a valid port. Readiness fails when the registry is
empty or a critical worker is not healthy; non-critical maintenance-worker
failures remain visible without taking readiness down. A critical worker error
is returned by `RunWorkers` so the process exits non-zero for orchestrator
restart.

## Environment and storage parity

`cmd/api`, `cmd/worker`, and `cmd/migrate` consume the same application
configuration surface. Required production values include the database
connection, JWT and encryption settings, OAuth configuration as enabled, CORS
and frontend URLs, and the S3-compatible storage settings:

- `S3_ENDPOINT` points to the internal MinIO service (`http://minio:9000`) from
  Compose containers;
- `S3_BUCKET`, `S3_ACCESS_KEY`, and `S3_SECRET_KEY` identify the application
  bucket and credentials;
- `MINIO_ROOT_USER` and `MINIO_ROOT_PASSWORD` are used by MinIO and the
  idempotent `minio-init` sidecar;
- MinIO credentials never belong in `VITE_*` variables or the browser bundle.

The exact production secret contract is maintained in the
[`deployment runbook`](DEPLOY.md#3-production-secrets-and-environment).

## Verification commands

Use these checks when changing entrypoint wiring or deployment topology:

```bash
make verify-entrypoint-topology
make backend-test

go vet ./...
go build ./...
```

The topology check confirms that `migrate`, `api`, and `worker` are the only
supported targets and that no legacy single-process surface has returned.

## Entrypoint removal status

The legacy single-process wrapper, Compose profile, Docker target, Makefile
compatibility targets, and server-specific database pool profile have been
removed. The completed audit is recorded in
[`CMD-SERVER-REMOVAL-AUDIT.md`](CMD-SERVER-REMOVAL-AUDIT.md).

## See also

- [`docs/DEPLOY.md`](DEPLOY.md) — canonical Docker Compose, MinIO, VPS, and
  Vercel deployment runbook.
- [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) — application and worker
  architecture.
- [`docker-compose.yml`](../docker-compose.yml) — canonical service graph.
- [`docker-compose.production.yml`](../docker-compose.production.yml) — VPS
  hardening overlay.
- [`Dockerfile`](../Dockerfile) — binary build targets.
- [`Makefile`](../Makefile) — local development and verification commands.
- [`verify-entrypoint-topology.sh`](../scripts/verify-entrypoint-topology.sh)
  — canonical entrypoint regression check.
