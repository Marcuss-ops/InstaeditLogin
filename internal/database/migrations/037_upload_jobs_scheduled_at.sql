-- Migration 037: upload_jobs.scheduled_at
-- Adds a nullable scheduled_at column so batch / folder-import flows can
-- schedule posts to publish at staggered times (e.g. one Drive folder =
-- one video every 4-6 hours). The UploadWorker now propagates the
-- scheduled_at into the created post so the existing publish_worker's
-- `scheduled_at <= NOW()` clause handles the gating natively.
--
-- Nullable → existing single-file async imports behave identically
-- (NULL scheduled_at = publish immediately, same as before).

ALTER TABLE upload_jobs ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_upload_jobs_scheduled
    ON upload_jobs(scheduled_at)
    WHERE status = 'pending' AND scheduled_at IS NOT NULL;
