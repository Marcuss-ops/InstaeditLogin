-- 115_velox_project_bridge_editor_metadata.sql
-- Make the bridge's editor-provider identity explicit and add the only
-- allowed operational metadata columns. The bridge remains an ownership
-- reference (InstaEdit project_id <-> opaque editor project); it is NOT a
-- replica of the editor: editor-internal document/render data stays in the
-- editor system and is forbidden here.

ALTER TABLE velox_project_bridges
    ADD COLUMN IF NOT EXISTS editor_provider TEXT NOT NULL DEFAULT 'velox';

ALTER TABLE velox_project_bridges
    ADD COLUMN IF NOT EXISTS editor_status TEXT;

ALTER TABLE velox_project_bridges
    ADD COLUMN IF NOT EXISTS last_editor_sync_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conrelid = 'velox_project_bridges'::regclass
           AND conname = 'velox_project_bridges_editor_provider_nonempty_ck'
    ) THEN
        ALTER TABLE velox_project_bridges
            ADD CONSTRAINT velox_project_bridges_editor_provider_nonempty_ck
            CHECK (btrim(editor_provider) <> '');
    END IF;
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conrelid = 'velox_project_bridges'::regclass
           AND conname = 'velox_project_bridges_editor_status_nonempty_ck'
    ) THEN
        ALTER TABLE velox_project_bridges
            ADD CONSTRAINT velox_project_bridges_editor_status_nonempty_ck
            CHECK (editor_status IS NULL OR btrim(editor_status) <> '');
    END IF;
END
$$;
