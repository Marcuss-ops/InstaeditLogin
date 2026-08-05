# OAuth connection / platform account consistency diagnostic

Use `scripts/db/oauth-account-status-consistency-diagnostic.sql` to identify
state drift between `platform_accounts` and their referenced
`oauth_connections`. It is read-only and returns no credentials or personal
profile data.

## Safe execution

Use a password-free PostgreSQL URL with a protected `.pgpass` file (mode
`0600`). Never put a password-bearing DSN in process arguments, shell history,
logs, tickets, or chat:

```bash
PGPASSFILE="$HOME/.pgpass-instaedit" \
  psql "postgresql://db-host:5432/instaedit?sslmode=verify-full" \
  -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/oauth-account-status-consistency-diagnostic.sql
```

## Detected inconsistencies

The query classifies only technical state drift:

- `MISSING_OAUTH_CONNECTION` — account FK is null or points to no grant;
- `OWNER_MISMATCH` — account and grant have different `user_id` values;
- `PROVIDER_MISMATCH` — `platform_accounts.platform` differs from grant provider;
- `ACTIVE_ACCOUNT_NONACTIVE_GRANT` — account is active but grant is not;
- `NONACTIVE_ACCOUNT_ACTIVE_GRANT` — account is non-active while grant is active;
- `ACCOUNT_REAUTH_GRANT_ACTIVE` — account requires reauthorization but grant is active;
- `DISCONNECTED_ACCOUNT_GRANT_ACTIVE` — account is disconnected but grant remains active;
- `ACTIVE_ACCOUNT_GRANT_REAUTH_REQUIRED` — grant has a reauthorization timestamp while account remains active.

The result contains only grant/account IDs, platform/provider labels, status
values, inconsistency reason, and affected-row counts. It does not return
usernames, email addresses, provider resource IDs, access tokens, refresh
tokens, ciphertext, or decrypted values.

A healthy database returns zero rows. Run the static query/privacy check
without connecting to a database:

```bash
./scripts/db/test-oauth-account-status-consistency-diagnostic.sh
```
