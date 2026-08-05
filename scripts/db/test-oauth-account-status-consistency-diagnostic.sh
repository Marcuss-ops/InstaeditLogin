#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
QUERY="$SCRIPT_DIR/oauth-account-status-consistency-diagnostic.sql"
TMP_DIR="$(mktemp -d -t oauth-account-consistency-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

[[ -f "$QUERY" ]] || {
    echo "OAuth/account consistency diagnostic query is missing" >&2
    exit 1
}

grep -Fq "FROM public.platform_accounts AS pa" "$QUERY"
grep -Fq "LEFT JOIN public.oauth_connections AS oc" "$QUERY"
grep -Fq "MISSING_OAUTH_CONNECTION" "$QUERY"
grep -Fq "OWNER_MISMATCH" "$QUERY"
grep -Fq "PROVIDER_MISMATCH" "$QUERY"
grep -Fq "ACTIVE_ACCOUNT_NONACTIVE_GRANT" "$QUERY"
grep -Fq "NONACTIVE_ACCOUNT_ACTIVE_GRANT" "$QUERY"
grep -Fq "reauth_required_at" "$QUERY"
grep -Fq "ACTIVE_ACCOUNT_GRANT_REAUTH_REQUIRED" "$QUERY"
grep -Fq "connection_reauth_required_at" "$QUERY"
grep -Fq "GROUP BY" "$QUERY"
grep -Fq "affected_platform_account_count" "$QUERY"

SQL_NO_COMMENTS="$TMP_DIR/query.sql"
sed -E 's/--.*$//' "$QUERY" > "$SQL_NO_COMMENTS"
if grep -Eiq '^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|GRANT|REVOKE)[[:space:]]' "$SQL_NO_COMMENTS"; then
    echo "OAuth/account consistency diagnostic contains a mutating statement" >&2
    exit 1
fi
if grep -Eiq '(^|[^a-z_])(SELECT[[:space:]]+\*|encrypted_[a-z_]+[[:space:],]|access_token|refresh_token)' "$SQL_NO_COMMENTS"; then
    echo "OAuth/account consistency diagnostic exposes credential material" >&2
    exit 1
fi
if grep -Eiq 'ya29\.|1//|Bearer[[:space:]]+[A-Za-z0-9]' "$SQL_NO_COMMENTS"; then
    echo "OAuth/account consistency diagnostic contains a credential literal" >&2
    exit 1
fi

printf 'OAuth/account consistency diagnostic is aggregate-only and credential-free\n'
