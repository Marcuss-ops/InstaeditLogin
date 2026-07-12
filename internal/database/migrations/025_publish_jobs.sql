-- ============================================================================
-- 025_publish_jobs.sql
--
-- Taglio 5.x: outbox + dispatcher commit step.
--
-- Backs `internal/models/post.go:PublishJob` (lines 196-216). The model was
-- added earlier but the table itself was never created — the previous
-- outbox migration (023_outbox_events.sql) created `outbox_events` only;
-- `publish_jobs` was referenced from 023's comment ("UNIQUE(publish_
-- jobs.outbox_event_id)") but the table did not yet exist on disk.
--
-- The user's mental model numbered this step as "014 (publish_jobs)" but
-- the next free migration number after the existing 024 (account_capabilities)
-- is 025; using the colliding 014 would shadow 017_api_keys.sql's lex order.
-- Per the runner's lex sort (`internal/database/migrations.go:RunMigrations`)
-- this migration runs AFTER 023 (outbox_events), so the FK from
-- publish_jobs.outbox_event_id → outbox_events.id is well-defined.
--
-- Idempotent: CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT EXISTS,
-- ADD COLUMN IF NOT EXISTS (so a future ALTER of this table stays safe).
-- ============================================================================

CREATE TABLE IF NOT EXISTS publish_jobs (
    id              BIGSERIAL    PRIMARY KEY,
    post_target_id  BIGINT       NOT NULL REFERENCES post_targets(id) ON DELETE CASCADE,
    -- Nullable so legacy CreatePost paths (pre-taglio-5.x) that materialise
    -- publish_jobs WITHOUT going through the outbox dispatcher don't break.
    -- The unique index below is partial (WHERE outbox_event_id IS NOT NULL)
    -- so multiple NULLs are allowed (matching the post_targets pattern).
    outbox_event_id BIGINT       REFERENCES outbox_events(id) ON DELETE SET NULL,
    -- Status follows the documented enum:
    --   'pending'     — created, not yet picked by the publisher
    --   'in_progress' — worker has claimed and is calling the platform
    --   'succeeded'   — terminal — platform returned success
    --   'failed'      — terminal — see error_message
    --   'cancelled'   — terminal — actor cancelled via API
    status         VARCHAR(50)  NOT NULL DEFAULT 'pending',
    attempt_number INTEGER      NOT NULL DEFAULT 0,
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    error_message  TEXT,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- The worker's hot-path query:
--   SELECT * FROM publish_jobs
--    WHERE status = 'pending' ORDER BY created_at ASC LIMIT N FOR UPDATE SKIP LOCKED;
-- The composite (status, post_target_id) index service the work-to-target
-- hot loop; partial index keeps it small (only 'pending' rows are
-- candidates, the rest fall out of the index once terminal).
CREATE INDEX IF NOT EXISTS idx_publish_jobs_status_target
    ON publish_jobs(status, post_target_id)
    WHERE status = 'pending';

-- Lookup-by-outbox-event-id is the dispatcher's mapping path:
--   given an outbox_event row it has claimed, find the matching
--   publish_jobs rows it produced (or SHOULD have produced).
-- Plain b-tree; cardinality grows with outbox_events.id.
CREATE INDEX IF NOT EXISTS idx_publish_jobs_outbox_event
    ON publish_jobs(outbox_event_id)
    WHERE outbox_event_id IS NOT NULL;

-- Idempotency marker: at most ONE publish_jobs row may exist per
-- non-NULL outbox_event_id. Combined with the worker's
-- FOR UPDATE SKIP LOCKED pattern this gives at-most-once materialisation
-- semantics even if a re-dispatch race occurs on outbox_events.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_publish_jobs_outbox_event
    ON publish_jobs(outbox_event_id)
    WHERE outbox_event_id IS NOT NULL;
