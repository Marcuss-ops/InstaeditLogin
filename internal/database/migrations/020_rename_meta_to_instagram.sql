-- 020_rename_meta_to_instagram.sql
-- Renames all platform_accounts rows with platform='meta' to platform='instagram'.
-- The legacy "meta" provider has been split into separate Instagram, Facebook,
-- and Threads providers (Taglio 2.1, Taglio 4.4).
--
-- Idempotent: the DO-block guard ensures the migration only runs if there are
-- still rows with platform='meta'. On re-run, the guard condition evaluates to
-- false and the block exits with zero rows affected.
--
-- Collision handling: if a platform_accounts row with platform='instagram' AND
-- the SAME platform_user_id already exists, the collision is surfaced as a
-- unique-violation that rolls back the entire UPDATE. The operator is expected
-- to resolve the conflict manually (merge the accounts) before re-running.
-- No silent deletion, no token loss, no orphan targets.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_accounts WHERE platform = 'meta') THEN
        -- Verify no collisions before any mutation
        IF EXISTS (
            SELECT 1 FROM platform_accounts meta
            JOIN platform_accounts ig ON ig.platform = 'instagram'
                AND ig.platform_user_id = meta.platform_user_id
            WHERE meta.platform = 'meta'
        ) THEN
            RAISE EXCEPTION 'Migration 020 aborted: meta→instagram collision detected. '
                'A platform_accounts row with platform=''instagram'' and the same platform_user_id '
                'already exists. Resolve the conflict manually (merge the accounts) before re-running '
                'this migration. No data has been modified.';
        END IF;

        UPDATE platform_accounts
        SET platform = 'instagram'
        WHERE platform = 'meta';
    END IF;
END $$;
