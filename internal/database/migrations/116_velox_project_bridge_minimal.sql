-- 116_velox_project_bridge_minimal.sql
-- Reduce the bridge to the InstaEdit project <-> opaque editor project
-- relation. Existing application/channel/video records remain authoritative;
-- no context is copied into or administered by this table.

ALTER TABLE velox_project_bridges
    RENAME COLUMN velox_project_id TO external_project_id;

ALTER TABLE velox_project_bridges
    DROP CONSTRAINT IF EXISTS velox_project_bridges_channel_fk,
    DROP CONSTRAINT IF EXISTS velox_project_bridges_platform_account_fk,
    DROP CONSTRAINT IF EXISTS velox_project_bridges_context_ck,
    DROP CONSTRAINT IF EXISTS velox_project_bridges_no_context_without_account_ck;

DROP INDEX IF EXISTS velox_project_bridges_channel_idx;

ALTER TABLE velox_project_bridges
    DROP COLUMN IF EXISTS platform,
    DROP COLUMN IF EXISTS platform_account_id,
    DROP COLUMN IF EXISTS channel_id,
    DROP COLUMN IF EXISTS video_id,
    DROP COLUMN IF EXISTS language;

ALTER TABLE velox_project_bridges
    RENAME CONSTRAINT velox_project_bridges_velox_project_id_nonempty_ck TO velox_project_bridges_external_project_id_nonempty_ck;

ALTER TABLE velox_project_bridges
    RENAME CONSTRAINT velox_project_bridges_velox_project_uq TO velox_project_bridges_external_project_uq;
