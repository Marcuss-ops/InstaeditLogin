-- =============================================================================
-- 053_oauth_tokens_retargeted.sql — P0#3 vault re-key
-- =============================================================================
-- Why this migration exists:
--
-- The credential vault (internal/credentials/vault.go) is the single source
-- of truth for OAuth token encryption + persistence. Its TokenStore
-- contract currently keys reads/writes/deletes by platform_account_id,
-- but the OAuth grant lineage (assembled in migration 043 via the
-- oauth_connections table) is the more correct anchor:
--
--   - platform_account_id → per-platform user identity row
--   - oauth_connection_id → the OAuth grant the user authorised
--
-- The eval plan (P0#3) requires the vault's storage layer to key by
-- oauth_connection_id so future grants outlast their bound
-- platform_accounts (e.g. on user disconnect, the historical token
-- lineages remain attributable to the original grant).
--
-- What this migration does:
--
--   1. Adds tokens.oauth_connection_id as a nullable BIGINT column.
--   2. Backfills the column from platform_accounts.oauth_connection_id
--      for every existing row (idempotent — `t.oauth_connection_id IS
--      NULL` guard makes a re-execution of the runner a no-op).
--   3. Drops orphan token rows whose owning platform_account has no
--      oauth_connection_id (only possible for pre-043 attach rows
--      that somehow survived in tokens after their backfill joined
--      against an unmigrated/purged platform_account — pre-043
--      attaches were never part of an oauth_connections lineage so
--      these rows are unrecoverable; the user re-auths).
--   4. SET NOT NULL on tokens.oauth_connection_id once orphans are
--      cleared — makes the schema rigorous and lets the vault's
--      SQL use the column directly without null-handling branches.
--   5. Adds the FK constraint to oauth_connections(id) with ON DELETE
--      CASCADE — when a grant is dropped, its tokens drop with it.
--   6. Adds an index on the new column for the vault's read path
--      performance.
--
-- Idempotency: every DDL has IF NOT EXISTS / DO-block guards. The
-- runner (internal/database/migrations.go::RunMigrations) executes
-- every .sql on every startup without a schema_migrations table,
-- so the migration MUST be idempotent on its own.
--
-- Encrypted-ciphertext preservation: NO touch to existing
-- tokens.encrypted_token or tokens.encrypted_refresh_token bytes.
-- The crypto keys + envelope format are unchanged. The vault
-- signature update (read/write via oauth_connection_id instead of
-- platform_account_id) is purely a WHERE-clause change at the SQL
-- layer and a derived-key lookup at the Go layer.
-- =============================================================================

-- New lookup index. Sibling index idx_tokens_platform_account_id is
-- left in place so existing admin/tooling queries on the legacy key
-- still scan reasonably.
CREATE INDEX IF NOT EXISTS idx_tokens_oauth_connection_id
    ON tokens (oauth_connection_id);

-- 1. Add the column nullable (idempotent ADD COLUMN IF NOT EXISTS).
ALTER TABLE tokens
    ADD COLUMN IF NOT EXISTS oauth_connection_id BIGINT;

-- 2. Backfill from the platform_accounts reference table — wrapped
--    in a DO block so the migration is ORDER-INDEPENDENT with respect
--    to migration 043. If 043 hasn't applied yet, the
--    platform_accounts.oauth_connection_id column doesn't exist and
--    the unconditional UPDATE would error with
--    `42703: column "oauth_connection_id" does not exist`. The IF
--    EXISTS guard turns the backfill into an idempotent no-op in that
--    case (043's own backfill will populate the column afterward, and
--    consumer migrations that run later can re-stamp the linkage via
--    a follow-up backfill — kept out of 053 to stay focused on the
--    tokens-table re-key). A second run on a fully-migrated DB is
--    also a no-op thanks to the inner `t.oauth_connection_id IS NULL`
--    guards.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'platform_accounts'
          AND column_name = 'oauth_connection_id'
    ) THEN
        UPDATE tokens t
        SET oauth_connection_id = pa.oauth_connection_id
        FROM platform_accounts pa
        WHERE t.platform_account_id = pa.id
          AND pa.oauth_connection_id IS NOT NULL
          AND t.oauth_connection_id IS NULL;

        -- Drop orphan rows whose owning platform_account has no
        -- oauth_connection_id (only possible for a pre-043-attach
        -- platform_account that joined via platform_user_id but
        -- never was linked to an oauth_connections row). The vault
        -- cannot read these rows after the SIGNATURE update, so they
        -- are unrecoverable; the user re-auths to recover.
        DELETE FROM tokens WHERE oauth_connection_id IS NULL;
    END IF;
END $$;

-- 4. Enforce NOT NULL once orphans are gone. Safe to retry: a second
--    run finds the column already NOT NULL (PostgreSQL stores the
--    not-null flag in pg_attribute — SET NOT NULL on an already
--    NOT NULL column is silently a no-op).
ALTER TABLE tokens ALTER COLUMN oauth_connection_id SET NOT NULL;

-- 5. FK + CASCADE. Wrapped in a DO block to keep the migration
--    idempotent on re-run (the runner has no schema_migrations
--    table). ON DELETE CASCADE mirrors the cascade policy on
--    platform_accounts.oauth_connection_id — when a grant is
--    revoked, its historical tokens drop with it.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tokens_oauth_connection_id_fkey'
    ) THEN
        ALTER TABLE tokens
            ADD CONSTRAINT tokens_oauth_connection_id_fkey
            FOREIGN KEY (oauth_connection_id) REFERENCES oauth_connections(id) ON DELETE CASCADE;
    END IF;
END $$;
