-- 093_livestreams_persistent_runs.sql
-- Persistent livestream execution model.
--
-- 089-092 are already recorded by deployed databases and therefore are
-- intentionally not rewritten. Migration 090 created an initial runtime
-- shape; this migration converges it in place while preserving existing
-- rows. The migration runner wraps each migration in one transaction, so
-- any error rolls back every schema/data change made below.
--
-- The legacy livestreams.youtube_* columns are retained for one rollout
-- cycle. Existing callers still read them; the worker will progressively
-- move ownership to livestream_runs before a later cleanup migration drops
-- those deprecated mirrors.

-- ---------------------------------------------------------------------
-- Runs: one durable row per execution of a reusable configuration.
-- ---------------------------------------------------------------------

ALTER TABLE livestream_runs
    ADD COLUMN IF NOT EXISTS platform_account_id BIGINT,
    ADD COLUMN IF NOT EXISTS generation BIGINT,
    ADD COLUMN IF NOT EXISTS youtube_broadcast_id TEXT,
    ADD COLUMN IF NOT EXISTS youtube_stream_id TEXT,
    ADD COLUMN IF NOT EXISTS configuration_version BIGINT,
    ADD COLUMN IF NOT EXISTS worker_id TEXT,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_frame_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS live_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempt_count INTEGER DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error_code TEXT,
    ADD COLUMN IF NOT EXISTS last_error_message TEXT,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;

-- 090 used the shared state alphabet but omitted preflighting. Keep all
-- existing labels valid and add the preflight checkpoint required by the
-- persistent reconciler.
ALTER TABLE livestream_runs
    DROP CONSTRAINT IF EXISTS livestream_runs_status_check;
ALTER TABLE livestream_runs
    ADD CONSTRAINT livestream_runs_status_check
    CHECK (status IN ('draft', 'preflighting', 'preparing', 'ready', 'scheduled',
        'starting', 'waiting_for_ingest', 'testing', 'live', 'degraded',
        'reconnecting', 'stopping', 'completed', 'failed', 'cancelled'));

-- Backfill the new run identity from the configuration that 090 already
-- required. ROW_NUMBER is deterministic for the pre-existing rows and
-- makes generation unique per reusable livestream.
UPDATE livestream_runs AS r
   SET platform_account_id = l.platform_account_id
  FROM livestreams AS l
 WHERE r.livestream_id = l.id
   AND r.platform_account_id IS NULL;

WITH numbered AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY livestream_id
               ORDER BY created_at ASC, id ASC
           ) AS next_generation
      FROM livestream_runs
)
UPDATE livestream_runs AS r
   SET generation = numbered.next_generation
  FROM numbered
 WHERE r.id = numbered.id
   AND r.generation IS NULL;

UPDATE livestream_runs
   SET configuration_version = 1
 WHERE configuration_version IS NULL;

UPDATE livestream_runs
   SET attempt_count = 0
 WHERE attempt_count IS NULL;

UPDATE livestream_runs
   SET last_error_code = COALESCE(error_code, '')
 WHERE last_error_code IS NULL;

UPDATE livestream_runs
   SET last_error_message = COALESCE(error_message, '')
 WHERE last_error_message IS NULL;

UPDATE livestream_runs
   SET updated_at = COALESCE(created_at, NOW())
 WHERE updated_at IS NULL;

-- 090 did not enforce one active run per channel. Preserve every row,
-- but deterministically retain only the newest active run before creating
-- the unique partial index; older conflicting attempts become terminal
-- history rather than making the deployment fail halfway through.
WITH ranked_active AS (
    SELECT r.id,
           ROW_NUMBER() OVER (
               PARTITION BY r.platform_account_id
               ORDER BY r.created_at DESC, r.id DESC
           ) AS active_rank
      FROM livestream_runs AS r
     WHERE r.status IN (
         'preparing', 'ready', 'starting', 'waiting_for_ingest',
         'testing', 'live', 'degraded', 'reconnecting', 'stopping'
     )
)
UPDATE livestream_runs AS r
   SET status = 'failed',
       error_code = CASE WHEN COALESCE(r.error_code, '') = ''
                         THEN 'MIGRATION_ACTIVE_RUN_CONFLICT'
                         ELSE r.error_code END,
       error_message = CASE WHEN COALESCE(r.error_message, '') = ''
                            THEN 'Superseded by a newer active livestream run during migration 093'
                            ELSE r.error_message END,
       last_error_code = CASE WHEN COALESCE(r.last_error_code, '') = ''
                              THEN 'MIGRATION_ACTIVE_RUN_CONFLICT'
                              ELSE r.last_error_code END,
       last_error_message = CASE WHEN COALESCE(r.last_error_message, '') = ''
                                 THEN 'Superseded by a newer active livestream run during migration 093'
                                 ELSE r.last_error_message END
  FROM ranked_active AS a
 WHERE r.id = a.id
   AND a.active_rank > 1;

-- A legacy configuration could have YouTube IDs but no run-owned copies.
-- Put those IDs on the newest run only; this avoids creating duplicate
-- resources while retaining the old configuration mirrors for rollout
-- compatibility. The partial unique indexes below catch any pre-existing
-- conflict rather than silently choosing between two runs.
WITH newest AS (
    SELECT DISTINCT ON (r.livestream_id)
           r.id, r.livestream_id
      FROM livestream_runs AS r
     ORDER BY r.livestream_id, r.created_at DESC, r.id DESC
)
UPDATE livestream_runs AS r
   SET youtube_broadcast_id = COALESCE(r.youtube_broadcast_id, NULLIF(l.youtube_broadcast_id, '')),
       youtube_stream_id = COALESCE(r.youtube_stream_id, NULLIF(l.youtube_stream_id, ''))
  FROM newest, livestreams AS l
 WHERE r.id = newest.id
   AND l.id = newest.livestream_id
   AND (r.youtube_broadcast_id IS NULL OR r.youtube_stream_id IS NULL)
   AND (NULLIF(l.youtube_broadcast_id, '') IS NOT NULL
        OR NULLIF(l.youtube_stream_id, '') IS NOT NULL);

ALTER TABLE livestream_runs
    ALTER COLUMN platform_account_id SET NOT NULL,
    ALTER COLUMN generation SET NOT NULL,
    ALTER COLUMN configuration_version SET NOT NULL,
    ALTER COLUMN attempt_count SET DEFAULT 0,
    ALTER COLUMN attempt_count SET NOT NULL,
    ALTER COLUMN last_error_code SET DEFAULT '',
    ALTER COLUMN last_error_code SET NOT NULL,
    ALTER COLUMN last_error_message SET DEFAULT '',
    ALTER COLUMN last_error_message SET NOT NULL,
    ALTER COLUMN updated_at SET DEFAULT NOW(),
    ALTER COLUMN updated_at SET NOT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestream_runs_platform_account_fk'
           AND conrelid = 'livestream_runs'::regclass
    ) THEN
        ALTER TABLE livestream_runs
            ADD CONSTRAINT livestream_runs_platform_account_fk
            FOREIGN KEY (platform_account_id)
            REFERENCES platform_accounts(id) ON DELETE CASCADE;
    END IF;
END $$;

-- Existing rows from 090 already have reconnect_count >= 0. These checks
-- make the new retry counters enforceable at the database boundary.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestream_runs_generation_positive_ck'
           AND conrelid = 'livestream_runs'::regclass
    ) THEN
        ALTER TABLE livestream_runs
            ADD CONSTRAINT livestream_runs_generation_positive_ck
            CHECK (generation > 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestream_runs_configuration_version_positive_ck'
           AND conrelid = 'livestream_runs'::regclass
    ) THEN
        ALTER TABLE livestream_runs
            ADD CONSTRAINT livestream_runs_configuration_version_positive_ck
            CHECK (configuration_version > 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestream_runs_attempt_count_nonnegative_ck'
           AND conrelid = 'livestream_runs'::regclass
    ) THEN
        ALTER TABLE livestream_runs
            ADD CONSTRAINT livestream_runs_attempt_count_nonnegative_ck
            CHECK (attempt_count >= 0);
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS livestream_runs_generation_uq
    ON livestream_runs (livestream_id, generation);

CREATE UNIQUE INDEX IF NOT EXISTS livestream_runs_broadcast_uq
    ON livestream_runs (youtube_broadcast_id)
    WHERE youtube_broadcast_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS livestream_runs_stream_uq
    ON livestream_runs (youtube_stream_id)
    WHERE youtube_stream_id IS NOT NULL;

-- A channel can have only one run in any non-terminal operational state.
-- The explicit state list is deliberately closed: completed, failed,
-- cancelled and draft runs remain historical/non-active records.
CREATE UNIQUE INDEX IF NOT EXISTS livestream_one_active_run_per_channel
    ON livestream_runs (platform_account_id)
    WHERE status IN (
        'preparing', 'ready', 'starting', 'waiting_for_ingest',
        'testing', 'live', 'degraded', 'reconnecting', 'stopping'
    );

CREATE INDEX IF NOT EXISTS livestream_runs_channel_status_idx
    ON livestream_runs (platform_account_id, status, updated_at DESC);

-- ---------------------------------------------------------------------
-- Playlist: retain UUID media IDs because media_assets.id is UUID. The
-- existing 090 foreign key is therefore preserved while the requested
-- duration/enabled metadata is added. The item ID is converted from the
-- old BIGSERIAL representation to text so it can be generated by the
-- application without exposing a sequence as part of the API contract.
-- ---------------------------------------------------------------------

ALTER TABLE livestream_media_items
    ALTER COLUMN id DROP DEFAULT,
    ALTER COLUMN id TYPE TEXT USING id::text,
    ADD COLUMN IF NOT EXISTS duration_ms BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestream_media_items_duration_nonnegative_ck'
           AND conrelid = 'livestream_media_items'::regclass
    ) THEN
        ALTER TABLE livestream_media_items
            ADD CONSTRAINT livestream_media_items_duration_nonnegative_ck
            CHECK (duration_ms IS NULL OR duration_ms >= 0);
    END IF;
END $$;

-- The old BIGSERIAL sequence is intentionally retained as an unused,
-- owned object for this rollout. Removing ownership/sequence is deferred
-- to a later cleanup migration so this migration never fails on a database
-- where another object still references the sequence.

CREATE INDEX IF NOT EXISTS livestream_media_items_order_idx
    ON livestream_media_items (livestream_id, position)
    WHERE enabled;

-- ---------------------------------------------------------------------
-- Events: expand the append-only event vocabulary while retaining the
-- original names from 090 for already-recorded event rows.
-- ---------------------------------------------------------------------

ALTER TABLE livestream_events
    ADD COLUMN IF NOT EXISTS severity TEXT NOT NULL DEFAULT 'info';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'livestream_events_severity_check'
           AND conrelid = 'livestream_events'::regclass
    ) THEN
        ALTER TABLE livestream_events
            ADD CONSTRAINT livestream_events_severity_check
            CHECK (severity IN ('info', 'warning', 'error', 'critical'));
    END IF;
END $$;

ALTER TABLE livestream_events
    DROP CONSTRAINT IF EXISTS livestream_events_event_type_check;

ALTER TABLE livestream_events
    ADD CONSTRAINT livestream_events_event_type_check
    CHECK (event_type IN (
        'run_created', 'run_leased', 'oauth_refreshed',
        'stream_created', 'youtube_stream_created',
        'broadcast_created', 'youtube_broadcast_created',
        'broadcast_bound', 'encoder_started', 'ingest_active',
        'broadcast_live', 'health_warning', 'health_degraded',
        'encoder_restarted', 'broadcast_completed',
        'run_failed', 'heartbeat_lost'
    ));

CREATE INDEX IF NOT EXISTS livestream_events_run_idx
    ON livestream_events (run_id, id);

CREATE INDEX IF NOT EXISTS livestream_events_type_created_idx
    ON livestream_events (event_type, created_at DESC);

-- ---------------------------------------------------------------------
-- Secrets: the ingest URL/name is encrypted at rest and is never part of
-- livestream_runs, livestream_events, frontend JSON, or log payloads.
-- ---------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS livestream_run_secrets (
    run_id              TEXT PRIMARY KEY
                        REFERENCES livestream_runs(id) ON DELETE CASCADE,
    encrypted_ingest_url BYTEA NOT NULL,
    encryption_key_id   TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Keep the event table append-only by contract (there is intentionally no
-- UPDATE/DELETE trigger here; worker repositories enforce the write path).
