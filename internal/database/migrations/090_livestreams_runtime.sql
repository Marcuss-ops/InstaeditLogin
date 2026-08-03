-- Livestream module — runtime tables: playlist items, per-run execution
-- records and the worker event log.
--
-- These tables are written exclusively by the livestream worker after a
-- configuration row (089_livestreams.sql) leaves 'draft'. The CRUD
-- endpoints never touch them directly:
--   * livestream_media_items — the ordered playlist the encoder
--     consumes. position 0 is the first media to play; gaps are
--     allowed so the worker can append and reorder.
--   * livestream_runs       — one row per execution attempt: the
--     single source of truth for encoder lifecycle (pid, heartbeat,
--     reconnect count) and terminal error details.
--   * livestream_events     — append-only audit trail of worker
--     transitions (broadcast created, ingest active, ...) backing the
--     /health and /events endpoints and post-mortem analysis.
--
-- Security: encoder_pid and error_message may surface in operator UI,
-- but the RTMP stream name / stream key are NEVER persisted here (see
-- 089). They are re-fetched from YouTube or held encrypted by the
-- worker in memory.

-- 089 shipped the configuration table with a plain BIGINT column (the
-- plan column was `platform_account_id BIGINT NOT NULL`). Declare the
-- FK constraint here idempotently so databases that already applied
-- 089 converge to the same schema as greenfield installs.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'livestreams_platform_account_fk'
    ) THEN
        ALTER TABLE livestreams
            ADD CONSTRAINT livestreams_platform_account_fk
            FOREIGN KEY (platform_account_id)
            REFERENCES platform_accounts(id) ON DELETE CASCADE;
    END IF;
END $$;

-- Ordered playlist consumed by the encoder. media_assets.id is UUID
-- (migration 006); the parent livestreams.id is TEXT (server-generated,
-- like youtube_thumbnail_batches). Deleting a livestream or a media
-- asset removes its playlist items.
--
-- Reordering caveat: UNIQUE (livestream_id, position) means an in-place
-- swap (pos 1 -> 2 while a row at 2 exists) raises a transient unique
-- violation. The worker must reorder via delete + reinsert (or a
-- DEFERRABLE unique) rather than row-by-row position updates.
CREATE TABLE IF NOT EXISTS livestream_media_items (
    id            BIGSERIAL PRIMARY KEY,
    livestream_id TEXT    NOT NULL REFERENCES livestreams(id) ON DELETE CASCADE,
    media_id      UUID    NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL CHECK (position >= 0),
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (livestream_id, position)
);

-- One row per execution attempt. status draws from the same state
-- alphabet as desired_state / actual_state (089) so the worker and the
-- health endpoints can reuse the shared state constants.
CREATE TABLE IF NOT EXISTS livestream_runs (
    id              TEXT PRIMARY KEY,
    livestream_id   TEXT NOT NULL REFERENCES livestreams(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'starting'
                    CHECK (status IN ('draft', 'preparing', 'ready', 'scheduled',
                        'starting', 'waiting_for_ingest', 'testing', 'live', 'degraded',
                        'reconnecting', 'stopping', 'completed', 'failed', 'cancelled')),
    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,
    encoder_pid     TEXT NOT NULL DEFAULT '',
    heartbeat_at    TIMESTAMPTZ,
    reconnect_count INTEGER NOT NULL DEFAULT 0 CHECK (reconnect_count >= 0),
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS livestream_runs_livestream_idx
    ON livestream_runs (livestream_id, created_at DESC);

-- Append-only audit trail of worker transitions. event_type is the
-- closed set from the livestream plan; payload carries context (error
-- codes, health metrics, YouTube resource ids) without widening the
-- schema.
CREATE TABLE IF NOT EXISTS livestream_events (
    id            BIGSERIAL PRIMARY KEY,
    livestream_id TEXT NOT NULL REFERENCES livestreams(id) ON DELETE CASCADE,
    run_id        TEXT REFERENCES livestream_runs(id) ON DELETE SET NULL,
    event_type    TEXT NOT NULL
                  CHECK (event_type IN ('broadcast_created', 'stream_created',
                      'broadcast_bound', 'encoder_started', 'ingest_active',
                      'broadcast_live', 'health_warning', 'encoder_restarted',
                      'broadcast_completed')),
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS livestream_events_livestream_idx
    ON livestream_events (livestream_id, id);
