-- 047_upload_jobs_ready_to_publish_enum.sql
-- P1 step 2 — ingest-pool / upload-pool split.
--
-- Adds 'ready_to_publish' to the upload_job_status enum. After the
-- ingest pool streams a Drive file to S3 and stamps asset_id, it
-- transitions the row from 'leased' (claimed by the ingest pool) to
-- 'ready_to_publish' (publish pool eligible). The publish pool's
-- ClaimBatchForPublish CTE then claims rows whose status =
-- 'ready_to_publish'.
--
-- Splits into its own file per the 045 pattern: Postgres forbids
-- ALTER TYPE ADD VALUE in the same implicit transaction as ALTER
-- TABLE on the dependent table (error 55P04). The migration runner
-- (internal/database/migrations.go) executes one .sql file per
-- implicit tx, so the enum extension lives alone. The companion
-- Go-side code lands in 047's follow-up commit along with the
-- ClaimBatchForPublish + MarkIngested repository methods.
--
-- Idempotency: pg_enum lookup guards the ADD VALUE so a DB that
-- already has the value (re-applied migration, partial recovery
-- from a crashed run) skips silently.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'ready_to_publish'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'ready_to_publish';
    END IF;
END
$$;
