-- 050_import_batches.sql
-- P1#7 — async folder-batch producer/consumer.
--
-- The previous Drive batch endpoint (pkg/api/drive_batch.go L499) ran
-- Drive pagination + upload_job creation INLINE in the HTTP request
-- thread, blocking the response for ~30s per folder listing. P1#7
-- refactors that handler into a producer/consumer: the API inserts
-- one row into import_batches and returns {batch_id, status:"queued"}
-- immediately; a background folder crawler (internal/worker.
-- drive_batch_crawler.go) claims batches via FOR UPDATE SKIP LOCKED
-- and walks the page_token cursor itself.
--
-- This file:
--   * Add import_batches header table: source / targets / schedule /
--     result metadata live HERE. upload_jobs.batch_id is a UUID FK so
--     one query joins both sides cleanly.
--   * Add upload_jobs.batch_id UUID NULL FK + partial index on
--     (batch_id) WHERE batch_id IS NOT NULL for the dashboard
--     "by-batch" aggregation.
--   * Future forward-compatible: source JSONB has a `provider` field
--     so a Dropbox endpoint can register a different source type.
--   * workspace_channels resolution uses `group_name` (string) per
--     migration 044 schema; a future migration will add a stable
--     UUID column. The handler-side validation looked up groups via
--     this string column.
--
-- Idempotency: every CREATE/ALTER uses IF NOT EXISTS so a re-apply
-- on a DB that already migrated is a no-op.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS import_batches (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id         BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    -- Source triple: provider discriminates (today: "google_drive"),
    -- drive_account_id is optional (zero = public folder via API
    -- key), folder_id is the source-locator.
    source_provider      TEXT        NOT NULL,
    source_drive_account BIGINT,                       -- nullable: public folder
    source_folder_id     TEXT        NOT NULL,

    -- target_account_ids[] is the resolved final list (post-group
    -- expansion). Original request may carry either target_account_ids
    -- or target_group_id, NOT both — XOR is enforced handler-side.
    target_account_ids    BIGINT[]    NOT NULL,
    target_group_name    TEXT,                        -- ad-hoc reference (D4.b)

    -- publish_schedule envelope. Persisted verbatim so the handler can
    -- reconstruct the response on idempotency replay without re-computing.
    publish_schedule_start_at TIMESTAMPTZ NOT NULL,
    publish_schedule_min_gap_seconds INTEGER NOT NULL,
    publish_schedule_max_gap_seconds INTEGER NOT NULL,

    -- Lifecycle: queued → processing → completed / failed / dead_letter.
    -- Partial-progress state lives in cursor_page_token (NULL once
    -- crawl is finished).
    status               TEXT        NOT NULL DEFAULT 'queued',
    cursor_page_token    TEXT,                        -- Drive nextPageToken
    cursor_indexed_count INTEGER     NOT NULL DEFAULT 0,
    schedule_clamped     BOOLEAN     NOT NULL DEFAULT FALSE,
    schedule_clamp_reason TEXT,                       -- human-readable why
    warnings             JSONB,                       -- array of strings
    error_message        TEXT,
    created_count        INTEGER     NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at         TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_import_batches_user_status
    ON import_batches (user_id, status, created_at DESC)
    WHERE status IN ('queued', 'processing', 'failed');

CREATE INDEX IF NOT EXISTS idx_import_batches_workspace
    ON import_batches (workspace_id, created_at DESC);

ALTER TABLE upload_jobs
    ADD COLUMN IF NOT EXISTS batch_id UUID REFERENCES import_batches(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_upload_jobs_batch_id
    ON upload_jobs (batch_id)
    WHERE batch_id IS NOT NULL;

COMMENT ON TABLE import_batches IS 'P1#7 async folder-batch header. Source + targets + schedule live here; upload_jobs.batch_id FK joins per-file ingest rows for "by-batch" aggregation.';
COMMENT ON COLUMN import_batches.source_provider               IS 'Today: "google_drive". Forward-compatible enum-style discriminator for future Dropbox/S3 sources.';
COMMENT ON COLUMN import_batches.target_group_name            IS 'Ad-hoc reference to workspace_channels.group_name (migration 044). A future migration will add a stable UUID column + FK. D4.b.';
COMMENT ON COLUMN import_batches.cursor_page_token            IS 'Drive nextPageToken for partial-progress checkpointing. NULL = crawl finished. Updated per page so a crawler crash picks up where it left off.';
COMMENT ON COLUMN import_batches.schedule_clamped             IS 'True when the cumulative publish schedule push would have exceeded the allowed horizon. Surfaced in response per user spec.';
COMMENT ON COLUMN import_batches.schedule_clamp_reason       IS 'Operator-readable string explaining WHY the clamp fired (e.g. "horizon=72d, max=60d, clamped at 60d").';
