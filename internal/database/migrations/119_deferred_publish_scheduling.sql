-- 119_deferred_publish_scheduling.sql
-- A future scheduled Drive import is prepared before its public publish
-- cursor. The upload job needs an explicit waiting-for-publish state.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'publish_scheduled'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'publish_scheduled';
    END IF;
END
$$;

COMMENT ON TYPE upload_job_status IS
    'Upload lifecycle includes deferred preparation: ingest_completed -> leased -> publish_scheduled -> publish_completed.';
