# InstaEditLogin — Database Migrations

Migrations are stored in `internal/database/migrations/` and applied lexicographically by `database.Migrate()`.

| Order | File | Description |
|-------|------|-------------|
| 001 | `001_init.sql` | Creates `users`, `platform_accounts`, `tokens` and initial indexes |
| 002 | `002_add_refresh_token.sql` | Adds `encrypted_refresh_token` to `tokens` |
| 003 | `003_posts_workspaces.sql` | Adds `workspaces`, `posts`, `post_targets` and `post_status` enum |
| 004 | `004_composite_token_index.sql` | Adds composite index `tokens(platform_account_id, token_type)` |

## Running Migrations

Migrations run automatically when the server starts via `database.Migrate(db)`.

## Verifying OAuth migrations 084/085

The repository contains a read-only diagnostic for the shared OAuth grant
migrations:

```bash
# Inspect the real database with a password-free URL and a protected
# PGPASSFILE (mode 0600). Never put a password-bearing DSN in process args.
PGPASSFILE="$HOME/.pgpass-instaedit" \
  psql "postgresql://db-host:5432/instaedit?sslmode=verify-full" \
  -X -q -w -v ON_ERROR_STOP=1 \
  -f scripts/db/oauth-migrations-084-085-diagnostic.sql
```

The query checks:

- rows for `084_oauth_subject_shared_connections.sql` and
  `085_grant_scoped_tokens.sql` in `schema_migrations`;
- recorded checksum and `applied_at` metadata;
- the five indexes required by those migrations, including uniqueness;
- aggregate PASS/FAIL/MISSING statuses only.

It never reads token rows or ciphertext. Compare each `recorded_checksum` with
the repository file checksum calculated outside PostgreSQL:

```bash
sha256sum \\
  internal/database/migrations/084_oauth_subject_shared_connections.sql \\
  internal/database/migrations/085_grant_scoped_tokens.sql
```

Expected checksums for the current checkout:

```text
084: 8feaf557d0ddf611ba8b075ac2862a5ae14fdc63524aad805302f9301c28713a
085: 197d5322cc2aeedf0988a7273500bdc01bc4335a5e598242450261db290b3b4e
```

Run static privacy checks without a database connection:

```bash
make oauth-migrations-diagnostic-test
```

## Adding a New Migration

1. Create a new file named `005_description.sql`.
2. Make every statement idempotent (`IF NOT EXISTS`).
3. Keep ordering dependencies explicit (create tables before foreign keys).
