-- 012_add_post_status_enum.sql
-- Adds `waiting_provider`, `queued`, and `partially_published` to the
-- post_status enum. Runs BEFORE 012_async_threads_support.sql so the
-- ALTER TYPE ADD VALUE completes in its own transaction (PostgreSQL
-- forbids ALTER TYPE ADD VALUE in the same transaction as ALTER TABLE
-- on tables using that type — error 55P04).
--
-- 'queued' is required by ListPending and UpdateStatus (the worker uses
--   status='queued' literal in WHERE clauses — PostStatusScheduled is a
--   deprecated alias of PostStatusQueued, but the Go alias is in-memory
--   only; the DB enum needs the explicit value to accept new INSERTs).
-- 'partially_published' is used by UpdateStatus when some targets
--   succeed and others fail (Taglio 4.0).
-- 'waiting_provider' is used for async publish flows (Taglio 4.2).

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'waiting_provider'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'waiting_provider';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'queued'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'queued';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'partially_published'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'partially_published';
    END IF;
END $$;
