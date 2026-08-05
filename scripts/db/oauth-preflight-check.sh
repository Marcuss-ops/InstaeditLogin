#!/usr/bin/env bash
#
# scripts/db/oauth-preflight-check.sh
#
# Read-only production/staging preflight for the shared OAuth grant model.
# It deliberately reports only migration names, UUID match status, and
# aggregate counts; it never selects ciphertext, plaintext tokens, or DSNs.
#
# Checks:
#   1. migrations 084/085 are recorded in schema_migrations
#   2. system_installation matches EXPECTED_DATABASE_INSTALLATION_UUID
#   3. no duplicate (oauth_connection_id, token_type) token groups exist
#   4. every active YouTube channel has a non-empty encrypted bearer refresh token
#   5. no active YouTube channel points at a non-active OAuth grant
#
# Usage:
#   DATABASE_URL="postgres://..." \
#   EXPECTED_DATABASE_INSTALLATION_UUID="..." \
#   ./scripts/db/oauth-preflight-check.sh
#   ./scripts/db/oauth-preflight-check.sh --url "$DATABASE_URL" \
#       --expected-installation-uuid "$EXPECTED_DATABASE_INSTALLATION_UUID"
#
# Exit codes:
#   0  all checks passed
#   1  preflight/tool/configuration/database query failure
#   2  invalid CLI argument
#   3  invariant failure

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
URL=""
EXPECTED_UUID=""

usage() {
    sed -n '2,33p' "$0"
}

fail_preflight() {
    echo "❌ OAuth DB preflight unavailable: $*" >&2
    exit 1
}

fail_invariant() {
    echo "❌ OAuth DB preflight failed: $*" >&2
    exit 3
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --url)
            [[ $# -ge 2 && -n "$2" ]] || { echo "❌ --url requires a value" >&2; exit 2; }
            URL="$2"
            shift 2
            ;;
        --expected-installation-uuid)
            [[ $# -ge 2 && -n "$2" ]] || { echo "❌ --expected-installation-uuid requires a value" >&2; exit 2; }
            EXPECTED_UUID="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "❌ unknown argument: $1" >&2
            exit 2
            ;;
    esac
done

URL="${URL:-${DATABASE_URL:-}}"
EXPECTED_UUID="${EXPECTED_UUID:-${EXPECTED_DATABASE_INSTALLATION_UUID:-}}"
[[ -n "$URL" ]] || fail_preflight "DATABASE_URL is required (env or --url)"
[[ -n "$EXPECTED_UUID" ]] || fail_preflight "EXPECTED_DATABASE_INSTALLATION_UUID is required (env or --expected-installation-uuid)"
command -v psql >/dev/null 2>&1 || fail_preflight "psql is required"
command -v python3 >/dev/null 2>&1 || fail_preflight "python3 is required to validate the installation UUID"

# Never pass a password-bearing DATABASE_URL as a process argument. When the
# URL contains credentials, move only the decoded password to a temporary
# 0600 .pgpass file and give psql a password-free URL. The URL and the
# password file are removed on exit; neither is printed.
CONNECTION_FILE="$(mktemp -t oauth-preflight-connection.XXXXXX)"
PGPASSFILE="$(mktemp -t oauth-preflight-pgpass.XXXXXX)"
chmod 600 "$CONNECTION_FILE" "$PGPASSFILE"
cleanup() {
    rm -f "$CONNECTION_FILE" "$PGPASSFILE"
}
trap cleanup EXIT
if ! DATABASE_URL="$URL" CONNECTION_FILE="$CONNECTION_FILE" PGPASSFILE="$PGPASSFILE" python3 - <<'PY' 2>/dev/null
import os
import urllib.parse

raw = os.environ["DATABASE_URL"]
parts = urllib.parse.urlsplit(raw)
if parts.scheme not in ("postgres", "postgresql"):
    raise SystemExit(1)
for key, _ in urllib.parse.parse_qsl(parts.query, keep_blank_values=True):
    if key.lower() in {"password", "passfile", "sslpassword"}:
        raise SystemExit(1)

username = parts.username
password = parts.password
host = parts.hostname or "*"
port = str(parts.port or "*")
database = parts.path.lstrip("/") or "*"

def pg_escape(value):
    return value.replace("\\", "\\\\").replace(":", "\\:")

if password is not None:
    decoded_password = urllib.parse.unquote(password)
    if "\n" in decoded_password or "\r" in decoded_password:
        raise SystemExit(1)
    pg_user = urllib.parse.unquote(username or "*")
    with open(os.environ["PGPASSFILE"], "w", encoding="utf-8") as f:
        f.write(":".join(map(pg_escape, [host, port, database, pg_user, decoded_password])))
        f.write("\n")

safe_netloc = ""
if username is not None:
    safe_netloc += urllib.parse.quote(urllib.parse.unquote(username), safe="") + "@"
if parts.hostname:
    safe_host = parts.hostname
    if ":" in safe_host and not safe_host.startswith("["):
        safe_host = "[" + safe_host + "]"
    safe_netloc += safe_host
    if parts.port:
        safe_netloc += ":" + str(parts.port)

safe_url = urllib.parse.urlunsplit((parts.scheme, safe_netloc, parts.path, parts.query, parts.fragment))
with open(os.environ["CONNECTION_FILE"], "w", encoding="utf-8") as f:
    f.write(safe_url)
PY
then
    fail_preflight "DATABASE_URL is invalid or could not be prepared safely"
fi
PSQL_URL="$(cat "$CONNECTION_FILE")"
# Do not leak the original credential-bearing URL to child processes.
unset DATABASE_URL EXPECTED_DATABASE_INSTALLATION_UUID
export PGPASSFILE

# Validate locally before opening a connection. The UUID itself is never
# printed, including on a mismatch.
if ! EXPECTED_UUID="$EXPECTED_UUID" python3 - <<'PY'
import os
import uuid
uuid.UUID(os.environ["EXPECTED_UUID"])
PY
then
    fail_preflight "EXPECTED_DATABASE_INSTALLATION_UUID is not a valid UUID"
fi

# Keep all SQL in this helper so every query uses ON_ERROR_STOP and no query
# can accidentally turn a server-side failure into an empty successful result.
query() {
    env -u DATABASE_URL -u PGPASSWORD -u PGSERVICE -u PGSERVICEFILE \
        psql "$PSQL_URL" -X -q -v ON_ERROR_STOP=1 -At -c "$1"
}

# Confirm the schema required by the checks before running invariant queries.
# This produces object labels only and avoids exposing any credential data.
schema_missing="$(query "
    SELECT object_name
      FROM (VALUES
        ('table:schema_migrations'),
        ('table:system_installation'),
        ('table:oauth_connections'),
        ('table:platform_accounts'),
        ('table:tokens'),
        ('column:oauth_connections.provider_subject_id'),
        ('column:oauth_connections.granted_scopes'),
        ('column:platform_accounts.oauth_connection_id'),
        ('column:platform_accounts.platform'),
        ('column:platform_accounts.status'),
        ('column:tokens.oauth_connection_id'),
        ('column:tokens.token_type'),
        ('column:tokens.encrypted_refresh_token'),
        ('index:idx_oauth_connections_user_provider_subject'),
        ('index:idx_oauth_connections_legacy_resource_unique'),
        ('index:idx_tokens_oauth_connection_token_type')
      ) AS required(object_name)
     WHERE CASE
       WHEN split_part(object_name, ':', 1) = 'table' THEN
         to_regclass('public.' || split_part(object_name, ':', 2)) IS NULL
       WHEN split_part(object_name, ':', 1) = 'index' THEN
         to_regclass('public.' || split_part(object_name, ':', 2)) IS NULL
       ELSE NOT EXISTS (
         SELECT 1
           FROM information_schema.columns c
          WHERE c.table_schema = 'public'
            AND c.table_name = split_part(split_part(object_name, ':', 2), '.', 1)
            AND c.column_name = split_part(split_part(object_name, ':', 2), '.', 2)
       )
     END
     ORDER BY object_name
")" || fail_preflight "schema probe query failed"
if [[ -n "$schema_missing" ]]; then
    echo "   missing schema objects:" >&2
    while IFS= read -r object_name; do
        [[ -n "$object_name" ]] && echo "    - $object_name" >&2
    done <<< "$schema_missing"
    fail_preflight "required OAuth schema is incomplete"
fi

# Presence alone is not enough: 084's subject/resource indexes must be
# UNIQUE partial indexes with the expected predicates, while 085's
# grant/token-type index must be UNIQUE and unconditional.
index_contract="$(query "
    SELECT COUNT(*)
      FROM pg_class idx
      JOIN pg_namespace ns ON ns.oid = idx.relnamespace
      JOIN pg_index i ON i.indexrelid = idx.oid
     WHERE ns.nspname = 'public'
       AND (
         (idx.relname = 'idx_oauth_connections_user_provider_subject'
          AND i.indisunique
          AND i.indkey = ARRAY[
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'user_id'),
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'provider'),
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'provider_subject_id')
          ]::int2vector
          AND i.indpred IS NOT NULL
          AND pg_get_expr(i.indpred, i.indrelid) LIKE '%provider_subject_id <>%')
         OR
         (idx.relname = 'idx_oauth_connections_legacy_resource_unique'
          AND i.indisunique
          AND i.indkey = ARRAY[
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'user_id'),
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'provider'),
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'provider_resource_id')
          ]::int2vector
          AND i.indpred IS NOT NULL
          AND pg_get_expr(i.indpred, i.indrelid) LIKE '%provider_subject_id =%')
         OR
         (idx.relname = 'idx_tokens_oauth_connection_token_type'
          AND i.indisunique
          AND i.indkey = ARRAY[
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'oauth_connection_id'),
              (SELECT attnum FROM pg_attribute WHERE attrelid = i.indrelid AND attname = 'token_type')
          ]::int2vector
          AND i.indpred IS NULL)
       )
")" || fail_preflight "could not inspect OAuth index definitions"
if [[ "$index_contract" != "3" ]]; then
    fail_invariant "OAuth unique index definitions do not match migrations 084/085"
fi
echo "✓ OAuth unique index definitions are correct"

# 1. Migrations are tracked by the migration runner. The primary key on
# schema_migrations prevents duplicate records; compare both filename and
# checksum so a manually fabricated/stale record cannot pass preflight.
MIGRATION_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/../../internal/database/migrations" 2>/dev/null && pwd)" \
    || fail_preflight "migration source directory is unavailable"
for migration in \
    084_oauth_subject_shared_connections.sql \
    085_grant_scoped_tokens.sql; do
    [[ -f "$MIGRATION_DIR/$migration" ]] || fail_preflight "migration source is missing: $migration"
done
recorded_checksums="$(query "
    SELECT filename || '|' || checksum
      FROM schema_migrations
     WHERE filename IN (
       '084_oauth_subject_shared_connections.sql',
       '085_grant_scoped_tokens.sql'
     )
     ORDER BY filename
")" || fail_preflight "could not read schema_migrations"
missing_migrations=()
checksum_mismatch=()
while IFS= read -r migration; do
    [[ -n "$migration" ]] || continue
    expected_checksum="$(sha256sum "$MIGRATION_DIR/$migration" | awk '{print $1}')"
    recorded_checksum="$(awk -F'|' -v name="$migration" '$1 == name {print $2}' <<< "$recorded_checksums")"
    if [[ -z "$recorded_checksum" ]]; then
        missing_migrations+=("$migration")
    elif [[ "$recorded_checksum" != "$expected_checksum" ]]; then
        checksum_mismatch+=("$migration")
    fi
done < <(printf '%s\n' \
    084_oauth_subject_shared_connections.sql \
    085_grant_scoped_tokens.sql)
if (( ${#missing_migrations[@]} > 0 )); then
    echo "   missing migration records:" >&2
    printf '    - %s\n' "${missing_migrations[@]}" >&2
    fail_invariant "required OAuth migrations are not applied"
fi
if (( ${#checksum_mismatch[@]} > 0 )); then
    echo "   migration checksum mismatch:" >&2
    printf '    - %s\n' "${checksum_mismatch[@]}" >&2
    fail_invariant "applied OAuth migration checksum differs from the repository"
fi
echo "✓ migrations 084/085 recorded with matching checksums"

# 2. Compare the UUID without printing either value.
actual_uuid="$(query "SELECT installation_uuid::text FROM system_installation WHERE id = 1")" \
    || fail_preflight "could not read system_installation"
if [[ -z "$actual_uuid" ]]; then
    fail_invariant "system_installation singleton row is missing"
fi
if ! EXPECTED_UUID="$EXPECTED_UUID" ACTUAL_UUID="$actual_uuid" python3 - <<'PY'
import os
import uuid
raise SystemExit(uuid.UUID(os.environ["EXPECTED_UUID"]) != uuid.UUID(os.environ["ACTUAL_UUID"]))
PY
then
    fail_invariant "database installation identity does not match the configured installation"
fi
echo "✓ database installation identity matches"

# 3. Migration 085's unique index is the database guard. The aggregate query
# remains useful on a legacy/partially migrated database and does not select
# token values.
duplicate_groups="$(query "
    SELECT COUNT(*)
      FROM (
        SELECT oauth_connection_id, token_type
          FROM tokens
         GROUP BY oauth_connection_id, token_type
        HAVING COUNT(*) > 1
      ) AS duplicate_groups
")" || fail_preflight "could not inspect token uniqueness"
if [[ "$duplicate_groups" != "0" ]]; then
    fail_invariant "found $duplicate_groups duplicate grant/token-type group(s)"
fi
echo "✓ no duplicate grant-scoped tokens"

# 4. An active YouTube channel must have a usable bearer refresh grant. Only
# presence is checked; encrypted bytes are never selected or logged.
active_without_refresh="$(query "
    SELECT COUNT(DISTINCT pa.id)
      FROM oauth_connections oc
      JOIN platform_accounts pa ON pa.oauth_connection_id = oc.id
      LEFT JOIN tokens t
        ON t.oauth_connection_id = oc.id
       AND t.token_type = 'bearer'
     WHERE oc.provider = 'youtube'
       AND pa.platform = 'youtube'
       AND pa.status = 'active'
       AND (
         t.id IS NULL
         OR COALESCE(octet_length(t.encrypted_refresh_token), 0) = 0
       )
")" || fail_preflight "could not inspect active channel refresh-token presence"
if [[ "$active_without_refresh" != "0" ]]; then
    fail_invariant "$active_without_refresh active YouTube channel(s) have no non-empty encrypted bearer refresh token"
fi
echo "✓ active YouTube channels have encrypted bearer refresh tokens"

# 5. The Vault blocks this state at runtime, but the dashboard/database must
# not advertise an active YouTube channel without a matching active YouTube
# grant. This catches both a missing FK binding and a status/provider mismatch.
# Count rows only; no account names or token material are returned.
inconsistent_channels="$(query "
    SELECT COUNT(*)
      FROM platform_accounts pa
      LEFT JOIN oauth_connections oc ON pa.oauth_connection_id = oc.id
     WHERE pa.platform = 'youtube'
       AND pa.status = 'active'
       AND (
         oc.id IS NULL
         OR oc.user_id <> pa.user_id
         OR oc.provider <> 'youtube'
         OR oc.status <> 'active'
       )
")" || fail_preflight "could not inspect grant/account status consistency"
if [[ "$inconsistent_channels" != "0" ]]; then
    fail_invariant "$inconsistent_channels active YouTube channel(s) lack a matching active YouTube OAuth grant"
fi
echo "✓ OAuth grant/channel statuses are consistent"

echo "✓ OAuth DB preflight passed (read-only)"
