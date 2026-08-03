#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/instaedit-db-restore-check"

bash -n "$SCRIPT"
grep -Fq 'set -Eeuo pipefail' "$SCRIPT"
grep -Fq 'trap cleanup EXIT' "$SCRIPT"
grep -Fq 'dropdb -U "$DB_OWNER" --if-exists' "$SCRIPT"
grep -Fq 'TEMP_DB_CREATED=0' "$SCRIPT"
grep -Fq 'TEMP_DB_CREATED=1' "$SCRIPT"
grep -Fq 'cleanup verified' "$SCRIPT"
grep -Fq -- '--exit-on-error' "$SCRIPT"
grep -Fq 'sha256sum --check' "$SCRIPT"
grep -Fq 'CREATE DATABASE' "$SCRIPT"
grep -Fq 'pg_restore' "$SCRIPT"
grep -Fq 'DB_OWNER' "$SCRIPT"
grep -Fq 'system_installation' "$SCRIPT"
grep -Fq 'instaedit_login' "$SCRIPT"
# The restore target must be generated, never user-supplied.
grep -Fq 'TEMP_DB="instaedit_restore_check_' "$SCRIPT"
! grep -Eq 'DROP DATABASE.*instaedit_login|dropdb.*instaedit_login' "$SCRIPT"
grep -Fq 'count invariant passed' "$SCRIPT"

printf 'restore-check static safety tests: PASS\n'
