# `cmd/server` Removal Audit

**Audit date:** 2026-08-07
**Status:** **COMPLETED — the legacy wrapper and Compose profile were removed.**

## Final topology

The supported runtime has three single-purpose entrypoints:

- `cmd/migrate` — one-shot database migration and installation-identity check;
- `cmd/api` — HTTP API and readiness endpoints;
- `cmd/worker` — supervised background workers.

The backend runs on the VPS with Docker Compose. Vercel serves the frontend;
PostgreSQL and MinIO remain private to the VPS and Compose network.

## Removed surfaces

The coordinated removal deleted or updated all compatibility surfaces:

- `cmd/server/main.go`;
- Dockerfile compilation and final stage for `/out/server`;
- the `server` service and `legacy` profile from `docker-compose.yml`;
- the local Compose `server` override;
- Makefile targets `run-server` and `run-server-api-only`;
- the `DBPoolRoleServer`, `DBServer`, and `DB_SERVER_*` configuration profile;
- topology-guard assertions that required the legacy wrapper;
- obsolete README, architecture, binary-topology, and recovery references.

## Safety checks

The topology guard now fails if any legacy server surface is reintroduced and
passes only when the canonical `api`, `worker`, and `migrate` entrypoints are
present. The local Compose graph contains only the canonical services plus
PostgreSQL and MinIO infrastructure; no legacy profile is available.

Validation for this removal must include:

```bash
./scripts/verify-entrypoint-topology.sh
docker compose --env-file .env.dev \
  -f docker-compose.yml \
  -f docker-compose.local.yml config
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

The frontend validation remains:

```bash
npm --prefix web test
npm --prefix web run build
```

## Operational migration

Operators must use the split topology for all local, recovery, and production
workflows:

```bash
make dev
make run-migrate
make run-api
make run-worker
```

A historical single-process deployment or recovery command must not be
reintroduced. If an operational procedure requires a new recovery mode, add a
new dedicated command with an explicit owner and validation contract rather
than restoring `cmd/server` or a Compose legacy profile.
