#!/usr/bin/env bash
#
# scripts/db/installation-identity-diagnostic.sh
#
# Read-only diagnostic for the PostgreSQL installation identity. It prints
# only MATCH, MISMATCH, or MISSING (plus generic tool/connection errors).
# Neither the expected UUID, database UUID, password, DSN, nor token material
# is printed.
#
# Usage:
#   DATABASE_URL="postgres://..." \
#   EXPECTED_DATABASE_INSTALLATION_UUID="..." \
#   ./scripts/db/installation-identity-diagnostic.sh
#
# Exit codes:
#   0  identity matches
#   1  tool/configuration/connection/query failure
#   2  invalid CLI argument
#   3  identity mismatch or missing singleton row

set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
QUERY="$SCRIPT_DIR/installation-identity-diagnostic.sql"
URL=""
EXPECTED_UUID=""

usage() {
    sed -n '2,25p' "$0"
}

fail_preflight() {
    echo "❌ installation identity diagnostic unavailable: $*" >&2
    exit 1
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
            echo "❌ unknown argument" >&2
            exit 2
            ;;
    esac
done

URL="${URL:-${DATABASE_URL:-}}"
EXPECTED_UUID="${EXPECTED_UUID:-${EXPECTED_DATABASE_INSTALLATION_UUID:-}}"
[[ -n "$URL" ]] || fail_preflight "DATABASE_URL is required (env or --url)"
[[ -n "$EXPECTED_UUID" ]] || fail_preflight "EXPECTED_DATABASE_INSTALLATION_UUID is required (env or --expected-installation-uuid)"
[[ -f "$QUERY" ]] || fail_preflight "diagnostic query is missing"
command -v psql >/dev/null 2>&1 || fail_preflight "psql is required"
command -v python3 >/dev/null 2>&1 || fail_preflight "python3 is required to validate the UUID and connection"

if ! EXPECTED_UUID="$EXPECTED_UUID" python3 - <<'PY'
import os
import uuid
try:
    uuid.UUID(os.environ["EXPECTED_UUID"])
except (ValueError, AttributeError):
    raise SystemExit(1)
PY
then
    fail_preflight "EXPECTED_DATABASE_INSTALLATION_UUID is not a valid UUID"
fi

# Prepare a password-free connection URL. If the supplied URL contains a
# password, put only that password in a temporary 0600 .pgpass file. The
# password-bearing URL never becomes a psql argument and all temporary files
# are removed on exit.
CONNECTION_FILE="$(mktemp -t installation-identity-connection.XXXXXX)"
PGPASSFILE="$(mktemp -t installation-identity-pgpass.XXXXXX)"
chmod 600 "$CONNECTION_FILE" "$PGPASSFILE"
cleanup() {
    rm -f "$CONNECTION_FILE" "$PGPASSFILE"
}
trap cleanup EXIT

if ! DATABASE_URL="$URL" CONNECTION_FILE="$CONNECTION_FILE" PGPASSFILE="$PGPASSFILE" python3 - <<'PY' 2>/dev/null
import os
import urllib.parse

raw = os.environ["DATABASE_URL"]
try:
    parts = urllib.parse.urlsplit(raw)
    if parts.scheme not in ("postgres", "postgresql"):
        raise ValueError
    # Do not allow alternate password sources hidden in query parameters.
    for key, _ in urllib.parse.parse_qsl(parts.query, keep_blank_values=True):
        if key.lower() in {"password", "passfile", "sslpassword", "options"}:
            raise ValueError
    host = parts.hostname or "*"
    port = str(parts.port or "*")
    database = parts.path.lstrip("/") or "*"
    username = urllib.parse.unquote(parts.username or "*")
    password = parts.password

    def pg_escape(value):
        return value.replace("\\", "\\\\").replace(":", "\\:")

    if password is not None:
        decoded_password = urllib.parse.unquote(password)
        if "\n" in decoded_password or "\r" in decoded_password:
            raise ValueError
        with open(os.environ["PGPASSFILE"], "w", encoding="utf-8") as f:
            f.write(":".join(map(pg_escape, [host, port, database, username, decoded_password])))
            f.write("\n")

    safe_netloc = ""
    if parts.username is not None:
        safe_netloc += urllib.parse.quote(username, safe="") + "@"
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
except (ValueError, TypeError):
    raise SystemExit(1)
PY
then
    fail_preflight "DATABASE_URL is invalid, uses an unsupported credential option, or could not be prepared safely"
fi
PSQL_URL="$(cat "$CONNECTION_FILE")"

# PGOPTIONS supplies the expected value as a session-local PostgreSQL setting,
# avoiding a query argument and keeping the SQL result limited to a status.
if ! status="$({
    env -u DATABASE_URL -u PGPASSWORD -u PGSERVICE -u PGSERVICEFILE \
        PGOPTIONS="-c app.diagnostic_expected_installation_uuid=$EXPECTED_UUID" \
        PGPASSFILE="$PGPASSFILE" \
        psql "$PSQL_URL" -X -q -w -At -v ON_ERROR_STOP=1 -f "$QUERY"
} 2>/dev/null)"; then
    fail_preflight "database identity query failed"
fi

case "$status" in
    MATCH)
        echo "MATCH"
        ;;
    MISMATCH)
        echo "MISMATCH"
        exit 3
        ;;
    MISSING)
        echo "MISSING"
        exit 3
        ;;
    *)
        fail_preflight "database identity query returned an invalid status"
        ;;
esac
