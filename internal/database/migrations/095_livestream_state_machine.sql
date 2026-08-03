-- 095_livestream_state_machine.sql
-- Separate operator intent versioning from per-run observed execution.
-- Migration 094 is owned by the thumbnail module; this migration uses the
-- next available sequence and does not rewrite any applied migration.

ALTER TABLE livestreams
    ADD COLUMN IF NOT EXISTS desired_generation BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS configuration_version BIGINT NOT NULL DEFAULT 1;

-- 089 used the observed-state alphabet for desired_state. Drop the old
-- constraint before converting existing values to operator intent; unknown
-- legacy values fail closed to draft.
ALTER TABLE livestreams
    DROP CONSTRAINT IF EXISTS livestreams_desired_state_check;

UPDATE livestreams
   SET desired_state = CASE desired_state
       WHEN 'draft' THEN 'draft'
       WHEN 'preparing' THEN 'prepared'
       WHEN 'ready' THEN 'prepared'
       WHEN 'scheduled' THEN 'prepared'
       WHEN 'starting' THEN 'running'
       WHEN 'waiting_for_ingest' THEN 'running'
       WHEN 'testing' THEN 'running'
       WHEN 'live' THEN 'running'
       WHEN 'degraded' THEN 'running'
       WHEN 'reconnecting' THEN 'running'
       WHEN 'stopping' THEN 'running'
       WHEN 'completed' THEN 'stopped'
       WHEN 'failed' THEN 'stopped'
       WHEN 'cancelled' THEN 'cancelled'
       ELSE 'draft'
   END;

ALTER TABLE livestreams
    ADD CONSTRAINT livestreams_desired_state_check
    CHECK (desired_state IN ('draft', 'prepared', 'running', 'stopped', 'cancelled'));

-- 093's runtime check omitted the preflight checkpoint even though the
-- reconciler uses it. Converge the check while this state-machine migration
-- is applied.
ALTER TABLE livestream_runs
    DROP CONSTRAINT IF EXISTS livestream_runs_status_check;
ALTER TABLE livestream_runs
    ADD CONSTRAINT livestream_runs_status_check
    CHECK (status IN ('draft', 'preflighting', 'preparing', 'ready', 'scheduled',
        'starting', 'waiting_for_ingest', 'testing', 'live', 'degraded',
        'reconnecting', 'stopping', 'completed', 'failed', 'cancelled'));

-- Existing runs are historical evidence of the latest requested execution.
-- Preserve that information when converging an already-populated database.
UPDATE livestreams AS l
   SET desired_generation = GREATEST(
           1,
           COALESCE((SELECT MAX(r.generation)
                       FROM livestream_runs AS r
                      WHERE r.livestream_id = l.id), 1)
       ),
       configuration_version = GREATEST(
           1,
           COALESCE((SELECT MAX(r.configuration_version)
                       FROM livestream_runs AS r
                      WHERE r.livestream_id = l.id), 1)
       );

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestreams_desired_generation_positive_ck'
           AND conrelid = 'livestreams'::regclass
    ) THEN
        ALTER TABLE livestreams
            ADD CONSTRAINT livestreams_desired_generation_positive_ck
            CHECK (desired_generation > 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestreams_configuration_version_positive_ck'
           AND conrelid = 'livestreams'::regclass
    ) THEN
        ALTER TABLE livestreams
            ADD CONSTRAINT livestreams_configuration_version_positive_ck
            CHECK (configuration_version > 0);
    END IF;
END $$;
