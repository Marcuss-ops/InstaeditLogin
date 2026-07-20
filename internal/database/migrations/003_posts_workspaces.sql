-- 003_posts_workspaces.sql
-- Adds the workspace + post domain: teams, scheduled posts, fan-out targets,
-- and the post_status lifecycle enum.
--
-- Idempotent re-run notes:
--   - workspaces, posts, post_targets use CREATE TABLE IF NOT EXISTS
--   - platform_accounts.workspace_id uses ADD COLUMN IF NOT EXISTS
--   - post_status uses a DO-block guard because Postgres < 18 doesn't support
--     `CREATE TYPE IF NOT EXISTS ... AS ENUM`.
--
-- FK ordering: workspaces BEFORE platform_accounts ALTER; post_status BEFORE
-- posts/post_targets; posts BEFORE post_targets.

-- Workspaces: separating ownership of posts/platform accounts from the
-- single-user identity. A user can own multiple workspaces.
CREATE TABLE IF NOT EXISTS workspaces (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT        NOT NULL,
    owner_id   BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Nullable for retro-compat: existing platform_accounts rows existed before
-- this migration, so workspace_id must be NULL-able until a backfill assigns
-- each historical account to a workspace.
ALTER TABLE platform_accounts
    ADD COLUMN IF NOT EXISTS workspace_id BIGINT REFERENCES workspaces(id) ON DELETE CASCADE;

-- Post lifecycle enum: draft → scheduled → publishing → published (or failed).
-- DO-block guard: idempotent across re-runs.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'post_status') THEN
        CREATE TYPE post_status AS ENUM (
            'draft',
            'scheduled',
            'publishing',
            'published',
            'failed'
        );
    END IF;
END $$;

-- Posts: a piece of content (idea → edit → publish pipeline) belonging to a
-- workspace. workspace_id is NOT NULL because posts is a brand-new table with
-- zero rows at migration time: every post must belong to a workspace from
-- day one.
CREATE TABLE IF NOT EXISTS posts (
    id           BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    title        TEXT,
    caption      TEXT,
    media_url    TEXT,
    scheduled_at TIMESTAMPTZ,
    status       post_status NOT NULL DEFAULT 'draft',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Post targets: fan-out — one post replicates to N platform_accounts, each
-- with its own status. Allows partial success (some platforms OK, others
-- failed) without losing the per-platform error context.
CREATE TABLE IF NOT EXISTS post_targets (
    id                  BIGSERIAL PRIMARY KEY,
    post_id             BIGINT      NOT NULL REFERENCES posts(id)             ON DELETE CASCADE,
    platform_account_id BIGINT      NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
    status              post_status NOT NULL DEFAULT 'scheduled',
    platform_post_id    TEXT,
    error_message       TEXT,
    published_at        TIMESTAMPTZ
);

-- Indices:
--   - posts(workspace_id, scheduled_at): composite, drives the worker query
--     "find scheduled posts whose scheduled_at <= NOW() in a workspace".
--     Postgres B-Tree left-prefix also covers pure workspace_id filter.
--   - post_targets(post_id, status): composite, drives the publishing worker
--     to enumerate "all targets of post X with status scheduled/publishing"
--     atomically per fan-out.
-- Idempotent guard: if the posts table was created by an previous
-- (partial/failed) run of this same migration, the column may be
-- missing. Adding it explicitly before the index keeps re-runs safe.
ALTER TABLE posts ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_posts_workspace_scheduled
    ON posts(workspace_id, scheduled_at);

CREATE INDEX IF NOT EXISTS idx_post_targets_post_status
    ON post_targets(post_id, status);
