-- Preparation worker lease + durable schedule-to-upload handoff.

ALTER TABLE content_schedules
    ADD COLUMN IF NOT EXISTS lease_owner TEXT,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS content_schedules_claim_idx
    ON content_schedules (prepare_at, next_attempt_at, lease_expires_at, id)
    WHERE status IN ('scheduled','preparing');

CREATE TABLE IF NOT EXISTS content_package_executions (
    id                  BIGSERIAL PRIMARY KEY,
    content_schedule_id BIGINT NOT NULL UNIQUE REFERENCES content_schedules(id) ON DELETE CASCADE,
    content_package_id  BIGINT NOT NULL REFERENCES content_packages(id) ON DELETE CASCADE,
    upload_job_id       BIGINT,
    status              TEXT NOT NULL DEFAULT 'preparing',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS upload_jobs_schedule_metadata_uq
    ON upload_jobs ((metadata->>'content_schedule_id'))
    WHERE metadata->>'content_schedule_id' IS NOT NULL;
