# InstaEditLogin — Binary Topology (Blocco #2.1)

This document describes the post-Blocco #2.1 binary split.
cmd/server/main.go (the pre-Blocco #2.1 monolith) is broken into four
single-purpose binaries, each consuming the same shared wiring layer
(`internal/bootstrap.Wire`) but starting only the components it needs.

## Binaries

| Binary | Source | Purpose |
| --- | --- | --- |
| `cmd/api`     | `cmd/api/main.go`     | HTTP server only. NO workers. Listens on $PORT (default 8080). |
| `cmd/worker`  | `cmd/worker/main.go`  | 5 background goroutines only. NO HTTP. (publish, reconcile, outbox dispatcher, webhook, metrics collector) |
| `cmd/migrate` | `cmd/migrate/main.go` | One-shot pre-deploy job. Connect + apply migrations + exit 0. NO HTTP. NO workers. |
| `cmd/server`  | `cmd/server/main.go`  | **Legacy / dev wrapper.** Runs `cmd/api` + (optionally) `cmd/worker` + `cmd/migrate` all in ONE process. Survives for local-dev convenience and Railway single-process deploy compatibility. |

## Topologies

### Production (recommended)

```
                ┌─────────────┐
                │ cmd/migrate │ ← one-shot Job (k8s / Railway pre-deploy)
                │ exit 0/1    │
                └──────┬──────┘
                       │ (success: schema up-to-date)
       ┌───────────────┴────────────────┐
       ▼                                ▼
┌─────────────┐                  ┌─────────────┐
│  cmd/api     │ ×N replicas     │ cmd/worker   │ ×M replicas
│  HTTP only   │ (HPA on         │ 5 goroutines │ (independent
│  port 8080   │  RPS/Latency)   │              │  cadence)
└─────────────┘                  └─────────────┘
```

- Migration runs as a one-shot job. Production deploy pipelines MUST
  block the rollout on its success exit code.
- `cmd/api` and `cmd/worker` run in separate pods. Each auto-scales
  independently — `cmd/api` on request rate, `cmd/worker` on
  pending publish/backlog. No process coupling.
- `cmd/api` and `cmd/worker` share the SAME environment configuration
  (databases, secrets, OAuth client credentials). The split is process-
  level only; the configuration surface is identical.

### Local Dev

```
                ┌─────────────┐
                │ cmd/migrate │ ← invoked via Dockerfile `migrate` target
                │ exit 0      │   in docker-compose.yml (Blocco #2.1 default)
                └──────┬──────┘
                       │
       ┌───────────────┴────────────────┐
       ▼                                ▼
┌─────────────┐                  ┌─────────────┐
│  cmd/api     │   http :8080    │ cmd/worker   │ 5 goroutines
│  HTTP only   │                  │              │
└─────────────┘                  └─────────────┘
```

`docker-compose.yml` (Blocco #2.1 default) models this with 4 services:
`db` + `migrate` + `api` + `worker`. The legacy `server` profile
(`docker compose --profile legacy up`) keeps the old single-process
shape for users who want it.

### Legacy Single-Bundle (`cmd/server` wrapper)

```
                ┌─────────────────────────┐
                │      cmd/server          │
                │ ┌───────────────────┐  │
                │ │ HTTP server (:8080) │  │
                │ └───────────────────┘  │
                │ ┌───────────────────┐  │
                │ │ 5 workers          │  │ ← only if RUN_WORKERS=true
                │ │ (publish, etc.)    │  │   (default)
                │ └───────────────────┘  │
                │ ┌───────────────────┐  │
                │ │ database.Migrate   │  │ ← dev-only; runs once
                │ │                    │  │   before serve
                │ └───────────────────┘  │
                └─────────────────────────┘
```

`cmd/server` is the wrapper around `cmd/api` and (optionally) `cmd/worker`,
with `database.Migrate` baked into the same process. Three shutdown paths
must be drained in parallel on SIGTERM:

1. HTTP server (`srv.Shutdown`, 30s drain budget).
2. 5 worker goroutines (`app.RunWorkers`, 15s drain budget per leaf).
3. DB connection (`defer app.DB.Close()` on graceful exit).

The wrapper serves as a backward-compat path for users on Railway / Render
single-process deploys who haven't migrated to the separate-pod topology
yet. New deploys SHOULD use `cmd/api` + `cmd/worker` + `cmd/migrate` so
per-service scaling works correctly.

## Dockerfile Targets

The `Dockerfile` defines 4 final stages, one per binary. Each stage copies
only its binary from the shared builder stage (multi-stage build, single
builder compile run).

```dockerfile
FROM golang:1.23-alpine AS builder
RUN go build -o /out/api     ./cmd/api
RUN go build -o /out/worker  ./cmd/worker
RUN go build -o /out/migrate ./cmd/migrate
RUN go build -o /out/server  ./cmd/server

FROM alpine:3.21 AS base     # ca-certificates + non-root appuser
FROM base AS api             # COPY --from=builder /out/api + EXPOSE 8080
FROM base AS worker          # COPY --from=builder /out/worker (no port)
FROM base AS migrate         # COPY --from=builder /out/migrate (one-shot)
FROM base AS server          # COPY --from=builder /out/server + EXPOSE 8080
```

Build per target:

```
docker build --target api     -t instaedit-api      .
docker build --target worker  -t instaedit-worker   .
docker build --target migrate -t instaedit-migrate  .
docker build --target server  -t instaedit-server   .  # dev / backward-compat
```

Default target (no `--target` flag): `api`.

Each stage is < 20 MB compressed (alpine + ~12 MB Go binary + ca-certs).
Multi-stage build keeps the final image free of the Go toolchain.

## Makefile Targets

The `Makefile` adds `run-api` / `run-worker` / `run-migrate` / `run-server`
targets. Each runs the corresponding binary directly via `go run`.

```
make run-migrate             # one-shot: connect + apply migrations + exit
make run-api                 # HTTP server only (no workers)
make run-worker              # 5 background goroutines only
make run-server              # legacy wrapper (RUN_WORKERS=true)
make run-server-api-only     # legacy wrapper (RUN_WORKERS=false)

make dev                     # docker compose up --build (3-service topology)
make backend-test            # go test -race ./...
make test-integration        # testcontainers-backed integration tests
```

`make run-api` against a remote database (e.g. staging) is the canonical
way to debug HTTP-only behavior without the worker-noise of staging
itself. `make run-migrate` against staging before deploying a schema
change is the canonical pre-deploy ritual.

## Runtime Ordering (within a single binary)

`internal/bootstrap.Wire(ctx)` runs in a fixed order:

1. **`config.Load`** — env-based config; fails fast on schema mismatches.
2. **S3 storage check** — `cfg.S3Endpoint / Bucket / AccessKey / SecretKey`
   must all be set; bail with a descriptive error otherwise.
3. **logger setup** — `slog.SetDefault(logger)`.
4. **`database.Connect`** — opens the pooled connection.
5. **`crypto.NewEncryptor`** — AES-256-GCM with key envelope (kek id 1
   initially; supports rotation via the key_version column).
6. **Repository construction** — `userRepo / tokenRepo / teamRepo /
   workspaceRepo / apiKeyRepo / idempotencyRepo / webhookRepo /
   sessionRepo`.
7. **Service construction** — `vault / authMgr / rateLimitSvc /
   sessionsSvc / authEmailSvc` (the auth email svc is an adapter over
   `*services.AuthService`).
8. **Provider registry** — `providers.BuildRegistry(cfg)` returns the
   per-platform capability router.
9. **Router setup** — `api.NewRouter(...)` + 13 `RouterOption`s wiring
   every middleware / store. Idle: `router.Setup()` produces the
   `http.Handler` exposed on `App.HTTPHandler`.
10. **`metrics.InitWorkerID`** — process-local worker_id singleton, set
    BEFORE any goroutine comes up so log lines from each worker tick
    carry the canonical id.

`Wire` does NOT run migrations and does NOT spawn any goroutine. Each
binary decides what to run after Wire returns.

### Migrate (cmd/migrate)

```
bootstrap.Wire (steps 1–10)
  → database.Migrate(app.DB)
  → exit 0  (or exit 1 on migration failure)
```

### API (cmd/api)

```
bootstrap.Wire (steps 1–10)
  → http.Server.ListenAndServe (1 goroutine, default port 8080)
  → on SIGTERM: srv.Shutdown(30s) → exit
```

### Worker (cmd/worker)

```
bootstrap.Wire (steps 1–10)
  → app.RunWorkers (5 goroutines, parallel ctx-managed drains)
  → on SIGTERM: cancel ctx → 5× (run_drain_or_15s_timeout)
```

### Server wrapper (cmd/server)

```
bootstrap.Wire (steps 1–10)
  → database.Migrate (dev-only assumption: exclusive DB access)
  → if RUN_WORKERS=true: app.RunWorkers (5 goroutines)
  → http.Server.ListenAndServe (1 goroutine)
  → on SIGTERM:
      - workersCancel() (triggers 15s drain per leaf)
      - srv.Shutdown(30s)
      - wg.Wait (workers + http both drained)
      - exit
```

## Environment Variable Parity

`cmd/api`, `cmd/worker`, `cmd/migrate`, and `cmd/server` (wrapper) all
read from the SAME `.env` surface. The split is process-level only —
no new env vars were introduced, no existing env vars moved.

Variables that MUST be present (config validation rejects otherwise):

- `DATABASE_URL` / Postgres DSN components (user, password, host, port, db)
- `JWT_SECRET` — shared between api (signs access tokens) and worker (validates session-row ownership via the SessionsService.Start path)
- `ENCRYPTION_KEY` — wraps OAuth refresh tokens at rest in the vaults table
- `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY` — media presign + asset reads
- `FRONTEND_URL` — OAuth callback redirect target
- `CORS_ALLOWED_ORIGINS` — cross-origin SPA allowlist

Variables that ONLY `cmd/worker` reads (no-op if absent on api):

- `PUBLISH_WORKER_INTERVAL_SECONDS` (default 30)
- `RECONCILE_WORKER_INTERVAL_SECONDS` (default 5)
- `WEBHOOK_WORKER_INTERVAL_SECONDS` (default 5)

Variables that ONLY the `cmd/server` wrapper reads:

- `RUN_WORKERS` — default true; false disables the 5 background goroutines

## Migration Lifecycle

Production deploy pattern:

1. **`cmd/migrate` runs as a one-shot job** (k8s `Job`, Railway pre-deploy
   hook, helm `pre-install` hook). Blocks the rollout on its success exit
   code (`exit 0` → safe to proceed, `exit 1` → abort).
2. **`cmd/api` Pods roll out** — auto-scaling group ready to serve on
   `:8080`. The new HTTP server starts against the migrated schema.
3. **`cmd/worker` Pods roll out** — independent auto-scaling group
   (typically 1–2 replicas; not request-driven).
4. **(optional) Old replicas drain** — `kubectl rollout` finishes old pods
   gracefully; production drain budget is 75s (matches the pre-Blocco #2.1
   staggered worker + HTTP shutdown).

Local dev lifecycle (docker-compose):

1. `db` container starts; healthy on pg_isready.
2. `migrate` container starts, runs migrations, exits.
3. `api` + `worker` containers start in parallel after `migrate` succeeds.
4. SIGTERM drains both with their respective budgets.

Risks:

- **Race on migration**: deploy pipelines MUST block `cmd/api` rollout on
  `cmd/migrate` exiting 0. `service_completed_successfully` in
  docker-compose.yml enforces this locally; the same rule applies to k8s
  via `Job` ordering and to Railway via pre-deploy hook sequencing.
- **Schema drift in dev**: if a developer hot-reloads `cmd/server` against
  a partly-migrated DB, the bootstrap path can fail at Wire time. Always
  run `make run-migrate` first when iterating on schema migrations locally.

## See Also

- `docs/ARCHITECTURE.md` — the high-level architecture incl. async
  publishing pipeline, transactional outbox, and security model.
- `Makefile` — concrete commands for local iteration.
- `Dockerfile` — multi-target build shapes for each binary.
- `docker-compose.yml` — the 4-service local-dev topology.
