-- 129_agent_runs.sql
-- Agent Gateway run bookkeeping.
--
-- The Agent Gateway (a separate service) drives AI agents that prepare
-- videos, generate thumbnails, attach them and publish. The gateway
-- NEVER writes to these tables directly — it talks to InstaeditLogin
-- through its official REST API. These tables are the server-side
-- record of every agent run and every tool step, giving operators a
-- timeline (prepare_video → generate_thumbnail → attach_thumbnail →
-- check_publish → publish_video) and a precise failure location.
--
-- Design notes:
--   * agent_runs carries only REFERENCES (media_id, project_id,
--     session_id, export_id), never binary assets. Files live in the
--     Media Library; the run row keeps pointers.
--   * actor_key_id links to api_keys (the dedicated agent key that
--     anchored the run). NULL for runs initiated outside a key.
--   * Status/step-status are TEXT with CHECK constraints (matching the
--     repo's enum-via-CHECK convention) so the set stays closed and
--     forward-only.
--   * The gateway's idempotency_key is UNIQUE per workspace: a retried
--     run with the same key reuses the same row instead of duplicating.
--
-- Migration contract (same as every file in this dir):
--   * Forward-only; the runner wraps this body + the schema_migrations
--     INSERT in one transaction. Any failure rolls back everything.
--   * Every additive DDL is replay-safe (IF NOT EXISTS / DO blocks) so
--     the SQL stays safe for operator re-verification and test fixtures
--     that execute a body twice.
--   * Do not edit an applied migration: the runner verifies SHA-256
--     checksums and rejects modified history.

CREATE TABLE IF NOT EXISTS agent_runs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    workspace_id        BIGINT NOT NULL
                        REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_key_id        BIGINT
                        REFERENCES api_keys(id) ON DELETE SET NULL,

    goal                TEXT NOT NULL,

    youtube_video_id    TEXT,
    editor_session_id   UUID,

    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'running', 'waiting_approval',
                                          'completed', 'failed', 'cancelled')),

    current_step        TEXT,

    -- Idempotency: a retried run with the same key + workspace reuses
    -- the same row. The gateway generates the key before the first call.
    idempotency_key     TEXT NOT NULL,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ,

    CONSTRAINT agent_runs_goal_nonempty_ck
        CHECK (btrim(goal) <> ''),
    CONSTRAINT agent_runs_idempotency_key_nonempty_ck
        CHECK (btrim(idempotency_key) <> ''),
    CONSTRAINT agent_runs_workspace_idempotency_uq
        UNIQUE (workspace_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS agent_runs_workspace_created_idx
    ON agent_runs (workspace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS agent_runs_workspace_status_idx
    ON agent_runs (workspace_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS agent_runs_session_idx
    ON agent_runs (editor_session_id);

CREATE TABLE IF NOT EXISTS agent_run_steps (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id              UUID NOT NULL
                        REFERENCES agent_runs(id) ON DELETE CASCADE,

    tool_name           TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'running'
                        CHECK (status IN ('running', 'completed', 'failed')),

    -- References, not assets: tools persist files in the Media Library
    -- and store only the resulting ids here.
    input_json          JSONB NOT NULL DEFAULT '{}'::jsonb,
    output_json         JSONB NOT NULL DEFAULT '{}'::jsonb,

    error_code          TEXT,
    error_message       TEXT,

    started_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ,

    CONSTRAINT agent_run_steps_tool_nonempty_ck
        CHECK (btrim(tool_name) <> '')
);

CREATE INDEX IF NOT EXISTS agent_run_steps_run_started_idx
    ON agent_run_steps (run_id, started_at ASC);

CREATE INDEX IF NOT EXISTS agent_run_steps_run_status_idx
    ON agent_run_steps (run_id, status);
