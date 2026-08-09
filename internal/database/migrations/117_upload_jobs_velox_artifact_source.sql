-- Migration 117: register the Velox artifact upload source.
--
-- The Velox delivery worker creates upload_jobs rows with source_type
-- `velox_artifact`. The Go model and source registry already define and
-- consume that value, so the PostgreSQL enum must expose it as well.
ALTER TYPE upload_job_source
    ADD VALUE IF NOT EXISTS 'velox_artifact';
