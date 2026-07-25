CREATE TABLE IF NOT EXISTS youtube_video_edits (
    id TEXT PRIMARY KEY,
    workspace_id BIGINT NOT NULL,
    platform_account_id BIGINT NOT NULL,
    youtube_video_id TEXT NOT NULL,
    velox_project_id TEXT NOT NULL,
    source_thumbnail_url TEXT,
    thumbnail_media_id BIGINT,
    desired_privacy TEXT NOT NULL DEFAULT 'public',
    publish_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'editing',
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_youtube_video_edits_workspace
    ON youtube_video_edits(workspace_id);

CREATE INDEX IF NOT EXISTS idx_youtube_video_edits_account
    ON youtube_video_edits(platform_account_id);

CREATE INDEX IF NOT EXISTS idx_youtube_video_edits_status
    ON youtube_video_edits(status);
