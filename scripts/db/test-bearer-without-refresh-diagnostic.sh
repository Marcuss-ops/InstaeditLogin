#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
QUERY="$SCRIPT_DIR/bearer-without-refresh-diagnostic.sql"
TMP_DIR="$(mktemp -d -t bearer-without-refresh-diagnostic-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

[[ -f "$QUERY" ]] || {
    echo "bearer-without-refresh diagnostic query is missing" >&2
    exit 1
}

grep -Fq "FROM public.tokens AS t" "$QUERY"
grep -Fq "t.token_type = 'bearer'" "$QUERY"
grep -Fq "COALESCE(octet_length(t.encrypted_refresh_token), 0) = 0" "$QUERY"
grep -Fq "GROUP BY t.oauth_connection_id" "$QUERY"
grep -Fq "bearer_token_row_count" "$QUERY"
grep -Fq "linked_platform_account_count" "$QUERY"
grep -Fq "active_platform_account_count" "$QUERY"
grep -Fq "oauth_connection_exists" "$QUERY"
if grep -Fq "latest_token_update" "$QUERY"; then
    echo "diagnostic exposes timestamp metadata outside the requested counts" >&2
    exit 1
fi

# Validate executable SQL rather than safety comments.
SQL_NO_COMMENTS="$TMP_DIR/query.sql"
sed -E 's/--.*$//' "$QUERY" > "$SQL_NO_COMMENTS"
if grep -Eiq '^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|GRANT|REVOKE)[[:space:]]' "$SQL_NO_COMMENTS"; then
    echo "bearer-without-refresh diagnostic contains a mutating statement" >&2
    exit 1
fi
if grep -Eiq '(^|[^a-z_])(SELECT[[:space:]]+\*|encrypted_[a-z_]+[[:space:],])' "$SQL_NO_COMMENTS"; then
    echo "bearer-without-refresh diagnostic projects token material or SELECT *" >&2
    exit 1
fi
if ! grep -Fq "FROM public.tokens AS t" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "LEFT JOIN public.oauth_connections AS oc" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "LEFT JOIN public.platform_accounts AS pa" "$SQL_NO_COMMENTS"; then
    echo "bearer-without-refresh diagnostic is missing safe relation joins" >&2
    exit 1
fi

if grep -Eiq 'ya29\.|1//|Bearer[[:space:]]+[A-Za-z0-9]' "$SQL_NO_COMMENTS"; then
    echo "bearer-without-refresh diagnostic contains a credential literal" >&2
    exit 1
fi

printf 'bearer-without-refresh diagnostic is aggregate-only and value-free\n'
