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

## Adding a New Migration

1. Create a new file named `005_description.sql`.
2. Make every statement idempotent (`IF NOT EXISTS`).
3. Keep ordering dependencies explicit (create tables before foreign keys).
