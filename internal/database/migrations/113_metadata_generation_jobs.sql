-- 113_metadata_generation_jobs.sql
-- Async NVIDIA metadata generation jobs: the POST /generate-metadata
-- endpoint enqueues a row here (status='queued') and returns 202
-- immediately; a background worker claims pending rows, calls
-- MetadataGenerator.Generate, and marks the result. The GET poll
-- endpoint reads back the status + result JSONB.
-- Forward-only and replay-safe.

CREATE TABLE IF NOT EXISTS metadata_generation_jobs (
    id               BIGSERIAL PRIMARY KEY,
    workspace_id     BIGINT NOT NULL
                     REFERENCES workspaces(id) ON DELETE CASCADE,
    velox_project_id TEXT   NOT NULL,
    prompt           TEXT   NOT NULL DEFAULT '',

    status           TEXT NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'processing', 'completed', 'failed')),

    result           JSONB,
    error_message    TEXT NOT NULL DEFAULT '',

    attempt_count    INT NOT NULL DEFAULT 0,
    max_attempts     INT NOT NULL DEFAULT 3,
    next_attempt_at  TIMESTAMPTZ,

    locked_by        TEXT,
    locked_at        TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_metadata_generation_jobs_claim
    ON metadata_generation_jobs (status, next_attempt_at)
    WHERE status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_metadata_generation_jobs_workspace
    ON metadata_generation_jobs (workspace_id, id);