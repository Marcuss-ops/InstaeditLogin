-- 086_upload_jobs_metadata.sql
-- Preserve the complete external publication envelope while the durable
-- upload worker materialises the Post row.

ALTER TABLE upload_jobs
    ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb;
