#!/usr/bin/env bash
# Verify the canonical entrypoint topology without building or starting services.
#
# cmd/server is intentionally retained for local recovery compatibility. This
# check makes sure it does not leak back into production or CI deployment paths.
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

grep -q 'target: migrate' docker-compose.yml || fail "compose has no migrate target"
grep -q 'target: api' docker-compose.yml || fail "compose has no api target"
grep -q 'target: worker' docker-compose.yml || fail "compose has no worker target"
grep -q 'service_completed_successfully' docker-compose.yml || fail "compose does not gate services on migration success"

# The Dockerfile must build and expose all three canonical entrypoints.
for entrypoint in api worker migrate; do
  grep -q "./cmd/${entrypoint}" Dockerfile || fail "Dockerfile does not build cmd/${entrypoint}"
  grep -q "FROM base AS ${entrypoint}" Dockerfile || fail "Dockerfile has no ${entrypoint} target"
done

# The server image may exist only as an explicitly documented compatibility
# target; production Compose and workflows must never select it.
grep -q 'FROM base AS server' Dockerfile || fail "legacy server target is not explicit"
grep -q 'cmd/server' Dockerfile || fail "legacy server target is not documented"

# The production overlay must not introduce the legacy single-process service.
if grep -nE 'cmd/server|target: server|^[[:space:]]+server:' docker-compose.production.yml; then
  fail "legacy cmd/server reference found in docker-compose.production.yml"
fi

# Deployment documentation and GitHub deployment workflows describe production.
for file in docs/DEPLOY.md .github/workflows/deploy.yml .github/workflows/integration-fast.yml; do
  test -f "$file" || fail "required production file missing: $file"
  if grep -nE 'cmd/server|go run ./cmd/server|/out/server' "$file"; then
    fail "legacy cmd/server reference found in production file: $file"
  fi
done

# Keep the deliberate legacy surfaces explicit and easy to audit.
test -f docs/CMD-SERVER-REMOVAL-AUDIT.md || fail "cmd/server removal audit is missing"
grep -q '\*\*Status:\*\* \*\*BLOCKED' docs/CMD-SERVER-REMOVAL-AUDIT.md || fail "removal audit status is not blocked"
grep -q 'profiles: \["legacy"\]' docker-compose.yml || fail "compose legacy profile is not explicit"
grep -q 'run-server' Makefile || fail "legacy Makefile compatibility target disappeared"
grep -q 'cmd/server is deprecated' cmd/server/main.go || fail "runtime deprecation warning is missing"

echo "entrypoint topology check: PASS (api + worker + migrate canonical; cmd/server legacy-only; removal audit blocked)"
