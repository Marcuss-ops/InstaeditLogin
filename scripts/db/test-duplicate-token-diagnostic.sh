#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
QUERY="$SCRIPT_DIR/duplicate-token-diagnostic.sql"
TMP_DIR="$(mktemp -d -t duplicate-token-diagnostic-test.XXXXXX)"
trap 'rm -rf "$TMP_DIR"' EXIT

[[ -f "$QUERY" ]] || {
    echo "duplicate-token diagnostic query is missing" >&2
    exit 1
}

# Pin the query to the intended aggregate-only shape.
grep -Fq 'FROM public.tokens' "$QUERY"
grep -Fq 'GROUP BY oauth_connection_id, token_type' "$QUERY"
grep -Fq 'HAVING COUNT(*) > 1' "$QUERY"
grep -Fq 'COUNT(*) AS token_row_count' "$QUERY"

# Reject token/ciphertext projections and obvious credential literals. Strip
# comments first so the policy checks the executable SQL, not safety prose.
SQL_NO_COMMENTS="$TMP_DIR/duplicate-token-diagnostic.sql"
sed -E 's/--.*$//' "$QUERY" > "$SQL_NO_COMMENTS"
if grep -Eiq '(encrypted_[a-z_]+|(^|[^a-z_])(access|refresh)_token([^a-z_]|$)|(^|[^a-z_])token([^a-z_]|$))' "$SQL_NO_COMMENTS"; then
    echo "duplicate-token diagnostic projects token material" >&2
    exit 1
fi
if ! grep -Eq '^SELECT[[:space:]]*$' "$SQL_NO_COMMENTS"; then
    echo "duplicate-token diagnostic must use a SELECT projection" >&2
    exit 1
fi
if grep -Eiq 'ya29\.|1//|Bearer[[:space:]]+[A-Za-z0-9]' "$QUERY"; then
    echo "duplicate-token diagnostic contains a credential literal" >&2
    exit 1
fi

# No write statements are allowed in a read-only diagnostic.
if grep -Eiq '^[[:space:]]*(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|GRANT|REVOKE)[[:space:]]' "$QUERY"; then
    echo "duplicate-token diagnostic contains a mutating SQL statement" >&2
    exit 1
fi

printf 'duplicate-token diagnostic is aggregate-only and value-free\n'
