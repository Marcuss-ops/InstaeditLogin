#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
SCRIPT="$SCRIPT_DIR/installation-identity-diagnostic.sh"
QUERY="$SCRIPT_DIR/installation-identity-diagnostic.sql"
TMP_DIR="$(mktemp -d -t installation-identity-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

bash -n "$SCRIPT"
grep -Fq "installation_identity_status" "$QUERY"
grep -Fq "app.diagnostic_expected_installation_uuid" "$QUERY"
grep -Fq "to_regclass('public.system_installation')" "$QUERY"
grep -Fq '\if :table_exists' "$QUERY"
grep -Fq "SELECT 'MISSING' AS installation_identity_status" "$QUERY"
# The SQL must classify, not project, the identity UUID.
if grep -Eq '^[[:space:]]*installation_uuid::text[[:space:]]*(,|FROM)' "$QUERY"; then
    echo "diagnostic query projects the installation UUID" >&2
    exit 1
fi
# The wrapper must be read-only and must not print secrets or UUID values.
grep -Fq -- '-f "$QUERY"' "$SCRIPT"
grep -Fq 'PGOPTIONS=' "$SCRIPT"
if grep -Eq '(^|[[:space:]])(UPDATE|DELETE|INSERT|DROP|ALTER|TRUNCATE)[[:space:]]' "$QUERY"; then
    echo "diagnostic query contains a mutating SQL statement" >&2
    exit 1
fi

FAKE_PSQL="$TMP_DIR/psql"
PSQL_ARGS_FILE="$TMP_DIR/psql-args"
PGPASS_CAPTURE="$TMP_DIR/pgpass-capture"
PGPASS_PATH_FILE="$TMP_DIR/pgpass-path"
cat > "$FAKE_PSQL" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "${PSQL_ARGS_FILE:?}"
printf '%s\n' "${PGPASSFILE:-}" > "${PGPASS_PATH_FILE:?}"
if [[ -n "${PGPASSFILE:-}" ]]; then
    stat -c '%a' "$PGPASSFILE" > "${PGPASS_CAPTURE:?}.mode"
    cat "$PGPASSFILE" > "${PGPASS_CAPTURE:?}"
fi
grep -Fxq -- '-f' "$PSQL_ARGS_FILE"
grep -Fq -- "$QUERY" "$PSQL_ARGS_FILE"
grep -Fq -- 'app.diagnostic_expected_installation_uuid=' <<< "${PGOPTIONS:-}"
query_file=""
while [[ $# -gt 0 ]]; do
    if [[ "$1" == "-f" ]]; then
        query_file="${2:-}"
        break
    fi
    shift
    if [[ $# -gt 0 && "$1" != "-f" ]]; then
        continue
    fi
done
[[ "$query_file" == "${QUERY:?}" ]]
grep -Fq 'installation_identity_status' "$query_file"
grep -Fq 'FROM public.system_installation' "$query_file"
if grep -Eiq '^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE)[[:space:]]' "$query_file"; then
    echo 'query file contains a mutating statement' >&2
    exit 1
fi
if grep -q 'operator:password' "$PSQL_ARGS_FILE"; then
    echo 'password-bearing URL passed to psql' >&2
    exit 1
fi
if [[ "${FAKE_PSQL_MODE:-match}" == "connection-error" ]]; then
    exit 1
fi
printf '%s\n' "${FAKE_PSQL_STATUS:-MATCH}"
EOF
chmod +x "$FAKE_PSQL"

EXPECTED='00000000-0000-4000-8000-000000000001'
run_check() {
    local mode="$1"
    local status="${2:-MATCH}"
    set +e
    PATH="$TMP_DIR:$PATH" \
    DATABASE_URL='postgres://operator:password@example.invalid/instaedit' \
    EXPECTED_DATABASE_INSTALLATION_UUID="$EXPECTED" \
    FAKE_PSQL_MODE="$mode" \
    FAKE_PSQL_STATUS="$status" \
    PSQL_ARGS_FILE="$PSQL_ARGS_FILE" \
    PGPASS_CAPTURE="$PGPASS_CAPTURE" \
    PGPASS_PATH_FILE="$PGPASS_PATH_FILE" \
    QUERY="$QUERY" \
    "$SCRIPT"
    local code=$?
    set -e
    return "$code"
}

output="$(run_check pass MATCH 2>/dev/null)"
if [[ "$output" != "MATCH" ]]; then
    echo "expected MATCH output, got: $output" >&2
    exit 1
fi
if grep -q 'operator:password' "$PSQL_ARGS_FILE"; then
    echo "psql received a password-bearing URL" >&2
    exit 1
fi
if ! grep -Fq 'example.invalid:*:instaedit:operator:password' "$PGPASS_CAPTURE"; then
    echo "pgpass did not receive the decoded password" >&2
    exit 1
fi
if [[ "$(cat "$PGPASS_CAPTURE.mode")" != "600" ]]; then
    echo "pgpass permissions are not 0600" >&2
    exit 1
fi
if [[ -e "$(cat "$PGPASS_PATH_FILE")" ]]; then
    echo "temporary pgpass file was not removed" >&2
    exit 1
fi

for expected_status in MISMATCH MISSING; do
    set +e
    output="$(run_check pass "$expected_status" 2>/dev/null)"
    code=$?
    set -e
    if [[ "$output" != "$expected_status" || "$code" -ne 3 ]]; then
        echo "expected $expected_status output and exit 3, got output=$output exit=$code" >&2
        exit 1
    fi
    if grep -Eq "$EXPECTED|operator:password|postgres://" <<< "$output"; then
        echo "diagnostic output exposed a secret or UUID" >&2
        exit 1
    fi
done
if run_check pass INVALID; then
    echo "invalid query status must return non-zero" >&2
    exit 1
fi
if run_check connection-error; then
    echo "connection failure must return non-zero" >&2
    exit 1
fi
if PATH="$TMP_DIR:$PATH" DATABASE_URL='postgres://operator:password@example.invalid/instaedit' \
   EXPECTED_DATABASE_INSTALLATION_UUID='not-a-uuid' "$SCRIPT" >/dev/null 2>&1; then
    echo "invalid UUID must return non-zero" >&2
    exit 1
fi
if PATH="$TMP_DIR:$PATH" DATABASE_URL='postgres://operator:password@example.invalid/instaedit?options=-c%20app.diagnostic_expected_installation_uuid%3D00000000-0000-4000-8000-000000000002' \
   EXPECTED_DATABASE_INSTALLATION_UUID="$EXPECTED" "$SCRIPT" >/dev/null 2>&1; then
    echo "DATABASE_URL options must be rejected" >&2
    exit 1
fi

printf 'installation identity diagnostic static and mocked tests: PASS\n'
