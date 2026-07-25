#!/usr/bin/env bash
#
# scripts/create-tiktok-test-user.sh
#
# One-shot provisioning of the TikTok App Review test user
# (`tiktoktest1@instaedit.org`) on the VPS production stack. This is
# the user that `docs/TIKTOK-APP-REVIEW.md` expects to exist before
# the developer portal form is submitted.
#
# Reads CREATE_USER_PASSWORD from the operator shell env (do NOT
# commit or echo the value). MUST run from the VPS, not from the
# operator laptop — `docker compose exec` talks to the local
# compose daemon on the VPS host (not the laptop's docker socket).
#
# Usage (on the VPS, as the `instaedit` user):
#   1. Load the password into the shell (NEVER paste into history):
#         read -rs CREATE_USER_PASSWORD
#         export CREATE_USER_PASSWORD
#   2. Run the script:
#         bash scripts/create-tiktok-test-user.sh
#
# Exit codes:
#   0  user provisioned OR already present (idempotent on email)
#   1  pre-flight failure (env var unset, project root missing,
#                          compose not up, DB probe errored, binary missing)
#   2  the create-user CLI returned non-zero (e.g. invalid email shape,
#      Postgres not reachable from inside the api container) — see CLI output

set -euo pipefail

# ---- pre-flight -------------------------------------------------------------
if [[ -z "${CREATE_USER_PASSWORD:-}" ]]; then
    echo "FAIL: CREATE_USER_PASSWORD env var is required." >&2
    echo "      Load it before invoking:" >&2
    echo "        read -rs CREATE_USER_PASSWORD; export CREATE_USER_PASSWORD" >&2
    exit 1
fi

PROJECT_ROOT="${PROJECT_ROOT:-/opt/instaedit/InstaeditLogin}"
if [[ ! -d "$PROJECT_ROOT" ]]; then
    echo "FAIL: PROJECT_ROOT=$PROJECT_ROOT not found" >&2
    echo "      expected the compose stack at /opt/instaedit/InstaeditLogin" >&2
    echo "      (set PROJECT_ROOT=/path/to/compose if you cloned elsewhere)" >&2
    exit 1
fi

cd "$PROJECT_ROOT"

# confirm docker compose is up before exec'ing (fast fail)
if ! docker compose ps --services --filter "status=running" 2>/dev/null \
       | grep -qE '^(api|worker|caddy)$'; then
    echo "FAIL: docker compose stack is not fully up (api/worker/caddy missing)." >&2
    echo "      Run: cd $PROJECT_ROOT && docker compose ps" >&2
    exit 1
fi

# ---- idempotency probe -------------------------------------------------------
# TikTok App Review re-submissions routinely re-run this script on the
# same VPS. Without this probe, two failure modes apply:
#   (a) `users.email` HAS a unique index (via 001_init.sql or later
#       migration) -> the create-user CLI trips a unique_violation
#       (SQLSTATE 23505) and exits non-zero.
#   (b) `users.email` has NO unique index -> create-user would silently
#       insert a duplicate row, corrupting the test-account identity
#       (auditors see N accounts named "tiktoktest1@...").
# In both cases, treat an existing row as success and short-circuit
# BEFORE invoking create-user.
#
# The probe runs against the canonical compose-managed Postgres
# service (`postgres`). The schema is `public` (per 001_init.sql); the
# unique-index-free default actually requires this probe MORE than
# a UNIQUE-protected column.
#
# Auth note: the probe uses `-U instaedit -d instaedit_login` with NO
# PGPASSWORD and NO `-W`. Inside the compose network the canonical
# Postgres image is configured for trust auth via POSTGRES_HOST_AUTH_METHOD
# (see ops/vps/Caddyfile runbook for the cutover). For a non-trust setup,
# PGPASSWORD would need to be exported first; this script intentionally
# does NOT source /srv/instaedit/.env.production because that file is
# outside the repo and would couple the script to a path the operator
# may move. Instead the probe FAILS LOUDLY on auth errors so a real
# problem surfaces instead of being silently swallowed by `|| true`.
if ! existing="$(docker compose exec -T postgres \
    psql -U instaedit -d instaedit_login -tAc \
        "SELECT 1 FROM users WHERE email='tiktoktest1@instaedit.org';" \
    2>&1)"; then
    echo "FAIL: idempotency probe died before returning." >&2
    echo "      Most likely cause: Postgres inside the compose stack is down," >&2
    echo "      auth is set to md5/scram-sha-256 without PGPASSWORD, or the" >&2
    echo "      `postgres` service is not on the running list." >&2
    echo "      --- probe output ---" >&2
    echo "$existing" >&2
    exit 1
fi

if [[ "$existing" =~ ^1$ ]]; then
    echo "[ok] tiktoktest1@instaedit.org already present in public.users (idempotent)" >&2
    echo "      re-run is safe — skipping create-user" >&2
    echo "[warn] your \$CREATE_USER_PASSWORD was NOT applied — the existing user" >&2
    echo "        retains its prior password. If you cannot log in with the" >&2
    echo "        password you just read into the shell, the password at the" >&2
    echo "        first-create time still applies (or rotate via the admin API)." >&2
    exit 0
fi

# ---- provision ---------------------------------------------------------------
# CREATE_USER_PASSWORD is inherited from the shell env by docker compose exec;
# APP_ENV / CREATE_USER_EMAIL / CREATE_USER_NAME are passed at exec-time so the
# per-test-user values do NOT pollute /srv/instaedit/.env.production.
docker compose \
    exec -T \
    -e APP_ENV=production \
    -e CREATE_USER_EMAIL=tiktoktest1@instaedit.org \
    -e CREATE_USER_NAME='TikTok Test 1' \
    api ./create-user --confirm-prod

echo "[ok] tiktoktest1@instaedit.org provisioned on the VPS compose stack"
echo "      next: log in via docs/TIKTOK-APP-REVIEW.md §5 (or via /api/v1/auth/tiktok/callback)"
