-- 112_velox_project_bridges.sql
-- InstaEdit-owned project bridge to the separate Velox editor/render system.
-- No groups, channel lists, membership snapshots, or Velox workspace copies
-- are stored here. This migration is forward-only and replay-safe.

CREATE TABLE IF NOT EXISTS velox_project_bridges (
    project_id          TEXT PRIMARY KEY,
    workspace_id        BIGINT NOT NULL
                        REFERENCES workspaces(id) ON DELETE CASCADE,
    velox_project_id    TEXT NOT NULL,
    platform            TEXT,
    platform_account_id BIGINT,
    channel_id          TEXT,
    video_id            TEXT,
    language            TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT velox_project_bridges_project_fk
        FOREIGN KEY (workspace_id, project_id)
        REFERENCES thumbnail_projects (workspace_id, id)
        ON DELETE CASCADE,
    CONSTRAINT velox_project_bridges_channel_fk
        FOREIGN KEY (workspace_id, platform_account_id)
        REFERENCES workspace_channels (workspace_id, platform_account_id)
        ON DELETE RESTRICT,
    CONSTRAINT velox_project_bridges_platform_account_fk
        FOREIGN KEY (platform_account_id, platform)
        REFERENCES platform_accounts (id, platform)
        ON DELETE RESTRICT,
    CONSTRAINT velox_project_bridges_project_id_nonempty_ck
        CHECK (btrim(project_id) <> ''),
    CONSTRAINT velox_project_bridges_velox_project_id_nonempty_ck
        CHECK (btrim(velox_project_id) <> ''),
    CONSTRAINT velox_project_bridges_context_ck
        CHECK (
            platform_account_id IS NULL
            OR (platform IS NOT NULL AND btrim(platform) <> '')
        ),
    CONSTRAINT velox_project_bridges_no_context_without_account_ck
        CHECK (
            platform_account_id IS NOT NULL
            OR (
                platform IS NULL AND channel_id IS NULL AND
                video_id IS NULL AND language IS NULL
            )
        ),
    -- project_id is already unique because it is the primary key.
    CONSTRAINT velox_project_bridges_velox_project_uq
        UNIQUE (velox_project_id)
);

CREATE INDEX IF NOT EXISTS velox_project_bridges_workspace_idx
    ON velox_project_bridges (workspace_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS velox_project_bridges_channel_idx
    ON velox_project_bridges (workspace_id, platform_account_id)
    WHERE platform_account_id IS NOT NULL;
