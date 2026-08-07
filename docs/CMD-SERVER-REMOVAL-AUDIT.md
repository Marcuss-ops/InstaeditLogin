# `cmd/server` Removal Audit

**Audit date:** 2026-08-01
**Verified commit:** `5d328350c2c737b52869cdc4e4e5d399c24584e4`
**Scope:** `cmd/server/main.go` versus the canonical `cmd/api`, `cmd/worker`, and `cmd/migrate` entrypoints.
**Status:** **BLOCKED — retain the wrapper for development/recovery compatibility.**

## Findings

### Production

No supported production path invokes the legacy wrapper.

- `docker-compose.yml` production-shaped services build `api`, `worker`, and
  `migrate` targets.
- `docker-compose.production.yml` does not add a `server` service or target.
- `docs/DEPLOY.md` documents the VPS topology as `migrate` → `api` + `worker`.
- `.github/workflows/deploy.yml` deploys the frontend only; it does not build
  or start `cmd/server`.
- `.github/workflows/integration-fast.yml` builds and tests the repository but
  contains no legacy server invocation.
- `scripts/verify-entrypoint-topology.sh` rejects legacy references in the
  production overlay, deployment documentation, and CI workflows.

**Conclusion:** `cmd/server` is not a production entrypoint and must not be
introduced into production Compose, deployment workflows, or operator runbooks.

### Development and recovery

The wrapper still has intentional, live uses:

- `make run-server` runs `RUN_WORKERS=true go run ./cmd/server`.
- `make run-server-api-only` runs `RUN_WORKERS=false go run ./cmd/server`.
- `docker compose --profile legacy up` selects the `server` service and its
  Dockerfile `server` target.
- `docs/BINARIES.md` and `README.md` describe the wrapper as a local
  recovery/compatibility path; `docs/LOCAL-DEVELOPMENT.md` documents the
  canonical split local stack.

**Conclusion:** removing `cmd/server/main.go`, its Docker target, Compose
profile, or Makefile targets now would break documented development/recovery
workflows. The wrapper remains deprecated and emits a runtime warning, but is
not yet removable.

## Canonical replacement

For normal development and all production-shaped execution, use:

1. `cmd/migrate` for the one-shot migration job;
2. `cmd/api` for the HTTP listener;
3. `cmd/worker` for background workers.

`make dev` and the default Docker Compose topology already use this split.

## Verification record

The audit was rechecked on `main` on 2026-08-01 with repository-wide searches
and the following entrypoint surfaces:

- Development compatibility remains explicit in `Makefile` (`run-server` and
  `run-server-api-only`) and in the Compose `legacy` profile.
- The canonical Compose services still select Docker targets `migrate`, `api`,
  and `worker`; the production overlay contains no `server` service or target.
- The Dockerfile still exposes the legacy target only as a separately named
  compatibility stage, not as the production default.
- Deployment and CI files contain no `cmd/server`, `go run ./cmd/server`, or
  `/out/server` invocation.
- `scripts/verify-entrypoint-topology.sh` is the automated guard: it asserts
  the three canonical entrypoints and rejects legacy references in production
  files while requiring the deliberate legacy surfaces to remain documented.
- The verification commands passed on this checkout: `./scripts/verify-entrypoint-topology.sh`,
  `go test -race ./...`, `go vet ./...`, and `go build ./...`.

This evidence confirms that removal is **not yet safe**: the wrapper is absent
from production but still has active, documented recovery/development callers.

## Removal gate

A future removal change may delete the wrapper and all compatibility surfaces
only after all of the following are recorded in a new audit or an update to
this one:

1. No repository search finds invocations of `run-server`,
   `run-server-api-only`, or `docker compose --profile legacy up` in active
   development/recovery instructions or automation.
2. Operators confirm that no deployed or maintained environment depends on the
   single-process wrapper, including historical recovery procedures.
3. The compatibility window has ended with no observed wrapper use.
4. The replacement instructions have been validated with `make dev`,
   `make run-migrate`, `make run-api`, and `make run-worker`.
5. One reviewed change removes, together, `cmd/server/main.go`, the Docker
   `server` build/final stage, the Compose `legacy` service, the two Makefile
   compatibility targets, and obsolete documentation references.
6. `make verify-entrypoint-topology`, `go test -race ./...`, `go vet ./...`,
   and `go build ./...` pass after the removal.

Until this gate is satisfied, the topology check must continue to assert both
that the canonical production path is clean and that the deliberate legacy
surfaces remain explicit.
