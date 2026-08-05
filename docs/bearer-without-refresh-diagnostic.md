# Bearer token without refresh-token diagnostic

Use `scripts/db/bearer-without-refresh-diagnostic.sql` to identify OAuth
bearer-token rows that do not have a non-empty encrypted refresh-token value.
The query is read-only and never returns or decrypts token/ciphertext data.

## Safe execution

Use a password-free PostgreSQL URL together with a protected `.pgpass` file
(mode `0600`). Do not put a password-bearing DSN in shell history, process
arguments, logs, tickets, or chat:

```bash
PGPASSFILE="$HOME/.pgpass-instaedit" \
  psql "postgresql://db-host:5432/instaedit?sslmode=verify-full" \
  -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/bearer-without-refresh-diagnostic.sql
```

## Returned fields

The result contains only technical identifiers, state, and counts:

- `oauth_connection_id`;
- whether the referenced OAuth connection exists;
- provider and OAuth connection status;
- number of affected bearer rows;
- number of linked platform accounts;
- number of linked active platform accounts.

It does **not** return usernames, email addresses, provider resource IDs,
access tokens, refresh tokens, encrypted bytes, or decrypted values.

A result with `oauth_connection_exists = false` identifies an orphaned token
binding. A result with `active_platform_account_count > 0` indicates an active platform
account whose bearer grant lacks a usable refresh token and requires priority
investigation. A healthy database returns zero rows.

The predicate treats both `NULL` and zero-length `encrypted_refresh_token`
values as missing:

```sql
COALESCE(octet_length(t.encrypted_refresh_token), 0) = 0
```

Run the static privacy/query-shape test without connecting to a database:

```bash
./scripts/db/test-bearer-without-refresh-diagnostic.sh
```
