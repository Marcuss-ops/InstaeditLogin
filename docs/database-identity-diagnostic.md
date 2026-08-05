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
