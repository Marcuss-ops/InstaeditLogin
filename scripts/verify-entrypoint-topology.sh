#!/usr/bin/env bash
# Verify the canonical entrypoint topology without building or starting services.
# The supported topology contains only cmd/api, cmd/worker, and cmd/migrate.
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT_DIR"

fail() {
  echo "entrypoint topology check: $*" >&2
  exit 1
}

for entrypoint in api worker migrate; do
  test -f "cmd/${entrypoint}/main.go" || fail "missing cmd/${entrypoint}/main.go"
done

test ! -e cmd/server || fail "removed legacy cmd/server still exists"

grep -q 'target: migrate' docker-compose.yml || fail "compose has no migrate target"
grep -q 'target: api' docker-compose.yml || fail "compose has no api target"
grep -q 'target: worker' docker-compose.yml || fail "compose has no worker target"
grep -q 'service_completed_successfully' docker-compose.yml || fail "compose does not gate services on migration success"
if grep -nE '^[[:space:]]+server:|profiles:.*legacy|target: server|cmd/server|RUN_WORKERS' docker-compose.yml docker-compose.local.yml; then
  fail "legacy server Compose reference found"
fi

# The Dockerfile must build and expose only the three canonical entrypoints.
for entrypoint in api worker migrate; do
  grep -q "./cmd/${entrypoint}" Dockerfile || fail "Dockerfile does not build cmd/${entrypoint}"
  grep -q "FROM base AS ${entrypoint}" Dockerfile || fail "Dockerfile has no ${entrypoint} target"
done
if grep -nE 'cmd/server|/out/server|FROM base AS server|target server' Dockerfile; then
  fail "legacy server Dockerfile reference found"
fi

# Production overlay and deployment/CI files must not select a legacy process.
if grep -nE 'cmd/server|target: server|^[[:space:]]+server:|RUN_WORKERS' docker-compose.production.yml; then
  fail "legacy server reference found in docker-compose.production.yml"
fi
for file in docs/DEPLOY.md .github/workflows/deploy.yml .github/workflows/integration-fast.yml; do
  test -f "$file" || fail "required production file missing: $file"
  if grep -nE 'cmd/server|go run ./cmd/server|/out/server|RUN_WORKERS|run-server|legacy profile' "$file"; then
    fail "legacy server reference found in production file: $file"
  fi
done

# Makefile must expose only the canonical individual process targets.
if grep -nE 'run-server|cmd/server|RUN_WORKERS|docker compose --profile legacy' Makefile; then
  fail "legacy server Makefile reference found"
fi

echo "entrypoint topology check: PASS (api + worker + migrate only; legacy server removed)"
