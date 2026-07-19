-- 049a_upload_jobs_status_enum_terminal_states.sql
-- P1#4 — split scheduled_at into ingest_after + publish_at; rename
-- terminal states on upload_jobs to make lifecycle semantics explicit.
--
-- This file is a sibling to 049b (posts columns) and 049c (upload_jobs
-- columns). PostgreSQL forbids ALTER TYPE ADD VALUE inside the same
-- implicit transaction as ALTER TABLE on dependent tables (error
-- 55P04). Each .sql file in the migration runner executes in its own
-- implicit tx, so the enum work has to live alone — same pattern as
-- 045_upload_jobs_worker_pool_enum.sql + 046_upload_jobs_worker_pool.sql
-- and 047_upload_jobs_ready_to_publish_enum.sql.
--
-- What this file does:
--   * Adds 'ingest_completed' to upload_job_status.
--       Replaces the existing 'ready_to_publish' semantically:
--       ingest (Drive → S3) finished, asset is ready in storage,
--       stamp asset_id is set, but the publish call has NOT happened
--       yet. The publish pool's ClaimBatchForPublish predicate
--       (renamed in repo commit) now filters on
--       status='ingest_completed' AND (publish_at IS NULL OR
--       publish_at <= NOW()), so the publish_pool waits for
--       publish_at without holding a lease.
--   * Adds 'publish_completed' to upload_job_status.
--       Replaces 'completed' semantically: ALL post_targets have
--       reached terminal-success status (or no targets were created
--       for legacy single-file flows and the public Post.Publish()
--       succeeded). This is the row's final at-rest state; BOTH
--       ingest and publish are done.
--
-- Why we keep 'ready_to_publish' and 'completed' AS LEGACY enums:
--   Postgres < 18 does NOT support ALTER TYPE ... DROP VALUE
--   (PostgreSQL 18 added it). Keeping the old values is harmless
--   because NO Go code path writes them after migration 049c's
--   UPDATE relabels existing rows + the rename in commit's code:
--   - upload_job_repo.go::MarkIngested writes 'ingest_completed'.
--   - upload_job_repo.go::MarkCompleted  writes 'publish_completed'.
--   - Mock stores (mocks_test.go + drive_batch_test.go + handlers.go)
--     updated to write the new values too.
--   A future Postgres-18 migration can DROP them.
--
-- Idempotency (matches the 045/047 pattern): each ADD VALUE is
-- guarded by a pg_enum lookup so a DB that already has the value
-- (re-applied migration, partial recovery from a crashed run) skips
-- silently.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'ingest_completed'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'ingest_completed';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'publish_completed'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'publish_completed';
    END IF;
END
$$;
