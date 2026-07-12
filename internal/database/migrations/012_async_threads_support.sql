-- 012_async_threads_support.sql
-- Zernio 2.1: async Threads publishing support.
--
-- This file contains table alterations, columns, and indices.
-- The post_status enum value additions have been extracted to
-- 012_add_post_status_enum.sql so ALTER TYPE ADD VALUE runs in
-- its own transaction (PostgreSQL forbids mixing ALTER TYPE ADD VALUE
-- with ALTER TABLE on dependent tables — error 55P04).

-- =========================================================================
-- 1. Add provider_state column (JSON blob for async platform state).
-- =========================================================================
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS provider_state TEXT;

-- =========================================================================
-- 2. Add container_id column (opaque async creation ID).
-- =========================================================================
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS container_id TEXT;

-- =========================================================================
-- 3. Index for the worker's waiting_provider pickup query.
-- =========================================================================
CREATE INDEX IF NOT EXISTS idx_post_targets_waiting_provider
    ON post_targets(status)
    WHERE status = 'waiting_provider';

-- =========================================================================
-- 4. Post column additions required by models/post.go (Taglio 4.2):
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
-- 5. Post_target column additions required by models/post.go (Taglio 4.2):
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
