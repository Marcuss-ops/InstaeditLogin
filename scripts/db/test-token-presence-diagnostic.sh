#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
QUERY="$SCRIPT_DIR/token-presence-diagnostic.sql"

# Required presence-only fields. Fixed-string assertions keep shell quoting
# unambiguous while still pinning the SQL shape.
grep -Fq "COALESCE(NULLIF(t.encrypted_access_token, ''::bytea), NULLIF(t.encrypted_token, ''::bytea))" "$QUERY"
grep -Fq "COALESCE(octet_length(t.encrypted_refresh_token), 0) > 0 AS has_refresh_token" "$QUERY"
grep -Fq 'granted_scope_count' "$QUERY"

# Reject direct encrypted-token projections. The diagnostic may inspect
# encrypted columns only through octet_length()/COALESCE presence booleans.
if grep -Eq '^[[:space:]]*t\.encrypted_(access|refresh)_token[[:space:],]' "$QUERY" \
   || grep -Eq '^[[:space:]]*t\.encrypted_token[[:space:],]' "$QUERY"; then
  echo 'diagnostic query directly selects encrypted token material' >&2
  exit 1
fi

# Reject obvious plaintext/token literals in the diagnostic source.
if grep -Eiq 'ya29\.|1//|Bearer[[:space:]]+[A-Za-z0-9]' "$QUERY"; then
  echo 'diagnostic query contains a credential literal' >&2
  exit 1
fi

echo 'token presence diagnostic is redacted and value-free'
