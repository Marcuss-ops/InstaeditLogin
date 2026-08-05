#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
SCRIPT="$SCRIPT_DIR/oauth-preflight-check.sh"
TMP_DIR="$(mktemp -d -t oauth-preflight-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

bash -n "$SCRIPT"
grep -Fq "084_oauth_subject_shared_connections.sql" "$SCRIPT"
grep -Fq "085_grant_scoped_tokens.sql" "$SCRIPT"
grep -Fq "system_installation" "$SCRIPT"
grep -Fq "octet_length(t.encrypted_refresh_token)" "$SCRIPT"
grep -Fq "GROUP BY oauth_connection_id, token_type" "$SCRIPT"
grep -Fq "oc.status <> 'active'" "$SCRIPT" || grep -Fq "oc.id IS NULL" "$SCRIPT"
grep -Fq "idx_tokens_oauth_connection_token_type" "$SCRIPT"
grep -Fq "oc.user_id <> pa.user_id" "$SCRIPT"
# The check must not print encrypted values or select their contents.
if grep -Eq 'SELECT[^;]*encrypted_refresh_token|SELECT[^;]*encrypted_access_token' "$SCRIPT"; then
    echo "preflight directly selects encrypted token material" >&2
    exit 1
fi

FAKE_PSQL="$TMP_DIR/psql"
PSQL_ARGS_FILE="$TMP_DIR/psql-args"
PGPASS_CAPTURE="$TMP_DIR/pgpass-capture"
PGPASS_PATH_FILE="$TMP_DIR/pgpass-path"
cat > "$FAKE_PSQL" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sql=""
printf '%s\n' "$@" > "${PSQL_ARGS_FILE:?}"
if [[ -n "${PGPASSFILE:-}" ]]; then
    printf '%s\n' "$PGPASSFILE" > "${PGPASS_PATH_FILE:?}"
    stat -c '%a' "$PGPASSFILE" > "${PGPASS_CAPTURE:?}.mode"
    cat "$PGPASSFILE" > "${PGPASS_CAPTURE:?}"
fi
while [[ $# -gt 0 ]]; do
    if [[ "$1" == "-c" ]]; then
        sql="${2:-}"
        shift 2
    else
        shift
    fi
done
case "$sql" in
    *"object_name"*)
        ;;
    *"i.indisunique"*)
        printf '3\n'
        ;;
    *"WHERE filename IN"*)
        if [[ "${FAKE_PSQL_MODE:-pass}" == "missing-migration" ]]; then
            printf '%s\n' '084_oauth_subject_shared_connections.sql|missing'
        else
            sha084="$(sha256sum "$SCRIPT_DIR/../../internal/database/migrations/084_oauth_subject_shared_connections.sql" | awk '{print $1}')"
            sha085="$(sha256sum "$SCRIPT_DIR/../../internal/database/migrations/085_grant_scoped_tokens.sql" | awk '{print $1}')"
            printf '%s\n' "084_oauth_subject_shared_connections.sql|$sha084" "085_grant_scoped_tokens.sql|$sha085"
        fi
        ;;
    *"installation_uuid::text"*)
        if [[ "${FAKE_PSQL_MODE:-pass}" == "identity-mismatch" ]]; then
            printf '%s\n' '00000000-0000-4000-8000-000000000002'
        else
            printf '%s\n' '00000000-0000-4000-8000-000000000001'
        fi
        ;;
    *"HAVING COUNT(*) > 1"*)
        [[ "${FAKE_PSQL_MODE:-pass}" == "duplicate-token" ]] && printf '1\n' || printf '0\n'
        ;;
    *"encrypted_refresh_token"*)
        [[ "${FAKE_PSQL_MODE:-pass}" == "missing-refresh" ]] && printf '1\n' || printf '0\n'
        ;;
    *"oc.status <> 'active'"*|*"oc.id IS NULL"*)
        [[ "${FAKE_PSQL_MODE:-pass}" == "inconsistent-status" ]] && printf '1\n' || printf '0\n'
        ;;
    *)
        echo "unexpected fake psql query: $sql" >&2
        exit 1
        ;;
esac
EOF
chmod +x "$FAKE_PSQL"

EXPECTED="00000000-0000-4000-8000-000000000001"
run_check() {
    PATH="$TMP_DIR:$PATH" \
    DATABASE_URL='postgres://operator:password@example.invalid/instaedit' \
    EXPECTED_DATABASE_INSTALLATION_UUID="$EXPECTED" \
    FAKE_PSQL_MODE="${1:-pass}" \
    PSQL_ARGS_FILE="$PSQL_ARGS_FILE" \
    PGPASS_CAPTURE="$PGPASS_CAPTURE" \
    PGPASS_PATH_FILE="$PGPASS_PATH_FILE" \
    SCRIPT_DIR="$SCRIPT_DIR" \
    "$SCRIPT" >/dev/null 2>&1
}

run_check pass
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
if run_check missing-migration || run_check identity-mismatch || \
   run_check duplicate-token || run_check missing-refresh || \
   run_check inconsistent-status; then
    echo "invariant failure scenarios must return non-zero" >&2
    exit 1
fi

if PATH="$TMP_DIR:$PATH" DATABASE_URL='postgres://operator:password@example.invalid/instaedit' \
   EXPECTED_DATABASE_INSTALLATION_UUID='not-a-uuid' "$SCRIPT" >/dev/null 2>&1; then
    echo "invalid UUID must return non-zero" >&2
    exit 1
fi

printf 'oauth preflight static and mocked tests: PASS\n'
