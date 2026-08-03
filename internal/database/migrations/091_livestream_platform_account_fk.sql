-- Livestream account integrity follow-up.
--
-- Migration 089 created the livestream configuration table before the
-- account binding constraint was finalized. Keep this additive and
-- idempotent rather than rewriting 089, which may already be recorded
-- by an installation's migration runner.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'livestreams_platform_account_id_fkey'
          AND conrelid = 'livestreams'::regclass
    ) THEN
        ALTER TABLE livestreams
            ADD CONSTRAINT livestreams_platform_account_id_fkey
            FOREIGN KEY (platform_account_id)
            REFERENCES platform_accounts(id)
            ON DELETE CASCADE;
    END IF;
END $$;
