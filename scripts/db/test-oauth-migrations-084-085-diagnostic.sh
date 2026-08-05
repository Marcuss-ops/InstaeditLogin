#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
QUERY="$SCRIPT_DIR/oauth-migrations-084-085-diagnostic.sql"
TMP_DIR="$(mktemp -d -t oauth-migrations-diagnostic-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

[[ -f "$QUERY" ]] || {
    echo "OAuth migration diagnostic query is missing" >&2
    exit 1
}

# Pin the migration and catalog objects under verification.
grep -Fq "084_oauth_subject_shared_connections.sql" "$QUERY"
grep -Fq "085_grant_scoped_tokens.sql" "$QUERY"
grep -Fq "public.schema_migrations" "$QUERY"
grep -Fq "recorded_checksum" "$QUERY"
grep -Fq "idx_oauth_connections_user_provider_subject" "$QUERY"
grep -Fq "idx_oauth_connections_legacy_resource_unique" "$QUERY"
grep -Fq "idx_oauth_connections_provider_subject" "$QUERY"
grep -Fq "idx_platform_accounts_oauth_connection_id" "$QUERY"
grep -Fq "idx_tokens_oauth_connection_token_type" "$QUERY"

# Keep the SQL's expected checksums synchronized with the migration files in
# this checkout. The comparison itself is local and never contacts a DB.
expected_084="$(sha256sum "$SCRIPT_DIR/../../internal/database/migrations/084_oauth_subject_shared_connections.sql" | awk '{print $1}')"
expected_085="$(sha256sum "$SCRIPT_DIR/../../internal/database/migrations/085_grant_scoped_tokens.sql" | awk '{print $1}')"
grep -Fq "'$expected_084'" "$QUERY"
grep -Fq "'$expected_085'" "$QUERY"

# Check executable SQL after removing comments. The diagnostic must remain
# SELECT/catalog-only and must not project token or ciphertext columns.
SQL_NO_COMMENTS="$TMP_DIR/query.sql"
sed -E 's/--.*$//' "$QUERY" > "$SQL_NO_COMMENTS"
if grep -Eiq '^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|GRANT|REVOKE)[[:space:]]' "$SQL_NO_COMMENTS"; then
    echo "migration diagnostic contains a mutating SQL statement" >&2
    exit 1
fi
if grep -Eiq '(encrypted_[a-z_]+|(^|[^a-z_])(access|refresh)_token([^a-z_]|$))' "$SQL_NO_COMMENTS"; then
    echo "migration diagnostic selects token material" >&2
    exit 1
fi
if ! grep -Fq "public.schema_migrations" "$SQL_NO_COMMENTS"; then
    echo "migration diagnostic does not read schema_migrations" >&2
    exit 1
fi
if ! grep -Fq "CHECKSUM_MISMATCH" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "expected_columns" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "pg_get_expr" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "indisvalid" "$SQL_NO_COMMENTS" || \
   ! grep -Fq "nspname = 'public'" "$SQL_NO_COMMENTS"; then
    echo "migration diagnostic is missing checksum or index contract checks" >&2
    exit 1
fi
if ! grep -Fq "pg_class" "$SQL_NO_COMMENTS"; then
    echo "migration diagnostic does not inspect PostgreSQL indexes" >&2
    exit 1
fi

printf 'OAuth migrations 084/085 diagnostic is read-only and secret-free\n'
