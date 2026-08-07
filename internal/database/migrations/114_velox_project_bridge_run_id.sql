-- 114_velox_project_bridge_run_id.sql
-- Persist the migration execution that created a bridge so rollback can
-- identify only rows from that run. Existing bridges remain valid and keep a
-- NULL marker because they were not created by this migration.

ALTER TABLE velox_project_bridges
    ADD COLUMN IF NOT EXISTS migration_run_id TEXT;

CREATE INDEX IF NOT EXISTS velox_project_bridges_migration_run_idx
    ON velox_project_bridges (migration_run_id)
    WHERE migration_run_id IS NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM pg_constraint
         WHERE conrelid = 'velox_project_bridges'::regclass
           AND conname = 'velox_project_bridges_migration_run_id_nonempty_ck'
    ) THEN
        ALTER TABLE velox_project_bridges
            ADD CONSTRAINT velox_project_bridges_migration_run_id_nonempty_ck
            CHECK (migration_run_id IS NULL OR btrim(migration_run_id) <> '');
    END IF;
END
$$;
