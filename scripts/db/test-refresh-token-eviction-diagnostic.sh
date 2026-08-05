#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
QUERY="$SCRIPT_DIR/refresh-token-eviction-diagnostic.sql"
TMP_DIR="$(mktemp -d -t refresh-token-eviction-diagnostic-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

[[ -f "$QUERY" ]] || {
    echo "Refresh-token eviction diagnostic query is missing" >&2
    exit 1
}

# Pin the tables and risk indicators under verification.
grep -Fq "oauth_connections" "$QUERY"
grep -Fq "FROM tokens" "$QUERY"
grep -Fq "platform_accounts" "$QUERY"
grep -Fq "provider_subject_id" "$QUERY"
grep -Fq "oauth_client_key" "$QUERY"
grep -Fq "reauth_required_at" "$QUERY"
grep -Fq "last_refresh_error" "$QUERY"
grep -Fq "invalid_grant" "$QUERY"

# Check executable SQL after removing comments. The diagnostic must remain
# SELECT-only and must never project token or ciphertext columns.
SQL_NO_COMMENTS="$TMP_DIR/query.sql"
sed -E 's/--.*$//' "$QUERY" > "$SQL_NO_COMMENTS"
if grep -Eiq '^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|GRANT|REVOKE)[[:space:]]' "$SQL_NO_COMMENTS"; then
    echo "eviction diagnostic contains a mutating SQL statement" >&2
    exit 1
fi
if grep -Eiq '(encrypted_[a-z_]+|(^|[^a-z_])(access|refresh)_token([^a-z_]|$))' "$SQL_NO_COMMENTS"; then
    echo "eviction diagnostic selects token material" >&2
    exit 1
fi
if ! grep -Fq "token_row_count" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "eviction_signals" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "orphan_tokens" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "channel_grant_consistency" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "subject_client" "$SQL_NO_COMMENTS"; then
    echo "eviction diagnostic is missing a required section" >&2
    exit 1
fi

printf 'Refresh-token eviction diagnostic is read-only and secret-free\n'
