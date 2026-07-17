-- Migration 036: upload_jobs
-- Background queue for importing videos from public or authenticated Google Drive
-- and publishing them to linked social accounts. Jobs survive server restarts.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'upload_job_status') THEN
        CREATE TYPE upload_job_status AS ENUM ('pending', 'processing', 'completed', 'failed');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'upload_job_source') THEN
        CREATE TYPE upload_job_source AS ENUM ('public_drive', 'authenticated_drive');
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS upload_jobs (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    workspace_id BIGINT NOT NULL,
    source_type upload_job_source NOT NULL,
    source_id TEXT NOT NULL,
    drive_account_id BIGINT,
    title TEXT,
    caption TEXT,
    targets JSONB NOT NULL DEFAULT '[]'::jsonb,
    status upload_job_status NOT NULL DEFAULT 'pending',
    error_message TEXT,
    post_id BIGINT,
    asset_id TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_upload_jobs_pending ON upload_jobs(created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_upload_jobs_user ON upload_jobs(user_id, created_at DESC);
