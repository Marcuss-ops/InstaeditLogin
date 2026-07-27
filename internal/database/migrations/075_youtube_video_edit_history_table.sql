-- =====================================================================
-- Migration 075: youtube_video_edit_history — append-only audit ledger
-- per (workspace, video_edit) showing before/after of every meaningful
-- YouTube thumbnail publishing event. One row per stamped event.
--
-- Append-only by convention: never UPDATE or DELETE rows. The DDL is
-- still PL/pgSQL-free (a forward-only additive table + 3 indexes) so a
-- DOWN migration isn't required — drop the table manually if a rollback
-- is needed (it is not part of the standard rollback harness).
--
-- Economics: every before_* column is NULL when the field was
-- unchanged in this event (asymmetric diff format — only the
-- fields someone touched are present). This keeps the row width
-- well under 2KB at typical edit cadence. The `before_*` and
-- `after_*` columns are *not* textually normalized; whatever
-- arrived from the publish orchestrator / draft API / drift
-- reconciler lands here verbatim.
--
-- Idempotency on retry: partial UNIQUE (video_edit_id, changed_at,
-- actor_type) makes a duplicate history INSERT (worker retry after
-- crash before commit + the next attempt) raise a 23505 we swallow
-- at the Go layer — see youtube_video_edit_history_repo.go Insert.
-- =====================================================================

CREATE TABLE IF NOT EXISTS youtube_video_edit_history (
    id                              BIGSERIAL PRIMARY KEY,

    -- Foreign key to the parent session row. ON DELETE CASCADE so a
    -- cleanup of youtube_video_edits automatically tidies up the
    -- timeline (matches the FK convention of the surrounding schema).
    youtube_video_edit_id           VARCHAR(64) NOT NULL REFERENCES youtube_video_edits(id) ON DELETE CASCADE,

    -- Denormalized workspace_id for workspace-scoped analytic
    -- queries without needing to JOIN parent rows. Cheap on inserts
    -- because the caller already loaded the parent row and has the
    -- workspace_id in context.
    workspace_id                    BIGINT NOT NULL,

    -- actor_type follows the audit_log action constants pattern:
    --   user             : direct publish from the SPA
    --                       (handlePublishYouTubeEditorSessionByProject)
    --   publish_worker   : async publish from the worker
    --                       (internal/worker/publish_worker.go)
    --   drift_reconciler : external YouTube Studio edit detected
    --                       (P3 followup, hook pre-wired here)
    --   external         : free-form external system; not currently
    --                       used (reserved for future compliance hooks)
    actor_type                      VARCHAR(32)  NOT NULL,
    -- actor_label is human-readable text shown in the SPA timeline.
    -- For 'user' the label is the caller's display name or
    -- 'Operatore' fallback. For 'publish_worker' it's 'Worker'.
    -- For 'drift_reconciler' it's 'YouTube Studio'.
    actor_label                     VARCHAR(128) NOT NULL,

    -- changed_at is server-stamped at INSERT time. We deliberately
    -- use clock_timestamp() (statement-stable) rather than NOW()
    -- (transaction-stable) so two INSERTs in the same transaction
    -- get distinct timestamps and the idempotency UNIQUE index
    -- below correctly distinguishes "duplicate retry" from
    -- "operator genuinely took two actions back-to-back".
    -- clock_timestamp() also avoids the sub-microsecond NOW()
    -- collision risk during high-cadence bursts.
    changed_at                      TIMESTAMPTZ  NOT NULL DEFAULT clock_timestamp(),

    -- per-field diff columns: NULL means "this field was
    -- unchanged in this event". TEXT[] for tags so we keep the
    -- canonical order of array elements.
    before_title                    TEXT,
    after_title                     TEXT,
    before_description              TEXT,
    after_description               TEXT,
    before_tags                     TEXT[],
    after_tags                      TEXT[],
    before_default_language         VARCHAR(16),
    after_default_language          VARCHAR(16),
    before_default_audio_language   VARCHAR(16),
    after_default_audio_language    VARCHAR(16),
    -- Translations are stored as a JSON object keyed by language
    -- code (matching the publish request contract):
    --   { "en": { "title": "...", "description": "..." }, ... }
    before_translations             JSONB,
    after_translations              JSONB,
    before_desired_privacy          VARCHAR(16),
    after_desired_privacy           VARCHAR(16),
    before_actual_privacy           VARCHAR(16),
    after_actual_privacy            VARCHAR(16),
    before_youtube_sync_status      VARCHAR(16),
    after_youtube_sync_status       VARCHAR(16),
    before_thumbnail_media_id       VARCHAR(64),
    after_thumbnail_media_id        VARCHAR(64)
);

-- Primary lookup pattern for the SPA timeline:
--   "give me the latest N history rows for a specific video_edit_id"
-- The composite (video_edit_id, changed_at DESC) covers ORDER BY
-- changed_at DESC LIMIT N exactly as the SPA timeline emits it.
CREATE INDEX IF NOT EXISTS idx_ytvideo_edit_history_timeline
    ON youtube_video_edit_history (youtube_video_edit_id, changed_at DESC);

-- Workspace-wide analytics query support
-- (e.g., "how many drift events in workspace 123 this week?").
CREATE INDEX IF NOT EXISTS idx_ytvideo_edit_history_workspace
    ON youtube_video_edit_history (workspace_id, changed_at DESC);

-- Idempotency guard. The publisher + worker both retry on transient
-- failure; we DO NOT want a duplicate history row if the first INSERT
-- succeeded but the surrounding transaction rolled back. Using
-- (video_edit_id, changed_at, actor_type) makes the constraint
-- specific enough that genuine back-to-back events (operator hits
-- publish, then immediately edits title and re-saves) are NOT
-- blocked — they produce distinct changed_at timestamps because
-- NOW() advances between calls.
CREATE UNIQUE INDEX IF NOT EXISTS uq_ytvideo_edit_history_idemp
    ON youtube_video_edit_history (youtube_video_edit_id, changed_at, actor_type);
