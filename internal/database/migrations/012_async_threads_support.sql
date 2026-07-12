-- 012_async_threads_support.sql
-- Zernio 2.1: async Threads publishing support.
--
-- 1. Adds `waiting_provider` to the post_status enum — the target has been
--    handed off to the platform (container created) and the worker polls
--    the provider on subsequent ticks via PublishReconciler.
-- 2. Adds `provider_state` TEXT column to post_targets — a JSON blob
--    for platform-specific async state (container ID, status URL, etc.)
--    so the worker can resume reconciliation after a restart.
-- 3. Adds `container_id` TEXT column to post_targets — the opaque ID
--    returned by the provider's async creation call (e.g. Threads
--    container ID, TikTok publish_id). Stored separately from
--    platform_post_id (which holds the final published media ID) so
--    the lifecycle is unambiguous.

-- =========================================================================
-- 1. Extend post_status enum with waiting_provider, queued, partially_published.
--    DO-block guard: idempotent across re-runs.
--    'queued' is required by ListPending and UpdateStatus (the worker uses
--      status='queued' literal in WHERE clauses — PostStatusScheduled is a
--      deprecated alias of PostStatusQueued, but the Go alias is in-memory
--      only; the DB enum needs the explicit value to accept new INSERTs).
--    'partially_published' is used by UpdateStatus when some targets
--      succeed and others fail (Taglio 4.0).
-- =========================================================================
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'waiting_provider'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'waiting_provider';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'queued'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'queued';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'partially_published'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'partially_published';
    END IF;
END $$;

-- =========================================================================
-- 2. Add provider_state column (JSON blob for async platform state).
-- =========================================================================
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS provider_state TEXT;

-- =========================================================================
-- 3. Add container_id column (opaque async creation ID).
-- =========================================================================
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS container_id TEXT;

-- =========================================================================
-- 4. Index for the worker's waiting_provider pickup query.
-- =========================================================================
CREATE INDEX IF NOT EXISTS idx_post_targets_waiting_provider
    ON post_targets(status)
    WHERE status = 'waiting_provider';

-- =========================================================================
-- 5. Post column additions required by models/post.go (Taglio 4.2):
--      posts.idempotency_key — set by Create on idempotent POSTs
--      posts.version        — optimistic-concurrency counter (post.version)
--      posts.updated_at     — bumped on every UpdateStatus call
--    All idempotent across re-runs. Default values fill existing rows so
--    NOT NULL is safe.
-- =========================================================================
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- =========================================================================
-- 6. Post_target column additions required by models/post.go (Taglio 4.2):
--      post_targets.version    — optimistic-concurrency counter
--      post_targets.created_at — audit timestamp (NULL on pre-existing rows
--                                but DEFAULT NOW() backfills all existing rows)
--      post_targets.updated_at — bumped on every UpdateStatus call
-- =========================================================================
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
