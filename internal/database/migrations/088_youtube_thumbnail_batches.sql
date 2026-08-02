-- Durable, idempotent batch application for YouTube thumbnails.
-- The media asset is uploaded once by the SPA; the batch owns the
-- association and the per-video progress from that point onwards.

CREATE TABLE IF NOT EXISTS youtube_thumbnail_batches (
    id              TEXT PRIMARY KEY,
    workspace_id    BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    group_id        BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL,
    request_hash    BYTEA NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued', 'processing', 'completed', 'partial', 'failed')),
    total           INTEGER NOT NULL CHECK (total > 0),
    completed       INTEGER NOT NULL DEFAULT 0 CHECK (completed >= 0),
    failed          INTEGER NOT NULL DEFAULT 0 CHECK (failed >= 0),
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    CONSTRAINT youtube_thumbnail_batches_key_hash_len CHECK (octet_length(request_hash) = 32),
    CONSTRAINT youtube_thumbnail_batches_counts_valid CHECK (completed + failed <= total)
);

CREATE UNIQUE INDEX IF NOT EXISTS youtube_thumbnail_batches_workspace_key_uq
    ON youtube_thumbnail_batches (workspace_id, idempotency_key);

CREATE TABLE IF NOT EXISTS youtube_thumbnail_batch_items (
    id                   BIGSERIAL PRIMARY KEY,
    batch_id             TEXT NOT NULL REFERENCES youtube_thumbnail_batches(id) ON DELETE CASCADE,
    platform_account_id  BIGINT NOT NULL,
    youtube_video_id     TEXT NOT NULL,
    variant_id            TEXT NOT NULL,
    thumbnail_media_id   TEXT NOT NULL,
    title                TEXT NOT NULL DEFAULT '',
    description          TEXT NOT NULL DEFAULT '',
    tags                 JSONB NOT NULL DEFAULT '[]'::jsonb,
    status               TEXT NOT NULL DEFAULT 'queued'
                         CHECK (status IN ('queued', 'processing', 'completed', 'failed')),
    editor_session_id    TEXT,
    public_url           TEXT NOT NULL DEFAULT '',
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (batch_id, platform_account_id, youtube_video_id, variant_id)
);

CREATE INDEX IF NOT EXISTS youtube_thumbnail_batch_items_batch_idx
    ON youtube_thumbnail_batch_items (batch_id, id);
