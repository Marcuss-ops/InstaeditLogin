-- 018_add_retrying_enum.sql
-- Adds 'retrying' to the post_status enum. Runs BEFORE
-- 018_publish_state_machine.sql so the ALTER TYPE ADD VALUE completes
-- in its own transaction (PostgreSQL forbids mixing ALTER TYPE ADD VALUE
-- with ALTER TABLE on tables using that type — error 55P04).
--
-- Lifecycle after this migration:
--   draft → queued → publishing → published
--                              → partially_published
--                              → waiting_provider
--                              → retrying ───→ (after next_attempt_at) ─→ publishing
--                              → failed

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'retrying'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'retrying';
    END IF;
END $$;
