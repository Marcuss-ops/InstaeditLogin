-- Livestream module — configuration + state-machine rows.
--
-- A row is a YouTube live configuration owned by a workspace and bound
-- to exactly one platform account (single-channel per live for the
-- first release). The state machine is split into desired_state
-- (operator intent) and actual_state (observed truth); the livestream
-- worker reconciles actual toward desired. Both are worker-owned after
-- creation — the CRUD endpoints create rows in 'draft' and never write
-- the state columns directly.

CREATE TABLE IF NOT EXISTS livestreams (
    id                   TEXT PRIMARY KEY,
    workspace_id         BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform_account_id  BIGINT NOT NULL,
    created_by           BIGINT NOT NULL,

    title                TEXT NOT NULL,
    description          TEXT NOT NULL DEFAULT '',
    privacy_status       TEXT NOT NULL
                         CHECK (privacy_status IN ('private', 'unlisted', 'public')),

    playback_mode        TEXT NOT NULL
                         CHECK (playback_mode IN ('loop_continuous', 'play_once')),
    schedule_type        TEXT NOT NULL
                         CHECK (schedule_type IN ('manual', 'now', 'scheduled', 'recurring')),
    scheduled_start_at   TIMESTAMPTZ,

    desired_state        TEXT NOT NULL DEFAULT 'draft'
                         CHECK (desired_state IN ('draft', 'preparing', 'ready', 'scheduled',
                             'starting', 'waiting_for_ingest', 'testing', 'live', 'degraded',
                             'reconnecting', 'stopping', 'completed', 'failed', 'cancelled')),
    actual_state         TEXT NOT NULL DEFAULT 'draft'
                         CHECK (actual_state IN ('draft', 'preparing', 'ready', 'scheduled',
                             'starting', 'waiting_for_ingest', 'testing', 'live', 'degraded',
                             'reconnecting', 'stopping', 'completed', 'failed', 'cancelled')),

    youtube_broadcast_id TEXT NOT NULL DEFAULT '',
    youtube_stream_id    TEXT NOT NULL DEFAULT '',

    resolution           TEXT NOT NULL DEFAULT '1080p30'
                         CHECK (resolution IN ('720p30', '1080p30')),
    frame_rate           INTEGER NOT NULL DEFAULT 30 CHECK (frame_rate = 30),

    auto_restart         BOOLEAN NOT NULL DEFAULT TRUE,

    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS livestreams_workspace_idx
    ON livestreams (workspace_id, updated_at DESC);
