# Database installation identity diagnostic

Use the read-only diagnostic before certifying a real PostgreSQL database or
connecting production OAuth channels. It verifies that the database singleton
row created by migration `096_system_installation_identity.sql` matches
`EXPECTED_DATABASE_INSTALLATION_UUID`.

## What it prints

The diagnostic prints only one of:

- `MATCH` — the database identity matches the configured expected identity;
- `MISMATCH` — the singleton exists but does not match;
- `MISSING` — the singleton row is absent.

It never prints the expected UUID, the database UUID, a password-bearing DSN,
token values, encrypted token bytes, or query results containing secrets.
`MISMATCH` and `MISSING` exit with status `3`; configuration, tool, connection,
and query errors exit with status `1`.

## Run it

From the repository root, using environment variables supplied by a secret
manager or a protected shell:

```bash
DATABASE_URL="$DATABASE_URL" \
EXPECTED_DATABASE_INSTALLATION_UUID="$EXPECTED_DATABASE_INSTALLATION_UUID" \
./scripts/db/installation-identity-diagnostic.sh
```

The equivalent Make target is:

```bash
make installation-identity-diagnostic
```

The script accepts `--url` and `--expected-installation-uuid` for operator
automation, but environment variables are preferable because command history
can persist arguments:

```bash
./scripts/db/installation-identity-diagnostic.sh \
  --url "$DATABASE_URL" \
  --expected-installation-uuid "$EXPECTED_DATABASE_INSTALLATION_UUID"
```

Do not paste real credentials into a terminal transcript, issue, chat, CI log,
or shell command committed to a script.

## Safety properties

- The SQL contains only a `SELECT` and reads `system_installation`.
- The query classifies the comparison and does not return either UUID.
- A password in a PostgreSQL URL is removed before invoking `psql` and is
  placed only in a temporary mode `0600` `.pgpass` file.
- Temporary connection and password files are deleted on exit.
- `psql` runs with `ON_ERROR_STOP=1`, no interactive password prompt, and a
  password-free connection URL.
- The tool is diagnostic only: it does not migrate, update, delete, lock, or
  repair database rows.

## Verification

Run the static/mocked test without a database connection:

```bash
./scripts/db/test-installation-identity-diagnostic.sh
# or
make installation-identity-diagnostic-test
```

A successful `MATCH` is only one part of the OAuth database certification.
Continue with `make oauth-preflight-check` to verify migrations `084/085`,
token uniqueness, encrypted refresh-token presence, and grant/account status
consistency. That preflight also reports aggregate results only; it does not
print token material.

## Inspect duplicate token groups safely

When the preflight reports duplicate grant/token-type groups, run the
read-only follow-up query with `psql` using credentials supplied by the
operator's protected environment:

```bash
# Use a password-free URL and a protected .pgpass (mode 0600).
# Never put a password-bearing DATABASE_URL in this command.
PGPASSFILE="$HOME/.pgpass-instaedit" \
  psql "postgresql://db-host:5432/instaedit?sslmode=verify-full" \
  -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/duplicate-token-diagnostic.sql
```

The result contains only (the connection URL above is illustrative; use the
operator's protected host/database values without embedding a password):

- `oauth_connection_id`;
- `token_type`;
- `token_row_count`.

A healthy database returns zero rows. If `oauth_connection_id` is `NULL`, the
row represents orphaned/unbound token records grouped together by PostgreSQL;
this is a binding anomaly and must not be treated as a valid grant.
 The query never selects, decrypts, or
prints access tokens, refresh tokens, encrypted ciphertext, usernames, or
connection secrets. Do not paste the DSN or query output containing production
identifiers into tickets or chat. Run its static privacy checks with:

```bash
make duplicate-token-diagnostic-test
```
